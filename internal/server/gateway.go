package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/providers"
	"github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/ratelimit"
	"github.com/relayedock/relayedock/internal/scheduler"
)

const apiKeyContext = "relayedock.api_key"
const apiKeyVersionContext = "relayedock.api_key_version"

func GatewayEngine(d Dependencies) *gin.Engine {
	r := gin.New()
	configureTrustedProxies(r, d)
	r.Use(recovery(d.Logger), requestMiddleware(d.Logger), cors(d.Config.AllowedOrigins))
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
		if !route.Enabled {
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
	stream, _ := payload["stream"].(bool)
	doneMetric := d.Metrics.Begin(stream)
	defer doneMetric()
	logEntry := domain.RequestLog{RequestID: requestID(c), UserID: key.UserID, APIKeyID: key.ID, OrganizationID: key.OrganizationID, ProjectID: key.ProjectID, RequestedModel: requestedModel, Endpoint: endpoint, Streaming: stream, CreatedAt: time.Now().UTC()}
	defer func() {
		logEntry.LatencyMS = time.Since(started).Milliseconds()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := d.Store.InsertScopedRequestLog(ctx, logEntry); err != nil {
			d.Logger.Error("request_log_failed", "request_id", logEntry.RequestID, "error", err)
			return
		}
		if logEntry.ErrorCode != "project_budget_exceeded" {
			if err := commitProjectBudget(ctx, d, key, logEntry); err != nil {
				d.Logger.Error("project_budget_commit_failed", "request_id", logEntry.RequestID, "project_id", key.ProjectID, "error", err)
			}
		}
	}()
	clientRequestID, ok := providerClientRequestID(c)
	if !ok {
		logEntry.StatusCode = http.StatusBadRequest
		logEntry.ErrorCode = "invalid_client_request_id"
		d.Metrics.Error()
		openAIError(c, http.StatusBadRequest, "invalid_client_request_id", "X-Client-Request-Id must not exceed 512 bytes.")
		return
	}
	routingDecision, err := d.Store.ResolveProjectRouteForEndpoint(c.Request.Context(), key.ProjectID, requestedModel, endpoint)
	projectRoute := routingDecision.Route
	if err != nil || !projectRoute.Enabled {
		logEntry.StatusCode = http.StatusNotFound
		logEntry.ErrorCode = "model_not_found"
		d.Metrics.Error()
		openAIError(c, http.StatusNotFound, "model_not_found", "The requested model does not exist or is not enabled for this project.")
		return
	}
	logEntry.RouteID = projectRoute.ModelRouteID
	logEntry.ProviderID = projectRoute.ProviderID
	logEntry.ResolvedModel = projectRoute.UpstreamModel
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
		if used >= *key.MonthlyCostLimit {
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
		if (user.MonthlyTokenLimit != nil && tokens+int64(estimatedTokens) > *user.MonthlyTokenLimit) || (user.MonthlyCostLimit != nil && cost >= *user.MonthlyCostLimit) {
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
				(team.MonthlyCostLimit != nil && cost >= *team.MonthlyCostLimit) {
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
	payload["model"] = projectRoute.UpstreamModel
	upstreamBody, err := json.Marshal(payload)
	if err != nil {
		logEntry.StatusCode = 400
		logEntry.ErrorCode = "invalid_request"
		openAIError(c, 400, "invalid_request", "Could not encode the request.")
		return
	}
	constraints := mergeCredentialConstraints(routeCredentialConstraints(projectRoute.FallbackConfig), routeCredentialConstraints(projectRoute.RoutingConfig))
	selection, err := d.Scheduler.SelectConstrained(c.Request.Context(), projectRoute.CredentialGroupID, projectRoute.RoutingPolicy, constraints)
	if err != nil && projectRoute.FallbackGroupID != nil {
		selection, err = d.Scheduler.SelectConstrained(c.Request.Context(), *projectRoute.FallbackGroupID, projectRoute.RoutingPolicy, constraints)
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
	forward := providers.ForwardRequest{BaseURL: projectRoute.ProviderBaseURL, Path: endpoint, Body: bytes.NewReader(upstreamBody), ContentType: "application/json", Accept: c.GetHeader("Accept"), ClientRequestID: clientRequestID, Credential: providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)}}
	resp, err := adapter.Forward(c.Request.Context(), forward)
	if err != nil {
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
	handleCredentialResult(c, d, cred.ID, resp.StatusCode, resp.Header)
	openai.ForwardResponseHeader(c.Writer.Header(), resp.Header)
	c.Header("X-Accel-Buffering", "no")
	c.Status(resp.StatusCode)
	capture := newTailCapture(2 << 20)
	firstByte := true
	buf := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if firstByte {
				ms := time.Since(started).Milliseconds()
				logEntry.TTFTMS = &ms
				firstByte = false
			}
			_, _ = capture.Write(buf[:n])
			if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
				logEntry.ErrorCode = "client_disconnected"
				break
			}
			if stream {
				c.Writer.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && logEntry.ErrorCode == "" {
				logEntry.ErrorCode = "upstream_stream_error"
			}
			break
		}
		select {
		case <-c.Request.Context().Done():
			logEntry.ErrorCode = "client_disconnected"
			return
		default:
		}
	}
	usage := extractUsage(capture.Bytes())
	logEntry.InputTokens = usage.Input
	logEntry.CachedInputTokens = usage.Cached
	logEntry.OutputTokens = usage.Output
	logEntry.TotalTokens = usage.Total
	if logEntry.TotalTokens == 0 {
		logEntry.TotalTokens = usage.Input + usage.Output
	}
	if cost, err := d.Store.CalculateCost(c.Request.Context(), projectRoute.ProviderID, projectRoute.UpstreamModel, usage.Input, usage.Cached, usage.Output); err == nil {
		logEntry.EstimatedCost = cost
	}
	if routingDecision.Strategy != "manual" {
		if reference, err := d.Store.CalculateProjectReferenceCost(c.Request.Context(), key.ProjectID, usage.Input, usage.Cached, usage.Output); err == nil {
			logEntry.ReferenceCost = reference
			if reference > logEntry.EstimatedCost {
				logEntry.SavingsAmount = reference - logEntry.EstimatedCost
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
