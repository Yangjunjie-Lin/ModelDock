package quality

import (
	"math/big"
	"time"
)

type Policy struct {
	Enabled                  bool
	MinimumSamples           int64
	AvailabilityTargetPct    string
	MaximumErrorRatePct      string
	Maximum429RatePct        string
	MaximumTTFTMS            int64
	MaximumFullLatencyMS     int64
	MinimumThroughputTPS     string
	MinimumOutputQuality     string
	AutoDownweight           bool
	AutoCircuitBreaker       bool
	CircuitFailureThreshold  int
	CircuitRecoveryThreshold int
	CircuitOpenDuration      time.Duration
	RampEnabled              bool
	RampInitialBPS           int
	RampStepBPS              int
	RampStepInterval         time.Duration
}

type Metrics struct {
	MeasurementCount   int64
	AvailabilityPct    string
	ErrorRatePct       string
	RateLimitedPct     string
	P95TTFTMS          *int64
	P95FullLatencyMS   *int64
	ThroughputTPS      *string
	OutputQualityScore string
	PriceTruthScore    string
	RegionCoveragePct  string
}

type State struct {
	CircuitState          string
	CircuitOpenUntil      *time.Time
	ConsecutiveBreaches   int
	ConsecutiveRecoveries int
	TrafficCapBPS         int
	RampStartedAt         *time.Time
	LastRampStepAt        *time.Time
	SupplierLinked        bool
}

type Breach struct {
	Observed  string
	Threshold string
	Critical  bool
}

type Decision struct {
	Grade                  string
	QualityScore           string
	RoutingMultiplier      string
	TrafficCapBPS          int
	CircuitState           string
	CircuitOpenUntil       *time.Time
	ConsecutiveBreaches    int
	ConsecutiveRecoveries  int
	RampStartedAt          *time.Time
	LastRampStepAt         *time.Time
	Breaches               map[string]Breach
	SufficientMeasurements bool
}

func Evaluate(policy Policy, metrics Metrics, state State, now time.Time) Decision {
	now = now.UTC()
	decision := Decision{
		Grade:                 "UNKNOWN",
		QualityScore:          "50.0000",
		RoutingMultiplier:     "1.000000",
		TrafficCapBPS:         normalizedCap(state.TrafficCapBPS),
		CircuitState:          normalizedCircuit(state.CircuitState),
		CircuitOpenUntil:      state.CircuitOpenUntil,
		ConsecutiveBreaches:   state.ConsecutiveBreaches,
		ConsecutiveRecoveries: state.ConsecutiveRecoveries,
		RampStartedAt:         state.RampStartedAt,
		LastRampStepAt:        state.LastRampStepAt,
		Breaches:              map[string]Breach{},
	}
	if !policy.Enabled {
		return decision
	}

	decision.SufficientMeasurements = metrics.MeasurementCount >= policy.MinimumSamples
	components := []struct {
		score  *big.Rat
		weight int64
	}{
		{minimumRatioScore(metrics.AvailabilityPct, policy.AvailabilityTargetPct), 25},
		{maximumRatioScore(metrics.ErrorRatePct, policy.MaximumErrorRatePct), 15},
		{maximumRatioScore(metrics.RateLimitedPct, policy.Maximum429RatePct), 10},
		{latencyScore(metrics.P95TTFTMS, policy.MaximumTTFTMS), 10},
		{latencyScore(metrics.P95FullLatencyMS, policy.MaximumFullLatencyMS), 10},
		{optionalMinimumScore(metrics.ThroughputTPS, policy.MinimumThroughputTPS), 10},
		{directScore(metrics.OutputQualityScore), 10},
		{directScore(metrics.PriceTruthScore), 5},
		{directScore(metrics.RegionCoveragePct), 5},
	}
	total := new(big.Rat)
	for _, component := range components {
		total.Add(total, new(big.Rat).Mul(component.score, big.NewRat(component.weight, 100)))
	}
	decision.QualityScore = decimal(total, 4)
	if decision.SufficientMeasurements {
		decision.Grade = grade(total)
		collectBreaches(&decision, policy, metrics)
	}
	if policy.AutoDownweight {
		decision.RoutingMultiplier = gradeMultiplier(decision.Grade)
	}

	critical := false
	for _, breach := range decision.Breaches {
		critical = critical || breach.Critical
	}
	if policy.AutoCircuitBreaker && decision.SufficientMeasurements {
		evaluateCircuit(&decision, policy, critical, now)
	}
	if decision.CircuitState == "OPEN" {
		decision.RoutingMultiplier = "0.000000"
		decision.TrafficCapBPS = 0
	} else {
		evaluateRamp(&decision, policy, metrics, state.SupplierLinked, critical, now)
	}
	return decision
}

