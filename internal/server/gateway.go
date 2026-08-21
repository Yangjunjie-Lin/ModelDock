package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/contentpolicy"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/providers"
	"github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/ratelimit"
	"github.com/relayedock/relayedock/internal/scheduler"
	"github.com/relayedock/relayedock/internal/store"
)

// #nosec G101 -- Gin context key label; this is not an API credential.
const apiKeyContext = "relayedock.api_key"

// #nosec G101 -- Gin context key label; this is not an API credential.
const apiKeyVersionContext = "relayedock.api_key_version"

var embeddedAPIKeyPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(rdk_(?:live|test)_[A-Za-z0-9_-]{43})(?:$|[^A-Za-z0-9_-])`)

func GatewayEngine(d Dependencies) *gin.Engine {
	r := gin.New()
	configureTrustedProxies(r, d)
	r.Use(recovery(d.Logger), requestMiddleware(d.Logger, d.Metrics, "gateway"), cors(d.Config.AllowedOrigins), requestBodyLimit(d.Config.MaxBodyBytes))
	registerHealth(r, d)
	v1 := r.Group("/v1")
	v1.Use(gatewayAuth(d))
	v1.GET("/models", func(c *gin.Context) { listGatewayModels(c, d) })
	v1.POST("/responses", func(c *gin.Context) { proxyGateway(c, d, "/responses") })
	v1.POST("/chat/completions", func(c *gin.Context) { proxyGateway(c, d, "/chat/completions") })
	v1.POST("/embeddings", func(c *gin.Context) { proxyGateway(c, d, "/embeddings") })
	return r
}

func gatewayAuth(d Dependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			gatewayAuthError(c)
			c.Abort()
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if !apikey.LooksValid(raw) {
			gatewayAuthError(c)
			c.Abort()
			return
		}
		authn, err := d.Store.AuthenticateAPIKeyVersion(c.Request.Context(), d.APIKeys.Hash(raw))
		if err != nil {
			gatewayAuthError(c)
			c.Abort()
			return
		}
		key := authn.Key
		if _, err = d.Store.CheckProjectAccess(c.Request.Context(), key.UserID, key.ProjectID, "DEVELOPER"); err != nil {
			gatewayAuthError(c)
			c.Abort()
			return
		}
		if allowed, _, riskErr := d.Store.GatewayRiskCheck(c.Request.Context(), key.UserID, key.OrganizationID); riskErr != nil || !allowed {
			if riskErr == nil {
				riskErr = store.ErrRiskRestricted
			}
			if errors.Is(riskErr, store.ErrRiskFrozen) {
				openAIError(c, http.StatusForbidden, "risk_frozen", "This account or organization is frozen pending review.")
			} else {
				openAIError(c, http.StatusForbidden, "risk_restricted", "This account or organization is restricted pending review.")
			}
			c.Abort()
			return
		}
		c.Set(apiKeyContext, key)
		c.Set(apiKeyVersionContext, authn.Version)
		c.Next()
	}
}
func gatewayAuthError(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="RelayDock"`)
	openAIError(c, http.StatusUnauthorized, "invalid_api_key", "Incorrect, expired, or disabled RelayDock API key provided.")
}
func gatewayKey(c *gin.Context) domain.APIKey {
	v, _ := c.Get(apiKeyContext)
	k, _ := v.(domain.APIKey)
	return k
}

func listGatewayModels(c *gin.Context, d Dependencies) {
	key := gatewayKey(c)
	if !allowRate(c, d, key, 1) {
		return
	}
	routes, err := d.Store.ListProjectRoutes(c.Request.Context(), key.ProjectID)
	if err != nil {
		openAIError(c, 500, "internal_error", "Could not load the model catalog.")
		return
	}
	data := make([]gin.H, 0, len(routes)+4)
	seen := make(map[string]bool)
	activeRoutes := 0
	for _, route := range routes {
		if !route.Enabled || !d.Store.ProviderCommerciallyAvailableForModel(c.Request.Context(), key.OrganizationID, key.UserID, route.ProviderID, route.UpstreamModel) {
			continue
		}
		activeRoutes++
		if !modelAllowed(key.AllowedModels, route.Alias) {
			continue
		}
		seen[route.Alias] = true
		data = append(data, gin.H{"id": route.Alias, "object": "model", "created": route.CreatedAt.Unix(), "owned_by": "modeldock"})
	}
	rules, err := d.Store.ListRoutingRules(c.Request.Context(), key.ProjectID)
	if err != nil {
		openAIError(c, 500, "internal_error", "Could not load the routing catalog.")
		return
	}
	for _, rule := range rules {
		if !rule.Enabled || seen[rule.Alias] || !modelAllowed(key.AllowedModels, rule.Alias) {
			continue
		}
		seen[rule.Alias] = true
		data = append(data, gin.H{"id": rule.Alias, "object": "model", "created": rule.CreatedAt.Unix(), "owned_by": "modeldock-router"})
	}
	if activeRoutes > 0 {
		for _, alias := range []string{"auto", "auto:cost", "auto:quality", "auto:balanced"} {
			if !seen[alias] && modelAllowed(key.AllowedModels, alias) {
				data = append(data, gin.H{"id": alias, "object": "model", "created": time.Now().Unix(), "owned_by": "modeldock-router"})
			}
		}
	}
	c.JSON(200, gin.H{"object": "list", "data": data})
}

