package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/pricing"
)

type providerRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func mapProviderIntegrityError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "credential_group_member_provider_match":
		return ErrCredentialGroupProviderMismatch
	case "model_route_primary_group_provider_match", "model_route_fallback_group_provider_match":
		return ErrModelRouteProviderMismatch
	default:
		return err
	}
}

func ensureGroupMemberProviderMatch(ctx context.Context, q providerRowQuerier, groupID, credentialID string) error {
	var groupProviderID, credentialProviderID string
	err := q.QueryRow(ctx, `SELECT g.provider_id,c.provider_id
		FROM credential_groups g CROSS JOIN provider_credentials c
		WHERE g.id=$1 AND c.id=$2
		FOR KEY SHARE OF g,c`, groupID, credentialID).Scan(&groupProviderID, &credentialProviderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapProviderIntegrityError(err)
	}
	if groupProviderID != credentialProviderID {
		return ErrCredentialGroupProviderMismatch
	}
	return nil
}

func ensureRouteProviderMatch(ctx context.Context, q providerRowQuerier, providerID, primaryGroupID string, fallbackGroupID *string) error {
	var primaryProviderID string
	err := q.QueryRow(ctx, `SELECT provider_id FROM credential_groups WHERE id=$1 FOR KEY SHARE`, primaryGroupID).Scan(&primaryProviderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapProviderIntegrityError(err)
	}
	if primaryProviderID != providerID {
		return ErrModelRouteProviderMismatch
	}
	if fallbackGroupID == nil {
		return nil
	}
	var fallbackProviderID string
	err = q.QueryRow(ctx, `SELECT provider_id FROM credential_groups WHERE id=$1 FOR KEY SHARE`, *fallbackGroupID).Scan(&fallbackProviderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapProviderIntegrityError(err)
	}
	if fallbackProviderID != providerID {
		return ErrModelRouteProviderMismatch
	}
	return nil
}

