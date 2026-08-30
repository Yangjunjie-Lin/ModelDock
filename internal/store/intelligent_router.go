package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

type routeCandidate struct {
	route             domain.ProjectModelRoute
	modelType         string
	capabilities      []string
	inputPrice        string
	outputPrice       string
	hasPrice          bool
	quality           float64
	latency           float64
	routingMultiplier float64
	trafficCapBPS     int
	providerSlug      string
	providerConfig    map[string]any
	dataRetention     string
	processingRegions []string
	throughput        string
	p95LatencyMS      *int64
	score             float64
}

const routeCandidateSelect = `SELECT pmr.id,p.organization_id,pmr.project_id,pmr.model_route_id,pmr.alias,pmr.enabled,
	pmr.routing_config,r.provider_id,pr.provider_type,pr.base_url,r.upstream_model,r.credential_group_id,r.fallback_group_id,
	r.routing_policy,r.fallback_config,pmr.created_at,pmr.updated_at,m.model_type,m.capabilities,
	COALESCE(mp.input_price_exact,mp.input_price,0)::text,COALESCE(mp.output_price_exact,mp.output_price,0)::text,(mp.model_id IS NOT NULL),
	CASE WHEN COALESCE(qp.enabled,false) THEN qs.quality_score::float8 ELSE 50::float8 END,
	CASE WHEN COALESCE(qp.enabled,false) THEN LEAST(100,COALESCE(qs.p95_full_latency_ms,5000)::float8/100) ELSE 50::float8 END,
	CASE WHEN COALESCE(qp.enabled,false) THEN qs.routing_multiplier::float8 ELSE 1::float8 END,
	CASE WHEN COALESCE(qp.enabled,false) THEN qs.traffic_cap_bps ELSE 10000 END,
	pr.slug,pr.config,pr.data_retention_policy,pr.data_processing_regions,
	COALESCE(qs.throughput_tps,0)::text,qs.p95_full_latency_ms
	FROM project_model_routes pmr JOIN projects p ON p.id=pmr.project_id
	JOIN model_routes r ON r.id=pmr.model_route_id JOIN providers pr ON pr.id=r.provider_id
	JOIN models m ON m.provider_id=r.provider_id AND m.provider_model_id=r.upstream_model
	LEFT JOIN provider_quality_policies qp ON qp.provider_id=pr.id
	LEFT JOIN provider_quality_states qs ON qs.provider_id=pr.id
	LEFT JOIN LATERAL (SELECT model_id,input_price,input_price_exact,output_price,output_price_exact FROM model_prices
		WHERE model_id=m.id AND effective_from<=now() ORDER BY effective_from DESC,version DESC LIMIT 1) mp ON true`

func scanRouteCandidate(row pgx.Row) (routeCandidate, error) {
	var candidate routeCandidate
	var routeConfig, fallbackConfig, capabilities, providerConfig, processingRegions []byte
	err := row.Scan(&candidate.route.ID, &candidate.route.OrganizationID, &candidate.route.ProjectID,
		&candidate.route.ModelRouteID, &candidate.route.Alias, &candidate.route.Enabled, &routeConfig,
		&candidate.route.ProviderID, &candidate.route.ProviderType, &candidate.route.ProviderBaseURL,
		&candidate.route.UpstreamModel, &candidate.route.CredentialGroupID, &candidate.route.FallbackGroupID,
		&candidate.route.RoutingPolicy, &fallbackConfig, &candidate.route.CreatedAt, &candidate.route.UpdatedAt,
		&candidate.modelType, &capabilities, &candidate.inputPrice, &candidate.outputPrice, &candidate.hasPrice,
		&candidate.quality, &candidate.latency, &candidate.routingMultiplier, &candidate.trafficCapBPS,
		&candidate.providerSlug, &providerConfig, &candidate.dataRetention, &processingRegions, &candidate.throughput, &candidate.p95LatencyMS)
	if err != nil {
		return candidate, err
	}
	decodeJSONNumbers(routeConfig, &candidate.route.RoutingConfig)
	decodeJSONNumbers(fallbackConfig, &candidate.route.FallbackConfig)
	_ = json.Unmarshal(capabilities, &candidate.capabilities)
	_ = json.Unmarshal(providerConfig, &candidate.providerConfig)
	_ = json.Unmarshal(processingRegions, &candidate.processingRegions)
	if candidate.route.RoutingConfig == nil {
		candidate.route.RoutingConfig = map[string]any{}
	}
	if candidate.route.FallbackConfig == nil {
		candidate.route.FallbackConfig = map[string]any{}
	}
	if candidate.providerConfig == nil {
		candidate.providerConfig = map[string]any{}
	}
	return candidate, nil
}