func proxyGateway(c *gin.Context, d Dependencies, endpoint string) {
	d.Metrics.Request()
	started := time.Now()
	key := gatewayKey(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, d.Config.MaxBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if _, ok := err.(*http.MaxBytesError); ok {
			openAIError(c, 413, "request_too_large", "The request body exceeds the configured size limit.")
		} else {
			openAIError(c, 400, "invalid_request", "Could not read the request body.")
		}
		d.Metrics.Error()
		return
	}
	var payload map[string]any
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil {
		openAIError(c, 400, "invalid_json", "The request body must be a valid JSON object.")
		d.Metrics.Error()
		return
	}
	requestedModel, ok := payload["model"].(string)
	if !ok || strings.TrimSpace(requestedModel) == "" {
		openAIError(c, 400, "invalid_request", "The model field is required.")
		d.Metrics.Error()
		return
	}
	requestedModel = strings.TrimSpace(requestedModel)
	logEntry := domain.RequestLog{RequestID: requestID(c), TraceID: traceID(c), UserID: key.UserID, APIKeyID: key.ID, OrganizationID: key.OrganizationID, ProjectID: key.ProjectID, RequestedModel: requestedModel, Endpoint: endpoint, Streaming: false, CreatedAt: time.Now().UTC()}
	var providerAttemptStarted time.Time
	var providerAttemptTTFTMS *int64
	stream, _ := payload["stream"].(bool)
	doneMetric := d.Metrics.Begin(stream)
	defer doneMetric()
	logEntry.Streaming = stream
	defer func() {
		logEntry.LatencyMS = time.Since(started).Milliseconds()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := d.Store.InsertScopedRequestLog(ctx, logEntry); err != nil {
			d.Logger.Error("request_log_failed", "request_id", logEntry.RequestID, "error", err)
		}
		d.Metrics.ObserveRequest(logEntry.ProviderID, logEntry.ResolvedModel, logEntry.StatusCode, logEntry.InputTokens, logEntry.OutputTokens, logEntry.LatencyMS, logEntry.TTFTMS, strings.Contains(strings.ToLower(logEntry.ErrorCode), "fallback"))
		if err := d.Store.DetectUsageAnomalies(ctx, logEntry); err != nil {
			d.Logger.Error("usage_anomaly_detection_failed", "request_id", logEntry.RequestID, "error", err)
		}
		if logEntry.ErrorCode != "project_budget_exceeded" {
			if err := commitProjectBudget(ctx, d, key, logEntry); err != nil {
				d.Logger.Error("project_budget_commit_failed", "request_id", logEntry.RequestID, "project_id", key.ProjectID, "error", err)
			}
		}
	}()
	if leaked, leakErr := detectEmbeddedAPIKeyLeak(c.Request.Context(), d, body, key.UserID, c.ClientIP()); leakErr != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "api_key_leak_check_unavailable"
		openAIError(c, logEntry.StatusCode, logEntry.ErrorCode, "API key leak detection is temporarily unavailable.")
		d.Metrics.Error()
		return
	} else if leaked {
		logEntry.StatusCode = http.StatusForbidden
		logEntry.ErrorCode = "api_key_leak_detected"
		openAIError(c, logEntry.StatusCode, logEntry.ErrorCode, "A RelayDock API key was detected in request content and automatically frozen.")
		d.Metrics.Error()
		return
	}
	if policyErr := evaluateContentPolicy(c, d, contentpolicy.Request{Phase: contentpolicy.PreRequest, OrganizationID: key.OrganizationID, UserID: key.UserID, Model: requestedModel, RequestID: requestID(c), Body: body}); policyErr != nil {
		logEntry.StatusCode = http.StatusForbidden
		logEntry.ErrorCode = "content_policy_blocked"
		if strings.Contains(policyErr.Error(), "manual review") {
			logEntry.StatusCode = http.StatusAccepted
			logEntry.ErrorCode = "manual_review_required"
		}
		openAIError(c, logEntry.StatusCode, logEntry.ErrorCode, "The request was held by content safety policy.")
		d.Metrics.Error()
		return
	}
	clientRequestID, ok := providerClientRequestID(c)
	if !ok {
		logEntry.StatusCode = http.StatusBadRequest
		logEntry.ErrorCode = "invalid_client_request_id"
		d.Metrics.Error()
		openAIError(c, http.StatusBadRequest, "invalid_client_request_id", "X-Client-Request-Id must not exceed 512 bytes.")
		return
	}
	idempotencyKey, ok := gatewayIdempotencyKey(c)
	if !ok {
		logEntry.StatusCode = http.StatusBadRequest
		logEntry.ErrorCode = "invalid_idempotency_key"
		d.Metrics.Error()
		openAIError(c, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must not exceed 200 characters.")
		return
	}
	routingDecision, err := d.Store.ResolveProjectRouteForEndpointActorRequest(c.Request.Context(), key.ProjectID, key.UserID, requestedModel, endpoint, logEntry.RequestID)
	projectRoute := routingDecision.Route
	if err != nil || !projectRoute.Enabled {
		if errors.Is(err, store.ErrProviderQualityCircuitOpen) || errors.Is(err, store.ErrProviderQualityRampLimited) {
			logEntry.StatusCode = http.StatusServiceUnavailable
			logEntry.ErrorCode = "provider_quality_circuit_open"
			message := "The requested model is temporarily unavailable while Provider quality recovers."
			if errors.Is(err, store.ErrProviderQualityRampLimited) {
				logEntry.ErrorCode = "provider_quality_ramp_limited"
				message = "The requested model is temporarily unavailable while Provider traffic ramps safely."
			}
			d.Metrics.Error()
			openAIError(c, http.StatusServiceUnavailable, "provider_unavailable", message)
			return
		}
		if isProviderCommercialDenial(err) {
			logEntry.StatusCode = http.StatusForbidden
			logEntry.ErrorCode = providerCommercialErrorCode(err)
			d.Metrics.Error()
			openAIError(c, http.StatusForbidden, logEntry.ErrorCode, providerCommercialErrorMessage(err))
			return
		}
		logEntry.StatusCode = http.StatusNotFound
		logEntry.ErrorCode = "model_not_found"
		d.Metrics.Error()
		openAIError(c, http.StatusNotFound, "model_not_found", "The requested model does not exist or is not enabled for this project.")
		return
	}
	if routingDecision.Strategy != "manual" {
		if entitlementErr := d.Store.RequireBooleanEntitlement(c.Request.Context(), key.OrganizationID, "advanced_routing"); entitlementErr != nil {
			logEntry.StatusCode = http.StatusForbidden
			logEntry.ErrorCode = "subscription_entitlement_required"
			d.Metrics.Error()
			openAIError(c, http.StatusForbidden, "subscription_entitlement_required", "The current subscription does not include advanced routing.")
			return
		}
	}
	logEntry.RouteID = projectRoute.ModelRouteID
	logEntry.ProviderID = projectRoute.ProviderID
	logEntry.ResolvedModel = projectRoute.UpstreamModel
	admission, admissionErr := d.Store.CheckProviderAdmission(c.Request.Context(), key.OrganizationID, key.UserID, projectRoute.ProviderID, projectRoute.UpstreamModel)
	if admissionErr != nil {
		if errors.Is(admissionErr, store.ErrProviderQualityCircuitOpen) {
			logEntry.StatusCode = http.StatusServiceUnavailable
			logEntry.ErrorCode = "provider_quality_circuit_open"
			d.Metrics.Error()
			openAIError(c, http.StatusServiceUnavailable, "provider_unavailable", "The requested model is temporarily unavailable while Provider quality recovers.")
			return
		}
		logEntry.StatusCode = http.StatusForbidden
		logEntry.ErrorCode = providerCommercialErrorCode(admissionErr)
		d.Metrics.Error()
		openAIError(c, http.StatusForbidden, logEntry.ErrorCode, providerCommercialErrorMessage(admissionErr))
		return
	}
	if admission.RateLimit != nil {
		providerRate, rateErr := d.Limiter.AllowProvider(c.Request.Context(), projectRoute.ProviderID, int64(*admission.RateLimit))
		if rateErr != nil {
			logEntry.StatusCode = http.StatusServiceUnavailable
			logEntry.ErrorCode = "provider_capacity_unavailable"
			d.Metrics.Error()
			openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Provider capacity state is temporarily unavailable.")
			return
		}
		if !providerRate.Allowed {
			logEntry.StatusCode = http.StatusTooManyRequests
			logEntry.ErrorCode = "provider_capacity_unavailable"
			d.Metrics.Error()
			c.Header("Retry-After", strconv.Itoa(int(providerRate.RetryAfter.Seconds())))
			openAIError(c, http.StatusTooManyRequests, "provider_capacity_unavailable", "The requested model is temporarily at commercial capacity.")
			return
		}
	}
	if !modelAllowed(key.AllowedModels, requestedModel) {
		logEntry.StatusCode = 403
		logEntry.ErrorCode = "model_not_allowed"
		d.Metrics.Error()
		openAIError(c, 403, "model_not_allowed", "This API key is not allowed to use the requested model.")
		return
	}
	walletAllowed, walletErr := d.Store.WalletAllowsRequest(c.Request.Context(), key.OrganizationID)
	if walletErr != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "billing_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Billing state is temporarily unavailable.")
		return
	}
	if !walletAllowed {
		logEntry.StatusCode = http.StatusPaymentRequired
		logEntry.ErrorCode = "insufficient_balance"
		d.Metrics.Error()
		openAIError(c, http.StatusPaymentRequired, "insufficient_balance", "The organization wallet is frozen or has insufficient prepaid balance.")
		return
	}
	estimatedTokens := len(body) / 4
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	maxOutputTokens := maximumOutputTokens(payload, endpoint)
	var fundingQuote domain.PricingQuote
	if quote, quoteErr := d.Store.QuotePricing(c.Request.Context(), store.PriceQuoteRequest{
		OrganizationID: key.OrganizationID, ProviderID: projectRoute.ProviderID, Model: projectRoute.UpstreamModel,
		InputTokens: int64(estimatedTokens), OutputTokens: maxOutputTokens, TaxRate: "0", ExchangeRate: "1",
		CreatedBy: stringPtr(key.UserID),
	}); quoteErr == nil {
		fundingQuote = quote
		logEntry.PricingVersionID = quote.PricingVersionID
		logEntry.PromotionAmount = quote.PromotionAmount
		logEntry.ExchangeRate = quote.ExchangeRate
		logEntry.TaxRate = quote.TaxRate
	} else if errors.Is(quoteErr, store.ErrProviderNotContracted) {
		logEntry.StatusCode = http.StatusForbidden
		logEntry.ErrorCode = "provider_pricing_disabled"
		d.Metrics.Error()
		openAIError(c, http.StatusForbidden, "provider_pricing_disabled", "The provider contract, region, or pricing switch does not allow this request.")
		return
	} else if errors.Is(quoteErr, store.ErrPricingUnavailable) {
		wallet, walletReadErr := d.Store.WalletByOrganization(c.Request.Context(), key.OrganizationID)
		if walletReadErr != nil || wallet.BillingMode == "PREPAID" || wallet.CreditEnforced {
			logEntry.StatusCode = http.StatusServiceUnavailable
			logEntry.ErrorCode = "pricing_state_unavailable"
			d.Metrics.Error()
			openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Approved pricing is required before this wallet can reserve funds.")
			return
		}
	} else {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "pricing_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Commercial pricing state is temporarily unavailable.")
		return
	}
	d.Metrics.ObserveModelPricing(projectRoute.UpstreamModel, fundingQuote.RetailAmount, fundingQuote.ProviderCostAmount, fundingQuote.GrossMargin)
	fingerprint := fundingRequestFingerprint(key.ProjectID, endpoint, body)
	operation, replay, fundingErr := d.Store.ReserveFunding(c.Request.Context(), store.FundingReservationRequest{
		OrganizationID: key.OrganizationID, ProjectID: key.ProjectID, APIKeyID: key.ID, RequestID: logEntry.RequestID,
		IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint, PricingVersionID: fundingQuote.PricingVersionID,
		Currency: firstNonEmpty(fundingQuote.Currency, "USD"), MaximumAmount: firstNonEmpty(fundingQuote.FinalAmount, "0"),
		PromotionAmount: fundingQuote.PromotionAmount, TaxRate: fundingQuote.TaxRate, ExchangeRate: fundingQuote.ExchangeRate,
		EstimatedInput: int64(estimatedTokens), MaxOutput: maxOutputTokens, CreatedBy: stringPtr(key.UserID),
	})
	if fundingErr != nil {
		if errors.Is(fundingErr, store.ErrWalletUnavailable) {
			d.Metrics.IncWalletReservationFailure()
			_ = d.Store.RecordOperationalAlert(c.Request.Context(), "WALLET_RESERVATION_FAILED", "WARNING", "Wallet reservation failed for a gateway request.", "wallet-reservation:"+key.OrganizationID, map[string]any{"organization_id": key.OrganizationID})
			logEntry.StatusCode = http.StatusPaymentRequired
			logEntry.ErrorCode = "insufficient_balance"
			d.Metrics.Error()
			openAIError(c, http.StatusPaymentRequired, "insufficient_balance", "The organization wallet cannot reserve the maximum request cost.")
			return
		}
		if errors.Is(fundingErr, store.ErrIdempotencyConflict) {
			logEntry.StatusCode = http.StatusConflict
			logEntry.ErrorCode = "idempotency_conflict"
			d.Metrics.Error()
			openAIError(c, http.StatusConflict, "idempotency_conflict", "The Idempotency-Key was already used for a different logical request.")
			return
		}
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "billing_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Billing reservation state is temporarily unavailable.")
		return
	}
	logEntry.FundingOperationID = operation.ID
	if replay {
		c.Header("X-RelayDock-Original-Request-Id", operation.RequestID)
		logEntry.StatusCode = http.StatusConflict
		logEntry.ErrorCode = "idempotent_replay"
		openAIError(c, http.StatusConflict, "idempotent_replay", "This logical request was already admitted; no additional funds were reserved or charged.")
		return
	}
	fundingUsage := tokenUsage{}
	fundingUsageSource := "NO_PROVIDER_USAGE"
	fundingObservedBytes := int64(0)
	lastFundingHeartbeatBytes := int64(0)
	lastFundingHeartbeatAt := time.Now()
	fundingPartial := false
	fundingWaive := true
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		settlementStarted := time.Now()
		settled, settleErr := d.Store.SettleFunding(ctx, store.FundingSettlementRequest{OperationID: operation.ID,
			InputTokens: fundingUsage.Input, CachedInput: fundingUsage.Cached, OutputTokens: fundingUsage.Output,
			ObservedBytes: fundingObservedBytes, UsageSource: fundingUsageSource, FailureCode: logEntry.ErrorCode,
			PartialFailure: fundingPartial, Waive: fundingWaive, SettlementSource: "gateway"})
		d.Metrics.ObserveSettlement(time.Since(settlementStarted).Milliseconds(), settleErr == nil)
		if settleErr != nil {
			d.Metrics.IncSettlementFailure()
			_ = d.Store.RecordOperationalAlert(ctx, "SETTLEMENT_FAILED", "CRITICAL", "A gateway funding settlement failed and requires recovery.", "settlement:"+operation.ID, map[string]any{"request_id": logEntry.RequestID, "funding_operation_id": operation.ID})
			d.Logger.Error("funding_settlement_failed", "request_id", logEntry.RequestID, "funding_operation_id", operation.ID, "error", settleErr)
			return
		}
		logEntry.UsageSource = settled.UsageSource
	}()
	blocked, budgetErr := admitProjectBudgets(c.Request.Context(), d, key, logEntry.RequestID, int64(estimatedTokens))
	if budgetErr != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "budget_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Project budget state is temporarily unavailable.")
		return
	}
	if blocked {
		logEntry.StatusCode = http.StatusForbidden
		logEntry.ErrorCode = "project_budget_exceeded"
		d.Metrics.Error()
		openAIError(c, http.StatusForbidden, "project_budget_exceeded", "The project budget has been reached.")
		return
	}
	if key.MonthlyTokenLimit != nil {
		used, err := d.Store.MonthlyTokens(c.Request.Context(), key.ID)
		if err != nil {
			logEntry.StatusCode = 503
			logEntry.ErrorCode = "quota_state_unavailable"
			openAIError(c, 503, "service_unavailable", "Quota state is temporarily unavailable.")
			return
		}
		if used+int64(estimatedTokens) > *key.MonthlyTokenLimit {
			logEntry.StatusCode = 403
			logEntry.ErrorCode = "quota_exceeded"
			openAIError(c, 403, "quota_exceeded", "The monthly token quota has been reached.")
			return
		}
	}
	if key.MonthlyCostLimit != nil {
		used, err := d.Store.MonthlyCost(c.Request.Context(), key.ID)
		if err != nil {
			logEntry.StatusCode = 503
			logEntry.ErrorCode = "quota_state_unavailable"
			d.Metrics.Error()
			openAIError(c, 503, "service_unavailable", "Quota state is temporarily unavailable.")
			return
		}
		if used.Compare(*key.MonthlyCostLimit) >= 0 {
			logEntry.StatusCode = 403
			logEntry.ErrorCode = "quota_exceeded"
			d.Metrics.Error()
			openAIError(c, 403, "quota_exceeded", "The monthly cost quota has been reached.")
			return
		}
	}
	user, err := d.Store.UserByID(c.Request.Context(), key.UserID)
	if err != nil {
		logEntry.StatusCode = 503
		logEntry.ErrorCode = "quota_state_unavailable"
		d.Metrics.Error()
		openAIError(c, 503, "service_unavailable", "Quota state is temporarily unavailable.")
		return
	}
	if user.MonthlyTokenLimit != nil || user.MonthlyCostLimit != nil {
		tokens, cost, err := d.Store.MonthlyUserUsage(c.Request.Context(), user.ID)
		if err != nil {
			logEntry.StatusCode = 503
			logEntry.ErrorCode = "quota_state_unavailable"
			d.Metrics.Error()
			openAIError(c, 503, "service_unavailable", "Quota state is temporarily unavailable.")
			return
		}
		if (user.MonthlyTokenLimit != nil && tokens+int64(estimatedTokens) > *user.MonthlyTokenLimit) || (user.MonthlyCostLimit != nil && cost.Compare(*user.MonthlyCostLimit) >= 0) {
			logEntry.StatusCode = 403
			logEntry.ErrorCode = "quota_exceeded"
			d.Metrics.Error()
			openAIError(c, 403, "quota_exceeded", "The user monthly quota has been reached.")
			return
		}
	}
	if key.TeamID != "" {
		team, err := d.Store.TeamByID(c.Request.Context(), key.TeamID)
		if err != nil || team.Status != "ACTIVE" {
			logEntry.StatusCode = http.StatusForbidden
			logEntry.ErrorCode = "team_unavailable"
			d.Metrics.Error()
			openAIError(c, http.StatusForbidden, "team_unavailable", "The API key team is not active.")
			return
		}
		if team.MonthlyTokenLimit != nil || team.MonthlyCostLimit != nil {
			tokens, cost, err := d.Store.TeamMonthlyUsage(c.Request.Context(), team.ID)
			if err != nil {
				logEntry.StatusCode = http.StatusServiceUnavailable
				logEntry.ErrorCode = "quota_state_unavailable"
				d.Metrics.Error()
				openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Team quota state is temporarily unavailable.")
				return
			}
			if (team.MonthlyTokenLimit != nil && tokens+int64(estimatedTokens) > *team.MonthlyTokenLimit) ||
				(team.MonthlyCostLimit != nil && cost.Compare(*team.MonthlyCostLimit) >= 0) {
				logEntry.StatusCode = http.StatusForbidden
				logEntry.ErrorCode = "team_quota_exceeded"
				d.Metrics.Error()
				openAIError(c, http.StatusForbidden, "team_quota_exceeded", "The team monthly quota has been reached.")
				return
			}
		}
	}
	if !allowRate(c, d, key, estimatedTokens) {
		logEntry.StatusCode = c.Writer.Status()
		logEntry.ErrorCode = "rate_limit_exceeded"
		d.Metrics.Error()
		return
	}
	entitlements, entitlementErr := d.Store.EffectiveEntitlements(c.Request.Context(), key.OrganizationID)
	if entitlementErr != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "subscription_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Subscription entitlement state is temporarily unavailable.")
		return
	}
	organizationRate, entitlementErr := d.Limiter.AllowOrganization(c.Request.Context(), key.OrganizationID, entitlements.RequestsPerMinute)
	if entitlementErr != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "subscription_rate_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Subscription rate limiting is temporarily unavailable.")
		return
	}
	c.Header("X-RelayDock-Subscription-RateLimit-Limit", strconv.FormatInt(entitlements.RequestsPerMinute, 10))
	c.Header("X-RelayDock-Subscription-RateLimit-Remaining", strconv.FormatInt(max64(0, entitlements.RequestsPerMinute-organizationRate.Requests), 10))
	if !organizationRate.Allowed {
		logEntry.StatusCode = http.StatusTooManyRequests
		logEntry.ErrorCode = "subscription_rate_limit_exceeded"
		d.Metrics.Error()
		c.Header("Retry-After", strconv.Itoa(int(organizationRate.RetryAfter.Seconds())))
		openAIError(c, http.StatusTooManyRequests, "subscription_rate_limit_exceeded", "The organization subscription request rate has been reached.")
		return
	}
	releaseConcurrency, _, entitlementErr := d.Limiter.AcquireOrganizationConcurrency(c.Request.Context(), key.OrganizationID, entitlements.Concurrency)
	if entitlementErr != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "subscription_concurrency_state_unavailable"
		d.Metrics.Error()
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Subscription concurrency state is temporarily unavailable.")
		return
	}
	if releaseConcurrency == nil {
		logEntry.StatusCode = http.StatusTooManyRequests
		logEntry.ErrorCode = "subscription_concurrency_exceeded"
		d.Metrics.Error()
		openAIError(c, http.StatusTooManyRequests, "subscription_concurrency_exceeded", "The organization subscription concurrency limit has been reached.")
		return
	}
	defer func() {
		releaseContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if releaseErr := releaseConcurrency(releaseContext); releaseErr != nil {
			d.Logger.Error("subscription_concurrency_release_failed", "organization_id", key.OrganizationID, "error", releaseErr)
		}
	}()
	payload["model"] = projectRoute.UpstreamModel
	upstreamBody, err := json.Marshal(payload)
	if err != nil {
		logEntry.StatusCode = 400
		logEntry.ErrorCode = "invalid_request"
		openAIError(c, 400, "invalid_request", "Could not encode the request.")
		return
	}
	if policyErr := evaluateContentPolicy(c, d, contentpolicy.Request{Phase: contentpolicy.ProviderNative, OrganizationID: key.OrganizationID, UserID: key.UserID, Model: projectRoute.UpstreamModel, RequestID: logEntry.RequestID, Body: upstreamBody}); policyErr != nil {
		logEntry.StatusCode = http.StatusForbidden
		logEntry.ErrorCode = "provider_moderation_blocked"
		openAIError(c, http.StatusForbidden, logEntry.ErrorCode, "The request was blocked by provider moderation policy.")
		d.Metrics.Error()
		return
	}
	constraints := mergeCredentialConstraints(routeCredentialConstraints(projectRoute.FallbackConfig), routeCredentialConstraints(projectRoute.RoutingConfig))
	selectedGroupID := projectRoute.CredentialGroupID
	selection, err := d.Scheduler.SelectConstrainedForOrganization(c.Request.Context(), projectRoute.CredentialGroupID, projectRoute.RoutingPolicy, constraints, key.OrganizationID)
	if err != nil && projectRoute.FallbackGroupID != nil {
		selectedGroupID = *projectRoute.FallbackGroupID
		selection, err = d.Scheduler.SelectConstrainedForOrganization(c.Request.Context(), *projectRoute.FallbackGroupID, projectRoute.RoutingPolicy, constraints, key.OrganizationID)
	}
	if err != nil {
		logEntry.StatusCode = 503
		logEntry.ErrorCode = "provider_unavailable"
		message := "No healthy provider credential is currently available."
		if errors.Is(err, scheduler.ErrStateUnavailable) {
			message = "Credential scheduling state is temporarily unavailable."
		}
		openAIError(c, 503, "provider_unavailable", message)
		d.Metrics.Error()
		return
	}
	defer selection.Release()
	cred := selection.Credential
	logEntry.CredentialID = cred.ID
	logEntry.SchedulerReason = selection.Reason
	if logEntry.SchedulerReason == nil {
		logEntry.SchedulerReason = map[string]any{}
	}
	logEntry.SchedulerReason["model_router"] = map[string]any{"strategy": routingDecision.Strategy,
		"score": routingDecision.Score, "candidates": routingDecision.Candidates, "selected_alias": projectRoute.Alias,
		"selected_model": projectRoute.UpstreamModel, "provider_type": projectRoute.ProviderType}
	secret, err := d.Vault.Decrypt(cred.EncryptedSecret, cred.ID)
	if err != nil {
		logEntry.StatusCode = 503
		logEntry.ErrorCode = "credential_decryption_failed"
		_ = d.Store.SetCredentialStatus(c.Request.Context(), cred.ID, "UNHEALTHY", nil)
		openAIError(c, 503, "provider_unavailable", "The selected credential could not be loaded.")
		d.Metrics.Error()
		return
	}
	d.Metrics.Upstream()
	adapter, err := providerAdapter(d, projectRoute.ProviderType)
	if err != nil {
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "provider_adapter_unavailable"
		openAIError(c, http.StatusServiceUnavailable, "provider_unavailable", "The configured provider adapter is unavailable.")
		d.Metrics.Error()
		return
	}
	attempt, err := d.Store.BeginFundingAttempt(c.Request.Context(), operation.ID, projectRoute.ProviderID, cred.ID,
		selectedGroupID, selectedGroupID != projectRoute.CredentialGroupID)
	if err != nil {
		if errors.Is(err, store.ErrProviderQualityCircuitOpen) {
			logEntry.StatusCode = http.StatusServiceUnavailable
			logEntry.ErrorCode = "provider_quality_circuit_open"
			openAIError(c, http.StatusServiceUnavailable, "provider_unavailable", "The requested model is temporarily unavailable while Provider quality recovers.")
			d.Metrics.Error()
			return
		}
		if isProviderCommercialDenial(err) {
			logEntry.StatusCode = http.StatusForbidden
			logEntry.ErrorCode = providerCommercialErrorCode(err)
			openAIError(c, http.StatusForbidden, logEntry.ErrorCode, providerCommercialErrorMessage(err))
			d.Metrics.Error()
			return
		}
		logEntry.StatusCode = http.StatusServiceUnavailable
		logEntry.ErrorCode = "billing_attempt_state_unavailable"
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Provider attempt state is temporarily unavailable.")
		d.Metrics.Error()
		return
	}
	providerCtx, cancelProvider := context.WithTimeout(c.Request.Context(), d.Config.ProviderTimeout)
	defer cancelProvider()
	forward := providers.ForwardRequest{BaseURL: projectRoute.ProviderBaseURL, Path: endpoint, Body: bytes.NewReader(upstreamBody), ContentType: "application/json", Accept: c.GetHeader("Accept"), ClientRequestID: clientRequestID, Traceparent: traceparent(c), Credential: providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)}}
	providerAttemptStarted = time.Now()
	resp, err := adapter.Forward(providerCtx, forward)
	if err != nil {
		recordTrafficQualityObservation(d, projectRoute.ProviderID, cred.ID, attempt.ID, providerAttemptStarted, nil, 0, false, 0, "provider_connection_error")
		attemptStatus := "FAILED"
		if errors.Is(err, context.DeadlineExceeded) {
			attemptStatus = "TIMED_OUT"
		}
		if errors.Is(err, context.Canceled) {
			attemptStatus = "CANCELLED"
		}
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = d.Store.FinishFundingAttempt(attemptCtx, attempt.ID, attemptStatus, 0, "", "provider_connection_error")
		attemptCancel()
		selection.Release()
		if projectRoute.FallbackGroupID != nil && selectedGroupID == projectRoute.CredentialGroupID &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			d.Metrics.IncFallback()
			cancelProvider()
			providerCtx, cancelProvider = context.WithTimeout(c.Request.Context(), d.Config.ProviderTimeout)
			defer cancelProvider()
			selectedGroupID = *projectRoute.FallbackGroupID
			selection, err = d.Scheduler.SelectConstrainedForOrganization(c.Request.Context(), selectedGroupID, projectRoute.RoutingPolicy, constraints, key.OrganizationID)
			if err == nil {
				defer selection.Release()
				cred = selection.Credential
				logEntry.CredentialID = cred.ID
				secret, err = d.Vault.Decrypt(cred.EncryptedSecret, cred.ID)
			}
			if err == nil {
				attempt, err = d.Store.BeginFundingAttempt(c.Request.Context(), operation.ID, projectRoute.ProviderID, cred.ID, selectedGroupID, true)
			}
			if err == nil {
				forward.Body = bytes.NewReader(upstreamBody)
				forward.Credential = providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)}
				providerAttemptStarted = time.Now()
				providerAttemptTTFTMS = nil
				resp, err = adapter.Forward(providerCtx, forward)
				if err != nil {
					recordTrafficQualityObservation(d, projectRoute.ProviderID, cred.ID, attempt.ID, providerAttemptStarted, nil, 0, false, 0, "provider_connection_error")
				}
			}
		}
	}
	if err != nil {
		_ = d.Store.RecordOperationalAlert(c.Request.Context(), "PROVIDER_FAILURE", "CRITICAL", "A provider request failed and the gateway could not complete the route.", "provider-failure:"+projectRoute.ProviderID, map[string]any{"provider_id": projectRoute.ProviderID, "request_id": logEntry.RequestID})
		if attempt.ID != "" {
			attemptStatus := "FAILED"
			if errors.Is(err, context.DeadlineExceeded) {
				attemptStatus = "TIMED_OUT"
			}
			attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = d.Store.FinishFundingAttempt(attemptCtx, attempt.ID, attemptStatus, 0, "", "provider_connection_error")
			attemptCancel()
		}
		logEntry.StatusCode = 502
		logEntry.ErrorCode = "provider_connection_error"
		d.Store.MarkCredentialFailure(c.Request.Context(), cred.ID)
		openAIError(c, 502, "provider_error", "The upstream provider could not be reached.")
		d.Metrics.Error()
		return
	}
	defer resp.Body.Close()
	logEntry.StatusCode = resp.StatusCode
	logEntry.UpstreamRequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("openai-request-id"))
	if resp.StatusCode == http.StatusTooManyRequests {
		_ = d.Store.RecordOperationalAlert(c.Request.Context(), "PROVIDER_RATE_LIMIT", "WARNING", "A provider is rate limiting gateway traffic.", "provider-rate-limit:"+projectRoute.ProviderID, map[string]any{"provider_id": projectRoute.ProviderID})
	} else if resp.StatusCode >= http.StatusInternalServerError {
		_ = d.Store.RecordOperationalAlert(c.Request.Context(), "PROVIDER_FAILURE", "CRITICAL", "A provider is returning server errors.", "provider-failure:"+projectRoute.ProviderID, map[string]any{"provider_id": projectRoute.ProviderID})
	} else {
		_ = d.Store.ResolveOperationalAlert(c.Request.Context(), "provider-failure:"+projectRoute.ProviderID)
		_ = d.Store.ResolveOperationalAlert(c.Request.Context(), "provider-rate-limit:"+projectRoute.ProviderID)
	}
	handleCredentialResult(c, d, cred.ID, resp.StatusCode, resp.Header)
	openai.ForwardResponseHeader(c.Writer.Header(), resp.Header)
	c.Header("X-Accel-Buffering", "no")
	c.Status(resp.StatusCode)
	capture := newTailCapture(2 << 20)
	firstByte := true
	buf := make([]byte, 32<<10)
