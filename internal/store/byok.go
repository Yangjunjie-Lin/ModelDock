package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
)

func (s *Store) CreateBYOKCredential(ctx context.Context, credential domain.Credential, projectID string) (domain.Credential, error) {
	if credential.CredentialOwner != domain.CredentialOwnerCustomer || credential.OwnerOrganizationID == nil ||
		credential.OwnershipConfirmedAt == nil || credential.OwnershipConfirmedBy == nil || credential.OwnershipTermsVersion == "" {
		return credential, errors.New("BYOK ownership confirmation is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return credential, err
	}
	defer tx.Rollback(ctx)
	var projectOrganizationID string
	if err = tx.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1 AND status='ACTIVE' FOR SHARE`, projectID).Scan(&projectOrganizationID); errors.Is(err, pgx.ErrNoRows) {
		return credential, ErrNotFound
	} else if err != nil {
		return credential, err
	}
	if projectOrganizationID != *credential.OwnerOrganizationID {
		return credential, ErrBYOKOrganizationMismatch
	}
	if credential.BYOKPrioritySection == "" {
		credential.BYOKPrioritySection = "PRIORITIZED"
	}
	if credential.SharedCapacityFallback == "" {
		credential.SharedCapacityFallback = "ALWAYS"
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_credentials(id,provider_id,name,credential_type,encrypted_secret,secret_last4,status,priority,weight,max_concurrency,current_health,
		credential_owner,owner_organization_id,ownership_confirmed_at,ownership_confirmed_by,ownership_terms_version,
		byok_priority_section,shared_capacity_fallback,model_filters,api_key_filters,member_filters)
		VALUES($1,$2,$3,$4,$5,$6,'ACTIVE',$7,$8,$9,'UNKNOWN','CUSTOMER',$10,$11,$12,$13,$14,$15,$16,$17,$18)`, credential.ID, credential.ProviderID, credential.Name,
		credential.CredentialType, credential.EncryptedSecret, credential.SecretLast4, credential.Priority, credential.Weight, credential.MaxConcurrency,
		credential.OwnerOrganizationID, credential.OwnershipConfirmedAt, credential.OwnershipConfirmedBy, credential.OwnershipTermsVersion,
		credential.BYOKPrioritySection, credential.SharedCapacityFallback, jsonBytes(normalizeStrings(credential.ModelFilters)),
		jsonBytes(normalizeStrings(credential.APIKeyFilters)), jsonBytes(normalizeStrings(credential.MemberFilters)))
	if err != nil {
		return credential, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO credential_group_members(group_id,credential_id,weight,priority)
		SELECT DISTINCT r.credential_group_id,$2,$3,$4 FROM project_model_routes pmr JOIN model_routes r ON r.id=pmr.model_route_id
		WHERE pmr.project_id=$1 AND pmr.deleted_at IS NULL AND pmr.enabled AND r.enabled AND r.provider_id=$5
		ON CONFLICT(group_id,credential_id) DO NOTHING`, projectID, credential.ID, credential.Weight, credential.Priority, credential.ProviderID)
	if err != nil {
		return credential, err
	}
	if tag.RowsAffected() == 0 {
		return credential, errors.New("the project has no enabled route for this provider")
	}
	if err = writeAuditTx(ctx, tx, credential.OwnershipConfirmedBy, "byok.credential_created", "provider_credential", credential.ID, map[string]any{
		"provider_id": credential.ProviderID, "organization_id": *credential.OwnerOrganizationID, "project_id": projectID, "terms_version": credential.OwnershipTermsVersion}); err != nil {
		return credential, err
	}
	if err = tx.Commit(ctx); err != nil {
		return credential, err
	}
	return s.CredentialByID(ctx, credential.ID)
}

func (s *Store) ListBYOKCredentials(ctx context.Context, organizationID string) ([]domain.Credential, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+credentialColumns+` FROM provider_credentials c JOIN providers p ON p.id=c.provider_id
		WHERE c.credential_owner='CUSTOMER' AND c.owner_organization_id=$1 ORDER BY c.created_at DESC`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Credential, 0)
	for rows.Next() {
		credential, scanErr := scanCredential(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, credential)
	}
	return out, rows.Err()
}

func (s *Store) DisableBYOKCredential(ctx context.Context, credentialID, organizationID string, actor *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE provider_credentials SET status='DISABLED',updated_at=now() WHERE id=$1 AND credential_owner='CUSTOMER' AND owner_organization_id=$2`, credentialID, organizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, actor, "byok.credential_disabled", "provider_credential", credentialID, map[string]any{"organization_id": organizationID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
