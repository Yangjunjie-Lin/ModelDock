package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

const enterpriseIdentityColumns = `id,organization_id,issuer_url,client_id,
	(client_secret_encrypted IS NOT NULL),(scim_token_hash IS NOT NULL),allowed_domains,sso_enabled,scim_enabled,
	enforce_sso,status,metadata,created_by,created_at,updated_at`

func scanEnterpriseIdentityConnection(row pgx.Row) (domain.EnterpriseIdentityConnection, error) {
	var out domain.EnterpriseIdentityConnection
	var domains, metadata []byte
	err := row.Scan(&out.ID, &out.OrganizationID, &out.IssuerURL, &out.ClientID, &out.HasClientSecret,
		&out.HasSCIMToken, &domains, &out.SSOEnabled, &out.SCIMEnabled, &out.EnforceSSO, &out.Status,
		&metadata, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(domains, &out.AllowedDomains)
	_ = json.Unmarshal(metadata, &out.Metadata)
	if out.AllowedDomains == nil {
		out.AllowedDomains = []string{}
	}
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	return out, nil
}

func (s *Store) EnterpriseIdentityConnection(ctx context.Context, organizationID string) (domain.EnterpriseIdentityConnection, error) {
	return scanEnterpriseIdentityConnection(s.pool.QueryRow(ctx, `SELECT `+enterpriseIdentityColumns+
		` FROM enterprise_identity_connections WHERE organization_id=$1`, organizationID))
}

// UpsertEnterpriseIdentityConnection preserves the existing secrets when a
// nil encrypted value or token digest is supplied. Plaintext credentials never
// cross the store boundary and are never returned by its projections.
func (s *Store) UpsertEnterpriseIdentityConnection(ctx context.Context, value domain.EnterpriseIdentityConnection,
	clientSecretEncrypted, scimTokenHash []byte, actor *string) (domain.EnterpriseIdentityConnection, error) {
	value.OrganizationID = strings.TrimSpace(value.OrganizationID)
	value.IssuerURL = strings.TrimSpace(value.IssuerURL)
	value.ClientID = strings.TrimSpace(value.ClientID)
	value.Status = strings.ToUpper(strings.TrimSpace(value.Status))
	if value.ID == "" {
		value.ID = id.UUID()
	}
	if value.Status == "" {
		value.Status = "DISABLED"
	}
	if value.OrganizationID == "" || (value.Status != "ACTIVE" && value.Status != "DISABLED") {
		return value, errors.New("invalid enterprise identity connection")
	}
	if value.EnforceSSO && (!value.SSOEnabled || value.Status != "ACTIVE") {
		return value, errors.New("SSO must be active before it can be enforced")
	}
	value.AllowedDomains = normalizeStrings(value.AllowedDomains)
	if value.Metadata == nil {
		value.Metadata = map[string]any{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO enterprise_identity_connections(id,organization_id,issuer_url,client_id,
		client_secret_encrypted,scim_token_hash,allowed_domains,sso_enabled,scim_enabled,enforce_sso,status,metadata,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT(organization_id) DO UPDATE SET issuer_url=EXCLUDED.issuer_url,client_id=EXCLUDED.client_id,
		client_secret_encrypted=COALESCE(EXCLUDED.client_secret_encrypted,enterprise_identity_connections.client_secret_encrypted),
		scim_token_hash=COALESCE(EXCLUDED.scim_token_hash,enterprise_identity_connections.scim_token_hash),
		allowed_domains=EXCLUDED.allowed_domains,sso_enabled=EXCLUDED.sso_enabled,scim_enabled=EXCLUDED.scim_enabled,
		enforce_sso=EXCLUDED.enforce_sso,status=EXCLUDED.status,metadata=EXCLUDED.metadata,updated_at=now()`,
		value.ID, value.OrganizationID, value.IssuerURL, value.ClientID, clientSecretEncrypted, scimTokenHash,
		jsonBytes(value.AllowedDomains), value.SSOEnabled, value.SCIMEnabled, value.EnforceSSO, value.Status,
		jsonBytes(value.Metadata), actor)
	if err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, actor, "enterprise.identity_connection_updated", "organization", value.OrganizationID,
		map[string]any{"issuer_url": value.IssuerURL, "sso_enabled": value.SSOEnabled, "scim_enabled": value.SCIMEnabled,
			"enforce_sso": value.EnforceSSO, "status": value.Status, "client_secret_rotated": len(clientSecretEncrypted) > 0,
			"scim_token_rotated": len(scimTokenHash) > 0}); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return s.EnterpriseIdentityConnection(ctx, value.OrganizationID)
}

func (s *Store) AuthenticateSCIM(ctx context.Context, organizationID string, tokenHash []byte) (domain.EnterpriseIdentityConnection, error) {
	return scanEnterpriseIdentityConnection(s.pool.QueryRow(ctx, `SELECT `+enterpriseIdentityColumns+
		` FROM enterprise_identity_connections WHERE organization_id=$1 AND status='ACTIVE' AND scim_enabled=true
		AND scim_token_hash=$2`, organizationID, tokenHash))
}

func scanSCIMUser(row pgx.Row) (domain.SCIMUserRecord, error) {
	var out domain.SCIMUserRecord
	err := row.Scan(&out.UserID, &out.OrganizationID, &out.ExternalID, &out.Email, &out.DisplayName, &out.Active,
		&out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

const scimUserSelect = `SELECT u.id,m.organization_id,COALESCE(l.external_id,''),u.email,u.display_name,
	(m.status='ACTIVE'),m.created_at,m.updated_at FROM organization_memberships m JOIN users u ON u.id=m.user_id
	JOIN scim_resource_links l ON l.organization_id=m.organization_id AND l.resource_type='USER' AND l.resource_id=u.id`

func (s *Store) ListSCIMUsers(ctx context.Context, organizationID string) ([]domain.SCIMUserRecord, error) {
	rows, err := s.pool.Query(ctx, scimUserSelect+` WHERE m.organization_id=$1 ORDER BY u.email,u.id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SCIMUserRecord, 0)
	for rows.Next() {
		item, scanErr := scanSCIMUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SCIMUser(ctx context.Context, organizationID, userID string) (domain.SCIMUserRecord, error) {
	return scanSCIMUser(s.pool.QueryRow(ctx, scimUserSelect+` WHERE m.organization_id=$1 AND u.id=$2`, organizationID, userID))
}

func (s *Store) UpsertSCIMUser(ctx context.Context, organizationID, userID, email, displayName, externalID string,
	active bool, passwordHash string) (domain.SCIMUserRecord, error) {
	organizationID = strings.TrimSpace(organizationID)
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	externalID = strings.TrimSpace(externalID)
	if organizationID == "" || email == "" || !strings.Contains(email, "@") || len(externalID) > 255 {
		return domain.SCIMUserRecord{}, errors.New("invalid SCIM user")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SCIMUserRecord{}, err
	}
	defer tx.Rollback(ctx)
	var storedEmail string
	if userID != "" {
		err = tx.QueryRow(ctx, `SELECT u.email FROM users u JOIN organization_memberships m ON m.user_id=u.id
			JOIN scim_resource_links l ON l.organization_id=m.organization_id AND l.resource_type='USER' AND l.resource_id=u.id
			WHERE u.id=$1 AND m.organization_id=$2 FOR UPDATE OF u,m,l`, userID, organizationID).Scan(&storedEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SCIMUserRecord{}, ErrNotFound
		}
		if err != nil {
			return domain.SCIMUserRecord{}, err
		}
		if !strings.EqualFold(storedEmail, email) {
			return domain.SCIMUserRecord{}, errors.New("SCIM cannot change a shared RelayDock login email")
		}
	} else {
		err = tx.QueryRow(ctx, `SELECT id,email FROM users WHERE lower(email)=lower($1) FOR UPDATE`, email).Scan(&userID, &storedEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			if passwordHash == "" {
				return domain.SCIMUserRecord{}, errors.New("password hash is required for a new SCIM user")
			}
			userID = id.UUID()
			_, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
				VALUES($1,$2,$3,$4,'USER','ACTIVE',now())`, userID, email, passwordHash, displayName)
		}
		if err != nil {
			return domain.SCIMUserRecord{}, err
		}
	}
	if !active {
		var role, status string
		err = tx.QueryRow(ctx, `SELECT role,status FROM organization_memberships WHERE organization_id=$1 AND user_id=$2`,
			organizationID, userID).Scan(&role, &status)
		if err == nil && role == "OWNER" && status == "ACTIVE" {
			var otherOwners int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1
				AND user_id<>$2 AND role='OWNER' AND status='ACTIVE'`, organizationID, userID).Scan(&otherOwners); err != nil {
				return domain.SCIMUserRecord{}, err
			}
			if otherOwners == 0 {
				return domain.SCIMUserRecord{}, errors.New("SCIM cannot deactivate the last organization owner")
			}
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.SCIMUserRecord{}, err
		}
	}
	status := "DISABLED"
	if active {
		status = "ACTIVE"
		if err = enforceOrganizationMemberActivationTx(ctx, tx, organizationID, userID); err != nil {
			return domain.SCIMUserRecord{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET display_name=CASE WHEN $2='' THEN display_name ELSE $2 END,updated_at=now() WHERE id=$1`,
		userID, displayName); err != nil {
		return domain.SCIMUserRecord{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,'MEMBER',$3) ON CONFLICT(organization_id,user_id) DO UPDATE SET
		role=CASE WHEN organization_memberships.role IN ('OWNER','ADMIN') THEN organization_memberships.role ELSE 'MEMBER' END,
		status=EXCLUDED.status,updated_at=now()`, organizationID, userID, status); err != nil {
		return domain.SCIMUserRecord{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO scim_resource_links(organization_id,resource_type,resource_id,external_id)
		VALUES($1,'USER',$2,$3) ON CONFLICT(organization_id,resource_type,resource_id) DO UPDATE SET
		external_id=EXCLUDED.external_id,updated_at=now()`, organizationID, userID, externalID); err != nil {
		return domain.SCIMUserRecord{}, err
	}
	if err = writeAuditTx(ctx, tx, nil, "scim.user_upserted", "user", userID,
		map[string]any{"organization_id": organizationID, "active": active, "external_id": externalID}); err != nil {
		return domain.SCIMUserRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SCIMUserRecord{}, err
	}
	return s.SCIMUser(ctx, organizationID, userID)
}

func (s *Store) DeleteSCIMUser(ctx context.Context, organizationID, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	if err = tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`,
		organizationID, userID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if role == "OWNER" {
		var owners int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1
			AND user_id<>$2 AND role='OWNER' AND status='ACTIVE'`, organizationID, userID).Scan(&owners); err != nil {
			return err
		}
		if owners == 0 {
			return errors.New("SCIM cannot deactivate the last organization owner")
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_memberships SET status='DISABLED',updated_at=now()
		WHERE organization_id=$1 AND user_id=$2`, organizationID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE project_memberships SET status='DISABLED',updated_at=now()
		WHERE organization_id=$1 AND user_id=$2`, organizationID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM team_memberships WHERE organization_id=$1 AND user_id=$2`, organizationID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM scim_resource_links WHERE organization_id=$1 AND resource_type='USER' AND resource_id=$2`,
		organizationID, userID); err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, nil, "scim.user_deactivated", "user", userID,
		map[string]any{"organization_id": organizationID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanSCIMGroup(row pgx.Row) (domain.SCIMGroupRecord, error) {
	var out domain.SCIMGroupRecord
	var members []byte
	err := row.Scan(&out.TeamID, &out.OrganizationID, &out.ExternalID, &out.DisplayName, &members, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(members, &out.MemberIDs)
		if out.MemberIDs == nil {
			out.MemberIDs = []string{}
		}
	}
	return out, err
}

const scimGroupSelect = `SELECT t.id,t.organization_id,COALESCE(l.external_id,''),t.name,
	COALESCE((SELECT jsonb_agg(tm.user_id ORDER BY tm.user_id) FROM team_memberships tm
	WHERE tm.team_id=t.id AND tm.status='ACTIVE'),'[]'::jsonb),t.created_at,t.updated_at
	FROM teams t JOIN scim_resource_links l ON l.organization_id=t.organization_id AND l.resource_type='GROUP' AND l.resource_id=t.id`

func (s *Store) ListSCIMGroups(ctx context.Context, organizationID string) ([]domain.SCIMGroupRecord, error) {
	rows, err := s.pool.Query(ctx, scimGroupSelect+` WHERE t.organization_id=$1 AND t.status='ACTIVE' ORDER BY t.name,t.id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SCIMGroupRecord, 0)
	for rows.Next() {
		item, scanErr := scanSCIMGroup(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SCIMGroup(ctx context.Context, organizationID, teamID string) (domain.SCIMGroupRecord, error) {
	return scanSCIMGroup(s.pool.QueryRow(ctx, scimGroupSelect+` WHERE t.organization_id=$1 AND t.id=$2 AND t.status='ACTIVE'`,
		organizationID, teamID))
}

func (s *Store) UpsertSCIMGroup(ctx context.Context, organizationID, teamID, displayName, externalID string,
	memberIDs []string) (domain.SCIMGroupRecord, error) {
	organizationID = strings.TrimSpace(organizationID)
	displayName = strings.TrimSpace(displayName)
	externalID = strings.TrimSpace(externalID)
	if organizationID == "" || displayName == "" || len(externalID) > 255 {
		return domain.SCIMGroupRecord{}, errors.New("invalid SCIM group")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SCIMGroupRecord{}, err
	}
	defer tx.Rollback(ctx)
	if teamID == "" {
		teamID = id.UUID()
		slug := "scim-" + strings.ReplaceAll(teamID, "-", "")
		_, err = tx.Exec(ctx, `INSERT INTO teams(id,organization_id,name,slug,status,metadata)
			VALUES($1,$2,$3,$4,'ACTIVE','{"managed_by":"SCIM"}'::jsonb)`, teamID, organizationID, displayName, slug)
	} else {
		var linked bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scim_resource_links WHERE organization_id=$1
			AND resource_type='GROUP' AND resource_id=$2)`, organizationID, teamID).Scan(&linked)
		if err == nil && !linked {
			return domain.SCIMGroupRecord{}, ErrNotFound
		}
		if err == nil {
			tag, updateErr := tx.Exec(ctx, `UPDATE teams SET name=$3,status='ACTIVE',updated_at=now() WHERE id=$1 AND organization_id=$2`,
				teamID, organizationID, displayName)
			if updateErr != nil {
				return domain.SCIMGroupRecord{}, updateErr
			}
			if tag.RowsAffected() == 0 {
				return domain.SCIMGroupRecord{}, ErrNotFound
			}
		}
	}
	if err != nil {
		return domain.SCIMGroupRecord{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO scim_resource_links(organization_id,resource_type,resource_id,external_id)
		VALUES($1,'GROUP',$2,$3) ON CONFLICT(organization_id,resource_type,resource_id) DO UPDATE SET
		external_id=EXCLUDED.external_id,updated_at=now()`, organizationID, teamID, externalID); err != nil {
		return domain.SCIMGroupRecord{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1`, teamID); err != nil {
		return domain.SCIMGroupRecord{}, err
	}
	seen := map[string]struct{}{}
	for _, userID := range memberIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		tag, insertErr := tx.Exec(ctx, `INSERT INTO team_memberships(team_id,organization_id,user_id,role,status)
			SELECT $1,$2,m.user_id,'MEMBER','ACTIVE' FROM organization_memberships m
			WHERE m.organization_id=$2 AND m.user_id=$3 AND m.status='ACTIVE'`, teamID, organizationID, userID)
		if insertErr != nil {
			return domain.SCIMGroupRecord{}, insertErr
		}
		if tag.RowsAffected() == 0 {
			return domain.SCIMGroupRecord{}, fmt.Errorf("SCIM group member %s is not active in the organization", userID)
		}
	}
	if err = writeAuditTx(ctx, tx, nil, "scim.group_upserted", "team", teamID,
		map[string]any{"organization_id": organizationID, "external_id": externalID, "member_count": len(seen)}); err != nil {
		return domain.SCIMGroupRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SCIMGroupRecord{}, err
	}
	return s.SCIMGroup(ctx, organizationID, teamID)
}

func (s *Store) DeleteSCIMGroup(ctx context.Context, organizationID, teamID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE teams SET status='ARCHIVED',updated_at=now() WHERE id=$1 AND organization_id=$2
		AND EXISTS(SELECT 1 FROM scim_resource_links WHERE organization_id=$2 AND resource_type='GROUP' AND resource_id=$1)`,
		teamID, organizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1`, teamID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM scim_resource_links WHERE organization_id=$1 AND resource_type='GROUP' AND resource_id=$2`,
		organizationID, teamID); err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, nil, "scim.group_deleted", "team", teamID,
		map[string]any{"organization_id": organizationID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