streamLoop:
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			allowedBytes, responseLimitReached := boundedChunkLimit(fundingObservedBytes, d.Config.StreamMaxBytes, n)
			if responseLimitReached {
				logEntry.ErrorCode = "upstream_response_too_large"
				fundingPartial = true
			}
			fundingObservedBytes += int64(allowedBytes)
			if lastFundingHeartbeatBytes == 0 || fundingObservedBytes-lastFundingHeartbeatBytes >= 256<<10 || time.Since(lastFundingHeartbeatAt) >= 5*time.Second {
				_ = d.Store.HeartbeatFunding(c.Request.Context(), operation.ID, fundingObservedBytes)
				lastFundingHeartbeatBytes, lastFundingHeartbeatAt = fundingObservedBytes, time.Now()
			}
			if firstByte {
				ms := time.Since(started).Milliseconds()
				logEntry.TTFTMS = &ms
				providerMS := time.Since(providerAttemptStarted).Milliseconds()
				providerAttemptTTFTMS = &providerMS
				firstByte = false
			}
			_, _ = capture.Write(buf[:allowedBytes])
			if allowedBytes > 0 {
				if _, writeErr := c.Writer.Write(buf[:allowedBytes]); writeErr != nil {
					logEntry.ErrorCode = "client_disconnected"
					fundingPartial = true
					break
				}
			}
			if stream {
				c.Writer.Flush()
			}
			if allowedBytes < n {
				break
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && logEntry.ErrorCode == "" {
				if c.Request.Context().Err() != nil {
					logEntry.ErrorCode = "client_disconnected"
				} else {
					logEntry.ErrorCode = "upstream_stream_error"
				}
				fundingPartial = true
			}
			break
		}
		select {
		case <-c.Request.Context().Done():
			logEntry.ErrorCode = "client_disconnected"
			fundingPartial = true
			break streamLoop
		default:
		}
	}
	providerAttemptFinished := time.Now()
	usage := extractUsage(capture.Bytes())
	if policyErr := evaluateContentPolicy(c, d, contentpolicy.Request{Phase: contentpolicy.PostResponse, OrganizationID: key.OrganizationID, UserID: key.UserID, Model: projectRoute.UpstreamModel, RequestID: logEntry.RequestID, Response: capture.Bytes()}); policyErr != nil {
		if logEntry.ErrorCode == "" {
			logEntry.ErrorCode = "post_response_policy_flagged"
		}
		d.Logger.Warn("post_response_policy_flagged", "request_id", logEntry.RequestID, "streaming", stream, "existing_error_code", logEntry.ErrorCode)
	}
	attemptResult := "SUCCEEDED"
	if resp.StatusCode >= 400 {
		attemptResult = "FAILED"
	}
	if logEntry.ErrorCode == "client_disconnected" {
		attemptResult = "CANCELLED"
	}
	if logEntry.ErrorCode == "upstream_stream_error" {
		attemptResult = "FAILED"
	}
	if logEntry.ErrorCode == "upstream_response_too_large" {
		attemptResult = "FAILED"
	}
	attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = d.Store.FinishFundingAttempt(attemptCtx, attempt.ID, attemptResult, resp.StatusCode, logEntry.UpstreamRequestID, logEntry.ErrorCode)
	attemptCancel()
	fundingUsage = usage
	if usage.Total > 0 || usage.Input > 0 || usage.Output > 0 {
		fundingUsageSource = "PROVIDER_REPORTED"
		fundingWaive = false
	} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fundingUsage = estimatedUsage(endpoint, int64(estimatedTokens), fundingObservedBytes)
		fundingUsageSource = "ESTIMATED_PROVIDER_MISSING"
		fundingWaive = false
	} else if fundingObservedBytes > 0 && (logEntry.ErrorCode == "client_disconnected" || logEntry.ErrorCode == "upstream_stream_error" || logEntry.ErrorCode == "upstream_response_too_large") {
		fundingUsage = estimatedUsage(endpoint, int64(estimatedTokens), fundingObservedBytes)
		fundingUsageSource = "ESTIMATED_PARTIAL_STREAM"
		fundingWaive = false
	}
	logEntry.InputTokens = usage.Input
	logEntry.CachedInputTokens = usage.Cached
	logEntry.OutputTokens = usage.Output
	logEntry.TotalTokens = usage.Total
	if logEntry.TotalTokens == 0 {
		logEntry.TotalTokens = usage.Input + usage.Output
	}
	if logEntry.TotalTokens == 0 && !fundingWaive {
		logEntry.InputTokens, logEntry.CachedInputTokens, logEntry.OutputTokens = fundingUsage.Input, fundingUsage.Cached, fundingUsage.Output
		logEntry.TotalTokens = fundingUsage.Total
	}
	logEntry.UsageSource = fundingUsageSource
	if cost, err := d.Store.CalculateCost(c.Request.Context(), projectRoute.ProviderID, projectRoute.UpstreamModel, usage.Input, usage.Cached, usage.Output); err == nil {
		logEntry.EstimatedCost = cost
	}
	if routingDecision.Strategy != "manual" {
		if reference, err := d.Store.CalculateProjectReferenceCost(c.Request.Context(), key.ProjectID, usage.Input, usage.Cached, usage.Output); err == nil {
			logEntry.ReferenceCost = reference
			if reference.Compare(logEntry.EstimatedCost) > 0 {
				logEntry.SavingsAmount = reference.Subtract(logEntry.EstimatedCost)
			}
		}
	}
	if resp.StatusCode >= 400 {
		if closer, ok := adapter.(providers.IdleConnectionCloser); ok {
			closer.CloseIdleConnections()
		}
		d.Metrics.Error()
		if logEntry.ErrorCode == "" {
			logEntry.ErrorCode = upstreamErrorCode(capture.Bytes())
		}
	}
	qualitySuccess := resp.StatusCode >= 200 && resp.StatusCode < 400 && logEntry.ErrorCode != "upstream_stream_error" && logEntry.ErrorCode != "upstream_response_too_large"
	qualityError := ""
	if !qualitySuccess {
		qualityError = providerQualityErrorClass(resp.StatusCode, logEntry.ErrorCode)
	}
	var qualityFullLatency *int64
	if logEntry.ErrorCode != "client_disconnected" {
		value := providerAttemptFinished.Sub(providerAttemptStarted).Milliseconds()
		qualityFullLatency = &value
	}
	recordTrafficQualityObservationWithUsage(d, projectRoute.ProviderID, cred.ID, attempt.ID, providerAttemptStarted,
		providerAttemptTTFTMS, resp.StatusCode, qualitySuccess, usage.Output, qualityFullLatency, qualityError)
}

