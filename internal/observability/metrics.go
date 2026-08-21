package observability

import (
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Metrics is a dependency-free Prometheus/OpenMetrics registry.  The metric
// names and labels follow OpenTelemetry semantic conventions where practical,
// so a Prometheus scrape can be forwarded to an OTel collector without a
// gateway-specific SDK dependency.
type Metrics struct {
	requests  atomic.Uint64
	errors    atomic.Uint64
	upstream  atomic.Uint64
	active    atomic.Int64
	streaming atomic.Int64

	requestLatencyMS atomic.Uint64
	requestLatencyN  atomic.Uint64
	ttftMS           atomic.Uint64
	ttftN            atomic.Uint64
	inputTokens      atomic.Uint64
	outputTokens     atomic.Uint64
	fallbacks        atomic.Uint64
	walletReserveErr atomic.Uint64
	settlementErr    atomic.Uint64
	settlementN      atomic.Uint64
	settlementOK     atomic.Uint64
	settlementMS     atomic.Uint64
	negativeMargins  atomic.Uint64

	mu              sync.RWMutex
	providers       map[string]*providerStats
	models          map[string]*modelStats
	payments        map[string]uint64
	controls        map[string]*endpointStats
	money           map[string]*big.Rat
	gauges          map[string]int64
	providerQuality map[string]providerQualityStats
}

type providerStats struct {
	requests  uint64
	successes uint64
	failures  uint64
	limited   uint64
}

type modelStats struct {
	requests uint64
	revenue  *big.Rat
	cost     *big.Rat
	margin   *big.Rat
}

type endpointStats struct {
	requests uint64
	success  uint64
}

type providerQualityStats struct {
	grade, score, availability, errorRate, limitedRate, throughput, multiplier string
	trafficCap                                                                 int
	circuitOpen                                                                bool
}

func (m *Metrics) initLocked() {
	if m.providers == nil {
		m.providers = map[string]*providerStats{}
	}
	if m.models == nil {
		m.models = map[string]*modelStats{}
	}
	if m.payments == nil {
		m.payments = map[string]uint64{}
	}
	if m.controls == nil {
		m.controls = map[string]*endpointStats{}
	}
	if m.money == nil {
		m.money = map[string]*big.Rat{}
	}
	if m.gauges == nil {
		m.gauges = map[string]int64{}
	}
	if m.providerQuality == nil {
		m.providerQuality = map[string]providerQualityStats{}
	}
}

func (m *Metrics) Request()  { m.requests.Add(1) }
func (m *Metrics) Error()    { m.errors.Add(1) }
func (m *Metrics) Upstream() { m.upstream.Add(1) }
func (m *Metrics) Begin(stream bool) func() {
	m.active.Add(1)
	if stream {
		m.streaming.Add(1)
	}
	return func() {
		m.active.Add(-1)
		if stream {
			m.streaming.Add(-1)
		}
	}
}

// ObserveRequest records request-level telemetry without converting currency
// values through binary floating point.  Monetary totals are updated through
// ObserveModelPricing, which accepts PostgreSQL NUMERIC-compatible strings.
func (m *Metrics) ObserveRequest(provider, model string, statusCode int, input, output, latencyMS int64, ttftMS *int64, fallback bool) {
	if latencyMS >= 0 {
		m.requestLatencyMS.Add(uint64(latencyMS))
		m.requestLatencyN.Add(1)
	}
	if ttftMS != nil && *ttftMS >= 0 {
		m.ttftMS.Add(uint64(*ttftMS))
		m.ttftN.Add(1)
	}
	if input > 0 {
		m.inputTokens.Add(uint64(input))
	}
	if output > 0 {
		m.outputTokens.Add(uint64(output))
	}
	if fallback {
		m.fallbacks.Add(1)
	}
	provider = metricLabel(provider, "unknown")
	model = metricLabel(model, "unknown")
	m.mu.Lock()
	m.initLocked()
	ps := m.providers[provider]
	if ps == nil {
		ps = &providerStats{}
		m.providers[provider] = ps
	}
	ps.requests++
	if statusCode == 429 {
		ps.limited++
	} else if statusCode >= 200 && statusCode < 400 {
		ps.successes++
	} else {
		ps.failures++
	}
	ms := m.models[model]
	if ms == nil {
		ms = &modelStats{revenue: new(big.Rat), cost: new(big.Rat), margin: new(big.Rat)}
		m.models[model] = ms
	}
	ms.requests++
	m.mu.Unlock()
}

func (m *Metrics) ObserveModelPricing(model, revenue, cost, margin string) {
	model = metricLabel(model, "unknown")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	ms := m.models[model]
	if ms == nil {
		ms = &modelStats{revenue: new(big.Rat), cost: new(big.Rat), margin: new(big.Rat)}
		m.models[model] = ms
	}
	if r := parseRat(revenue); r != nil {
		ms.revenue.Add(ms.revenue, r)
	}
	if r := parseRat(cost); r != nil {
		ms.cost.Add(ms.cost, r)
	}
	if r := parseRat(margin); r != nil {
		ms.margin.Add(ms.margin, r)
		if r.Sign() < 0 {
			m.negativeMargins.Add(1)
		}
	}
}

func (m *Metrics) IncWalletReservationFailure() { m.walletReserveErr.Add(1) }
func (m *Metrics) IncSettlementFailure()        { m.settlementErr.Add(1) }
func (m *Metrics) IncFallback()                 { m.fallbacks.Add(1) }
func (m *Metrics) ObserveSettlement(latencyMS int64, succeeded bool) {
	m.settlementN.Add(1)
	if latencyMS > 0 {
		m.settlementMS.Add(uint64(latencyMS))
	}
	if succeeded {
		m.settlementOK.Add(1)
	}
}
func (m *Metrics) IncPayment(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.payments[metricLabel(status, "unknown")]++
}

func (m *Metrics) ObserveReconciliationDifference(amount string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	if r := parseRat(amount); r != nil {
		if m.money["reconciliation_difference"] == nil {
			m.money["reconciliation_difference"] = new(big.Rat)
		}
		m.money["reconciliation_difference"].Add(m.money["reconciliation_difference"], r)
	}
}

func (m *Metrics) ObserveReconciliation(localAmount, providerAmount string) {
	local := parseRat(localAmount)
	provider := parseRat(providerAmount)
	if local == nil || provider == nil {
		return
	}
	difference := new(big.Rat).Sub(local, provider)
	if difference.Sign() < 0 {
		difference.Neg(difference)
	}
	m.ObserveReconciliationDifference(difference.RatString())
}

func (m *Metrics) ObserveControl(component string, statusCode int) {
	component = metricLabel(component, "control")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	stats := m.controls[component]
	if stats == nil {
		stats = &endpointStats{}
		m.controls[component] = stats
	}
	stats.requests++
	if statusCode < 500 {
		stats.success++
	}
}

func (m *Metrics) SetGauge(name string, value int64) {
	if !strings.HasPrefix(name, "relaydock_") {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.gauges[name] = value
}

func (m *Metrics) SetProviderQuality(provider, grade, score, availability, errorRate, limitedRate, throughput, multiplier string, trafficCap int, circuitOpen bool) {
	for _, value := range []string{score, availability, errorRate, limitedRate, multiplier} {
		if parseRat(value) == nil {
			return
		}
	}
	if throughput != "" && parseRat(throughput) == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	m.providerQuality[metricLabel(provider, "unknown")] = providerQualityStats{grade: metricLabel(grade, "UNKNOWN"),
		score: score, availability: availability, errorRate: errorRate, limitedRate: limitedRate,
		throughput: throughput, multiplier: multiplier, trafficCap: trafficCap, circuitOpen: circuitOpen}
}

func (m *Metrics) Write(w io.Writer) {
	_, _ = fmt.Fprintf(w, "# HELP relayedock_requests_total Total gateway requests.\n# TYPE relayedock_requests_total counter\nrelaydock_requests_total %d\n", m.requests.Load())
	_, _ = fmt.Fprintf(w, "# HELP relayedock_errors_total Total failed requests.\n# TYPE relayedock_errors_total counter\nrelaydock_errors_total %d\n", m.errors.Load())
	_, _ = fmt.Fprintf(w, "# HELP relayedock_upstream_requests_total Total upstream requests.\n# TYPE relayedock_upstream_requests_total counter\nrelaydock_upstream_requests_total %d\n", m.upstream.Load())
	_, _ = fmt.Fprintf(w, "# HELP relayedock_active_requests Current active requests.\n# TYPE relayedock_active_requests gauge\nrelaydock_active_requests %d\n", m.active.Load())
	_, _ = fmt.Fprintf(w, "# HELP relayedock_streaming_requests Current streaming requests.\n# TYPE relayedock_streaming_requests gauge\nrelaydock_streaming_requests %d\n", m.streaming.Load())
	_, _ = fmt.Fprintf(w, "# HELP relaydock_request_latency_ms_sum Total request latency in milliseconds.\n# TYPE relaydock_request_latency_ms_sum counter\nrelaydock_request_latency_ms_sum %d\n# HELP relaydock_request_latency_ms_count Total observed request latencies.\n# TYPE relaydock_request_latency_ms_count counter\nrelaydock_request_latency_ms_count %d\n", m.requestLatencyMS.Load(), m.requestLatencyN.Load())
	_, _ = fmt.Fprintf(w, "# HELP relaydock_first_token_latency_ms_sum Total time-to-first-token in milliseconds.\n# TYPE relaydock_first_token_latency_ms_sum counter\nrelaydock_first_token_latency_ms_sum %d\n# HELP relaydock_first_token_latency_ms_count Observed first-token timings.\n# TYPE relaydock_first_token_latency_ms_count counter\nrelaydock_first_token_latency_ms_count %d\n", m.ttftMS.Load(), m.ttftN.Load())
	_, _ = fmt.Fprintf(w, "# HELP relaydock_input_tokens_total Total input tokens.\n# TYPE relaydock_input_tokens_total counter\nrelaydock_input_tokens_total %d\n# HELP relaydock_output_tokens_total Total output tokens.\n# TYPE relaydock_output_tokens_total counter\nrelaydock_output_tokens_total %d\n", m.inputTokens.Load(), m.outputTokens.Load())
	_, _ = fmt.Fprintf(w, "# HELP relaydock_fallback_total Total fallback route attempts.\n# TYPE relaydock_fallback_total counter\nrelaydock_fallback_total %d\n# HELP relaydock_wallet_reservation_failures_total Failed wallet reservations.\n# TYPE relaydock_wallet_reservation_failures_total counter\nrelaydock_wallet_reservation_failures_total %d\n# HELP relaydock_settlement_failures_total Failed wallet settlements.\n# TYPE relaydock_settlement_failures_total counter\nrelaydock_settlement_failures_total %d\n# HELP relaydock_negative_margin_requests_total Requests with negative gross margin.\n# TYPE relaydock_negative_margin_requests_total counter\nrelaydock_negative_margin_requests_total %d\n", m.fallbacks.Load(), m.walletReserveErr.Load(), m.settlementErr.Load(), m.negativeMargins.Load())
	_, _ = fmt.Fprintf(w, "relaydock_settlement_attempts_total %d\nrelaydock_settlement_success_total %d\nrelaydock_settlement_latency_ms_sum %d\n", m.settlementN.Load(), m.settlementOK.Load(), m.settlementMS.Load())

	m.mu.RLock()
	providers := make([]string, 0, len(m.providers))
	for key := range m.providers {
		providers = append(providers, key)
	}
	sort.Strings(providers)
	for _, key := range providers {
		v := m.providers[key]
		_, _ = fmt.Fprintf(w, "relaydock_provider_requests_total{provider=\"%s\"} %d\nrelaydock_provider_success_total{provider=\"%s\"} %d\nrelaydock_provider_failures_total{provider=\"%s\"} %d\nrelaydock_provider_rate_limited_total{provider=\"%s\"} %d\n", escapeLabel(key), v.requests, escapeLabel(key), v.successes, escapeLabel(key), v.failures, escapeLabel(key), v.limited)
	}
	models := make([]string, 0, len(m.models))
	for key := range m.models {
		models = append(models, key)
	}
	sort.Strings(models)
	for _, key := range models {
		v := m.models[key]
		_, _ = fmt.Fprintf(w, "relaydock_model_requests_total{model=\"%s\"} %d\nrelaydock_model_revenue_total{model=\"%s\"} %s\nrelaydock_model_cost_total{model=\"%s\"} %s\nrelaydock_model_gross_margin_total{model=\"%s\"} %s\nrelaydock_model_gross_margin_ratio{model=\"%s\"} %s\n", escapeLabel(key), v.requests, escapeLabel(key), ratString(v.revenue), escapeLabel(key), ratString(v.cost), escapeLabel(key), ratString(v.margin), escapeLabel(key), ratioString(v.margin, v.revenue))
	}
	payments := make([]string, 0, len(m.payments))
	for key := range m.payments {
		payments = append(payments, key)
	}
	sort.Strings(payments)
	for _, key := range payments {
		_, _ = fmt.Fprintf(w, "relaydock_payment_attempts_total{status=\"%s\"} %d\n", escapeLabel(key), m.payments[key])
	}
	components := make([]string, 0, len(m.controls))
	for key := range m.controls {
		components = append(components, key)
	}
	sort.Strings(components)
	for _, key := range components {
		v := m.controls[key]
		_, _ = fmt.Fprintf(w, "relaydock_control_requests_total{component=\"%s\"} %d\nrelaydock_control_success_total{component=\"%s\"} %d\n", escapeLabel(key), v.requests, escapeLabel(key), v.success)
	}
	if difference := m.money["reconciliation_difference"]; difference != nil {
		_, _ = fmt.Fprintf(w, "relaydock_reconciliation_difference_total %s\n", ratString(difference))
	}
	gauges := make([]string, 0, len(m.gauges))
	for key := range m.gauges {
		gauges = append(gauges, key)
	}
	sort.Strings(gauges)
	for _, key := range gauges {
		_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %d\n", key, key, m.gauges[key])
	}
	qualityProviders := make([]string, 0, len(m.providerQuality))
	for key := range m.providerQuality {
		qualityProviders = append(qualityProviders, key)
	}
	sort.Strings(qualityProviders)
	for _, key := range qualityProviders {
		value := m.providerQuality[key]
		throughput := value.throughput
		if throughput == "" {
			throughput = "0"
		}
		circuit := 0
		if value.circuitOpen {
			circuit = 1
		}
		_, _ = fmt.Fprintf(w, "relaydock_provider_quality_score{provider=\"%s\",grade=\"%s\"} %s\nrelaydock_provider_quality_availability_percent{provider=\"%s\"} %s\nrelaydock_provider_quality_error_percent{provider=\"%s\"} %s\nrelaydock_provider_quality_429_percent{provider=\"%s\"} %s\nrelaydock_provider_quality_throughput_tps{provider=\"%s\"} %s\nrelaydock_provider_quality_routing_multiplier{provider=\"%s\"} %s\nrelaydock_provider_quality_traffic_cap_bps{provider=\"%s\"} %d\nrelaydock_provider_quality_circuit_open{provider=\"%s\"} %d\n",
			escapeLabel(key), escapeLabel(value.grade), value.score, escapeLabel(key), value.availability,
			escapeLabel(key), value.errorRate, escapeLabel(key), value.limitedRate, escapeLabel(key), throughput,
			escapeLabel(key), value.multiplier, escapeLabel(key), value.trafficCap, escapeLabel(key), circuit)
	}
	m.mu.RUnlock()
}

func (m *Metrics) Snapshot() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	provider := map[string]any{}
	for key, value := range m.providers {
		provider[key] = map[string]uint64{"requests": value.requests, "successes": value.successes, "failures": value.failures, "rate_limited": value.limited}
	}
	model := map[string]any{}
	for key, value := range m.models {
		model[key] = map[string]any{"requests": value.requests, "revenue": ratString(value.revenue), "cost": ratString(value.cost), "gross_margin": ratString(value.margin), "gross_margin_ratio": ratioString(value.margin, value.revenue)}
	}
	controls := map[string]any{}
	for key, value := range m.controls {
		controls[key] = map[string]any{"success": value.success, "total": value.requests, "percent": percentString(value.success, value.requests)}
	}
	payments := map[string]uint64{}
	var paymentTotal, paymentSuccess uint64
	for key, value := range m.payments {
		payments[key] = value
		paymentTotal += value
		if key == "webhook_succeeded" {
			paymentSuccess += value
		}
	}
	var providerTotal, providerSuccess uint64
	for _, value := range m.providers {
		providerTotal += value.requests
		providerSuccess += value.successes
	}
	gauges := map[string]int64{}
	for key, value := range m.gauges {
		gauges[key] = value
	}
	return map[string]any{
		"requests": m.requests.Load(), "errors": m.errors.Load(), "upstream_requests": m.upstream.Load(),
		"active_requests": m.active.Load(), "streaming_requests": m.streaming.Load(),
		"request_latency_ms_sum": m.requestLatencyMS.Load(), "request_latency_ms_count": m.requestLatencyN.Load(),
		"first_token_latency_ms_sum": m.ttftMS.Load(), "first_token_latency_ms_count": m.ttftN.Load(),
		"input_tokens": m.inputTokens.Load(), "output_tokens": m.outputTokens.Load(),
		"fallbacks": m.fallbacks.Load(), "wallet_reservation_failures": m.walletReserveErr.Load(),
		"settlement_failures": m.settlementErr.Load(), "negative_margin_requests": m.negativeMargins.Load(),
		"providers": provider, "models": model, "payments": payments, "gauges": gauges,
		"slo_evidence": map[string]any{
			"gateway_availability":       controls["gateway"],
			"control_plane_availability": controls["control_plane"],
			"payment_webhook_processing": map[string]any{"success": paymentSuccess, "total": paymentTotal, "percent": percentString(paymentSuccess, paymentTotal)},
			"ledger_settlement":          map[string]any{"success": m.settlementOK.Load(), "total": m.settlementN.Load(), "percent": percentString(m.settlementOK.Load(), m.settlementN.Load()), "latency_ms_sum": m.settlementMS.Load()},
			"provider_routing_success":   map[string]any{"success": providerSuccess, "total": providerTotal, "percent": percentString(providerSuccess, providerTotal)},
		},
	}
}

func percentString(success, total uint64) string {
	if total == 0 {
		return "100.0000"
	}
	value := new(big.Rat).SetFrac(new(big.Int).SetUint64(success), new(big.Int).SetUint64(total))
	value.Mul(value, big.NewRat(100, 1))
	return value.FloatString(4)
}

func parseRat(value string) *big.Rat {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil
	}
	return r
}

func ratString(value *big.Rat) string {
	if value == nil {
		return "0"
	}
	return value.FloatString(12)
}

func ratioString(numerator, denominator *big.Rat) string {
	if numerator == nil || denominator == nil || denominator.Sign() == 0 {
		return "0.000000000000"
	}
	return new(big.Rat).Quo(numerator, denominator).FloatString(12)
}

func metricLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func escapeLabel(value string) string {
	return strconv.Quote(value)[1 : len(strconv.Quote(value))-1]
}