// ResolveProjectRoute preserves exact alias routing and only invokes the
// intelligent router for a configured rule or one of the built-in auto aliases.
func (s *Store) ResolveProjectRoute(ctx context.Context, projectID, requestedModel string) (domain.RoutingDecision, error) {
	return s.ResolveProjectRouteForEndpointActor(ctx, projectID, "", requestedModel, "")
}

func (s *Store) ResolveProjectRouteForEndpoint(ctx context.Context, projectID, requestedModel, endpoint string) (domain.RoutingDecision, error) {
	return s.ResolveProjectRouteForEndpointActor(ctx, projectID, "", requestedModel, endpoint)
}

func (s *Store) ResolveProjectRouteForEndpointActor(ctx context.Context, projectID, userID, requestedModel, endpoint string) (domain.RoutingDecision, error) {
	return s.ResolveProjectRouteForEndpointActorRequest(ctx, projectID, userID, requestedModel, endpoint, "")
}

func (s *Store) ResolveProjectRouteForEndpointActorRequest(ctx context.Context, projectID, userID, requestedModel, endpoint, requestID string) (domain.RoutingDecision, error) {
	return s.ResolveProjectRouteForEndpointActorRequestPolicy(ctx, projectID, userID, requestedModel, endpoint, requestID, domain.RequestProviderPolicy{})
}

func (s *Store) ResolveProjectRouteForEndpointActorRequestPolicy(ctx context.Context, projectID, userID, requestedModel, endpoint, requestID string, policy domain.RequestProviderPolicy) (domain.RoutingDecision, error) {
	manual, err := scanRouteCandidate(s.pool.QueryRow(ctx, routeCandidateSelect+` WHERE pmr.project_id=$1 AND lower(pmr.alias)=lower($2)
		AND pmr.deleted_at IS NULL AND pmr.enabled AND r.enabled AND pr.enabled AND m.enabled`, projectID, requestedModel))
	if err == nil {
		config := map[string]any{}
		switch endpoint {
		case "/embeddings":
			config["model_type"] = "embedding"
		case "/responses", "/chat/completions":
			config["model_type"] = "text"
		}
		if strings.EqualFold(strings.TrimSpace(requestedModel), "auto:free") {
			config["free_only"] = true
		}
		applyProviderPolicyConfig(config, policy)
		if !candidateMatches(manual, config) {
			return domain.RoutingDecision{}, ErrNotFound
		}
		if _, admissionErr := s.CheckProviderAdmission(ctx, manual.route.OrganizationID, userID, manual.route.ProviderID, manual.route.UpstreamModel); admissionErr != nil {
			return domain.RoutingDecision{}, admissionErr
		}
		if admitted, rampErr := s.providerQualityTrafficAdmitted(ctx, manual.route.ProviderID, manual.route.ModelRouteID, requestID); rampErr != nil {
			return domain.RoutingDecision{}, rampErr
		} else if !admitted {
			return domain.RoutingDecision{}, ErrProviderQualityRampLimited
		}
		return domain.RoutingDecision{Route: manual.route, Strategy: "manual", Score: 1, Candidates: 1, Policy: policy,
			CandidateTrace: []domain.RoutingCandidateTrace{{ProviderID: manual.route.ProviderID, ProviderSlug: manual.providerSlug,
				Model: manual.route.UpstreamModel, Eligible: true, Reason: "manual project route eligible after policy gates", Score: 1,
				InputPrice: manual.inputPrice, OutputPrice: manual.outputPrice, LatencyMS: manual.p95LatencyMS,
				ThroughputTPS: manual.throughput}}}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
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
	if strings.EqualFold(strings.TrimSpace(requestedModel), "auto:free") {
		config["free_only"] = true
	}
	applyProviderPolicyConfig(config, policy)
	if policy.Sort != "" {
		switch policy.Sort {
		case "price":
			rule.Strategy = "cost_optimized"
		case "latency":
			rule.Strategy = "latency_optimized"
		case "throughput":
			rule.Strategy = "throughput_optimized"
		}
	}
	rule.Config = config
	candidates, rampLimited, err := s.routingCandidates(ctx, projectID, userID, config, requestID)
	if err != nil {
		return domain.RoutingDecision{}, err
	}
	if len(candidates) == 0 && rampLimited {
		return domain.RoutingDecision{}, ErrProviderQualityRampLimited
	}
	selected, err := chooseCandidate(candidates, rule)
	if err != nil {
		return domain.RoutingDecision{}, err
	}
	traces := make([]domain.RoutingCandidateTrace, 0, len(candidates))
	for _, candidate := range candidates {
		traces = append(traces, domain.RoutingCandidateTrace{ProviderID: candidate.route.ProviderID, ProviderSlug: candidate.providerSlug,
			Model: candidate.route.UpstreamModel, Eligible: true, Reason: "eligible after commercial, privacy, capability, and price gates",
			Score: candidate.score, InputPrice: candidate.inputPrice, OutputPrice: candidate.outputPrice,
			LatencyMS: candidate.p95LatencyMS, ThroughputTPS: candidate.throughput})
	}
	return domain.RoutingDecision{Route: selected.route, Strategy: rule.Strategy, Score: selected.score, Candidates: len(candidates), Policy: policy, CandidateTrace: traces}, nil
}

func (s *Store) providerQualityTrafficAdmitted(ctx context.Context, providerID, routeID, requestID string) (bool, error) {
	if requestID == "" {
		return true, nil
	}
	var enabled, rampEnabled, supplierLinked bool
	var capBPS int
	err := s.pool.QueryRow(ctx, `SELECT qp.enabled,qp.ramp_enabled,qs.traffic_cap_bps,
		EXISTS(SELECT 1 FROM supplier_provider_links sp WHERE sp.provider_id=qp.provider_id AND sp.status='ACTIVE')
		FROM provider_quality_policies qp JOIN provider_quality_states qs ON qs.provider_id=qp.provider_id
		WHERE qp.provider_id=$1`, providerID).Scan(&enabled, &rampEnabled, &capBPS, &supplierLinked)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !enabled || !rampEnabled || !supplierLinked {
		return true, nil
	}
	return qualityTrafficAdmitted(requestID, providerID, routeID, capBPS), nil
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
	case "auto:free":
		strategy = "cost_optimized"
	default:
		return domain.RoutingRule{}, ErrNotFound
	}
	return domain.RoutingRule{ProjectID: projectID, Alias: alias, Strategy: strategy, QualityWeight: .5,
		PriceWeight: .3, LatencyWeight: .2, Enabled: true, Config: map[string]any{}}, nil
}

