package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrFreeRouteDisabled = errors.New("free model routing is disabled for this workspace")
	ErrFreeQuotaExceeded = errors.New("free model daily quota exceeded")
)

func scanWorkspaceSettings(row pgx.Row) (domain.WorkspaceSettings, error) {
	var out domain.WorkspaceSettings
	var providerPolicy, privacy, observability, regions []byte
	err := row.Scan(&out.ProjectID, &providerPolicy, &privacy, &observability, &out.IncludeBYOKInBudgets,
		&out.FreeDailyRequestLimit, &out.FreeDailyTokenLimit, &regions, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(providerPolicy, &out.DefaultProviderPolicy)
	_ = json.Unmarshal(privacy, &out.PrivacyPolicy)
	_ = json.Unmarshal(observability, &out.ObservabilityConfig)
	_ = json.Unmarshal(regions, &out.AllowedProcessingRegions)
	if out.PrivacyPolicy == nil {
		out.PrivacyPolicy = map[string]any{}
	}
	if out.ObservabilityConfig == nil {
		out.ObservabilityConfig = map[string]any{}
	}
	if out.AllowedProcessingRegions == nil {
		out.AllowedProcessingRegions = []string{}
	}
	return out, nil
}

const workspaceSettingsSelect = `SELECT project_id,default_provider_policy,privacy_policy,observability_config,
	include_byok_in_budgets,free_daily_request_limit,free_daily_token_limit,allowed_processing_regions,created_at,updated_at
	FROM workspace_settings`

func (s *Store) WorkspaceSettings(ctx context.Context, projectID string) (domain.WorkspaceSettings, error) {
	return scanWorkspaceSettings(s.pool.QueryRow(ctx, workspaceSettingsSelect+` WHERE project_id=$1`, projectID))
}

func (s *Store) UpsertWorkspaceSettings(ctx context.Context, value domain.WorkspaceSettings, actor *string) (domain.WorkspaceSettings, error) {
	if strings.TrimSpace(value.ProjectID) == "" || value.FreeDailyRequestLimit < 0 || value.FreeDailyTokenLimit < 0 {
		return value, errors.New("invalid workspace settings")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_settings(project_id,default_provider_policy,privacy_policy,observability_config,
		include_byok_in_budgets,free_daily_request_limit,free_daily_token_limit,allowed_processing_regions)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(project_id) DO UPDATE SET
		default_provider_policy=EXCLUDED.default_provider_policy,privacy_policy=EXCLUDED.privacy_policy,
		observability_config=EXCLUDED.observability_config,include_byok_in_budgets=EXCLUDED.include_byok_in_budgets,
		free_daily_request_limit=EXCLUDED.free_daily_request_limit,free_daily_token_limit=EXCLUDED.free_daily_token_limit,
		allowed_processing_regions=EXCLUDED.allowed_processing_regions,updated_at=now()`, value.ProjectID,
		jsonBytes(value.DefaultProviderPolicy), jsonBytes(value.PrivacyPolicy), jsonBytes(value.ObservabilityConfig),
		value.IncludeBYOKInBudgets, value.FreeDailyRequestLimit, value.FreeDailyTokenLimit, jsonBytes(value.AllowedProcessingRegions)); err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, actor, "workspace.settings_updated", "project", value.ProjectID, map[string]any{
		"include_byok_in_budgets": value.IncludeBYOKInBudgets, "free_daily_request_limit": value.FreeDailyRequestLimit,
		"free_daily_token_limit": value.FreeDailyTokenLimit, "allowed_processing_regions": value.AllowedProcessingRegions,
	}); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return s.WorkspaceSettings(ctx, value.ProjectID)
}

// AdmitFreeModelUsage reserves conservative estimated tokens atomically. A
// zero limit means the free router is intentionally disabled, not unlimited.
func (s *Store) AdmitFreeModelUsage(ctx context.Context, projectID, apiKeyID string, estimatedTokens int64) (string, error) {
	if estimatedTokens < 0 {
		return "", errors.New("invalid free usage estimate")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var requestLimit int
	var tokenLimit int64
	if err = tx.QueryRow(ctx, `SELECT free_daily_request_limit,free_daily_token_limit FROM workspace_settings WHERE project_id=$1 FOR SHARE`, projectID).
		Scan(&requestLimit, &tokenLimit); errors.Is(err, pgx.ErrNoRows) || (err == nil && (requestLimit == 0 || tokenLimit == 0)) {
		return "", ErrFreeRouteDisabled
	} else if err != nil {
		return "", err
	}
	today := time.Now().UTC().Format("2006-01-02")
	if _, err = tx.Exec(ctx, `INSERT INTO free_model_usage_daily(usage_date,project_id,api_key_id) VALUES($1,$2,$3)
		ON CONFLICT(usage_date,project_id,api_key_id) DO NOTHING`, today, projectID, apiKeyID); err != nil {
		return "", err
	}
	var requests, reserved, settled int64
	if err = tx.QueryRow(ctx, `SELECT requests,reserved_tokens,settled_tokens FROM free_model_usage_daily
		WHERE usage_date=$1 AND project_id=$2 AND api_key_id=$3 FOR UPDATE`, today, projectID, apiKeyID).
		Scan(&requests, &reserved, &settled); err != nil {
		return "", err
	}
	if requests+1 > int64(requestLimit) || reserved+settled+estimatedTokens > tokenLimit {
		return "", ErrFreeQuotaExceeded
	}
	if _, err = tx.Exec(ctx, `UPDATE free_model_usage_daily SET requests=requests+1,reserved_tokens=reserved_tokens+$4,updated_at=now()
		WHERE usage_date=$1 AND project_id=$2 AND api_key_id=$3`, today, projectID, apiKeyID, estimatedTokens); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return today, nil
}

func (s *Store) SettleFreeModelUsage(ctx context.Context, usageDate, projectID, apiKeyID string, estimatedTokens, actualTokens int64) error {
	if estimatedTokens < 0 || actualTokens < 0 {
		return errors.New("invalid free usage settlement")
	}
	if _, err := time.Parse("2006-01-02", usageDate); err != nil {
		return errors.New("invalid free usage date")
	}
	_, err := s.pool.Exec(ctx, `UPDATE free_model_usage_daily SET reserved_tokens=GREATEST(0,reserved_tokens-$4),
		settled_tokens=settled_tokens+$5,updated_at=now() WHERE usage_date=$1 AND project_id=$2 AND api_key_id=$3`,
		usageDate, projectID, apiKeyID, estimatedTokens, actualTokens)
	return err
}

func (s *Store) UpdateBYOKRoutingPolicy(ctx context.Context, credentialID, organizationID, section, fallback string,
	modelFilters, apiKeyFilters, memberFilters []string, actor *string) (domain.Credential, error) {
	section = strings.ToUpper(strings.TrimSpace(section))
	fallback = strings.ToUpper(strings.TrimSpace(fallback))
	if (section != "PRIORITIZED" && section != "FALLBACK") ||
		(fallback != "ALWAYS" && fallback != "OUTSIDE_FILTERS" && fallback != "NEVER") {
		return domain.Credential{}, errors.New("invalid BYOK routing policy")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Credential{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE provider_credentials SET byok_priority_section=$3,shared_capacity_fallback=$4,
		model_filters=$5,api_key_filters=$6,member_filters=$7,updated_at=now()
		WHERE id=$1 AND credential_owner='CUSTOMER' AND owner_organization_id=$2`, credentialID, organizationID, section,
		fallback, jsonBytes(normalizeStrings(modelFilters)), jsonBytes(normalizeStrings(apiKeyFilters)), jsonBytes(normalizeStrings(memberFilters)))
	if err != nil {
		return domain.Credential{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Credential{}, ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, actor, "byok.routing_policy_updated", "provider_credential", credentialID, map[string]any{
		"organization_id": organizationID, "byok_priority_section": section, "shared_capacity_fallback": fallback,
		"model_filters": modelFilters, "api_key_filters": apiKeyFilters, "member_filters": memberFilters,
	}); err != nil {
		return domain.Credential{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Credential{}, err
	}
	return s.CredentialByID(ctx, credentialID)
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func capabilityDocumentDigest(document map[string]any) (string, []byte, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), raw, nil
}

func (s *Store) PublishProviderCapabilityDocument(ctx context.Context, value domain.ProviderCapabilityDocument) (domain.ProviderCapabilityDocument, error) {
	if strings.TrimSpace(value.ProviderID) == "" || strings.TrimSpace(value.SchemaVersion) == "" || len(value.Document) == 0 {
		return value, errors.New("provider, schema version, and capability document are required")
	}
	digest, raw, err := capabilityDocumentDigest(value.Document)
	if err != nil {
		return value, err
	}
	value.ID = id.UUID()
	value.SourceSHA256 = digest
	value.Status = "ACTIVE"
	value.FetchedAt = time.Now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return value, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE provider_capability_documents SET status='SUPERSEDED' WHERE provider_id=$1 AND status='ACTIVE'`, value.ProviderID); err != nil {
		return value, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO provider_capability_documents(id,provider_id,schema_version,document,source_url,source_sha256,status,fetched_at,created_by)
		VALUES($1,$2,$3,$4,$5,$6,'ACTIVE',$7,$8) RETURNING created_at`, value.ID, value.ProviderID, value.SchemaVersion, raw,
		nullString(value.SourceURL), value.SourceSHA256, value.FetchedAt, value.CreatedBy).Scan(&value.CreatedAt)
	if err != nil {
		return value, err
	}
	if err = writeAuditTx(ctx, tx, value.CreatedBy, "provider.capability_document_published", "provider", value.ProviderID,
		map[string]any{"document_id": value.ID, "schema_version": value.SchemaVersion, "source_sha256": value.SourceSHA256}); err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return value, nil
}

func scanProviderCapabilityDocument(row pgx.Row) (domain.ProviderCapabilityDocument, error) {
	var out domain.ProviderCapabilityDocument
	var raw []byte
	err := row.Scan(&out.ID, &out.ProviderID, &out.ProviderName, &out.SchemaVersion, &raw, &out.SourceURL,
		&out.SourceSHA256, &out.Status, &out.FetchedAt, &out.CreatedBy, &out.CreatedAt)
	if err == nil {
		_ = json.Unmarshal(raw, &out.Document)
	}
	return out, err
}

func (s *Store) ListProviderCapabilityDocuments(ctx context.Context, providerID string, publicOnly bool) ([]domain.ProviderCapabilityDocument, error) {
	where := " WHERE d.status='ACTIVE'"
	args := []any{}
	if strings.TrimSpace(providerID) != "" {
		args = append(args, providerID)
		where += ` AND d.provider_id=$1`
	}
	if publicOnly {
		where += ` AND p.enabled=true AND p.commercial_status='COMMERCIAL_APPROVED' AND p.emergency_kill_switch=false`
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id,d.provider_id,p.name,d.schema_version,d.document,COALESCE(d.source_url,''),
		d.source_sha256,d.status,d.fetched_at,d.created_by,d.created_at FROM provider_capability_documents d
		JOIN providers p ON p.id=d.provider_id`+where+` ORDER BY p.name,d.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProviderCapabilityDocument, 0)
	for rows.Next() {
		value, scanErr := scanProviderCapabilityDocument(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
