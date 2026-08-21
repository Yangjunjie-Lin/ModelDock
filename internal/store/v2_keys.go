package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

// #nosec G101 -- SQL projection names API-key columns but contains no credential value.
const v2APIKeyColumns = `k.id,k.user_id,k.organization_id,k.project_id,COALESCE(k.team_id::text,''),k.name,k.environment,k.key_prefix,k.key_hash,k.status,
	k.expires_at,k.rate_limit_rpm,k.rate_limit_tpm,k.monthly_token_limit,k.monthly_cost_limit,k.allowed_models,
	 k.created_at,k.updated_at,k.last_used_at,COALESCE(k.frozen_reason,''),k.frozen_at,k.last_leak_detected_at,
	COALESCE((SELECT max(v.version) FROM api_key_versions v WHERE v.api_key_id=k.id),0)`

func scanV2APIKey(row pgx.Row) (domain.APIKey, error) {
	var key domain.APIKey
	var allowed []byte
	err := row.Scan(&key.ID, &key.UserID, &key.OrganizationID, &key.ProjectID, &key.TeamID, &key.Name, &key.Environment,
		&key.KeyPrefix, &key.KeyHash, &key.Status, &key.ExpiresAt, &key.RateLimitRPM, &key.RateLimitTPM,
		&key.MonthlyTokenLimit, &key.MonthlyCostLimit, &allowed, &key.CreatedAt, &key.UpdatedAt,
		&key.LastUsedAt, &key.FrozenReason, &key.FrozenAt, &key.LastLeakDetectedAt, &key.CurrentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return key, ErrNotFound
	}
	if err != nil {
		return key, err
	}
	_ = json.Unmarshal(allowed, &key.AllowedModels)
	if key.AllowedModels == nil {
		key.AllowedModels = []string{}
	}
	return key, nil
}

func (s *Store) ProjectAPIKeyByID(ctx context.Context, keyID string) (domain.APIKey, error) {
	return scanV2APIKey(s.pool.QueryRow(ctx, `SELECT `+v2APIKeyColumns+` FROM api_keys k WHERE k.id=$1`, keyID))
}

