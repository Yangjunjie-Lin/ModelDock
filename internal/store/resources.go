package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
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
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.name,p.slug,p.provider_type,p.base_url,p.enabled,p.config,p.created_at,p.updated_at,(SELECT count(*) FROM provider_credentials c WHERE c.provider_id=p.id) FROM providers p ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Provider
	for rows.Next() {
		var p domain.Provider
		var raw []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.ProviderType, &p.BaseURL, &p.Enabled, &raw, &p.CreatedAt, &p.UpdatedAt, &p.CredentialsCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &p.Config)
		if p.Config == nil {
			p.Config = map[string]any{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProviderByID(ctx context.Context, providerID string) (domain.Provider, error) {
	var p domain.Provider
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id,name,slug,provider_type,base_url,enabled,config,created_at,updated_at FROM providers WHERE id=$1`, providerID).
		Scan(&p.ID, &p.Name, &p.Slug, &p.ProviderType, &p.BaseURL, &p.Enabled, &raw, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(raw, &p.Config)
	if p.Config == nil {
		p.Config = map[string]any{}
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
	_, err := s.pool.Exec(ctx, `INSERT INTO providers(id,name,slug,provider_type,base_url,enabled,config) VALUES($1,$2,$3,$4,$5,$6,$7)`, p.ID, p.Name, strings.ToLower(p.Slug), p.ProviderType, strings.TrimRight(p.BaseURL, "/"), p.Enabled, jsonBytes(p.Config))
	if err != nil {
		return domain.Provider{}, err
	}
	return s.ProviderByID(ctx, p.ID)
}

func (s *Store) UpdateProvider(ctx context.Context, p domain.Provider) (domain.Provider, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE providers SET name=$2,base_url=$3,enabled=$4,config=$5,updated_at=now() WHERE id=$1`, p.ID, p.Name, strings.TrimRight(p.BaseURL, "/"), p.Enabled, jsonBytes(p.Config))
	if err != nil {
		return domain.Provider{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Provider{}, ErrNotFound
	}
	return s.ProviderByID(ctx, p.ID)
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
	var tags []byte
	err := row.Scan(&c.ID, &c.ProviderID, &c.ProviderName, &c.GroupName, &c.Name, &c.CredentialType, &c.EncryptedSecret, &c.SecretLast4, &c.OrganizationID, &c.ProjectID, &c.Status, &c.Priority, &c.Weight, &c.MaxConcurrency, &c.CurrentHealth, &c.LastSuccessAt, &c.LastFailureAt, &c.CooldownUntil, &c.CreatedAt, &c.UpdatedAt, &tags)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	c.HasSecret = len(c.EncryptedSecret) > 0
	_ = json.Unmarshal(tags, &c.Tags)
	if c.Tags == nil {
		c.Tags = []string{}
	}
	return c, err
}

const credentialColumns = `c.id,c.provider_id,p.name,COALESCE((SELECT string_agg(g.name,', ' ORDER BY g.name) FROM credential_group_members gm JOIN credential_groups g ON g.id=gm.group_id WHERE gm.credential_id=c.id),''),c.name,c.credential_type,c.encrypted_secret,c.secret_last4,c.organization_id,c.project_id,c.status,c.priority,c.weight,c.max_concurrency,c.current_health,c.last_success_at,c.last_failure_at,c.cooldown_until,c.created_at,c.updated_at,COALESCE((SELECT jsonb_agg(t.tag ORDER BY t.tag) FROM credential_tags t WHERE t.credential_id=c.id),'[]'::jsonb)`

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
func (s *Store) CredentialByID(ctx context.Context, credentialID string) (domain.Credential, error) {
	return scanCredential(s.pool.QueryRow(ctx, `SELECT `+credentialColumns+` FROM provider_credentials c JOIN providers p ON p.id=c.provider_id WHERE c.id=$1`, credentialID))
}

func (s *Store) CreateCredential(ctx context.Context, c domain.Credential, groupID *string) (domain.Credential, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO provider_credentials(id,provider_id,name,credential_type,encrypted_secret,secret_last4,organization_id,project_id,status,priority,weight,max_concurrency,current_health) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'UNKNOWN')`, c.ID, c.ProviderID, c.Name, c.CredentialType, c.EncryptedSecret, c.SecretLast4, c.OrganizationID, c.ProjectID, c.Status, c.Priority, c.Weight, c.MaxConcurrency)
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
	rows, err := s.pool.Query(ctx, `SELECT id,provider_id,provider_model_id,display_name,model_type,enabled,capabilities,capability_source,context_window,metadata,created_at,updated_at FROM models ORDER BY provider_model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Model
	for rows.Next() {
		var m domain.Model
		var caps, meta []byte
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderModelID, &m.DisplayName, &m.ModelType, &m.Enabled, &caps, &m.CapabilitySource, &m.ContextWindow, &meta, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
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
		_, err = tx.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,model_type,enabled,capabilities,capability_source,context_window,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(provider_id,provider_model_id) DO UPDATE SET display_name=EXCLUDED.display_name,enabled=EXCLUDED.enabled,metadata=EXCLUDED.metadata,updated_at=now()`, m.ID, providerID, m.ProviderModelID, m.DisplayName, m.ModelType, true, jsonBytes(m.Capabilities), m.CapabilitySource, m.ContextWindow, jsonBytes(m.Metadata))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListModelPrices(ctx context.Context, modelID string) ([]domain.ModelPrice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,model_id,version,effective_from,input_price::float8,cached_input_price::float8,output_price::float8,currency,unit,source,created_at FROM model_prices WHERE model_id=$1 ORDER BY effective_from DESC,version DESC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelPrice
	for rows.Next() {
		var p domain.ModelPrice
		if err := rows.Scan(&p.ID, &p.ModelID, &p.Version, &p.EffectiveFrom, &p.InputPrice, &p.CachedInputPrice, &p.OutputPrice, &p.Currency, &p.Unit, &p.Source, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) CreateModelPrice(ctx context.Context, p domain.ModelPrice) (domain.ModelPrice, error) {
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
	err := s.pool.QueryRow(ctx, `INSERT INTO model_prices(id,model_id,version,effective_from,input_price,cached_input_price,output_price,currency,unit,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING created_at`, p.ID, p.ModelID, p.Version, p.EffectiveFrom, p.InputPrice, p.CachedInputPrice, p.OutputPrice, p.Currency, p.Unit, p.Source).Scan(&p.CreatedAt)
	return p, err
}
func (s *Store) CalculateCost(ctx context.Context, providerID, upstreamModel string, input, cached, output int64) (float64, error) {
	var inputPrice, cachedPrice, outputPrice float64
	var unit int64
	err := s.pool.QueryRow(ctx, `SELECT mp.input_price::float8,mp.cached_input_price::float8,mp.output_price::float8,mp.unit FROM model_prices mp JOIN models m ON m.id=mp.model_id WHERE m.provider_id=$1 AND m.provider_model_id=$2 AND mp.effective_from<=now() ORDER BY mp.effective_from DESC,mp.version DESC LIMIT 1`, providerID, upstreamModel).Scan(&inputPrice, &cachedPrice, &outputPrice, &unit)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if cached > input {
		cached = input
	}
	noncached := input - cached
	return (float64(noncached)*inputPrice + float64(cached)*cachedPrice + float64(output)*outputPrice) / float64(unit), nil
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
		var tags []byte
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.ProviderName, &c.GroupName, &c.Name, &c.CredentialType, &c.EncryptedSecret, &c.SecretLast4, &c.OrganizationID, &c.ProjectID, &c.Status, &c.Priority, &c.Weight, &c.MaxConcurrency, &c.CurrentHealth, &c.LastSuccessAt, &c.LastFailureAt, &c.CooldownUntil, &c.CreatedAt, &c.UpdatedAt, &tags, &c.EffectiveWeight, &c.EffectivePriority); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(tags, &c.Tags)
		if c.Tags == nil {
			c.Tags = []string{}
		}
		c.HasSecret = true
		out = append(out, c)
	}
	return out, rows.Err()
}