func collectBreaches(decision *Decision, policy Policy, metrics Metrics) {
	below(decision.Breaches, "AVAILABILITY", metrics.AvailabilityPct, policy.AvailabilityTargetPct, true)
	above(decision.Breaches, "ERROR_RATE", metrics.ErrorRatePct, policy.MaximumErrorRatePct, true)
	above(decision.Breaches, "RATE_LIMITED_RATE", metrics.RateLimitedPct, policy.Maximum429RatePct, true)
	if metrics.P95TTFTMS != nil && *metrics.P95TTFTMS > policy.MaximumTTFTMS {
		decision.Breaches["TTFT"] = Breach{Observed: integer(*metrics.P95TTFTMS), Threshold: integer(policy.MaximumTTFTMS)}
	}
	if metrics.P95FullLatencyMS != nil && *metrics.P95FullLatencyMS > policy.MaximumFullLatencyMS {
		decision.Breaches["FULL_LATENCY"] = Breach{Observed: integer(*metrics.P95FullLatencyMS), Threshold: integer(policy.MaximumFullLatencyMS)}
	}
	if metrics.ThroughputTPS != nil {
		below(decision.Breaches, "THROUGHPUT", *metrics.ThroughputTPS, policy.MinimumThroughputTPS, false)
	}
	below(decision.Breaches, "OUTPUT_QUALITY", metrics.OutputQualityScore, policy.MinimumOutputQuality, false)
	below(decision.Breaches, "PRICE_TRUTH", metrics.PriceTruthScore, "100", false)
	below(decision.Breaches, "REGION_COVERAGE", metrics.RegionCoveragePct, "100", false)
}

func evaluateCircuit(decision *Decision, policy Policy, critical bool, now time.Time) {
	switch decision.CircuitState {
	case "OPEN":
		if decision.CircuitOpenUntil != nil && now.Before(*decision.CircuitOpenUntil) {
			return
		}
		decision.CircuitState = "HALF_OPEN"
		decision.CircuitOpenUntil = nil
	}
	if critical {
		decision.ConsecutiveBreaches++
		decision.ConsecutiveRecoveries = 0
		if decision.ConsecutiveBreaches >= policy.CircuitFailureThreshold {
			until := now.Add(policy.CircuitOpenDuration)
			decision.CircuitState = "OPEN"
			decision.CircuitOpenUntil = &until
		}
		return
	}
	decision.ConsecutiveBreaches = 0
	decision.ConsecutiveRecoveries++
	if decision.CircuitState == "HALF_OPEN" && decision.ConsecutiveRecoveries >= policy.CircuitRecoveryThreshold {
		decision.CircuitState = "CLOSED"
		decision.CircuitOpenUntil = nil
		decision.ConsecutiveRecoveries = 0
	}
}

func evaluateRamp(decision *Decision, policy Policy, metrics Metrics, supplierLinked, critical bool, now time.Time) {
	if !supplierLinked || !policy.RampEnabled {
		decision.TrafficCapBPS = 10000
		return
	}
	if decision.RampStartedAt == nil {
		started := now
		decision.RampStartedAt = &started
		decision.LastRampStepAt = &started
		decision.TrafficCapBPS = policy.RampInitialBPS
		return
	}
	if decision.TrafficCapBPS <= 0 {
		decision.TrafficCapBPS = policy.RampInitialBPS
	}
	healthyGrade := decision.Grade == "A" || decision.Grade == "B" || decision.Grade == "C"
	if critical || !healthyGrade || compare(metrics.PriceTruthScore, "100") < 0 || compare(metrics.RegionCoveragePct, "100") < 0 {
		return
	}
	last := decision.LastRampStepAt
	if last == nil {
		last = decision.RampStartedAt
	}
	if last != nil && now.Sub(*last) >= policy.RampStepInterval {
		decision.TrafficCapBPS += policy.RampStepBPS
		if decision.TrafficCapBPS > 10000 {
			decision.TrafficCapBPS = 10000
		}
		stepped := now
		decision.LastRampStepAt = &stepped
	}
}

