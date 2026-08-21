package quality

import (
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{Enabled: true, MinimumSamples: 10, AvailabilityTargetPct: "99", MaximumErrorRatePct: "5",
		Maximum429RatePct: "10", MaximumTTFTMS: 1000, MaximumFullLatencyMS: 5000,
		MinimumThroughputTPS: "10", MinimumOutputQuality: "90", AutoDownweight: true,
		AutoCircuitBreaker: true, CircuitFailureThreshold: 2, CircuitRecoveryThreshold: 2,
		CircuitOpenDuration: time.Minute, RampEnabled: true, RampInitialBPS: 500,
		RampStepBPS: 500, RampStepInterval: time.Hour}
}

func TestEvaluateOpensCircuitAfterConsecutiveCriticalBreaches(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	metrics := Metrics{MeasurementCount: 20, AvailabilityPct: "50", ErrorRatePct: "50", RateLimitedPct: "0",
		OutputQualityScore: "100", PriceTruthScore: "100", RegionCoveragePct: "100"}
	first := Evaluate(testPolicy(), metrics, State{CircuitState: "CLOSED", TrafficCapBPS: 10000}, now)
	if first.CircuitState != "CLOSED" || first.ConsecutiveBreaches != 1 {
		t.Fatalf("unexpected first decision: %+v", first)
	}
	second := Evaluate(testPolicy(), metrics, State{CircuitState: first.CircuitState, ConsecutiveBreaches: first.ConsecutiveBreaches, TrafficCapBPS: 10000}, now.Add(time.Second))
	if second.CircuitState != "OPEN" || second.CircuitOpenUntil == nil || second.RoutingMultiplier != "0.000000" || second.TrafficCapBPS != 0 {
		t.Fatalf("circuit did not open: %+v", second)
	}
}

func TestEvaluateSupplierRampRequiresIndependentPriceAndRegionEvidence(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	ttft, full, throughput := int64(100), int64(500), "50"
	metrics := Metrics{MeasurementCount: 20, AvailabilityPct: "100", ErrorRatePct: "0", RateLimitedPct: "0",
		P95TTFTMS: &ttft, P95FullLatencyMS: &full, ThroughputTPS: &throughput,
		OutputQualityScore: "100", PriceTruthScore: "0", RegionCoveragePct: "100"}
	started := now.Add(-2 * time.Hour)
	decision := Evaluate(testPolicy(), metrics, State{CircuitState: "CLOSED", TrafficCapBPS: 500, SupplierLinked: true, RampStartedAt: &started, LastRampStepAt: &started}, now)
	if decision.TrafficCapBPS != 500 {
		t.Fatalf("ramp advanced without price truth: %+v", decision)
	}
	metrics.PriceTruthScore = "100"
	decision = Evaluate(testPolicy(), metrics, State{CircuitState: "CLOSED", TrafficCapBPS: 500, SupplierLinked: true, RampStartedAt: &started, LastRampStepAt: &started}, now)
	if decision.TrafficCapBPS != 1000 {
		t.Fatalf("healthy ramp did not advance: %+v", decision)
	}
}

func TestEvaluateInsufficientEvidenceCannotCreatePlatformGrade(t *testing.T) {
	decision := Evaluate(testPolicy(), Metrics{MeasurementCount: 1, AvailabilityPct: "100", ErrorRatePct: "0", RateLimitedPct: "0", OutputQualityScore: "100", PriceTruthScore: "100", RegionCoveragePct: "100"}, State{CircuitState: "CLOSED", TrafficCapBPS: 10000}, time.Now())
	if decision.Grade != "UNKNOWN" || decision.SufficientMeasurements {
		t.Fatalf("insufficient evidence was graded: %+v", decision)
	}
}
