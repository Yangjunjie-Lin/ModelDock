package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrSupplierNotOwner         = errors.New("supplier organization access denied")
	ErrSupplierNotReady         = errors.New("supplier application is not ready for approval")
	ErrSupplierApprovalRequired = errors.New("supplier approval must be performed by an administrator")
)

const supplierColumns = `id,organization_id,owner_user_id,legal_name,display_name,registration_number,incorporation_country,website,kyb_status,contract_status,contract_version,contract_start_at,contract_end_at,status,payout_account_last4,payout_currency,tax_id,tax_country,tax_residency,tax_form_type,version,created_at,updated_at`

func scanSupplier(row pgx.Row) (domain.SupplierOrganization, error) {
	var out domain.SupplierOrganization
	err := row.Scan(&out.ID, &out.OrganizationID, &out.OwnerUserID, &out.LegalName, &out.DisplayName, &out.RegistrationNumber, &out.IncorporationCountry, &out.Website, &out.KYBStatus, &out.ContractStatus, &out.ContractVersion, &out.ContractStartAt, &out.ContractEndAt, &out.Status, &out.PayoutAccountLast4, &out.PayoutCurrency, &out.TaxID, &out.TaxCountry, &out.TaxResidency, &out.TaxFormType, &out.Version, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func (s *Store) SupplierByID(ctx context.Context, supplierID string, includeEvidence bool) (domain.SupplierOrganization, error) {
	out, err := scanSupplier(s.pool.QueryRow(ctx, `SELECT `+supplierColumns+` FROM supplier_organizations WHERE id=$1`, supplierID))
	if err != nil {
		return out, err
	}
	if includeEvidence {
		if err = s.loadSupplierEvidence(ctx, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Store) SupplierByOwner(ctx context.Context, userID string) (domain.SupplierOrganization, error) {
	return scanSupplier(s.pool.QueryRow(ctx, `SELECT `+supplierColumns+` FROM supplier_organizations WHERE owner_user_id=$1 AND status<>'EXITED' ORDER BY created_at DESC LIMIT 1`, userID))
}

func (s *Store) ListSuppliers(ctx context.Context, status string, limit, offset int) ([]domain.SupplierOrganization, error) {
	query := `SELECT ` + supplierColumns + ` FROM supplier_organizations`
	args := []any{}
	if strings.TrimSpace(status) != "" {
		query += ` WHERE status=$1`
		args = append(args, strings.ToUpper(strings.TrimSpace(status)))
	}
	query += ` ORDER BY created_at DESC,id LIMIT $` + itoa(len(args)+1) + ` OFFSET $` + itoa(len(args)+2)
	args = append(args, clamp(limit), max(offset, 0))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupplierOrganization, 0)
	for rows.Next() {
		item, scanErr := scanSupplier(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateSupplier(ctx context.Context, supplier domain.SupplierOrganization, ownerUserID string, payoutEncrypted []byte, payoutLast4 string, contact domain.SupplierContact) (domain.SupplierOrganization, error) {
	supplier.ID, supplier.OrganizationID = id.UUID(), id.UUID()
	supplier.OwnerUserID = ownerUserID
	supplier.Status, supplier.KYBStatus, supplier.ContractStatus = "DRAFT", "NOT_STARTED", "NOT_STARTED"
	if supplier.PayoutCurrency == "" {
		supplier.PayoutCurrency = "USD"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return supplier, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,billing_region,metadata,allowed_provider_ids,prohibited_provider_ids,required_data_regions,minimum_gross_margin) VALUES($1,$2,$3,'ACTIVE',$4,'{}'::jsonb,'[]'::jsonb,'[]'::jsonb,'[]'::jsonb,0)`, supplier.OrganizationID, supplier.DisplayName, strings.ToLower(supplier.ID), supplier.IncorporationCountry); err != nil {
		return supplier, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status) VALUES($1,$2,'OWNER','ACTIVE')`, supplier.OrganizationID, ownerUserID); err != nil {
		return supplier, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_organizations(id,organization_id,owner_user_id,legal_name,display_name,registration_number,incorporation_country,website,payout_account_encrypted,payout_account_last4,payout_currency,tax_id,tax_country,tax_residency,tax_form_type) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, supplier.ID, supplier.OrganizationID, ownerUserID, strings.TrimSpace(supplier.LegalName), strings.TrimSpace(supplier.DisplayName), strings.TrimSpace(supplier.RegistrationNumber), strings.ToUpper(strings.TrimSpace(supplier.IncorporationCountry)), strings.TrimSpace(supplier.Website), payoutEncrypted, payoutLast4, supplier.PayoutCurrency, strings.TrimSpace(supplier.TaxID), strings.ToUpper(strings.TrimSpace(supplier.TaxCountry)), strings.TrimSpace(supplier.TaxResidency), strings.TrimSpace(supplier.TaxFormType)); err != nil {
		return supplier, err
	}
	contact.ID, contact.SupplierID = id.UUID(), supplier.ID
	if contact.ContactType == "" {
		contact.ContactType = "PRIMARY"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_contacts(id,supplier_id,contact_type,full_name,title,email,phone) VALUES($1,$2,$3,$4,$5,lower($6),$7)`, contact.ID, supplier.ID, strings.ToUpper(contact.ContactType), strings.TrimSpace(contact.FullName), strings.TrimSpace(contact.Title), strings.TrimSpace(contact.Email), strings.TrimSpace(contact.Phone)); err != nil {
		return supplier, err
	}
	if err = writeAuditTx(ctx, tx, &ownerUserID, "supplier.created", "supplier_organization", supplier.ID, map[string]any{"organization_id": supplier.OrganizationID}); err != nil {
		return supplier, err
	}
	if err = tx.Commit(ctx); err != nil {
		return supplier, err
	}
	return s.SupplierByID(ctx, supplier.ID, true)
}

func (s *Store) UpdateSupplierProfile(ctx context.Context, supplier domain.SupplierOrganization, actor string, payoutEncrypted []byte, payoutLast4 string) (domain.SupplierOrganization, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return supplier, err
	}
	defer tx.Rollback(ctx)
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_user_id FROM supplier_organizations WHERE id=$1 FOR UPDATE`, supplier.ID).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return supplier, ErrNotFound
	}
	if err != nil {
		return supplier, err
	}
	if owner != actor {
		return supplier, ErrSupplierNotOwner
	}
	if _, err = tx.Exec(ctx, `UPDATE supplier_organizations SET legal_name=$2,display_name=$3,registration_number=$4,incorporation_country=$5,website=$6,payout_account_encrypted=COALESCE($7,payout_account_encrypted),payout_account_last4=CASE WHEN $8='' THEN payout_account_last4 ELSE $8 END,payout_currency=$9,tax_id=$10,tax_country=$11,tax_residency=$12,tax_form_type=$13,version=version+1,updated_at=now() WHERE id=$1`, supplier.ID, strings.TrimSpace(supplier.LegalName), strings.TrimSpace(supplier.DisplayName), strings.TrimSpace(supplier.RegistrationNumber), strings.ToUpper(strings.TrimSpace(supplier.IncorporationCountry)), strings.TrimSpace(supplier.Website), payoutEncrypted, payoutLast4, supplier.PayoutCurrency, strings.TrimSpace(supplier.TaxID), strings.ToUpper(strings.TrimSpace(supplier.TaxCountry)), strings.TrimSpace(supplier.TaxResidency), strings.TrimSpace(supplier.TaxFormType)); err != nil {
		return supplier, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.profile_updated", "supplier_organization", supplier.ID, map[string]any{"payout_account_updated": len(payoutEncrypted) > 0}); err != nil {
		return supplier, err
	}
	if err = tx.Commit(ctx); err != nil {
		return supplier, err
	}
	return s.SupplierByID(ctx, supplier.ID, true)
}

func (s *Store) UpsertSupplierContact(ctx context.Context, supplierID, actor string, contact domain.SupplierContact) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, supplierID, actor) {
		return ErrSupplierNotOwner
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_contacts(id,supplier_id,contact_type,full_name,title,email,phone) VALUES($1,$2,$3,$4,$5,lower($6),$7) ON CONFLICT(supplier_id,contact_type) DO UPDATE SET full_name=EXCLUDED.full_name,title=EXCLUDED.title,email=EXCLUDED.email,phone=EXCLUDED.phone,updated_at=now()`, id.UUID(), supplierID, strings.ToUpper(contact.ContactType), strings.TrimSpace(contact.FullName), strings.TrimSpace(contact.Title), strings.TrimSpace(contact.Email), strings.TrimSpace(contact.Phone))
	if err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.contact_updated", "supplier_contact", supplierID, map[string]any{"contact_type": contact.ContactType}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sponsorOwnerTx(ctx context.Context, tx pgx.Tx, supplierID, actor string) bool {
	var ok bool
	_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM supplier_organizations WHERE id=$1 AND owner_user_id=$2)`, supplierID, actor).Scan(&ok)
	return ok
}

func (s *Store) SubmitSupplier(ctx context.Context, supplierID, actor string) (domain.SupplierOrganization, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierOrganization{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM supplier_organizations WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, supplierID, actor).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierOrganization{}, ErrSupplierNotOwner
	}
	if err != nil {
		return domain.SupplierOrganization{}, err
	}
	if status != "DRAFT" && status != "REJECTED" {
		return domain.SupplierOrganization{}, errors.New("supplier application cannot be submitted in its current state")
	}
	if _, err = tx.Exec(ctx, `UPDATE supplier_organizations SET status='SUBMITTED',version=version+1,updated_at=now() WHERE id=$1`, supplierID); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_status_events(supplier_id,from_status,to_status,actor_id,reason) VALUES($1,$2,'SUBMITTED',$3,'supplier submitted application')`, supplierID, status, actor); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.submitted", "supplier_organization", supplierID, nil); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierOrganization{}, err
	}
	return s.SupplierByID(ctx, supplierID, true)
}

func (s *Store) RequestSupplierExit(ctx context.Context, supplierID, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM supplier_organizations WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, supplierID, actor).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return ErrSupplierNotOwner
	}
	if err != nil {
		return err
	}
	if status == "EXITED" || status == "EXIT_REQUESTED" {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE supplier_organizations SET status='EXIT_REQUESTED',version=version+1,updated_at=now() WHERE id=$1`, supplierID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_status_events(supplier_id,from_status,to_status,actor_id,reason) VALUES($1,$2,'EXIT_REQUESTED',$3,'supplier requested exit')`, supplierID, status, actor); err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.exit_requested", "supplier_organization", supplierID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateSupplierEndpoint(ctx context.Context, endpoint domain.SupplierEndpoint, actor string, challengeHash []byte) (domain.SupplierEndpoint, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return endpoint, err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, endpoint.SupplierID, actor) {
		return endpoint, ErrSupplierNotOwner
	}
	endpoint.ID = id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO supplier_endpoints(id,supplier_id,endpoint_url,challenge_hash) VALUES($1,$2,$3,$4)`, endpoint.ID, endpoint.SupplierID, endpoint.EndpointURL, challengeHash)
	if err != nil {
		return endpoint, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.endpoint_created", "supplier_endpoint", endpoint.ID, map[string]any{"endpoint_url": endpoint.EndpointURL}); err != nil {
		return endpoint, err
	}
	if err = tx.Commit(ctx); err != nil {
		return endpoint, err
	}
	return s.SupplierEndpointByID(ctx, endpoint.ID)
}