// CreateProjectAPIKey is the tenant-aware counterpart of the V1 CreateAPIKey.
// The migration's INSERT trigger atomically creates version 1.
func (s *Store) CreateProjectAPIKey(ctx context.Context, key domain.APIKey) (domain.APIKey, error) {
	key.ID = id.UUID()
	if key.OrganizationID == "" {
		key.OrganizationID = domain.LegacyOrganizationID
	}
	if key.ProjectID == "" {
		key.ProjectID = domain.LegacyProjectID
	}
	if key.Status == "" {
		key.Status = "ACTIVE"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.APIKey{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('api-key-create:'||$1,0))`, key.UserID); err != nil {
		return domain.APIKey{}, err
	}
	var userAbuse, userPayment, orgAbuse, orgPayment string
	var userScore, orgScore int
	if err = tx.QueryRow(ctx, `SELECT u.abuse_status,u.payment_risk,u.risk_score,o.abuse_status,o.payment_risk,o.risk_score
		FROM users u JOIN organizations o ON o.id=$2 WHERE u.id=$1`, key.UserID, key.OrganizationID).
		Scan(&userAbuse, &userPayment, &userScore, &orgAbuse, &orgPayment, &orgScore); err != nil {
		return domain.APIKey{}, err
	}
	if userAbuse == "FROZEN" || orgAbuse == "FROZEN" || userPayment == "BLOCKED" || orgPayment == "BLOCKED" {
		return domain.APIKey{}, ErrRiskFrozen
	}
	if userAbuse == "RESTRICTED" || orgAbuse == "RESTRICTED" || userScore >= 90 || orgScore >= 90 {
		return domain.APIKey{}, ErrRiskRestricted
	}
	var recent int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE user_id=$1 AND created_at>=now()-interval '1 hour'`, key.UserID).Scan(&recent); err != nil {
		return domain.APIKey{}, err
	}
	if recent >= 10 {
		return domain.APIKey{}, ErrKeyCreationRate
	}
	if _, err = effectivePlanVersionTx(ctx, tx, key.OrganizationID); err != nil {
		return domain.APIKey{}, err
	}
	var activeKeys int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE organization_id=$1 AND status='ACTIVE'`, key.OrganizationID).Scan(&activeKeys); err != nil {
		return domain.APIKey{}, err
	}
	if err = enforceIntegerEntitlementTx(ctx, tx, key.OrganizationID, "api_key_count", activeKeys, 1); err != nil {
		return domain.APIKey{}, err
	}
	if key.TeamID != "" {
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_memberships tm JOIN teams t ON t.id=tm.team_id
			WHERE tm.team_id=$1 AND tm.user_id=$2 AND tm.organization_id=$3 AND tm.status='ACTIVE' AND t.status='ACTIVE')`,
			key.TeamID, key.UserID, key.OrganizationID).Scan(&allowed)
		if err != nil {
			return domain.APIKey{}, err
		}
		if !allowed {
			return domain.APIKey{}, ErrNotFound
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO api_keys(id,user_id,organization_id,project_id,team_id,name,environment,key_prefix,key_hash,status,
		expires_at,rate_limit_rpm,rate_limit_tpm,monthly_token_limit,monthly_cost_limit,allowed_models)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, key.ID, key.UserID, key.OrganizationID,
		key.ProjectID, nullString(key.TeamID), key.Name, key.Environment, key.KeyPrefix, key.KeyHash, key.Status, key.ExpiresAt,
		key.RateLimitRPM, key.RateLimitTPM, key.MonthlyTokenLimit, key.MonthlyCostLimit, jsonBytes(key.AllowedModels))
	if err != nil {
		return domain.APIKey{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.APIKey{}, err
	}
	return s.ProjectAPIKeyByID(ctx, key.ID)
}