func (s *Store) routingCandidates(ctx context.Context, projectID, userID string, config map[string]any, requestID string) ([]routeCandidate, bool, error) {
	rows, err := s.pool.Query(ctx, routeCandidateSelect+` WHERE pmr.project_id=$1 AND pmr.deleted_at IS NULL
		AND pmr.enabled AND r.enabled AND pr.enabled AND m.enabled ORDER BY pmr.alias,pmr.id`, projectID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var candidates []routeCandidate
	rampLimited := false
	for rows.Next() {
		candidate, err := scanRouteCandidate(rows)
		if err != nil {
			return nil, false, err
		}
		if _, admissionErr := s.CheckProviderAdmission(ctx, candidate.route.OrganizationID, userID, candidate.route.ProviderID, candidate.route.UpstreamModel); admissionErr == nil && candidateMatches(candidate, config) {
			if !qualityTrafficAdmitted(requestID, candidate.route.ProviderID, candidate.route.ID, candidate.trafficCapBPS) {
				rampLimited = true
				continue
			}
			candidates = append(candidates, candidate)
		}
	}
	return candidates, rampLimited, rows.Err()
}

func qualityTrafficAdmitted(requestID, providerID, routeID string, capBPS int) bool {
	if capBPS >= 10000 || requestID == "" {
		return true
	}
	if capBPS <= 0 {
		return false
	}
	sum := sha256.Sum256([]byte(requestID + "\x00" + providerID + "\x00" + routeID))
	bucket := binary.BigEndian.Uint64(sum[:8]) % 10000
	return bucket < uint64(capBPS)
}

func candidateMatches(candidate routeCandidate, config map[string]any) bool {
	if config == nil {
		return true
	}
	if modelType, _ := config["model_type"].(string); modelType != "" && !strings.EqualFold(modelType, candidate.modelType) {
		return false
	}
	if ignored := configStrings(config["ignore_providers"]); containsFold(ignored, candidate.route.ProviderID) || containsFold(ignored, candidate.route.ProviderType) || containsFold(ignored, candidate.providerSlug) {
		return false
	}
	if providers := configStrings(config["providers"]); len(providers) > 0 && !containsFold(providers, candidate.providerSlug) && !containsFold(providers, candidate.route.ProviderID) && !containsFold(providers, candidate.route.ProviderType) {
		return false
	}
	for _, capability := range configStrings(config["required_capabilities"]) {
		if !containsFold(candidate.capabilities, capability) {
			return false
		}
	}
	if maxPrice, ok := configDecimal(config["max_input_price"]); ok && (!candidate.hasPrice || decimalRat(candidate.inputPrice).Cmp(maxPrice) > 0) {
		return false
	}
	if maxPrice, ok := configDecimal(config["max_output_price"]); ok && (!candidate.hasPrice || decimalRat(candidate.outputPrice).Cmp(maxPrice) > 0) {
		return false
	}
	if required, _ := config["zdr"].(bool); required && !providerConfigBool(candidate.providerConfig, "zdr") {
		return false
	}
	if collection, _ := config["data_collection"].(string); strings.EqualFold(collection, "deny") && providerMayRetain(candidate.dataRetention, candidate.providerConfig) {
		return false
	}
	if quantizations := configStrings(config["quantizations"]); len(quantizations) > 0 && !containsFold(quantizations, providerConfigString(candidate.providerConfig, "quantization")) {
		return false
	}
	if regions := configStrings(config["processing_regions"]); len(regions) > 0 && !intersectsFold(regions, candidate.processingRegions) {
		return false
	}
	if minimum, ok := configDecimal(config["preferred_min_throughput"]); ok && decimalRat(candidate.throughput).Cmp(minimum) < 0 {
		return false
	}
	if maximum, ok := configInt64(config["preferred_max_latency_ms"]); ok && (candidate.p95LatencyMS == nil || *candidate.p95LatencyMS > maximum) {
		return false
	}
	if freeOnly, _ := config["free_only"].(bool); freeOnly && (!candidate.hasPrice || decimalRat(candidate.inputPrice).Sign() != 0 || decimalRat(candidate.outputPrice).Sign() != 0) {
		return false
	}
	return true
}

func applyProviderPolicyConfig(config map[string]any, policy domain.RequestProviderPolicy) {
	if len(policy.Only) > 0 {
		config["providers"] = policy.Only
	}
	if len(policy.Ignore) > 0 {
		config["ignore_providers"] = policy.Ignore
	}
	if len(policy.Order) > 0 {
		config["provider_order"] = policy.Order
	}
	if len(policy.RequiredCapabilities) > 0 {
		existing := configStrings(config["required_capabilities"])
		config["required_capabilities"] = append(existing, policy.RequiredCapabilities...)
	}
	if policy.MaxInputPrice != nil {
		config["max_input_price"] = policy.MaxInputPrice.String()
	}
	if policy.MaxOutputPrice != nil {
		config["max_output_price"] = policy.MaxOutputPrice.String()
	}
	if policy.ZDR {
		config["zdr"] = true
	}
	if policy.DataCollection != "" {
		config["data_collection"] = policy.DataCollection
	}
	if len(policy.Quantizations) > 0 {
		config["quantizations"] = policy.Quantizations
	}
	if len(policy.ProcessingRegions) > 0 {
		config["processing_regions"] = policy.ProcessingRegions
	}
	if policy.PreferredMinThroughput != nil {
		config["preferred_min_throughput"] = policy.PreferredMinThroughput.String()
	}
	if policy.PreferredMaxLatencyMS != nil {
		config["preferred_max_latency_ms"] = *policy.PreferredMaxLatencyMS
	}
	if free, _ := config["free_only"].(bool); !free {
		// auto:free sets this below after policy application.
	}
}

func providerConfigBool(config map[string]any, key string) bool {
	if value, ok := config[key].(bool); ok {
		return value
	}
	if compliance, ok := config["compliance"].(map[string]any); ok {
		value, _ := compliance[key].(bool)
		return value
	}
	return false
}

func providerConfigString(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return value
}

func providerMayRetain(retention string, config map[string]any) bool {
	if providerConfigBool(config, "zdr") {
		return false
	}
	retention = strings.ToLower(strings.TrimSpace(retention))
	return retention != "zero" && retention != "zdr" && retention != "none" && retention != "0"
}

func intersectsFold(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
				return true
			}
		}
	}
	return false
}

func configInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	case string:
		parsed, ok := new(big.Int).SetString(v, 10)
		if !ok || !parsed.IsInt64() {
			return 0, false
		}
		return parsed.Int64(), true
	default:
		return 0, false
	}
}

func chooseCandidate(candidates []routeCandidate, rule domain.RoutingRule) (routeCandidate, error) {
	if len(candidates) == 0 {
		return routeCandidate{}, ErrNotFound
	}
	hasPriced := false
	var minPrice, maxPrice *big.Rat
	for _, candidate := range candidates {
		if candidate.hasPrice {
			hasPriced = true
			price := candidateCombinedPrice(candidate)
			if minPrice == nil || price.Cmp(minPrice) < 0 {
				minPrice = new(big.Rat).Set(price)
			}
			if maxPrice == nil || price.Cmp(maxPrice) > 0 {
				maxPrice = new(big.Rat).Set(price)
			}
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
		routingMultiplier := eligible[index].routingMultiplier
		if routingMultiplier <= 0 {
			routingMultiplier = 1
		}
		pricePenalty := 0.0
		if eligible[index].hasPrice && maxPrice != nil && minPrice != nil && maxPrice.Cmp(minPrice) > 0 {
			numerator := new(big.Rat).Sub(candidateCombinedPrice(eligible[index]), minPrice)
			denominator := new(big.Rat).Sub(maxPrice, minPrice)
			pricePenalty, _ = new(big.Rat).Quo(numerator, denominator).Float64()
		} else if !eligible[index].hasPrice && hasPriced {
			pricePenalty = 1
		}
		switch rule.Strategy {
		case "cost_optimized":
			eligible[index].score = -pricePenalty
		case "quality_optimized":
			eligible[index].score = (eligible[index].quality/100 - pricePenalty/1000 - eligible[index].latency/100000) * routingMultiplier
		case "latency_optimized":
			if eligible[index].p95LatencyMS == nil {
				eligible[index].score = -math.MaxFloat64
			} else {
				eligible[index].score = -float64(*eligible[index].p95LatencyMS)
			}
		case "throughput_optimized":
			throughput, _ := new(big.Rat).SetString(eligible[index].throughput)
			if throughput != nil {
				eligible[index].score, _ = throughput.Float64()
			}
		default:
			eligible[index].score = rule.QualityWeight*(eligible[index].quality/100) -
				rule.PriceWeight*pricePenalty - rule.LatencyWeight*(eligible[index].latency/100)
			eligible[index].score *= routingMultiplier
		}
	}
	providerOrder := configStrings(rule.Config["provider_order"])
	orderRank := func(candidate routeCandidate) int {
		for index, provider := range providerOrder {
			if strings.EqualFold(provider, candidate.providerSlug) || strings.EqualFold(provider, candidate.route.ProviderID) || strings.EqualFold(provider, candidate.route.ProviderType) {
				return index
			}
		}
		return len(providerOrder) + 1
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if len(providerOrder) > 0 && orderRank(eligible[i]) != orderRank(eligible[j]) {
			return orderRank(eligible[i]) < orderRank(eligible[j])
		}
		if rule.Strategy == "cost_optimized" {
			if comparison := candidateCombinedPrice(eligible[i]).Cmp(candidateCombinedPrice(eligible[j])); comparison != 0 {
				return comparison < 0
			}
		}
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

func configDecimal(value any) (*big.Rat, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, ok := new(big.Rat).SetString(typed.String())
		return parsed, ok
	case string:
		parsed, ok := new(big.Rat).SetString(strings.TrimSpace(typed))
		return parsed, ok
	case int:
		return new(big.Rat).SetInt64(int64(typed)), true
	case int64:
		return new(big.Rat).SetInt64(typed), true
	default:
		return nil, false
	}
}

func decodeJSONNumbers(raw []byte, target any) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(target)
}

func decimalRat(value string) *big.Rat {
	parsed, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok {
		return new(big.Rat)
	}
	return parsed
}

func candidateCombinedPrice(candidate routeCandidate) *big.Rat {
	return new(big.Rat).Add(decimalRat(candidate.inputPrice), decimalRat(candidate.outputPrice))
}