func (s *Store) SupplierEndpointByID(ctx context.Context, endpointID string) (domain.SupplierEndpoint, error) {
	var out domain.SupplierEndpoint
	var ip *string
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,endpoint_url,verification_status,isolation_status,verified_at,host(last_checked_ip),last_error,created_at,updated_at FROM supplier_endpoints WHERE id=$1`, endpointID).Scan(&out.ID, &out.SupplierID, &out.EndpointURL, &out.VerificationStatus, &out.IsolationStatus, &out.VerifiedAt, &ip, &out.LastError, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if ip != nil {
		out.LastCheckedIP = *ip
	}
	return out, err
}

func (s *Store) SupplierEndpointChallengeValid(ctx context.Context, endpointID, token string) bool {
	var expected []byte
	if err := s.pool.QueryRow(ctx, `SELECT challenge_hash FROM supplier_endpoints WHERE id=$1 AND verification_status<>'VERIFIED'`, endpointID).Scan(&expected); err != nil {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return len(expected) == len(actual) && subtle.ConstantTimeCompare(expected, actual[:]) == 1
}

func (s *Store) MarkSupplierEndpointVerification(ctx context.Context, endpointID, actor, ip string, passed bool, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var supplierID, owner string
	if err = tx.QueryRow(ctx, `SELECT e.supplier_id,s.owner_user_id FROM supplier_endpoints e JOIN supplier_organizations s ON s.id=e.supplier_id WHERE e.id=$1 FOR UPDATE`, endpointID).Scan(&supplierID, &owner); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != actor {
		return ErrSupplierNotOwner
	}
	if passed {
		_, err = tx.Exec(ctx, `UPDATE supplier_endpoints SET verification_status='VERIFIED',isolation_status='PASSED',verified_at=now(),last_checked_ip=NULLIF($2,'')::inet,last_error='',updated_at=now() WHERE id=$1`, endpointID, ip)
	} else {
		_, err = tx.Exec(ctx, `UPDATE supplier_endpoints SET verification_status='FAILED',isolation_status='FAILED',last_error=$2,updated_at=now() WHERE id=$1`, endpointID, reason)
	}
	if err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.endpoint_verification", "supplier_endpoint", endpointID, map[string]any{"passed": passed, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateSupplierCredential(ctx context.Context, credential domain.SupplierCredential, actor string, encrypted []byte) (domain.SupplierCredential, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return credential, err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, credential.SupplierID, actor) {
		return credential, ErrSupplierNotOwner
	}
	if credential.ID == "" {
		credential.ID = id.UUID()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_credentials(id,supplier_id,provider_id,name,credential_type,encrypted_secret,secret_last4) VALUES($1,$2,$3,$4,$5,$6,$7)`, credential.ID, credential.SupplierID, credential.ProviderID, strings.TrimSpace(credential.Name), credential.CredentialType, encrypted, credential.SecretLast4); err != nil {
		return credential, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.credential_created", "supplier_credential", credential.ID, map[string]any{"supplier_id": credential.SupplierID, "provider_id": credential.ProviderID}); err != nil {
		return credential, err
	}
	if err = tx.Commit(ctx); err != nil {
		return credential, err
	}
	return s.SupplierCredentialByID(ctx, credential.ID)
}

