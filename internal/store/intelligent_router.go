package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

type routeCandidate struct {
	route        domain.ProjectModelRoute
	modelType    string
	capabilities []string
	inputPrice   float64
	outputPrice  float64
	hasPrice     bool
	quality      float64
	latency      float64
	score        float64
}

const routeCandidateSelect = `SELECT pmr.id,p.organization_id,pmr.project_id,pmr.model_route_id,pmr.alias,pmr.enabled,
	pmr.routing_config,r.provider_id,pr.provider_type,pr.base_url,r.upstream_model,r.credential_group_id,r.fallback_group_id,
	r.routing_policy,r.fallback_config,pmr.created_at,pmr.updated_at,m.model_type,m.capabilities,
	COALESCE(mp.input_price,0)::float8,COALESCE(mp.output_price,0)::float8,(mp.model_id IS NOT NULL),
	m.quality_score::float8,m.latency_score::float8
	FROM project_model_routes pmr JOIN projects p ON p.id=pmr.project_id
	JOIN model_routes r ON r.id=pmr.model_route_id JOIN providers pr ON pr.id=r.provider_id
	JOIN models m ON m.provider_id=r.provider_id AND m.provider_model_id=r.upstream_model
	LEFT JOIN LATERAL (SELECT model_id,input_price,output_price FROM model_prices
		WHERE model_id=m.id AND effective_from<=now() ORDER BY effective_from DESC,version DESC LIMIT 1) mp ON true`

func scanRouteCandidate(row pgx.Row) (routeCandidate, error) {
	var candidate routeCandidate
	var routeConfig, fallbackConfig, capabilities []byte
	err := row.Scan(&candidate.route.ID, &candidate.route.OrganizationID, &candidate.route.ProjectID,
		&candidate.route.ModelRouteID, &candidate.route.Alias, &candidate.route.Enabled, &routeConfig,
		&candidate.route.ProviderID, &candidate.route.ProviderType, &candidate.route.ProviderBaseURL,
		&candidate.route.UpstreamModel, &candidate.route.CredentialGroupID, &candidate.route.FallbackGroupID,
		&candidate.route.RoutingPolicy, &fallbackConfig, &candidate.route.CreatedAt, &candidate.route.UpdatedAt,
		&candidate.modelType, &capabilities, &candidate.inputPrice, &candidate.outputPrice, &candidate.hasPrice,
		&candidate.quality, &candidate.latency)
	if err != nil {
		return candidate, err
	}
	_ = json.Unmarshal(routeConfig, &candidate.route.RoutingConfig)
	_ = json.Unmarshal(fallbackConfig, &candidate.route.FallbackConfig)
	_ = json.Unmarshal(capabilities, &candidate.capabilities)
	if candidate.route.RoutingConfig == nil {
		candidate.route.RoutingConfig = map[string]any{}
	}
	if candidate.route.FallbackConfig == nil {
		candidate.route.FallbackConfig = map[string]any{}
	}
	return candidate, nil
}

// ResolveProjectRoute preserves exact alias routing and only invokes the
// intelligent router for a configured rule or one of the built-in auto aliases.
func (s *Store) ResolveProjectRoute(ctx context.Context, projectID, requestedModel string) (domain.RoutingDecision, error) {
	return s.ResolveProjectRouteForEndpoint(ctx, projectID, requestedModel, "")
}

func (s *Store) ResolveProjectRouteForEndpoint(ctx context.Context, projectID, requestedModel, endpoint string) (domain.RoutingDecision, error) {
	route, err := s.ProjectRouteByAlias(ctx, projectID, requestedModel)
	if err == nil {
		return domain.RoutingDecision{Route: route, Strategy: "manual", Score: 1, Candidates: 1}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.RoutingDecision{}, err
	}

	rule, err := s.routingRuleForAlias(ctx, projectID, requestedModel)
	if errors.Is(err, ErrNotFound) {
		rule, err = builtinRoutingRule(projectID, requestedModel)
	}
	if err != nil {
		return domain.RoutingDecision{}, err
	}
	config := make(map[string]any, len(rule.Config)+1)
	for key, value := range rule.Config {
		config[key] = value
	}
	if _, configured := config["model_type"]; !configured {
		switch endpoint {
		case "/embeddings":
			config["model_type"] = "embedding"
		case "/responses", "/chat/completions":
			config["model_type"] = "text"
		}
	}
	candidates, err := s.routingCandidates(ctx, projectID, config)
	if err != nil {
		return domain.RoutingDecision{}, err
	}
	selected, err := chooseCandidate(candidates, rule)
	if err != nil {
		return domain.RoutingDecision{}, err
	}
	return domain.RoutingDecision{Route: selected.route, Strategy: rule.Strategy, Score: selected.score, Candidates: len(candidates)}, nil
}