func (s *Store) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.name,p.slug,p.provider_type,p.base_url,p.enabled,p.config,p.created_at,p.updated_at,(SELECT count(*) FROM provider_credentials c WHERE c.provider_id=p.id),p.contract_status,p.commercial_status,p.allowed_regions,p.pricing_disabled,p.contract_reviewed_at,p.legal_entity,p.contract_type,p.contract_start_at,p.contract_end_at,p.commercial_resale_status,p.credential_owner,p.allowed_customer_regions,p.prohibited_regions,p.data_processing_regions,p.data_retention_policy,p.terms_version,COALESCE(p.cost_limit,0)::text,p.rate_limit,p.settlement_currency,p.emergency_kill_switch FROM providers p ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Provider
	for rows.Next() {
		var p domain.Provider
		var raw, regions, customerRegions, prohibitedRegions, processingRegions []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.ProviderType, &p.BaseURL, &p.Enabled, &raw, &p.CreatedAt, &p.UpdatedAt, &p.CredentialsCount, &p.ContractStatus, &p.CommercialStatus, &regions, &p.PricingDisabled, &p.ContractReviewedAt, &p.LegalEntity, &p.ContractType, &p.ContractStartAt, &p.ContractEndAt, &p.CommercialResaleStatus, &p.CredentialOwner, &customerRegions, &prohibitedRegions, &processingRegions, &p.DataRetentionPolicy, &p.TermsVersion, &p.CostLimit, &p.RateLimit, &p.SettlementCurrency, &p.EmergencyKillSwitch); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &p.Config)
		_ = json.Unmarshal(regions, &p.AllowedRegions)
		_ = json.Unmarshal(customerRegions, &p.AllowedCustomerRegions)
		_ = json.Unmarshal(prohibitedRegions, &p.ProhibitedRegions)
		_ = json.Unmarshal(processingRegions, &p.DataProcessingRegions)
		p.CommercialStatus = domain.ProviderCommercialStatus(p.CommercialStatus, p.ContractReviewedAt != nil)
		if p.Config == nil {
			p.Config = map[string]any{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProviderByID(ctx context.Context, providerID string) (domain.Provider, error) {
	var p domain.Provider
	var raw, regions, customerRegions, prohibitedRegions, processingRegions []byte
	err := s.pool.QueryRow(ctx, `SELECT id,name,slug,provider_type,base_url,enabled,config,created_at,updated_at,contract_status,commercial_status,allowed_regions,pricing_disabled,contract_reviewed_at,legal_entity,contract_type,contract_start_at,contract_end_at,commercial_resale_status,credential_owner,allowed_customer_regions,prohibited_regions,data_processing_regions,data_retention_policy,terms_version,COALESCE(cost_limit,0)::text,rate_limit,settlement_currency,emergency_kill_switch FROM providers WHERE id=$1`, providerID).
		Scan(&p.ID, &p.Name, &p.Slug, &p.ProviderType, &p.BaseURL, &p.Enabled, &raw, &p.CreatedAt, &p.UpdatedAt, &p.ContractStatus, &p.CommercialStatus, &regions, &p.PricingDisabled, &p.ContractReviewedAt, &p.LegalEntity, &p.ContractType, &p.ContractStartAt, &p.ContractEndAt, &p.CommercialResaleStatus, &p.CredentialOwner, &customerRegions, &prohibitedRegions, &processingRegions, &p.DataRetentionPolicy, &p.TermsVersion, &p.CostLimit, &p.RateLimit, &p.SettlementCurrency, &p.EmergencyKillSwitch)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(raw, &p.Config)
	_ = json.Unmarshal(regions, &p.AllowedRegions)
	_ = json.Unmarshal(customerRegions, &p.AllowedCustomerRegions)
	_ = json.Unmarshal(prohibitedRegions, &p.ProhibitedRegions)
	_ = json.Unmarshal(processingRegions, &p.DataProcessingRegions)
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	if p.ContractStatus == "" {
		p.ContractStatus = "ACTIVE"
	}
	p.CommercialStatus = domain.ProviderCommercialStatus(p.CommercialStatus, p.ContractReviewedAt != nil)
	if len(p.AllowedRegions) == 0 {
		p.AllowedRegions = []string{"*"}
	}
	return p, nil
}

func (s *Store) ProviderBySlug(ctx context.Context, slug string) (domain.Provider, error) {
	var id string
	if err := s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug=$1`, slug).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return domain.Provider{}, ErrNotFound
	} else if err != nil {
		return domain.Provider{}, err
	}
	return s.ProviderByID(ctx, id)
}

func (s *Store) CreateProvider(ctx context.Context, p domain.Provider) (domain.Provider, error) {
	p.ID = id.UUID()
	if p.ProviderType == "" {
		p.ProviderType = p.Slug
	}
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	if p.ContractStatus == "" {
		p.ContractStatus = "PENDING_REVIEW"
	}
	if p.CommercialStatus == "" {
		p.CommercialStatus = domain.ProviderCommercialStatus(p.ContractStatus, p.ContractReviewedAt != nil)
	}
	if len(p.AllowedRegions) == 0 {
		p.AllowedRegions = []string{"*"}
	}
	if len(p.AllowedCustomerRegions) == 0 {
		p.AllowedCustomerRegions = append([]string(nil), p.AllowedRegions...)
	}
	if p.ProhibitedRegions == nil {
		p.ProhibitedRegions = []string{}
	}
	if p.DataProcessingRegions == nil {
		p.DataProcessingRegions = []string{}
	}
	if p.CredentialOwner == "" {
		p.CredentialOwner = domain.CredentialOwnerPlatform
	}
	if p.CommercialResaleStatus == "" {
		p.CommercialResaleStatus = "NOT_APPROVED"
	}
	if p.ContractType == "" {
		p.ContractType = "UNSPECIFIED"
	}
	if p.SettlementCurrency == "" {
		p.SettlementCurrency = "USD"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO providers(id,name,slug,provider_type,base_url,enabled,config,contract_status,commercial_status,allowed_regions,pricing_disabled,contract_reviewed_at,legal_entity,contract_type,contract_start_at,contract_end_at,commercial_resale_status,credential_owner,allowed_customer_regions,prohibited_regions,data_processing_regions,data_retention_policy,terms_version,cost_limit,rate_limit,settlement_currency,emergency_kill_switch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)`, p.ID, p.Name, strings.ToLower(p.Slug), p.ProviderType, strings.TrimRight(p.BaseURL, "/"), p.Enabled, jsonBytes(p.Config), p.ContractStatus, p.CommercialStatus, jsonBytes(p.AllowedRegions), p.PricingDisabled, p.ContractReviewedAt, p.LegalEntity, p.ContractType, p.ContractStartAt, p.ContractEndAt, p.CommercialResaleStatus, p.CredentialOwner, jsonBytes(p.AllowedCustomerRegions), jsonBytes(p.ProhibitedRegions), jsonBytes(p.DataProcessingRegions), p.DataRetentionPolicy, p.TermsVersion, nullString(p.CostLimit), p.RateLimit, p.SettlementCurrency, p.EmergencyKillSwitch)
	if err != nil {
		return domain.Provider{}, err
	}
	return s.ProviderByID(ctx, p.ID)
}

func (s *Store) UpdateProvider(ctx context.Context, p domain.Provider) (domain.Provider, error) {
	if p.ContractStatus == "" {
		p.ContractStatus = "PENDING_REVIEW"
	}
	if p.CommercialStatus == "" {
		p.CommercialStatus = domain.ProviderCommercialStatus(p.ContractStatus, p.ContractReviewedAt != nil)
	}
	if len(p.AllowedRegions) == 0 {
		p.AllowedRegions = []string{"*"}
	}
	if len(p.AllowedCustomerRegions) == 0 {
		p.AllowedCustomerRegions = append([]string(nil), p.AllowedRegions...)
	}
	if p.ProhibitedRegions == nil {
		p.ProhibitedRegions = []string{}
	}
	if p.DataProcessingRegions == nil {
		p.DataProcessingRegions = []string{}
	}
	if p.CredentialOwner == "" {
		p.CredentialOwner = domain.CredentialOwnerPlatform
	}
	if p.CommercialResaleStatus == "" {
		p.CommercialResaleStatus = "NOT_APPROVED"
	}
	if p.ContractType == "" {
		p.ContractType = "UNSPECIFIED"
	}
	if p.SettlementCurrency == "" {
		p.SettlementCurrency = "USD"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE providers SET name=$2,base_url=$3,enabled=$4,config=$5,contract_status=$6,commercial_status=$7,allowed_regions=$8,pricing_disabled=$9,contract_reviewed_at=$10,legal_entity=$11,contract_type=$12,contract_start_at=$13,contract_end_at=$14,commercial_resale_status=$15,credential_owner=$16,allowed_customer_regions=$17,prohibited_regions=$18,data_processing_regions=$19,data_retention_policy=$20,terms_version=$21,cost_limit=$22,rate_limit=$23,settlement_currency=$24,emergency_kill_switch=$25,updated_at=now() WHERE id=$1`, p.ID, p.Name, strings.TrimRight(p.BaseURL, "/"), p.Enabled, jsonBytes(p.Config), p.ContractStatus, p.CommercialStatus, jsonBytes(p.AllowedRegions), p.PricingDisabled, p.ContractReviewedAt, p.LegalEntity, p.ContractType, p.ContractStartAt, p.ContractEndAt, p.CommercialResaleStatus, p.CredentialOwner, jsonBytes(p.AllowedCustomerRegions), jsonBytes(p.ProhibitedRegions), jsonBytes(p.DataProcessingRegions), p.DataRetentionPolicy, p.TermsVersion, nullString(p.CostLimit), p.RateLimit, p.SettlementCurrency, p.EmergencyKillSwitch)
	if err != nil {
		return domain.Provider{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Provider{}, ErrNotFound
	}
	return s.ProviderByID(ctx, p.ID)
}

func (s *Store) SetProviderKillSwitch(ctx context.Context, providerID string, enabled bool, actor *string) (domain.Provider, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Provider{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE providers SET emergency_kill_switch=$2,updated_at=now() WHERE id=$1`, providerID, enabled)
	if err != nil {
		return domain.Provider{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Provider{}, ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, actor, "provider.emergency_kill_switch", "provider", providerID, map[string]any{"enabled": enabled}); err != nil {
		return domain.Provider{}, err
	}
	if enabled {
		if _, err = tx.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,resource_type,resource_id) VALUES($1,'provider_kill_switch','critical','Provider emergency kill switch stopped new traffic.','provider',$2)`, id.UUID(), providerID); err != nil {
			return domain.Provider{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Provider{}, err
	}
	return s.ProviderByID(ctx, providerID)
}

func (s *Store) DeleteProvider(ctx context.Context, providerID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM providers WHERE id=$1 AND slug<>'openai'`, providerID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func scanCredential(row pgx.Row) (domain.Credential, error) {
	var c domain.Credential
	var tags, modelFilters, apiKeyFilters, memberFilters []byte
	err := row.Scan(&c.ID, &c.ProviderID, &c.ProviderName, &c.GroupName, &c.Name, &c.CredentialType, &c.EncryptedSecret, &c.SecretLast4, &c.OrganizationID, &c.ProjectID, &c.Status, &c.Priority, &c.Weight, &c.MaxConcurrency, &c.CurrentHealth, &c.LastSuccessAt, &c.LastFailureAt, &c.CooldownUntil, &c.CreatedAt, &c.UpdatedAt, &tags, &c.CredentialOwner, &c.OwnerOrganizationID, &c.OwnershipConfirmedAt, &c.OwnershipConfirmedBy, &c.OwnershipTermsVersion, &c.BYOKPrioritySection, &c.SharedCapacityFallback, &modelFilters, &apiKeyFilters, &memberFilters)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	c.HasSecret = len(c.EncryptedSecret) > 0
	_ = json.Unmarshal(tags, &c.Tags)
	_ = json.Unmarshal(modelFilters, &c.ModelFilters)
	_ = json.Unmarshal(apiKeyFilters, &c.APIKeyFilters)
	_ = json.Unmarshal(memberFilters, &c.MemberFilters)
	if c.Tags == nil {
		c.Tags = []string{}
	}
	return c, err
}

// #nosec G101 -- SQL projection names encrypted credential columns but contains no credential value.
const credentialColumns = `c.id,c.provider_id,p.name,COALESCE((SELECT string_agg(g.name,', ' ORDER BY g.name) FROM credential_group_members gm JOIN credential_groups g ON g.id=gm.group_id WHERE gm.credential_id=c.id),''),c.name,c.credential_type,c.encrypted_secret,c.secret_last4,c.organization_id,c.project_id,c.status,c.priority,c.weight,c.max_concurrency,c.current_health,c.last_success_at,c.last_failure_at,c.cooldown_until,c.created_at,c.updated_at,COALESCE((SELECT jsonb_agg(t.tag ORDER BY t.tag) FROM credential_tags t WHERE t.credential_id=c.id),'[]'::jsonb),c.credential_owner,c.owner_organization_id,c.ownership_confirmed_at,c.ownership_confirmed_by,c.ownership_terms_version,c.byok_priority_section,c.shared_capacity_fallback,c.model_filters,c.api_key_filters,c.member_filters`

func (s *Store) ListCredentials(ctx context.Context, limit, offset int) ([]domain.Credential, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+credentialColumns+` FROM provider_credentials c JOIN providers p ON p.id=c.provider_id ORDER BY c.created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCredentialsFiltered powers the multi-provider credential management UI.
// Filters are applied in PostgreSQL so pagination cannot hide matching records.
func (s *Store) ListCredentialsFiltered(ctx context.Context, search, status, group string, limit, offset int) ([]domain.Credential, int64, error) {
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if search = strings.TrimSpace(search); search != "" {
		args = append(args, "%"+search+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		conditions = append(conditions, `(c.name ILIKE `+placeholder+` OR p.name ILIKE `+placeholder+` OR
			COALESCE(c.project_id,'') ILIKE `+placeholder+` OR EXISTS(
				SELECT 1 FROM credential_tags search_tag WHERE search_tag.credential_id=c.id AND search_tag.tag ILIKE `+placeholder+`
			))`)
	}
	if status = strings.ToUpper(strings.TrimSpace(status)); status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("c.status=$%d", len(args)))
	}
	if group = strings.TrimSpace(group); group != "" {
		args = append(args, group)
		placeholder := fmt.Sprintf("$%d", len(args))
		conditions = append(conditions, `EXISTS(SELECT 1 FROM credential_group_members filter_member
			JOIN credential_groups filter_group ON filter_group.id=filter_member.group_id
			WHERE filter_member.credential_id=c.id AND (filter_group.id::text=`+placeholder+` OR filter_group.name=`+placeholder+`))`)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM provider_credentials c JOIN providers p ON p.id=c.provider_id`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any(nil), args...), clamp(limit), max(offset, 0))
	rows, err := s.pool.Query(ctx, `SELECT `+credentialColumns+` FROM provider_credentials c JOIN providers p ON p.id=c.provider_id`+
		where+fmt.Sprintf(" ORDER BY c.created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2), queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]domain.Credential, 0)
	for rows.Next() {
		credential, scanErr := scanCredential(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		out = append(out, credential)
	}
	return out, total, rows.Err()
}
func (s *Store) CredentialByID(ctx context.Context, credentialID string) (domain.Credential, error) {
	return scanCredential(s.pool.QueryRow(ctx, `SELECT `+credentialColumns+` FROM provider_credentials c JOIN providers p ON p.id=c.provider_id WHERE c.id=$1`, credentialID))
}

func (s *Store) CreateCredential(ctx context.Context, c domain.Credential, groupID *string) (domain.Credential, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	if c.CredentialOwner == "" {
		c.CredentialOwner = domain.CredentialOwnerPlatform
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_credentials(id,provider_id,name,credential_type,encrypted_secret,secret_last4,organization_id,project_id,status,priority,weight,max_concurrency,current_health,credential_owner,owner_organization_id,ownership_confirmed_at,ownership_confirmed_by,ownership_terms_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'UNKNOWN',$13,$14,$15,$16,$17)`, c.ID, c.ProviderID, c.Name, c.CredentialType, c.EncryptedSecret, c.SecretLast4, c.OrganizationID, c.ProjectID, c.Status, c.Priority, c.Weight, c.MaxConcurrency, c.CredentialOwner, c.OwnerOrganizationID, c.OwnershipConfirmedAt, c.OwnershipConfirmedBy, c.OwnershipTermsVersion)
	if err != nil {
		return c, mapProviderIntegrityError(err)
	}
	if groupID != nil && *groupID != "" {
		if err = ensureGroupMemberProviderMatch(ctx, tx, *groupID, c.ID); err != nil {
			return c, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO credential_group_members(group_id,credential_id,weight,priority) VALUES($1,$2,$3,$4) ON CONFLICT(group_id,credential_id) DO UPDATE SET weight=EXCLUDED.weight,priority=EXCLUDED.priority`, *groupID, c.ID, c.Weight, c.Priority)
		if err != nil {
			return c, mapProviderIntegrityError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return c, mapProviderIntegrityError(err)
	}
	return s.CredentialByID(ctx, c.ID)
}

func (s *Store) UpdateCredential(ctx context.Context, c domain.Credential, replaceSecret bool) (domain.Credential, error) {
	if replaceSecret {
		tag, err := s.pool.Exec(ctx, `UPDATE provider_credentials SET name=$2,encrypted_secret=$3,secret_last4=$4,organization_id=$5,project_id=$6,status=$7,priority=$8,weight=$9,max_concurrency=$10,updated_at=now() WHERE id=$1`, c.ID, c.Name, c.EncryptedSecret, c.SecretLast4, c.OrganizationID, c.ProjectID, c.Status, c.Priority, c.Weight, c.MaxConcurrency)
		if err != nil {
			return c, err
		}
		if tag.RowsAffected() == 0 {
			return c, ErrNotFound
		}
	} else {
		tag, err := s.pool.Exec(ctx, `UPDATE provider_credentials SET name=$2,organization_id=$3,project_id=$4,status=$5,priority=$6,weight=$7,max_concurrency=$8,updated_at=now() WHERE id=$1`, c.ID, c.Name, c.OrganizationID, c.ProjectID, c.Status, c.Priority, c.Weight, c.MaxConcurrency)
		if err != nil {
			return c, err
		}
		if tag.RowsAffected() == 0 {
			return c, ErrNotFound
		}
	}
	return s.CredentialByID(ctx, c.ID)
}
func (s *Store) SetCredentialStatus(ctx context.Context, credentialID, status string, cooldown *time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE provider_credentials SET status=$2,cooldown_until=$3,last_failure_at=CASE WHEN $2 IN ('AUTH_FAILED','RATE_LIMITED','COOLDOWN','UNHEALTHY') THEN now() ELSE last_failure_at END,updated_at=now() WHERE id=$1`, credentialID, status, cooldown)
	if err == nil && (status == "AUTH_FAILED" || status == "RATE_LIMITED" || status == "COOLDOWN") {
		kind := "credential_rate_limited"
		severity := "warning"
		message := "Provider credential entered cooldown after an upstream rate limit."
		if status == "AUTH_FAILED" {
			kind = "credential_auth_failed"
			severity = "critical"
			message = "Provider credential authentication failed and it was removed from scheduling."
		}
		_, _ = s.pool.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,resource_type,resource_id) VALUES($1,$2,$3,$4,'provider_credential',$5)`, id.UUID(), kind, severity, message, credentialID)
	}
	return err
}
func (s *Store) MarkCredentialSuccess(ctx context.Context, credentialID string) {
	_, _ = s.pool.Exec(ctx, `UPDATE provider_credentials SET status='ACTIVE',current_health='HEALTHY',last_success_at=now(),cooldown_until=NULL,updated_at=now() WHERE id=$1 AND status NOT IN ('DISABLED','AUTH_FAILED')`, credentialID)
}

// MarkCredentialFailure records an observed transient failure without
// disabling an otherwise authorized credential. Network/TLS/DNS failures are
// not proof that a credential is invalid.
func (s *Store) MarkCredentialFailure(ctx context.Context, credentialID string) {
	_, _ = s.pool.Exec(ctx, `UPDATE provider_credentials SET current_health='DEGRADED',last_failure_at=now(),updated_at=now() WHERE id=$1 AND status NOT IN ('DISABLED','AUTH_FAILED')`, credentialID)
}
func (s *Store) DeleteCredential(ctx context.Context, credentialID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM provider_credentials WHERE id=$1`, credentialID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListGroups(ctx context.Context) ([]domain.CredentialGroup, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.id,g.provider_id,g.name,g.description,count(m.credential_id),count(m.credential_id) FILTER(WHERE c.status='ACTIVE' AND c.current_health<>'UNHEALTHY'),COALESCE(sum(c.max_concurrency),0),g.created_at,g.updated_at FROM credential_groups g LEFT JOIN credential_group_members m ON m.group_id=g.id LEFT JOIN provider_credentials c ON c.id=m.credential_id GROUP BY g.id ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CredentialGroup
	for rows.Next() {
		var g domain.CredentialGroup
		if err := rows.Scan(&g.ID, &g.ProviderID, &g.Name, &g.Description, &g.MemberCount, &g.HealthyCount, &g.TotalCapacity, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.CredentialsCount = g.MemberCount
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) CreateGroup(ctx context.Context, g domain.CredentialGroup) (domain.CredentialGroup, error) {
	if g.ProviderID == "" {
		p, err := s.ProviderBySlug(ctx, "openai")
		if err != nil {
			return g, err
		}
		g.ProviderID = p.ID
	}
	g.ID = id.UUID()
	err := s.pool.QueryRow(ctx, `INSERT INTO credential_groups(id,provider_id,name,description) VALUES($1,$2,$3,$4) RETURNING created_at,updated_at`, g.ID, g.ProviderID, g.Name, g.Description).Scan(&g.CreatedAt, &g.UpdatedAt)
	return g, err
}
func (s *Store) DeleteGroup(ctx context.Context, groupID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM credential_groups WHERE id=$1`, groupID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (s *Store) SetGroupMember(ctx context.Context, groupID, credentialID string, weight, priority int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = ensureGroupMemberProviderMatch(ctx, tx, groupID, credentialID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO credential_group_members(group_id,credential_id,weight,priority) VALUES($1,$2,$3,$4) ON CONFLICT(group_id,credential_id) DO UPDATE SET weight=EXCLUDED.weight,priority=EXCLUDED.priority`, groupID, credentialID, weight, priority); err != nil {
		return mapProviderIntegrityError(err)
	}
	return mapProviderIntegrityError(tx.Commit(ctx))
}
func (s *Store) RemoveGroupMember(ctx context.Context, groupID, credentialID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM credential_group_members WHERE group_id=$1 AND credential_id=$2`, groupID, credentialID)
	return err
}

func (s *Store) ListModels(ctx context.Context) ([]domain.Model, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id,m.provider_id,p.name,m.provider_model_id,m.display_name,m.model_type,m.enabled,
		m.capabilities,m.capability_source,m.context_window,m.latency_score::float8,m.quality_score::float8,
		COALESCE(mp.input_price_exact,mp.input_price,0)::text,COALESCE(mp.output_price_exact,mp.output_price,0)::text,COALESCE(NULLIF(mp.currency,''),p.settlement_currency),
		m.metadata,m.created_at,m.updated_at,COALESCE(m.service_subject,''),COALESCE(m.filing_info,''),COALESCE(m.generated_content_label_capability,'UNKNOWN'),COALESCE(m.user_disclosure,'') FROM models m JOIN providers p ON p.id=m.provider_id
		LEFT JOIN LATERAL (SELECT input_price,input_price_exact,output_price,output_price_exact,currency FROM model_prices
			WHERE model_id=m.id AND effective_from<=now() ORDER BY effective_from DESC,version DESC LIMIT 1) mp ON true
		ORDER BY p.name,m.provider_model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Model
	for rows.Next() {
		var m domain.Model
		var caps, meta []byte
		var inputPrice, outputPrice string
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderName, &m.ProviderModelID, &m.DisplayName, &m.ModelType,
			&m.Enabled, &caps, &m.CapabilitySource, &m.ContextWindow, &m.LatencyScore, &m.QualityScore,
			&inputPrice, &outputPrice, &m.PriceCurrency, &meta, &m.CreatedAt, &m.UpdatedAt, &m.ServiceSubject, &m.FilingInfo, &m.GeneratedContentLabelCapability, &m.UserDisclosure); err != nil {
			return nil, err
		}
		m.InputPrice, err = domain.ParseDecimal(inputPrice)
		if err != nil {
			return nil, fmt.Errorf("scan model %s input price: %w", m.ID, err)
		}
		m.OutputPrice, err = domain.ParseDecimal(outputPrice)
		if err != nil {
			return nil, fmt.Errorf("scan model %s output price: %w", m.ID, err)
		}
		_ = json.Unmarshal(caps, &m.Capabilities)
		_ = json.Unmarshal(meta, &m.Metadata)
		if m.Capabilities == nil {
			m.Capabilities = []string{}
		}
		if m.Metadata == nil {
			m.Metadata = map[string]any{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) UpsertModels(ctx context.Context, providerID string, models []domain.Model) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, m := range models {
		if m.ID == "" {
			m.ID = id.UUID()
		}
		if m.DisplayName == "" {
			m.DisplayName = m.ProviderModelID
		}
		if m.ModelType == "" {
			m.ModelType = "text"
		}
		if m.CapabilitySource == "" {
			m.CapabilitySource = "provider"
		}
		_, err = tx.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,model_type,enabled,capabilities,capability_source,context_window,latency_score,quality_score,metadata,service_subject,filing_info,generated_content_label_capability,user_disclosure)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),NULLIF($14,''),$15,NULLIF($16,'')) ON CONFLICT(provider_id,provider_model_id) DO UPDATE
			SET display_name=EXCLUDED.display_name,enabled=EXCLUDED.enabled,metadata=EXCLUDED.metadata,service_subject=EXCLUDED.service_subject,filing_info=EXCLUDED.filing_info,generated_content_label_capability=EXCLUDED.generated_content_label_capability,user_disclosure=EXCLUDED.user_disclosure,updated_at=now()`,
			m.ID, providerID, m.ProviderModelID, m.DisplayName, m.ModelType, true, jsonBytes(m.Capabilities),
			m.CapabilitySource, m.ContextWindow, defaultScore(m.LatencyScore), defaultScore(m.QualityScore), jsonBytes(m.Metadata), m.ServiceSubject, m.FilingInfo, firstNonEmptyStore(m.GeneratedContentLabelCapability, "UNKNOWN"), m.UserDisclosure)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func defaultScore(value float64) float64 {
	if value == 0 {
		return 50
	}
	return value
}

func (s *Store) CreateModel(ctx context.Context, m domain.Model) (domain.Model, error) {
	if m.ID == "" {
		m.ID = id.UUID()
	}
	if m.DisplayName == "" {
		m.DisplayName = m.ProviderModelID
	}
	if m.ModelType == "" {
		m.ModelType = "text"
	}
	if m.CapabilitySource == "" {
		m.CapabilitySource = "manual"
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	if m.Capabilities == nil {
		m.Capabilities = []string{}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,model_type,enabled,
		capabilities,capability_source,context_window,latency_score,quality_score,metadata,service_subject,filing_info,generated_content_label_capability,user_disclosure)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),NULLIF($14,''),$15,NULLIF($16,''))`, m.ID, m.ProviderID, m.ProviderModelID,
		m.DisplayName, m.ModelType, m.Enabled, jsonBytes(m.Capabilities), m.CapabilitySource, m.ContextWindow,
		defaultScore(m.LatencyScore), defaultScore(m.QualityScore), jsonBytes(m.Metadata), m.ServiceSubject, m.FilingInfo,
		firstNonEmptyStore(m.GeneratedContentLabelCapability, "UNKNOWN"), m.UserDisclosure); err != nil {
		return domain.Model{}, err
	}
	return s.ModelByID(ctx, m.ID)
}

func (s *Store) ModelByID(ctx context.Context, modelID string) (domain.Model, error) {
	models, err := s.ListModels(ctx)
	if err != nil {
		return domain.Model{}, err
	}
	for _, model := range models {
		if model.ID == modelID {
			return model, nil
		}
	}
	return domain.Model{}, ErrNotFound
}

func (s *Store) UpdateModel(ctx context.Context, m domain.Model) (domain.Model, error) {
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	if m.Capabilities == nil {
		m.Capabilities = []string{}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE models SET provider_id=$2,provider_model_id=$3,display_name=$4,model_type=$5,
		enabled=$6,capabilities=$7,capability_source=$8,context_window=$9,latency_score=$10,quality_score=$11,
		metadata=$12,service_subject=NULLIF($13,''),filing_info=NULLIF($14,''),generated_content_label_capability=$15,
		user_disclosure=NULLIF($16,''),updated_at=now() WHERE id=$1`, m.ID, m.ProviderID, m.ProviderModelID, m.DisplayName, m.ModelType,
		m.Enabled, jsonBytes(m.Capabilities), m.CapabilitySource, m.ContextWindow, m.LatencyScore, m.QualityScore,
		jsonBytes(m.Metadata), m.ServiceSubject, m.FilingInfo, firstNonEmptyStore(m.GeneratedContentLabelCapability, "UNKNOWN"), m.UserDisclosure)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.Model{}, ErrNotFound
	}
	if err != nil {
		return domain.Model{}, err
	}
	return s.ModelByID(ctx, m.ID)
}

func (s *Store) DisableModel(ctx context.Context, modelID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE models SET enabled=false,updated_at=now() WHERE id=$1`, modelID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListModelPrices(ctx context.Context, modelID string) ([]domain.ModelPrice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,model_id,version,effective_from,COALESCE(input_price_exact,input_price)::text,COALESCE(cached_input_price_exact,cached_input_price)::text,COALESCE(output_price_exact,output_price)::text,currency,unit,source,created_at FROM model_prices WHERE model_id=$1 ORDER BY effective_from DESC,version DESC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelPrice
	for rows.Next() {
		var p domain.ModelPrice
		var inputPrice, cachedInputPrice, outputPrice string
		if err := rows.Scan(&p.ID, &p.ModelID, &p.Version, &p.EffectiveFrom, &inputPrice, &cachedInputPrice, &outputPrice, &p.Currency, &p.Unit, &p.Source, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.InputPrice, err = domain.ParseDecimal(inputPrice)
		if err != nil {
			return nil, fmt.Errorf("scan model price %s input: %w", p.ID, err)
		}
		p.CachedInputPrice, err = domain.ParseDecimal(cachedInputPrice)
		if err != nil {
			return nil, fmt.Errorf("scan model price %s cached input: %w", p.ID, err)
		}
		p.OutputPrice, err = domain.ParseDecimal(outputPrice)
		if err != nil {
			return nil, fmt.Errorf("scan model price %s output: %w", p.ID, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) CreateModelPrice(ctx context.Context, p domain.ModelPrice) (domain.ModelPrice, error) {
	p = normalizeModelPriceDefaults(p)
	for label, value := range map[string]domain.Decimal{"input": p.InputPrice, "cached input": p.CachedInputPrice, "output": p.OutputPrice} {
		negative, err := value.IsNegative()
		if err != nil {
			return domain.ModelPrice{}, fmt.Errorf("invalid %s model price: %w", label, err)
		}
		if negative {
			return domain.ModelPrice{}, fmt.Errorf("invalid %s model price: amount must not be negative", label)
		}
	}
	p.ID = id.UUID()
	if p.Version <= 0 {
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM model_prices WHERE model_id=$1`, p.ModelID).Scan(&p.Version)
	}
	if p.EffectiveFrom.IsZero() {
		p.EffectiveFrom = time.Now().UTC()
	}
	if p.Currency == "" {
		p.Currency = "USD"
	}
	if p.Unit <= 0 {
		p.Unit = 1000000
	}
	if p.Source == "" {
		p.Source = "manual"
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO model_prices(id,model_id,version,effective_from,input_price,cached_input_price,output_price,input_price_exact,cached_input_price_exact,output_price_exact,currency,unit,source) VALUES($1,$2,$3,$4,round($5::numeric,10),round($6::numeric,10),round($7::numeric,10),$5,$6,$7,$8,$9,$10) RETURNING created_at`, p.ID, p.ModelID, p.Version, p.EffectiveFrom, p.InputPrice.String(), p.CachedInputPrice.String(), p.OutputPrice.String(), p.Currency, p.Unit, p.Source).Scan(&p.CreatedAt)
	return p, err
}

func normalizeModelPriceDefaults(p domain.ModelPrice) domain.ModelPrice {
	if strings.TrimSpace(p.CachedInputPrice.String()) == "" {
		p.CachedInputPrice = domain.MustDecimal("0")
	}
	return p
}

func (s *Store) CalculateCost(ctx context.Context, providerID, upstreamModel string, input, cached, output int64) (domain.Decimal, error) {
	var inputPrice, cachedPrice, outputPrice string
	var currency string
	var unit int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(mp.input_price_exact,mp.input_price)::text,COALESCE(mp.cached_input_price_exact,mp.cached_input_price)::text,COALESCE(mp.output_price_exact,mp.output_price)::text,mp.unit,COALESCE(NULLIF(mp.currency,''),p.settlement_currency) FROM model_prices mp JOIN models m ON m.id=mp.model_id JOIN providers p ON p.id=m.provider_id WHERE m.provider_id=$1 AND m.provider_model_id=$2 AND mp.effective_from<=now() ORDER BY mp.effective_from DESC,mp.version DESC LIMIT 1`, providerID, upstreamModel).Scan(&inputPrice, &cachedPrice, &outputPrice, &unit, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MustDecimal("0"), nil
	}
	if err != nil {
		return "", err
	}
	result, err := pricing.Calculate(
		pricing.Rate{Input: inputPrice, Cached: cachedPrice, Output: outputPrice, Fixed: "0", Unit: unit, Currency: currency},
		pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: unit, Currency: currency},
		pricing.Tokens{Input: input, Cached: min(cached, input), Output: output}, "0", "0", "1",
	)
	if err != nil {
		return "", err
	}
	return domain.ParseDecimal(result.ProviderCost)
}

// CalculateProjectReferenceCost returns the highest current catalog cost among
// enabled models granted to the project. Intelligent-router savings use this
// explicit, reproducible baseline and are still estimates rather than invoices.
func (s *Store) CalculateProjectReferenceCost(ctx context.Context, projectID string, input, cached, output int64) (domain.Decimal, error) {
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(mp.input_price_exact,mp.input_price)::text,COALESCE(mp.cached_input_price_exact,mp.cached_input_price)::text,
		COALESCE(mp.output_price_exact,mp.output_price)::text,mp.unit,COALESCE(NULLIF(mp.currency,''),p.settlement_currency) FROM project_model_routes pmr JOIN model_routes r ON r.id=pmr.model_route_id
		JOIN models m ON m.provider_id=r.provider_id AND m.provider_model_id=r.upstream_model
		JOIN providers p ON p.id=m.provider_id
		JOIN LATERAL (SELECT input_price,input_price_exact,cached_input_price,cached_input_price_exact,
			output_price,output_price_exact,unit,currency FROM model_prices WHERE model_id=m.id
			AND effective_from<=now() ORDER BY effective_from DESC,version DESC LIMIT 1) mp ON true
		WHERE pmr.project_id=$1 AND pmr.deleted_at IS NULL AND pmr.enabled AND r.enabled AND m.enabled`, projectID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if cached > input {
		cached = input
	}
	highest := domain.MustDecimal("0")
	highestCurrency := ""
	for rows.Next() {
		var inputPrice, cachedPrice, outputPrice, currency string
		var unit int64
		if err := rows.Scan(&inputPrice, &cachedPrice, &outputPrice, &unit, &currency); err != nil {
			return "", err
		}
		if highestCurrency != "" && !strings.EqualFold(highestCurrency, currency) {
			return "", fmt.Errorf("project reference cost spans currencies %s and %s without an approved FX conversion", highestCurrency, currency)
		}
		highestCurrency = currency
		result, calculateErr := pricing.Calculate(
			pricing.Rate{Input: inputPrice, Cached: cachedPrice, Output: outputPrice, Fixed: "0", Unit: unit, Currency: currency},
			pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: unit, Currency: currency},
			pricing.Tokens{Input: input, Cached: cached, Output: output}, "0", "0", "1",
		)
		if calculateErr != nil {
			return "", calculateErr
		}
		cost, decimalErr := domain.ParseDecimal(result.ProviderCost)
		if decimalErr != nil {
			return "", decimalErr
		}
		comparison, decimalErr := cost.Compare(highest)
		if decimalErr != nil {
			return "", decimalErr
		}
		if comparison > 0 {
			highest = cost
		}
	}
	return highest, rows.Err()
}

func scanRoute(row pgx.Row) (domain.ModelRoute, error) {
	var r domain.ModelRoute
	var fallback []byte
	err := row.Scan(&r.ID, &r.Alias, &r.ProviderID, &r.ProviderBaseURL, &r.UpstreamModel, &r.CredentialGroupID, &r.FallbackGroupID, &r.Enabled, &r.RoutingPolicy, &fallback, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(fallback, &r.FallbackConfig)
	if r.FallbackConfig == nil {
		r.FallbackConfig = map[string]any{}
	}
	return r, nil
}

const routeColumns = `r.id,r.alias,r.provider_id,p.base_url,r.upstream_model,r.credential_group_id,r.fallback_group_id,r.enabled,r.routing_policy,r.fallback_config,r.created_at,r.updated_at`

func (s *Store) ListRoutes(ctx context.Context) ([]domain.ModelRoute, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+routeColumns+` FROM model_routes r JOIN providers p ON p.id=r.provider_id ORDER BY r.alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelRoute
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) RouteByModel(ctx context.Context, model string) (domain.ModelRoute, error) {
	return scanRoute(s.pool.QueryRow(ctx, `SELECT `+routeColumns+` FROM model_routes r JOIN providers p ON p.id=r.provider_id WHERE r.enabled=true AND p.enabled=true AND (r.alias=$1 OR (r.upstream_model=$1 AND EXISTS(SELECT 1 FROM models m WHERE m.provider_id=r.provider_id AND m.provider_model_id=$1 AND m.enabled=true))) ORDER BY CASE WHEN r.alias=$1 THEN 0 ELSE 1 END LIMIT 1`, model))
}
func (s *Store) RouteByID(ctx context.Context, routeID string) (domain.ModelRoute, error) {
	return scanRoute(s.pool.QueryRow(ctx, `SELECT `+routeColumns+` FROM model_routes r JOIN providers p ON p.id=r.provider_id WHERE r.id=$1`, routeID))
}
func (s *Store) CreateRoute(ctx context.Context, r domain.ModelRoute) (domain.ModelRoute, error) {
	r.ID = id.UUID()
	if r.RoutingPolicy == "" {
		r.RoutingPolicy = "priority_weighted"
	}
	if r.FallbackConfig == nil {
		r.FallbackConfig = map[string]any{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx)
	if err = ensureRouteProviderMatch(ctx, tx, r.ProviderID, r.CredentialGroupID, r.FallbackGroupID); err != nil {
		return r, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO model_routes(id,alias,provider_id,upstream_model,credential_group_id,fallback_group_id,enabled,routing_policy,fallback_config) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.ID, r.Alias, r.ProviderID, r.UpstreamModel, r.CredentialGroupID, r.FallbackGroupID, r.Enabled, r.RoutingPolicy, jsonBytes(r.FallbackConfig)); err != nil {
		return r, mapProviderIntegrityError(err)
	}
	// V1 routes remain immediately usable by V1 keys through the deterministic
	// Legacy project. Explicit V2 projects still require their own grants.
	if _, err = tx.Exec(ctx, `INSERT INTO project_model_routes(project_id,model_route_id,alias,enabled)
		VALUES($1,$2,$3,$4) ON CONFLICT(project_id,model_route_id) DO UPDATE
		SET alias=EXCLUDED.alias,enabled=EXCLUDED.enabled,updated_at=now()`,
		domain.LegacyProjectID, r.ID, r.Alias, r.Enabled); err != nil {
		return r, mapProviderIntegrityError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return r, mapProviderIntegrityError(err)
	}
	return s.RouteByID(ctx, r.ID)
}
func (s *Store) UpdateRoute(ctx context.Context, r domain.ModelRoute) (domain.ModelRoute, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	err = tx.QueryRow(ctx, `SELECT true FROM model_routes WHERE id=$1 FOR UPDATE`, r.ID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, mapProviderIntegrityError(err)
	}
	if err = ensureRouteProviderMatch(ctx, tx, r.ProviderID, r.CredentialGroupID, r.FallbackGroupID); err != nil {
		return r, err
	}
	tag, err := tx.Exec(ctx, `UPDATE model_routes SET alias=$2,provider_id=$3,upstream_model=$4,credential_group_id=$5,fallback_group_id=$6,enabled=$7,routing_policy=$8,fallback_config=$9,updated_at=now() WHERE id=$1`, r.ID, r.Alias, r.ProviderID, r.UpstreamModel, r.CredentialGroupID, r.FallbackGroupID, r.Enabled, r.RoutingPolicy, jsonBytes(r.FallbackConfig))
	if err != nil {
		return r, mapProviderIntegrityError(err)
	}
	if tag.RowsAffected() == 0 {
		return r, ErrNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_model_routes(project_id,model_route_id,alias,enabled)
		VALUES($1,$2,$3,$4) ON CONFLICT(project_id,model_route_id) DO UPDATE
		SET alias=EXCLUDED.alias,enabled=EXCLUDED.enabled,updated_at=now()`,
		domain.LegacyProjectID, r.ID, r.Alias, r.Enabled); err != nil {
		return r, mapProviderIntegrityError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return r, mapProviderIntegrityError(err)
	}
	return s.RouteByID(ctx, r.ID)
}
func (s *Store) DeleteRoute(ctx context.Context, routeID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM project_model_routes WHERE project_id=$1 AND model_route_id=$2`, domain.LegacyProjectID, routeID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM model_routes WHERE id=$1`, routeID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Candidates(ctx context.Context, groupID string) ([]domain.Credential, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+credentialColumns+`,m.weight,m.priority FROM credential_group_members m JOIN provider_credentials c ON c.id=m.credential_id JOIN providers p ON p.id=c.provider_id WHERE m.group_id=$1 AND p.enabled=true AND c.status IN ('ACTIVE','RATE_LIMITED','COOLDOWN') AND (c.cooldown_until IS NULL OR c.cooldown_until<=now()) ORDER BY m.priority DESC,c.priority DESC,c.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Credential
	for rows.Next() {
		var c domain.Credential
		var tags, modelFilters, apiKeyFilters, memberFilters []byte
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.ProviderName, &c.GroupName, &c.Name, &c.CredentialType, &c.EncryptedSecret, &c.SecretLast4, &c.OrganizationID, &c.ProjectID, &c.Status, &c.Priority, &c.Weight, &c.MaxConcurrency, &c.CurrentHealth, &c.LastSuccessAt, &c.LastFailureAt, &c.CooldownUntil, &c.CreatedAt, &c.UpdatedAt, &tags, &c.CredentialOwner, &c.OwnerOrganizationID, &c.OwnershipConfirmedAt, &c.OwnershipConfirmedBy, &c.OwnershipTermsVersion, &c.BYOKPrioritySection, &c.SharedCapacityFallback, &modelFilters, &apiKeyFilters, &memberFilters, &c.EffectiveWeight, &c.EffectivePriority); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(tags, &c.Tags)
		_ = json.Unmarshal(modelFilters, &c.ModelFilters)
		_ = json.Unmarshal(apiKeyFilters, &c.APIKeyFilters)
		_ = json.Unmarshal(memberFilters, &c.MemberFilters)
		if c.Tags == nil {
			c.Tags = []string{}
		}
		c.HasSecret = true
		out = append(out, c)
	}
	return out, rows.Err()
}