func (s *Store) SupplierCredentialByID(ctx context.Context, credentialID string) (domain.SupplierCredential, error) {
	var out domain.SupplierCredential
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,provider_id,name,credential_type,secret_last4,status,created_at,updated_at FROM supplier_credentials WHERE id=$1`, credentialID).Scan(&out.ID, &out.SupplierID, &out.ProviderID, &out.Name, &out.CredentialType, &out.SecretLast4, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func (s *Store) CreateSupplierResidency(ctx context.Context, value domain.SupplierResidencyDeclaration, actor string) (domain.SupplierResidencyDeclaration, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, value.SupplierID, actor) {
		return value, ErrSupplierNotOwner
	}
	value.ID = id.UUID()
	if value.ProcessingRegions == nil {
		value.ProcessingRegions = []string{}
	}
	if value.StorageRegions == nil {
		value.StorageRegions = []string{}
	}
	if value.Subprocessors == nil {
		value.Subprocessors = []string{}
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_data_residency_declarations(id,supplier_id,endpoint_id,processing_regions,storage_regions,cross_border_transfer,retention_days,subprocessors,attestation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, value.ID, value.SupplierID, value.EndpointID, jsonBytes(value.ProcessingRegions), jsonBytes(value.StorageRegions), value.CrossBorderTransfer, value.RetentionDays, jsonBytes(value.Subprocessors), strings.TrimSpace(value.Attestation))
	if err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.residency_submitted", "supplier_residency", value.ID, map[string]any{"supplier_id": value.SupplierID, "endpoint_id": value.EndpointID, "processing_region_count": len(value.ProcessingRegions), "storage_region_count": len(value.StorageRegions), "cross_border_transfer": value.CrossBorderTransfer}); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return s.SupplierResidencyByID(ctx, value.ID)
}
func (s *Store) SupplierResidencyByID(ctx context.Context, idv string) (domain.SupplierResidencyDeclaration, error) {
	var out domain.SupplierResidencyDeclaration
	var p, r, sp []byte
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,endpoint_id,processing_regions,storage_regions,cross_border_transfer,retention_days,subprocessors,attestation,status,created_at,updated_at FROM supplier_data_residency_declarations WHERE id=$1`, idv).Scan(&out.ID, &out.SupplierID, &out.EndpointID, &p, &r, &out.CrossBorderTransfer, &out.RetentionDays, &sp, &out.Attestation, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	_ = json.Unmarshal(p, &out.ProcessingRegions)
	_ = json.Unmarshal(r, &out.StorageRegions)
	_ = json.Unmarshal(sp, &out.Subprocessors)
	return out, err
}