func boundedChunkLimit(written, maximum int64, chunk int) (int, bool) {
	if chunk <= 0 || maximum <= 0 || written+int64(chunk) <= maximum {
		return max(chunk, 0), false
	}
	remaining := maximum - written
	if remaining <= 0 {
		return 0, true
	}
	return int(remaining), true
}

func detectEmbeddedAPIKeyLeak(ctx context.Context, d Dependencies, body []byte, actor, ip string) (bool, error) {
	for _, match := range embeddedAPIKeyPattern.FindAllSubmatch(body, -1) {
		if len(match) != 2 {
			continue
		}
		candidate := string(match[1])
		if !apikey.LooksValid(candidate) {
			continue
		}
		err := d.Store.FreezeAPIKeyByHash(ctx, d.APIKeys.Hash(candidate), "request_content_leak", actor, ip)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
	}
	return false, nil
}

func routeCredentialConstraints(config map[string]any) scheduler.CredentialConstraints {
	return scheduler.CredentialConstraints{
		RequiredTags: stringList(config["required_credential_tags"]),
		ExcludedTags: stringList(config["excluded_credential_tags"]),
	}
}

func stringList(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed
		}
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.ToLower(strings.TrimSpace(text)))
		}
	}
	return out
}

func allowRate(c *gin.Context, d Dependencies, key domain.APIKey, estimated int) bool {
	result, err := d.Limiter.Allow(c.Request.Context(), key.ID, key.RateLimitRPM, key.RateLimitTPM, estimated)
	if err != nil {
		code := "service_unavailable"
		message := "Rate limiting is temporarily unavailable."
		if !errors.Is(err, ratelimit.ErrUnavailable) {
			message = "The request could not be rate limited."
		}
		openAIError(c, 503, code, message)
		return false
	}
	c.Header("X-RateLimit-Limit-Requests", strconv.Itoa(key.RateLimitRPM))
	c.Header("X-RateLimit-Remaining-Requests", strconv.FormatInt(max64(0, int64(key.RateLimitRPM)-result.Requests), 10))
	c.Header("X-RateLimit-Limit-Tokens", strconv.Itoa(key.RateLimitTPM))
	c.Header("X-RateLimit-Remaining-Tokens", strconv.FormatInt(max64(0, int64(key.RateLimitTPM)-result.Tokens), 10))
	c.Header("X-RelayDock-RateLimit-Limit-Requests", strconv.Itoa(key.RateLimitRPM))
	c.Header("X-RelayDock-RateLimit-Remaining-Requests", strconv.FormatInt(max64(0, int64(key.RateLimitRPM)-result.Requests), 10))
	c.Header("X-RelayDock-RateLimit-Limit-Tokens", strconv.Itoa(key.RateLimitTPM))
	c.Header("X-RelayDock-RateLimit-Remaining-Tokens", strconv.FormatInt(max64(0, int64(key.RateLimitTPM)-result.Tokens), 10))
	if !result.Allowed {
		c.Header("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())))
		openAIError(c, 429, "rate_limit_exceeded", "Rate limit reached for this API key.")
		return false
	}
	return true
}
func handleCredentialResult(c *gin.Context, d Dependencies, id string, status int, headers http.Header) {
	cooldown := d.Config.Cooldown
	if status == http.StatusTooManyRequests {
		cooldown = upstreamCooldown(headers, cooldown)
	}
	transition := scheduler.TransitionForHTTP(status, time.Now(), cooldown)
	if transition.MarkSuccess {
		d.Store.MarkCredentialSuccess(c.Request.Context(), id)
		return
	}
	if transition.Status != "" {
		_ = d.Store.SetCredentialStatus(c.Request.Context(), id, transition.Status, transition.CooldownUntil)
	}
}

func upstreamCooldown(headers http.Header, fallback time.Duration) time.Duration {
	best := fallback
	if value := strings.TrimSpace(headers.Get("Retry-After")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			best = maxDuration(best, time.Duration(seconds)*time.Second)
		} else if when, err := http.ParseTime(value); err == nil {
			best = maxDuration(best, time.Until(when))
		}
	}
	for _, name := range []string{"x-ratelimit-reset-requests", "x-ratelimit-reset-tokens", "x-ratelimit-reset-project-tokens"} {
		if parsed, err := time.ParseDuration(strings.TrimSpace(headers.Get(name))); err == nil && parsed > 0 {
			best = maxDuration(best, parsed)
		}
	}
	if best < time.Second {
		best = time.Second
	}
	if best > 24*time.Hour {
		best = 24 * time.Hour
	}
	return best
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
func modelAllowed(allowed []string, model string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, v := range allowed {
		if v == model {
			return true
		}
	}
	return false
}

func isProviderCommercialDenial(err error) bool {
	return errors.Is(err, store.ErrProviderCommercialUnavailable) || errors.Is(err, store.ErrProviderRegionDenied) ||
		errors.Is(err, store.ErrProviderPolicyDenied) || errors.Is(err, store.ErrProviderDataResidencyDenied) ||
		errors.Is(err, store.ErrProviderBudgetExceeded) || errors.Is(err, store.ErrProviderRateExceeded) ||
		errors.Is(err, store.ErrProviderMarginInsufficient)
}

func providerCommercialErrorCode(err error) string {
	switch {
	case errors.Is(err, store.ErrProviderRegionDenied), errors.Is(err, store.ErrProviderDataResidencyDenied):
		return "provider_region_unavailable"
	case errors.Is(err, store.ErrProviderBudgetExceeded), errors.Is(err, store.ErrProviderRateExceeded):
		return "provider_capacity_unavailable"
	default:
		return "provider_commercial_unavailable"
	}
}

func providerCommercialErrorMessage(err error) string {
	if errors.Is(err, store.ErrProviderRegionDenied) || errors.Is(err, store.ErrProviderDataResidencyDenied) {
		return "The requested model is unavailable in the customer's region or data-residency policy."
	}
	return "The requested model is not currently available for commercial traffic."
}

func recordTrafficQualityObservation(d Dependencies, providerID, credentialID, attemptID string, started time.Time, ttft *int64, status int, succeeded bool, outputTokens int64, errorClass string) {
	full := time.Since(started).Milliseconds()
	recordTrafficQualityObservationWithUsage(d, providerID, credentialID, attemptID, started, ttft, status, succeeded, outputTokens, &full, errorClass)
}

func recordTrafficQualityObservationWithUsage(d Dependencies, providerID, credentialID, attemptID string, _ time.Time, ttft *int64, status int, succeeded bool, outputTokens int64, fullLatency *int64, errorClass string) {
	if d.Store == nil || providerID == "" || attemptID == "" {
		return
	}
	credential, attempt := credentialID, attemptID
	var statusPointer *int
	if status > 0 {
		statusPointer = &status
	}
	var outputPointer *int64
	if outputTokens > 0 {
		outputPointer = &outputTokens
	}
	var throughput *domain.Decimal
	if outputTokens > 0 && fullLatency != nil && ttft != nil && *fullLatency > *ttft {
		value := domain.Decimal(new(big.Rat).Mul(big.NewRat(outputTokens, *fullLatency-*ttft), big.NewRat(1000, 1)).FloatString(6))
		throughput = &value
	}
	observation := domain.ProviderQualityObservation{IdempotencyKey: "traffic:" + attemptID, ProviderID: providerID,
		CredentialID: &credential, ProviderAttemptID: &attempt, Source: "PLATFORM_TRAFFIC", StatusCode: statusPointer,
		Succeeded: succeeded, RateLimited: status == http.StatusTooManyRequests, TTFTMS: ttft, FullLatencyMS: fullLatency,
		OutputTokens: outputPointer, ThroughputTPS: throughput, ErrorClass: errorClass, ObservedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := d.Store.RecordProviderQualityObservation(ctx, observation); err != nil && d.Logger != nil {
		d.Logger.Error("provider_quality_traffic_observation_failed", "provider_id", providerID, "attempt_id", attemptID, "error", err)
	}
}

func providerQualityErrorClass(status int, existing string) string {
	if existing == "upstream_stream_error" || existing == "upstream_response_too_large" {
		return existing
	}
	switch {
	case status == http.StatusTooManyRequests:
		return "http_429"
	case status >= 500:
		return "http_5xx"
	case status >= 400:
		return "http_4xx"
	default:
		return existing
	}
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func providerClientRequestID(c *gin.Context) (string, bool) {
	value := strings.TrimSpace(c.GetHeader("X-Client-Request-Id"))
	if value == "" {
		return requestID(c), true
	}
	return value, len(value) <= 512
}

func gatewayIdempotencyKey(c *gin.Context) (string, bool) {
	value := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(value) > 200 {
		return "", false
	}
	if value == "" {
		value = requestID(c)
	}
	return value, true
}

func fundingRequestFingerprint(projectID, endpoint string, body []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(projectID))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(endpoint))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(body)
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func maximumOutputTokens(payload map[string]any, endpoint string) int64 {
	if endpoint == "/embeddings" {
		return 0
	}
	for _, name := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if value := number(payload[name]); value > 0 {
			return min64(value, 1_000_000)
		}
	}
	return 4096
}

func estimatedUsage(endpoint string, inputTokens, observedBytes int64) tokenUsage {
	output := observedBytes / 4
	if endpoint == "/embeddings" {
		output = 0
	}
	usage := tokenUsage{Input: inputTokens, Output: output}
	usage.Total = usage.Input + usage.Output
	return usage
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type tailCapture struct {
	max int
	buf []byte
}

func newTailCapture(max int) *tailCapture { return &tailCapture{max: max, buf: make([]byte, 0, max)} }
func (t *tailCapture) Write(p []byte) (int, error) {
	n := len(p)
	if n >= t.max {
		t.buf = append(t.buf[:0], p[n-t.max:]...)
		return n, nil
	}
	overflow := len(t.buf) + n - t.max
	if overflow > 0 {
		copy(t.buf, t.buf[overflow:])
		t.buf = t.buf[:len(t.buf)-overflow]
	}
	t.buf = append(t.buf, p...)
	return n, nil
}
func (t *tailCapture) Bytes() []byte { return t.buf }

type tokenUsage struct{ Input, Cached, Output, Total int64 }

func extractUsage(data []byte) tokenUsage {
	var best tokenUsage
	try := func(raw []byte) {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walkUsage(v, &best)
		}
	}
	try(data)
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			raw := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if !bytes.Equal(raw, []byte("[DONE]")) {
				try(raw)
			}
		}
	}
	return best
}
func walkUsage(v any, best *tokenUsage) {
	m, ok := v.(map[string]any)
	if !ok {
		if a, ok := v.([]any); ok {
			for _, item := range a {
				walkUsage(item, best)
			}
		}
		return
	}
	if raw, ok := m["usage"].(map[string]any); ok {
		u := tokenUsage{Input: number(raw["input_tokens"]), Output: number(raw["output_tokens"]), Total: number(raw["total_tokens"])}
		if u.Input == 0 {
			u.Input = number(raw["prompt_tokens"])
		}
		if u.Output == 0 {
			u.Output = number(raw["completion_tokens"])
		}
		if details, ok := raw["input_tokens_details"].(map[string]any); ok {
			u.Cached = number(details["cached_tokens"])
		}
		if details, ok := raw["prompt_tokens_details"].(map[string]any); ok {
			u.Cached = number(details["cached_tokens"])
		}
		if u.Total == 0 {
			u.Total = u.Input + u.Output
		}
		if u.Total >= best.Total {
			*best = u
		}
	}
	for _, child := range m {
		walkUsage(child, best)
	}
}
func number(v any) int64 {
	if n, ok := v.(float64); ok {
		return int64(n)
	}
	return 0
}
func upstreamErrorCode(data []byte) string {
	var v struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &v) == nil {
		if s, ok := v.Error.Code.(string); ok && s != "" {
			return s
		}
		return v.Error.Type
	}
	return "upstream_error"
}
