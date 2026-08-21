package observability

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestMetricsExposeExactMoneyAndCoreSignals(t *testing.T) {
	metrics := &Metrics{}
	ttft := int64(45)
	metrics.ObserveRequest("openai", "model-a", 201, 11, 7, 120, &ttft, false)
	metrics.ObserveRequest("openai", "model-a", 429, 0, 0, 9, nil, false)
	metrics.ObserveRequest("openai", "model-a", 502, 0, 0, 30, nil, false)
	metrics.IncFallback()
	metrics.IncWalletReservationFailure()
	metrics.ObserveSettlement(18, true)
	metrics.ObserveSettlement(22, false)
	metrics.IncSettlementFailure()
	metrics.IncPayment("webhook_succeeded")
	metrics.ObserveReconciliation("10.001", "10.000")
	metrics.ObserveModelPricing("model-a", "0.100000000001", "0.120000000001", "-0.020000000000")
	metrics.SetProviderQuality("openai", "B", "88.5000", "99.2500", "0.5000", "0.2500", "42.125000", "0.900000", 5000, false)

	var output bytes.Buffer
	metrics.Write(&output)
	for _, expected := range []string{
		`relaydock_provider_requests_total{provider="openai"} 3`,
		`relaydock_provider_success_total{provider="openai"} 1`,
		`relaydock_provider_failures_total{provider="openai"} 1`,
		`relaydock_provider_rate_limited_total{provider="openai"} 1`,
		`relaydock_model_revenue_total{model="model-a"} 0.100000000001`,
		`relaydock_model_cost_total{model="model-a"} 0.120000000001`,
		`relaydock_model_gross_margin_total{model="model-a"} -0.020000000000`,
		`relaydock_model_gross_margin_ratio{model="model-a"} -0.199999999998`,
		`relaydock_reconciliation_difference_total 0.001000000000`,
		`relaydock_negative_margin_requests_total 1`,
		`relaydock_provider_quality_score{provider="openai",grade="B"} 88.5000`,
		`relaydock_provider_quality_traffic_cap_bps{provider="openai"} 5000`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("metric output does not contain %q:\n%s", expected, output.String())
		}
	}
	if got := metrics.Snapshot()["fallbacks"]; got != uint64(1) {
		t.Fatalf("fallbacks=%v", got)
	}
}

func TestMetricsConcurrentUpdates(t *testing.T) {
	metrics := &Metrics{}
	const workers = 32
	const iterations = 100
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				metrics.ObserveRequest("provider", "model", 200, 1, 2, 3, nil, false)
				metrics.ObserveModelPricing("model", "0.01", "0.005", "0.005")
			}
		}()
	}
	wg.Wait()
	snapshot := metrics.Snapshot()
	provider := snapshot["providers"].(map[string]any)["provider"].(map[string]uint64)
	want := uint64(workers * iterations)
	if provider["requests"] != want || provider["successes"] != want {
		t.Fatalf("provider=%v want=%d", provider, want)
	}
	model := snapshot["models"].(map[string]any)["model"].(map[string]any)
	if model["revenue"] != "32.000000000000" || model["gross_margin"] != "16.000000000000" {
		t.Fatalf("model totals=%v", model)
	}
}