func (s *Store) CreateSupplierQuestionnaire(ctx context.Context, value domain.SupplierSecurityQuestionnaire, actor string) (domain.SupplierSecurityQuestionnaire, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, value.SupplierID, actor) {
		return value, ErrSupplierNotOwner
	}
	value.ID = id.UUID()
	if value.Answers == nil {
		value.Answers = map[string]any{}
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_security_questionnaires(id,supplier_id,version,answers,submitted_at) VALUES($1,$2,$3,$4,now())`, value.ID, value.SupplierID, value.Version, jsonBytes(value.Answers))
	if err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.security_questionnaire_submitted", "supplier_security_questionnaire", value.ID, map[string]any{"version": value.Version}); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return s.SupplierQuestionnaireByID(ctx, value.ID)
}
func (s *Store) SupplierQuestionnaireByID(ctx context.Context, idv string) (domain.SupplierSecurityQuestionnaire, error) {
	var out domain.SupplierSecurityQuestionnaire
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,version,answers,status,submitted_at,reviewed_at,reviewed_by,created_at FROM supplier_security_questionnaires WHERE id=$1`, idv).Scan(&out.ID, &out.SupplierID, &out.Version, &raw, &out.Status, &out.SubmittedAt, &out.ReviewedAt, &out.ReviewedBy, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	_ = json.Unmarshal(raw, &out.Answers)
	return out, err
}

