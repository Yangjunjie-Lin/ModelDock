package store

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
)

func normalizeCredentialTags(tags []string) ([]string, error) {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if len(tag) > 64 {
			return nil, errors.New("credential tags must not exceed 64 bytes")
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	if len(out) > 32 {
		return nil, errors.New("a credential may have at most 32 tags")
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) CredentialTags(ctx context.Context, credentialID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT tag FROM credential_tags WHERE credential_id=$1 ORDER BY tag`, credentialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_credentials WHERE id=$1)`, credentialID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return out, nil
}

func (s *Store) SetCredentialTags(ctx context.Context, credentialID string, tags []string) error {
	normalized, err := normalizeCredentialTags(tags)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err = tx.QueryRow(ctx, `SELECT id FROM provider_credentials WHERE id=$1 FOR KEY SHARE`, credentialID).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM credential_tags WHERE credential_id=$1`, credentialID); err != nil {
		return err
	}
	for _, tag := range normalized {
		if _, err = tx.Exec(ctx, `INSERT INTO credential_tags(credential_id,tag) VALUES($1,$2)`, credentialID, tag); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) AddCredentialTag(ctx context.Context, credentialID, rawTag string) error {
	tags, err := normalizeCredentialTags([]string{rawTag})
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return errors.New("credential tag is required")
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO credential_tags(credential_id,tag)
		SELECT id,$2 FROM provider_credentials WHERE id=$1 ON CONFLICT DO NOTHING`, credentialID, tags[0])
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_credentials WHERE id=$1)`, credentialID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) RemoveCredentialTag(ctx context.Context, credentialID, rawTag string) error {
	tags, err := normalizeCredentialTags([]string{rawTag})
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return errors.New("credential tag is required")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM credential_tags WHERE credential_id=$1 AND tag=$2`, credentialID, tags[0])
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) CredentialWithTagsByID(ctx context.Context, credentialID string) (domain.Credential, error) {
	credential, err := s.CredentialByID(ctx, credentialID)
	if err != nil {
		return domain.Credential{}, err
	}
	credential.Tags, err = s.CredentialTags(ctx, credentialID)
	return credential, err
}

// ListCredentialIDsByTags returns credentials containing every required tag
// and none of the denied tags. Empty filters intentionally match all rows.
func (s *Store) ListCredentialIDsByTags(ctx context.Context, required, denied []string, limit int) ([]string, error) {
	var err error
	if required, err = normalizeCredentialTags(required); err != nil {
		return nil, err
	}
	if denied, err = normalizeCredentialTags(denied); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id FROM provider_credentials c
		WHERE NOT EXISTS (
			SELECT 1 FROM unnest($1::text[]) required(tag)
			WHERE NOT EXISTS (SELECT 1 FROM credential_tags ct WHERE ct.credential_id=c.id AND ct.tag=required.tag)
		) AND NOT EXISTS (
			SELECT 1 FROM credential_tags ct WHERE ct.credential_id=c.id AND ct.tag=ANY($2::text[])
		) ORDER BY c.created_at,c.id LIMIT $3`, required, denied, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var credentialID string
		if err := rows.Scan(&credentialID); err != nil {
			return nil, err
		}
		out = append(out, credentialID)
	}
	return out, rows.Err()
}