func (s *Store) ListProjectAPIKeys(ctx context.Context, projectID string, userID *string, limit, offset int) ([]domain.APIKey, error) {
	query := `SELECT ` + v2APIKeyColumns + ` FROM api_keys k WHERE k.project_id=$1`
	args := []any{projectID}
	if userID != nil {
		args = append(args, *userID)
		query += ` AND k.user_id=$2`
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY k.created_at DESC,k.id DESC LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.APIKey, 0)
	for rows.Next() {
		key, err := scanV2APIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func scanAPIKeyVersion(row pgx.Row) (domain.APIKeyVersion, error) {
	var version domain.APIKeyVersion
	err := row.Scan(&version.ID, &version.APIKeyID, &version.Version, &version.KeyPrefix, &version.KeyHash,
		&version.Status, &version.ValidFrom, &version.GraceExpiresAt, &version.ExpiresAt,
		&version.CreatedAt, &version.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return version, ErrNotFound
	}
	return version, err
}

// #nosec G101 -- SQL projection names API-key columns but contains no credential value.
const apiKeyVersionColumns = `id,api_key_id,version,key_prefix,key_hash,status,valid_from,grace_expires_at,expires_at,created_at,last_used_at`

func (s *Store) ListAPIKeyVersions(ctx context.Context, keyID string) ([]domain.APIKeyVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+apiKeyVersionColumns+` FROM api_key_versions WHERE api_key_id=$1 ORDER BY version DESC`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.APIKeyVersion, 0)
	for rows.Next() {
		version, err := scanAPIKeyVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, rows.Err()
}

// AuthenticateAPIKeyVersion accepts an ACTIVE version or an unexpired GRACE
// version.  Parent-key, user, and expiry status are checked in the same query,
// so disabling a user or key invalidates every version immediately.
func (s *Store) AuthenticateAPIKeyVersion(ctx context.Context, hash []byte) (domain.APIKeyAuthentication, error) {
	var authn domain.APIKeyAuthentication
	var allowed []byte
	err := s.pool.QueryRow(ctx, `SELECT `+v2APIKeyColumns+`,
		v.id,v.api_key_id,v.version,v.key_prefix,v.key_hash,v.status,v.valid_from,v.grace_expires_at,v.expires_at,v.created_at,v.last_used_at
		FROM api_key_versions v JOIN api_keys k ON k.id=v.api_key_id JOIN users u ON u.id=k.user_id
		JOIN projects p ON p.id=k.project_id JOIN organizations o ON o.id=k.organization_id
		WHERE v.key_hash=$1 AND k.status='ACTIVE' AND u.status='ACTIVE' AND p.status='ACTIVE' AND o.status='ACTIVE'
		AND (k.team_id IS NULL OR EXISTS(SELECT 1 FROM teams t JOIN team_memberships tm ON tm.team_id=t.id
			WHERE t.id=k.team_id AND t.organization_id=k.organization_id AND t.status='ACTIVE' AND tm.user_id=k.user_id AND tm.status='ACTIVE'))
		AND (k.expires_at IS NULL OR k.expires_at>now()) AND v.valid_from<=now()
		AND (v.expires_at IS NULL OR v.expires_at>now())
		AND (v.status='ACTIVE' OR (v.status='GRACE' AND v.grace_expires_at>now()))`, hash).
		Scan(&authn.Key.ID, &authn.Key.UserID, &authn.Key.OrganizationID, &authn.Key.ProjectID, &authn.Key.TeamID, &authn.Key.Name,
			&authn.Key.Environment, &authn.Key.KeyPrefix, &authn.Key.KeyHash, &authn.Key.Status, &authn.Key.ExpiresAt,
			&authn.Key.RateLimitRPM, &authn.Key.RateLimitTPM, &authn.Key.MonthlyTokenLimit, &authn.Key.MonthlyCostLimit,
			&allowed, &authn.Key.CreatedAt, &authn.Key.UpdatedAt, &authn.Key.LastUsedAt, &authn.Key.FrozenReason, &authn.Key.FrozenAt,
			&authn.Key.LastLeakDetectedAt, &authn.Key.CurrentVersion,
			&authn.Version.ID, &authn.Version.APIKeyID, &authn.Version.Version, &authn.Version.KeyPrefix,
			&authn.Version.KeyHash, &authn.Version.Status, &authn.Version.ValidFrom, &authn.Version.GraceExpiresAt,
			&authn.Version.ExpiresAt, &authn.Version.CreatedAt, &authn.Version.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.APIKeyAuthentication{}, ErrNotFound
	}
	if err != nil {
		return domain.APIKeyAuthentication{}, err
	}
	_ = json.Unmarshal(allowed, &authn.Key.AllowedModels)
	if authn.Key.AllowedModels == nil {
		authn.Key.AllowedModels = []string{}
	}
	_, _ = s.pool.Exec(ctx, `UPDATE api_key_versions SET last_used_at=now() WHERE id=$1`, authn.Version.ID)
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, authn.Key.ID)
	return authn, nil
}

func (s *Store) APIKeyByVersionHash(ctx context.Context, hash []byte) (domain.APIKey, domain.APIKeyVersion, error) {
	authn, err := s.AuthenticateAPIKeyVersion(ctx, hash)
	return authn.Key, authn.Version, err
}

func (s *Store) RotateAPIKeyVersion(ctx context.Context, keyID, prefix string, hash []byte, graceUntil time.Time) (domain.APIKeyVersion, error) {
	return s.rotateAPIKeyVersion(ctx, keyID, prefix, hash, graceUntil, nil)
}

func (s *Store) RotateAPIKeyVersionWithExpiry(ctx context.Context, keyID, prefix string, hash []byte, graceUntil time.Time, expiresAt *time.Time) (domain.APIKeyVersion, error) {
	return s.rotateAPIKeyVersion(ctx, keyID, prefix, hash, graceUntil, expiresAt)
}

func (s *Store) rotateAPIKeyVersion(ctx context.Context, keyID, prefix string, hash []byte, graceUntil time.Time, expiresAt *time.Time) (domain.APIKeyVersion, error) {
	if len(hash) == 0 || prefix == "" {
		return domain.APIKeyVersion{}, errors.New("API key prefix and hash are required")
	}
	if !graceUntil.After(time.Now()) {
		return domain.APIKeyVersion{}, errors.New("grace expiry must be in the future")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.APIKeyVersion{}, err
	}
	defer tx.Rollback(ctx)
	var currentPrefix string
	var currentHash []byte
	var keyExpiresAt *time.Time
	var keyStatus string
	if err = tx.QueryRow(ctx, `SELECT key_prefix,key_hash,expires_at,status FROM api_keys WHERE id=$1 FOR UPDATE`, keyID).
		Scan(&currentPrefix, &currentHash, &keyExpiresAt, &keyStatus); errors.Is(err, pgx.ErrNoRows) {
		return domain.APIKeyVersion{}, ErrNotFound
	} else if err != nil {
		return domain.APIKeyVersion{}, err
	}
	if keyStatus != "ACTIVE" {
		return domain.APIKeyVersion{}, errors.New("only an active API key can be rotated")
	}
	// Repair a key created before the V2 trigger was installed, if necessary.
	if _, err = tx.Exec(ctx, `INSERT INTO api_key_versions(id,api_key_id,version,key_prefix,key_hash,status,valid_from,expires_at)
		SELECT $1,$2,1,$3,$4,'ACTIVE',now(),$5
		WHERE NOT EXISTS(SELECT 1 FROM api_key_versions WHERE api_key_id=$2)`, id.UUID(), keyID, currentPrefix, currentHash, keyExpiresAt); err != nil {
		return domain.APIKeyVersion{}, err
	}
	var nextVersion int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM api_key_versions WHERE api_key_id=$1`, keyID).Scan(&nextVersion); err != nil {
		return domain.APIKeyVersion{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE api_key_versions SET status='GRACE',grace_expires_at=$2
		WHERE api_key_id=$1 AND status='ACTIVE'`, keyID, graceUntil.UTC()); err != nil {
		return domain.APIKeyVersion{}, err
	}
	newID := id.UUID()
	if expiresAt == nil {
		expiresAt = keyExpiresAt
	}
	if _, err = tx.Exec(ctx, `INSERT INTO api_key_versions(id,api_key_id,version,key_prefix,key_hash,status,valid_from,expires_at)
		VALUES($1,$2,$3,$4,$5,'ACTIVE',now(),$6)`, newID, keyID, nextVersion, prefix, hash, expiresAt); err != nil {
		return domain.APIKeyVersion{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE api_keys SET key_prefix=$2,key_hash=$3,expires_at=$4,updated_at=now() WHERE id=$1`, keyID, prefix, hash, expiresAt); err != nil {
		return domain.APIKeyVersion{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.APIKeyVersion{}, err
	}
	return scanAPIKeyVersion(s.pool.QueryRow(ctx, `SELECT `+apiKeyVersionColumns+` FROM api_key_versions WHERE id=$1`, newID))
}

func (s *Store) FinalizeAPIKeyRotation(ctx context.Context, keyID string, version int) error {
	query := `UPDATE api_key_versions SET status='REVOKED',grace_expires_at=NULL WHERE api_key_id=$1 AND status='GRACE'`
	args := []any{keyID}
	if version > 0 {
		query += ` AND version=$2`
		args = append(args, version)
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) FinalizeExpiredAPIKeyRotations(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE api_key_versions SET status='REVOKED',grace_expires_at=NULL
		WHERE status='GRACE' AND grace_expires_at <= $1`, now.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