func (s *Store) CreateSupplierModel(ctx context.Context, value domain.SupplierModelApplication, actor string) (domain.SupplierModelApplication, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, value.SupplierID, actor) {
		return value, ErrSupplierNotOwner
	}
	var endpointSupplier string
	err = tx.QueryRow(ctx, `SELECT supplier_id FROM supplier_endpoints WHERE id=$1`, value.EndpointID).Scan(&endpointSupplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	if err != nil {
		return value, err
	}
	if endpointSupplier != value.SupplierID {
		return value, ErrSupplierNotOwner
	}
	value.ID = id.UUID()
	if value.Capabilities == nil {
		value.Capabilities = []string{}
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_model_applications(id,supplier_id,endpoint_id,model_name,model_type,capabilities,data_residency_declaration_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, value.ID, value.SupplierID, value.EndpointID, strings.TrimSpace(value.ModelName), value.ModelType, jsonBytes(value.Capabilities), value.ResidencyDeclarationID)
	if err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.model_application_submitted", "supplier_model_application", value.ID, value); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return s.SupplierModelByID(ctx, value.ID)
}
func (s *Store) SupplierModelByID(ctx context.Context, idv string) (domain.SupplierModelApplication, error) {
	var out domain.SupplierModelApplication
	var caps []byte
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,endpoint_id,model_name,model_type,capabilities,data_residency_declaration_id,status,review_reason,created_at,updated_at FROM supplier_model_applications WHERE id=$1`, idv).Scan(&out.ID, &out.SupplierID, &out.EndpointID, &out.ModelName, &out.ModelType, &caps, &out.ResidencyDeclarationID, &out.Status, &out.ReviewReason, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	_ = json.Unmarshal(caps, &out.Capabilities)
	return out, err
}

func (s *Store) CreateSupplierPrice(ctx context.Context, value domain.SupplierPriceApplication, actor string) (domain.SupplierPriceApplication, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	if !sponsorOwnerTx(ctx, tx, value.SupplierID, actor) {
		return value, ErrSupplierNotOwner
	}
	var modelSupplier string
	err = tx.QueryRow(ctx, `SELECT supplier_id FROM supplier_model_applications WHERE id=$1`, value.ModelApplicationID).Scan(&modelSupplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	if err != nil {
		return value, err
	}
	if modelSupplier != value.SupplierID {
		return value, ErrSupplierNotOwner
	}
	value.ID = id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO supplier_price_applications(id,supplier_id,model_application_id,input_token_price,cached_input_token_price,output_token_price,request_fixed_price,currency,unit) VALUES($1,$2,$3,$4::numeric,$5::numeric,$6::numeric,$7::numeric,$8,$9)`, value.ID, value.SupplierID, value.ModelApplicationID, value.InputTokenPrice, value.CachedInputTokenPrice, value.OutputTokenPrice, value.RequestFixedPrice, strings.ToUpper(value.Currency), value.Unit)
	if err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.price_application_submitted", "supplier_price_application", value.ID, map[string]any{"supplier_id": value.SupplierID, "model_application_id": value.ModelApplicationID, "currency": value.Currency}); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return s.SupplierPriceByID(ctx, value.ID)
}
func (s *Store) SupplierPriceByID(ctx context.Context, idv string) (domain.SupplierPriceApplication, error) {
	var out domain.SupplierPriceApplication
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,model_application_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,status,review_reason,created_at,updated_at FROM supplier_price_applications WHERE id=$1`, idv).Scan(&out.ID, &out.SupplierID, &out.ModelApplicationID, &out.InputTokenPrice, &out.CachedInputTokenPrice, &out.OutputTokenPrice, &out.RequestFixedPrice, &out.Currency, &out.Unit, &out.Status, &out.ReviewReason, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func (s *Store) ReviewSupplier(ctx context.Context, supplierID, decision, reason, actor string) (domain.SupplierOrganization, error) {
	decision = strings.ToUpper(strings.TrimSpace(decision))
	if decision == "APPROVED" && strings.TrimSpace(reason) == "" {
		return domain.SupplierOrganization{}, errors.New("an approval reason is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierOrganization{}, err
	}
	defer tx.Rollback(ctx)
	if !ensureSupplierAdminTx(ctx, tx, actor) {
		return domain.SupplierOrganization{}, ErrSupplierApprovalRequired
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err != nil {
		return domain.SupplierOrganization{}, err
	}
	var current, kyb, contract string
	if err = tx.QueryRow(ctx, `SELECT status,kyb_status,contract_status FROM supplier_organizations WHERE id=$1 FOR UPDATE`, supplierID).Scan(&current, &kyb, &contract); errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierOrganization{}, ErrNotFound
	}
	if err != nil {
		return domain.SupplierOrganization{}, err
	}
	newStatus := map[string]string{"APPROVED": "APPROVED", "REJECTED": "REJECTED", "REQUESTED_CHANGES": "IN_REVIEW", "SUSPENDED": "SUSPENDED", "EXITED": "EXITED"}[decision]
	if newStatus == "" {
		return domain.SupplierOrganization{}, errors.New("invalid supplier review decision")
	}
	if decision == "APPROVED" {
		var endpoints, questionnaires int
		if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE verification_status='VERIFIED' AND isolation_status='PASSED'),count(*) FILTER (WHERE status='APPROVED') FROM supplier_endpoints e LEFT JOIN supplier_security_questionnaires q ON q.supplier_id=e.supplier_id WHERE e.supplier_id=$1`, supplierID).Scan(&endpoints, &questionnaires); err != nil {
			return domain.SupplierOrganization{}, err
		}
		if kyb != "VERIFIED" || contract != "ACTIVE" || endpoints < 1 || questionnaires < 1 {
			return domain.SupplierOrganization{}, ErrSupplierNotReady
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE supplier_organizations SET status=$2,version=version+1,updated_at=now() WHERE id=$1`, supplierID, newStatus); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_reviews(supplier_id,reviewer_id,decision,reason) VALUES($1,$2,$3,$4)`, supplierID, actor, decision, reason); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO supplier_status_events(supplier_id,from_status,to_status,actor_id,reason) VALUES($1,$2,$3,$4,$5)`, supplierID, current, newStatus, actor, reason); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.reviewed", "supplier_organization", supplierID, map[string]any{"decision": decision, "reason": reason}); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierOrganization{}, err
	}
	return s.SupplierByID(ctx, supplierID, true)
}

func (s *Store) UpdateSupplierCompliance(ctx context.Context, supplierID, kybStatus, contractStatus, contractVersion string, startAt, endAt *time.Time, actor string) (domain.SupplierOrganization, error) {
	kybStatus, contractStatus = strings.ToUpper(strings.TrimSpace(kybStatus)), strings.ToUpper(strings.TrimSpace(contractStatus))
	if kybStatus != "NOT_STARTED" && kybStatus != "PENDING" && kybStatus != "VERIFIED" && kybStatus != "REJECTED" && kybStatus != "EXPIRED" {
		return domain.SupplierOrganization{}, errors.New("invalid KYB status")
	}
	if contractStatus != "NOT_STARTED" && contractStatus != "PENDING" && contractStatus != "ACTIVE" && contractStatus != "EXPIRED" && contractStatus != "TERMINATED" {
		return domain.SupplierOrganization{}, errors.New("invalid contract status")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierOrganization{}, err
	}
	defer tx.Rollback(ctx)
	if !ensureSupplierAdminTx(ctx, tx, actor) {
		return domain.SupplierOrganization{}, ErrSupplierApprovalRequired
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err != nil {
		return domain.SupplierOrganization{}, err
	}
	var oldKYB, oldContract string
	if err = tx.QueryRow(ctx, `SELECT kyb_status,contract_status FROM supplier_organizations WHERE id=$1 FOR UPDATE`, supplierID).Scan(&oldKYB, &oldContract); errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierOrganization{}, ErrNotFound
	}
	if err != nil {
		return domain.SupplierOrganization{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE supplier_organizations SET kyb_status=$2,contract_status=$3,contract_version=$4,contract_start_at=$5,contract_end_at=$6,version=version+1,updated_at=now() WHERE id=$1`, supplierID, kybStatus, contractStatus, strings.TrimSpace(contractVersion), startAt, endAt); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.compliance_updated", "supplier_organization", supplierID, map[string]any{"kyb_status": kybStatus, "contract_status": contractStatus, "previous_kyb_status": oldKYB, "previous_contract_status": oldContract}); err != nil {
		return domain.SupplierOrganization{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierOrganization{}, err
	}
	return s.SupplierByID(ctx, supplierID, true)
}