func builtinRoutingRule(projectID, alias string) (domain.RoutingRule, error) {
	strategy := ""
	switch strings.ToLower(strings.TrimSpace(alias)) {
	case "auto", "auto:balanced":
		strategy = "balanced"
	case "auto:cost":
		strategy = "cost_optimized"
	case "auto:quality":
		strategy = "quality_optimized"
	default:
		return domain.RoutingRule{}, ErrNotFound
	}
	return domain.RoutingRule{ProjectID: projectID, Alias: alias, Strategy: strategy, QualityWeight: .5,
		PriceWeight: .3, LatencyWeight: .2, Enabled: true, Config: map[string]any{}}, nil
}

func (s *Store) routingCandidates(ctx context.Context, projectID string, config map[string]any) ([]routeCandidate, error) {
	rows, err := s.pool.Query(ctx, routeCandidateSelect+` WHERE pmr.project_id=$1 AND pmr.deleted_at IS NULL
		AND pmr.enabled AND r.enabled AND pr.enabled AND m.enabled ORDER BY pmr.alias,pmr.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []routeCandidate
	for rows.Next() {
		candidate, err := scanRouteCandidate(rows)
		if err != nil {
			return nil, err
		}
		if candidateMatches(candidate, config) {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, rows.Err()
}

func candidateMatches(candidate routeCandidate, config map[string]any) bool {
	if config == nil {
		return true
	}
	if modelType, _ := config["model_type"].(string); modelType != "" && !strings.EqualFold(modelType, candidate.modelType) {
		return false
	}
	if providers := configStrings(config["providers"]); len(providers) > 0 && !containsFold(providers, candidate.route.ProviderID) && !containsFold(providers, candidate.route.ProviderType) {
		return false
	}
	for _, capability := range configStrings(config["required_capabilities"]) {
		if !containsFold(candidate.capabilities, capability) {
			return false
		}
	}
	if maxPrice, ok := configNumber(config["max_input_price"]); ok && (!candidate.hasPrice || candidate.inputPrice > maxPrice) {
		return false
	}
	if maxPrice, ok := configNumber(config["max_output_price"]); ok && (!candidate.hasPrice || candidate.outputPrice > maxPrice) {
		return false
	}
	return true
}

func chooseCandidate(candidates []routeCandidate, rule domain.RoutingRule) (routeCandidate, error) {
	if len(candidates) == 0 {
		return routeCandidate{}, ErrNotFound
	}
	hasPriced := false
	minPrice, maxPrice := math.MaxFloat64, 0.0
	for _, candidate := range candidates {
		if candidate.hasPrice {
			hasPriced = true
			price := candidate.inputPrice + candidate.outputPrice
			minPrice = math.Min(minPrice, price)
			maxPrice = math.Max(maxPrice, price)
		}
	}
	if rule.Strategy == "cost_optimized" && !hasPriced {
		return routeCandidate{}, ErrNotFound
	}
	eligible := append([]routeCandidate(nil), candidates...)
	if rule.Strategy == "cost_optimized" && hasPriced {
		eligible = eligible[:0]
		for _, candidate := range candidates {
			if candidate.hasPrice {
				eligible = append(eligible, candidate)
			}
		}
	}
	for index := range eligible {
		pricePenalty := 0.0
		if eligible[index].hasPrice && maxPrice > minPrice {
			pricePenalty = (eligible[index].inputPrice + eligible[index].outputPrice - minPrice) / (maxPrice - minPrice)
		} else if !eligible[index].hasPrice && hasPriced {
			pricePenalty = 1
		}
		switch rule.Strategy {
		case "cost_optimized":
			eligible[index].score = -(eligible[index].inputPrice + eligible[index].outputPrice)
		case "quality_optimized":
			eligible[index].score = eligible[index].quality/100 - pricePenalty/1000 - eligible[index].latency/100000
		default:
			eligible[index].score = rule.QualityWeight*(eligible[index].quality/100) -
				rule.PriceWeight*pricePenalty - rule.LatencyWeight*(eligible[index].latency/100)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if math.Abs(eligible[i].score-eligible[j].score) > 1e-12 {
			return eligible[i].score > eligible[j].score
		}
		if eligible[i].route.Alias != eligible[j].route.Alias {
			return eligible[i].route.Alias < eligible[j].route.Alias
		}
		return eligible[i].route.ID < eligible[j].route.ID
	})
	return eligible[0], nil
}

func scanRoutingRule(row pgx.Row) (domain.RoutingRule, error) {
	var rule domain.RoutingRule
	var config []byte
	err := row.Scan(&rule.ID, &rule.OrganizationID, &rule.ProjectID, &rule.Name, &rule.Alias, &rule.Strategy,
		&rule.QualityWeight, &rule.PriceWeight, &rule.LatencyWeight, &rule.Enabled, &config, &rule.CreatedAt, &rule.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return rule, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(config, &rule.Config)
		if rule.Config == nil {
			rule.Config = map[string]any{}
		}
	}
	return rule, err
}

const routingRuleColumns = `id,organization_id,project_id,name,alias,strategy,quality_weight::float8,
	price_weight::float8,latency_weight::float8,enabled,config,created_at,updated_at`

func (s *Store) routingRuleForAlias(ctx context.Context, projectID, alias string) (domain.RoutingRule, error) {
	return scanRoutingRule(s.pool.QueryRow(ctx, `SELECT `+routingRuleColumns+` FROM routing_rules
		WHERE project_id=$1 AND alias=$2 AND enabled`, projectID, alias))
}

func (s *Store) routingRuleByID(ctx context.Context, ruleID string) (domain.RoutingRule, error) {
	return scanRoutingRule(s.pool.QueryRow(ctx, `SELECT `+routingRuleColumns+` FROM routing_rules WHERE id=$1`, ruleID))
}

func (s *Store) ListRoutingRules(ctx context.Context, projectID string) ([]domain.RoutingRule, error) {
	query := `SELECT ` + routingRuleColumns + ` FROM routing_rules`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id=$1`
		args = append(args, projectID)
	}
	query += ` ORDER BY project_id,alias,id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RoutingRule
	for rows.Next() {
		rule, err := scanRoutingRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Store) UpsertRoutingRule(ctx context.Context, rule domain.RoutingRule) (domain.RoutingRule, error) {
	if rule.Config == nil {
		rule.Config = map[string]any{}
	}
	if rule.Strategy == "" {
		rule.Strategy = "balanced"
	}
	if rule.QualityWeight == 0 && rule.PriceWeight == 0 && rule.LatencyWeight == 0 {
		rule.QualityWeight, rule.PriceWeight, rule.LatencyWeight = .5, .3, .2
	}
	if err := s.pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, rule.ProjectID).Scan(&rule.OrganizationID); errors.Is(err, pgx.ErrNoRows) {
		return domain.RoutingRule{}, ErrNotFound
	} else if err != nil {
		return domain.RoutingRule{}, err
	}
	if rule.ID != "" {
		tag, err := s.pool.Exec(ctx, `UPDATE routing_rules SET organization_id=$2,project_id=$3,name=$4,alias=$5,strategy=$6,
			quality_weight=$7,price_weight=$8,latency_weight=$9,enabled=$10,config=$11,updated_at=now() WHERE id=$1`,
			rule.ID, rule.OrganizationID, rule.ProjectID, rule.Name, rule.Alias, rule.Strategy, rule.QualityWeight,
			rule.PriceWeight, rule.LatencyWeight, rule.Enabled, jsonBytes(rule.Config))
		if err == nil && tag.RowsAffected() == 0 {
			return domain.RoutingRule{}, ErrNotFound
		}
		if err != nil {
			return domain.RoutingRule{}, err
		}
		return s.routingRuleByID(ctx, rule.ID)
	}
	rule.ID = id.UUID()
	_, err := s.pool.Exec(ctx, `INSERT INTO routing_rules(id,organization_id,project_id,name,alias,strategy,quality_weight,
		price_weight,latency_weight,enabled,config) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		rule.ID, rule.OrganizationID, rule.ProjectID,
		rule.Name, rule.Alias, rule.Strategy, rule.QualityWeight, rule.PriceWeight, rule.LatencyWeight, rule.Enabled,
		jsonBytes(rule.Config))
	if err != nil {
		return domain.RoutingRule{}, err
	}
	return s.routingRuleByID(ctx, rule.ID)
}

func (s *Store) DeleteRoutingRule(ctx context.Context, ruleID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM routing_rules WHERE id=$1`, ruleID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func configStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func containsFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func configNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