func below(out map[string]Breach, name, observed, threshold string, critical bool) {
	if compare(observed, threshold) < 0 {
		out[name] = Breach{Observed: observed, Threshold: threshold, Critical: critical}
	}
}

func above(out map[string]Breach, name, observed, threshold string, critical bool) {
	if compare(observed, threshold) > 0 {
		out[name] = Breach{Observed: observed, Threshold: threshold, Critical: critical}
	}
}

func grade(score *big.Rat) string {
	switch {
	case score.Cmp(big.NewRat(90, 1)) >= 0:
		return "A"
	case score.Cmp(big.NewRat(80, 1)) >= 0:
		return "B"
	case score.Cmp(big.NewRat(70, 1)) >= 0:
		return "C"
	case score.Cmp(big.NewRat(60, 1)) >= 0:
		return "D"
	default:
		return "F"
	}
}

func gradeMultiplier(grade string) string {
	switch grade {
	case "A":
		return "1.000000"
	case "B":
		return "0.900000"
	case "C":
		return "0.700000"
	case "D":
		return "0.350000"
	case "F":
		return "0.100000"
	default:
		return "0.250000"
	}
}

func normalizedCircuit(value string) string {
	if value == "OPEN" || value == "HALF_OPEN" {
		return value
	}
	return "CLOSED"
}

func normalizedCap(value int) int {
	if value < 0 || value > 10000 {
		return 10000
	}
	return value
}

func directScore(value string) *big.Rat {
	return clamp(rat(value), big.NewRat(0, 1), big.NewRat(100, 1))
}

func minimumRatioScore(observed, target string) *big.Rat {
	t := rat(target)
	if t.Sign() <= 0 {
		return big.NewRat(100, 1)
	}
	return clamp(new(big.Rat).Mul(new(big.Rat).Quo(rat(observed), t), big.NewRat(100, 1)), big.NewRat(0, 1), big.NewRat(100, 1))
}

func maximumRatioScore(observed, maximum string) *big.Rat {
	o, m := rat(observed), rat(maximum)
	if o.Cmp(m) <= 0 {
		return big.NewRat(100, 1)
	}
	denominator := new(big.Rat).Sub(big.NewRat(100, 1), m)
	if denominator.Sign() <= 0 {
		return big.NewRat(0, 1)
	}
	penalty := new(big.Rat).Mul(new(big.Rat).Quo(new(big.Rat).Sub(o, m), denominator), big.NewRat(100, 1))
	return clamp(new(big.Rat).Sub(big.NewRat(100, 1), penalty), big.NewRat(0, 1), big.NewRat(100, 1))
}

func latencyScore(observed *int64, maximum int64) *big.Rat {
	if observed == nil || *observed < 0 || maximum <= 0 {
		return big.NewRat(0, 1)
	}
	if *observed <= maximum {
		return big.NewRat(100, 1)
	}
	return clamp(new(big.Rat).Mul(big.NewRat(maximum, *observed), big.NewRat(100, 1)), big.NewRat(0, 1), big.NewRat(100, 1))
}

func optionalMinimumScore(observed *string, minimum string) *big.Rat {
	if observed == nil {
		return big.NewRat(0, 1)
	}
	return minimumRatioScore(*observed, minimum)
}

func compare(left, right string) int { return rat(left).Cmp(rat(right)) }

func rat(value string) *big.Rat {
	parsed, ok := new(big.Rat).SetString(value)
	if !ok {
		return new(big.Rat)
	}
	return parsed
}

func clamp(value, minimum, maximum *big.Rat) *big.Rat {
	if value.Cmp(minimum) < 0 {
		return new(big.Rat).Set(minimum)
	}
	if value.Cmp(maximum) > 0 {
		return new(big.Rat).Set(maximum)
	}
	return value
}

func decimal(value *big.Rat, scale int) string { return value.FloatString(scale) }

func integer(value int64) string { return new(big.Int).SetInt64(value).String() }