func ensureSupplierAdminTx(ctx context.Context, tx pgx.Tx, actor string) bool {
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND status='ACTIVE'`, actor).Scan(&role); err != nil {
		return false
	}
	return role == "ADMIN" || role == "SUPER_ADMIN"
}

func (s *Store) ReviewSupplierEvidence(ctx context.Context, evidenceType, evidenceID, status, reason, actor string) error {
	evidenceType, status = strings.ToUpper(strings.TrimSpace(evidenceType)), strings.ToUpper(strings.TrimSpace(status))
	if status != "IN_REVIEW" && status != "APPROVED" && status != "REJECTED" {
		return errors.New("invalid supplier evidence status")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if !ensureSupplierAdminTx(ctx, tx, actor) {
		return ErrSupplierApprovalRequired
	}
	var tag pgconn.CommandTag
	switch evidenceType {
	case "QUESTIONNAIRE":
		if status == "IN_REVIEW" {
			return errors.New("questionnaire status must be APPROVED or REJECTED")
		}
		tag, err = tx.Exec(ctx, `UPDATE supplier_security_questionnaires SET status=$2,reviewed_at=now(),reviewed_by=$3 WHERE id=$1`, evidenceID, status, actor)
	case "MODEL":
		tag, err = tx.Exec(ctx, `UPDATE supplier_model_applications SET status=$2,review_reason=$3,updated_at=now() WHERE id=$1`, evidenceID, status, strings.TrimSpace(reason))
	case "PRICE":
		tag, err = tx.Exec(ctx, `UPDATE supplier_price_applications SET status=$2,review_reason=$3,updated_at=now() WHERE id=$1`, evidenceID, status, strings.TrimSpace(reason))
	case "RESIDENCY":
		if status == "IN_REVIEW" {
			return errors.New("residency status must be APPROVED or REJECTED")
		}
		tag, err = tx.Exec(ctx, `UPDATE supplier_data_residency_declarations SET status=$2,updated_at=now() WHERE id=$1`, evidenceID, status)
	default:
		return errors.New("invalid supplier evidence type")
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.evidence_reviewed", "supplier_"+strings.ToLower(evidenceType), evidenceID, map[string]any{"status": status, "reason": strings.TrimSpace(reason)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) loadSupplierEvidence(ctx context.Context, out *domain.SupplierOrganization) error {
	rows, err := s.pool.Query(ctx, `SELECT id,supplier_id,contact_type,full_name,title,email,phone,created_at,updated_at FROM supplier_contacts WHERE supplier_id=$1 ORDER BY contact_type`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierContact
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.ContactType, &v.FullName, &v.Title, &v.Email, &v.Phone, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		out.Contacts = append(out.Contacts, v)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id,supplier_id,endpoint_url,verification_status,isolation_status,verified_at,host(last_checked_ip),last_error,created_at,updated_at FROM supplier_endpoints WHERE supplier_id=$1 ORDER BY created_at`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierEndpoint
		var ip *string
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.EndpointURL, &v.VerificationStatus, &v.IsolationStatus, &v.VerifiedAt, &ip, &v.LastError, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		if ip != nil {
			v.LastCheckedIP = *ip
		}
		out.Endpoints = append(out.Endpoints, v)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id,supplier_id,provider_id,name,credential_type,secret_last4,status,created_at,updated_at FROM supplier_credentials WHERE supplier_id=$1 ORDER BY created_at`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierCredential
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.ProviderID, &v.Name, &v.CredentialType, &v.SecretLast4, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		out.Credentials = append(out.Credentials, v)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id,supplier_id,endpoint_id,processing_regions,storage_regions,cross_border_transfer,retention_days,subprocessors,attestation,status,created_at,updated_at FROM supplier_data_residency_declarations WHERE supplier_id=$1 ORDER BY created_at`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierResidencyDeclaration
		var processing, storage, subprocessors []byte
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.EndpointID, &processing, &storage, &v.CrossBorderTransfer, &v.RetentionDays, &subprocessors, &v.Attestation, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(processing, &v.ProcessingRegions)
		_ = json.Unmarshal(storage, &v.StorageRegions)
		_ = json.Unmarshal(subprocessors, &v.Subprocessors)
		out.Residency = append(out.Residency, v)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id,supplier_id,version,answers,status,submitted_at,reviewed_at,reviewed_by,created_at FROM supplier_security_questionnaires WHERE supplier_id=$1 ORDER BY created_at`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierSecurityQuestionnaire
		var answers []byte
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.Version, &answers, &v.Status, &v.SubmittedAt, &v.ReviewedAt, &v.ReviewedBy, &v.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(answers, &v.Answers)
		out.Questionnaires = append(out.Questionnaires, v)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id,supplier_id,endpoint_id,model_name,model_type,capabilities,data_residency_declaration_id,status,review_reason,created_at,updated_at FROM supplier_model_applications WHERE supplier_id=$1 ORDER BY created_at`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierModelApplication
		var capabilities []byte
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.EndpointID, &v.ModelName, &v.ModelType, &capabilities, &v.ResidencyDeclarationID, &v.Status, &v.ReviewReason, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(capabilities, &v.Capabilities)
		out.Models = append(out.Models, v)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT id,supplier_id,model_application_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,status,review_reason,created_at,updated_at FROM supplier_price_applications WHERE supplier_id=$1 ORDER BY created_at`, out.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v domain.SupplierPriceApplication
		if err = rows.Scan(&v.ID, &v.SupplierID, &v.ModelApplicationID, &v.InputTokenPrice, &v.CachedInputTokenPrice, &v.OutputTokenPrice, &v.RequestFixedPrice, &v.Currency, &v.Unit, &v.Status, &v.ReviewReason, &v.CreatedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		out.Prices = append(out.Prices, v)
	}
	rows.Close()
	return nil
}
