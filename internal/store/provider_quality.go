package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	qualitycalc "github.com/relayedock/relayedock/internal/quality"
)

var (
	ErrProviderQualityCircuitOpen = errors.New("provider quality circuit is open")
	ErrProviderQualityRampLimited = errors.New("provider quality ramp temporarily withheld this request")
)

const providerQualityPolicyColumns = `provider_id,enabled,probe_model_id,probe_interval_seconds,probe_timeout_ms,
	evaluation_window_minutes,minimum_samples,availability_target_pct::text,maximum_error_rate_pct::text,
	maximum_429_rate_pct::text,maximum_ttft_ms,maximum_full_latency_ms,minimum_throughput_tps::text,
	minimum_output_quality_score::text,price_truth_tolerance_bps,required_test_regions,auto_downweight_enabled,
	auto_circuit_breaker_enabled,circuit_failure_threshold,circuit_recovery_threshold,circuit_open_seconds,
	ramp_enabled,ramp_initial_bps,ramp_step_bps,ramp_step_interval_seconds,configuration_source,created_at,updated_at`

const providerQualityStateColumns = `provider_id,grade,quality_score::text,routing_multiplier::text,traffic_cap_bps,
	circuit_state,circuit_open_until,consecutive_breaches,consecutive_recoveries,availability_pct::text,
	error_rate_pct::text,rate_limited_pct::text,p95_ttft_ms,p95_full_latency_ms,throughput_tps::text,
	output_quality_score::text,price_truth_score::text,region_coverage_pct::text,measurement_count,
	ramp_started_at,last_ramp_step_at,last_probe_at,last_evaluated_at,state_version,updated_at`

func scanProviderQualityPolicy(row pgx.Row) (domain.ProviderQualityPolicy, error) {
	var out domain.ProviderQualityPolicy
	var availability, errorRate, limitedRate, throughput, outputQuality string
	var regions []byte
	err := row.Scan(&out.ProviderID, &out.Enabled, &out.ProbeModelID, &out.ProbeIntervalSeconds, &out.ProbeTimeoutMS,
		&out.EvaluationWindowMinutes, &out.MinimumSamples, &availability, &errorRate, &limitedRate,
		&out.MaximumTTFTMS, &out.MaximumFullLatencyMS, &throughput, &outputQuality, &out.PriceTruthToleranceBPS,
		&regions, &out.AutoDownweightEnabled, &out.AutoCircuitBreakerEnabled, &out.CircuitFailureThreshold,
		&out.CircuitRecoveryThreshold, &out.CircuitOpenSeconds, &out.RampEnabled, &out.RampInitialBPS,
		&out.RampStepBPS, &out.RampStepIntervalSeconds, &out.ConfigurationSource, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.AvailabilityTargetPct = domain.Decimal(availability)
	out.MaximumErrorRatePct = domain.Decimal(errorRate)
	out.Maximum429RatePct = domain.Decimal(limitedRate)
	out.MinimumThroughputTPS = domain.Decimal(throughput)
	out.MinimumOutputQualityScore = domain.Decimal(outputQuality)
	_ = json.Unmarshal(regions, &out.RequiredTestRegions)
	if out.RequiredTestRegions == nil {
		out.RequiredTestRegions = []string{}
	}
	return out, nil
}

func scanProviderQualityState(row pgx.Row) (domain.ProviderQualityState, error) {
	var out domain.ProviderQualityState
	var score, multiplier, availability, errorRate, limitedRate, outputQuality, priceTruth, regionCoverage string
	var throughput *string
	err := row.Scan(&out.ProviderID, &out.Grade, &score, &multiplier, &out.TrafficCapBPS, &out.CircuitState,
		&out.CircuitOpenUntil, &out.ConsecutiveBreaches, &out.ConsecutiveRecoveries, &availability, &errorRate,
		&limitedRate, &out.P95TTFTMS, &out.P95FullLatencyMS, &throughput, &outputQuality, &priceTruth,
		&regionCoverage, &out.MeasurementCount, &out.RampStartedAt, &out.LastRampStepAt, &out.LastProbeAt,
		&out.LastEvaluatedAt, &out.StateVersion, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.QualityScore, out.RoutingMultiplier = domain.Decimal(score), domain.Decimal(multiplier)
	out.AvailabilityPct, out.ErrorRatePct = domain.Decimal(availability), domain.Decimal(errorRate)
	out.RateLimitedPct = domain.Decimal(limitedRate)
	if throughput != nil {
		value := domain.Decimal(*throughput)
		out.ThroughputTPS = &value
	}
	out.OutputQualityScore, out.PriceTruthScore = domain.Decimal(outputQuality), domain.Decimal(priceTruth)
	out.RegionCoveragePct = domain.Decimal(regionCoverage)
	return out, nil
}

func (s *Store) ProviderQualityPolicy(ctx context.Context, providerID string) (domain.ProviderQualityPolicy, error) {
	return scanProviderQualityPolicy(s.pool.QueryRow(ctx, `SELECT `+providerQualityPolicyColumns+` FROM provider_quality_policies WHERE provider_id=$1`, providerID))
}

func (s *Store) ProviderQualityState(ctx context.Context, providerID string) (domain.ProviderQualityState, error) {
	return scanProviderQualityState(s.pool.QueryRow(ctx, `SELECT `+providerQualityStateColumns+` FROM provider_quality_states WHERE provider_id=$1`, providerID))
}

func (s *Store) ListProviderQualitySummaries(ctx context.Context) ([]domain.ProviderQualitySummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.name,p.slug,sp.supplier_id,COALESCE(so.display_name,''),`+
		prefixColumns(providerQualityPolicyColumns, "qp")+`,`+prefixColumns(providerQualityStateColumns, "qs")+`
		FROM providers p JOIN provider_quality_policies qp ON qp.provider_id=p.id
		JOIN provider_quality_states qs ON qs.provider_id=p.id
		LEFT JOIN supplier_provider_links sp ON sp.provider_id=p.id AND sp.status='ACTIVE'
		LEFT JOIN supplier_organizations so ON so.id=sp.supplier_id ORDER BY p.name,p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProviderQualitySummary
	for rows.Next() {
		var item domain.ProviderQualitySummary
		var policyAvailability, policyError, policy429, policyThroughput, policyOutput string
		var policyRegions []byte
		var stateScore, stateMultiplier, stateAvailability, stateError, state429, stateOutput, statePrice, stateRegion string
		var stateThroughput *string
		err = rows.Scan(&item.ProviderID, &item.ProviderName, &item.ProviderSlug, &item.SupplierID, &item.SupplierName,
			&item.Policy.ProviderID, &item.Policy.Enabled, &item.Policy.ProbeModelID, &item.Policy.ProbeIntervalSeconds,
			&item.Policy.ProbeTimeoutMS, &item.Policy.EvaluationWindowMinutes, &item.Policy.MinimumSamples,
			&policyAvailability, &policyError, &policy429, &item.Policy.MaximumTTFTMS,
			&item.Policy.MaximumFullLatencyMS, &policyThroughput, &policyOutput, &item.Policy.PriceTruthToleranceBPS,
			&policyRegions, &item.Policy.AutoDownweightEnabled, &item.Policy.AutoCircuitBreakerEnabled,
			&item.Policy.CircuitFailureThreshold, &item.Policy.CircuitRecoveryThreshold, &item.Policy.CircuitOpenSeconds,
			&item.Policy.RampEnabled, &item.Policy.RampInitialBPS, &item.Policy.RampStepBPS,
			&item.Policy.RampStepIntervalSeconds, &item.Policy.ConfigurationSource, &item.Policy.CreatedAt, &item.Policy.UpdatedAt,
			&item.State.ProviderID, &item.State.Grade, &stateScore, &stateMultiplier, &item.State.TrafficCapBPS,
			&item.State.CircuitState, &item.State.CircuitOpenUntil, &item.State.ConsecutiveBreaches,
			&item.State.ConsecutiveRecoveries, &stateAvailability, &stateError, &state429, &item.State.P95TTFTMS,
			&item.State.P95FullLatencyMS, &stateThroughput, &stateOutput, &statePrice, &stateRegion,
			&item.State.MeasurementCount, &item.State.RampStartedAt, &item.State.LastRampStepAt, &item.State.LastProbeAt,
			&item.State.LastEvaluatedAt, &item.State.StateVersion, &item.State.UpdatedAt)
		if err != nil {
			return nil, err
		}
		item.Policy.AvailabilityTargetPct, item.Policy.MaximumErrorRatePct = domain.Decimal(policyAvailability), domain.Decimal(policyError)
		item.Policy.Maximum429RatePct, item.Policy.MinimumThroughputTPS = domain.Decimal(policy429), domain.Decimal(policyThroughput)
		item.Policy.MinimumOutputQualityScore = domain.Decimal(policyOutput)
		_ = json.Unmarshal(policyRegions, &item.Policy.RequiredTestRegions)
		item.State.QualityScore, item.State.RoutingMultiplier = domain.Decimal(stateScore), domain.Decimal(stateMultiplier)
		item.State.AvailabilityPct, item.State.ErrorRatePct = domain.Decimal(stateAvailability), domain.Decimal(stateError)
		item.State.RateLimitedPct = domain.Decimal(state429)
		if stateThroughput != nil {
			value := domain.Decimal(*stateThroughput)
			item.State.ThroughputTPS = &value
		}
		item.State.OutputQualityScore, item.State.PriceTruthScore = domain.Decimal(stateOutput), domain.Decimal(statePrice)
		item.State.RegionCoveragePct = domain.Decimal(stateRegion)
		out = append(out, item)
	}
	return out, rows.Err()
}

func prefixColumns(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if cast := strings.Index(part, "::"); cast >= 0 {
			parts[index] = alias + "." + part[:cast] + part[cast:]
		} else {
			parts[index] = alias + "." + part
		}
	}
	return strings.Join(parts, ",")
}

func (s *Store) UpsertProviderQualityPolicy(ctx context.Context, policy domain.ProviderQualityPolicy, actor string) (domain.ProviderQualityPolicy, error) {
	policy.RequiredTestRegions = normalizeQualityRegions(policy.RequiredTestRegions)
	if policy.ProviderID == "" || policy.ProbeIntervalSeconds < 30 || policy.ProbeTimeoutMS < 1000 || policy.EvaluationWindowMinutes < 5 || policy.MinimumSamples <= 0 || len(policy.RequiredTestRegions) > 32 {
		return domain.ProviderQualityPolicy{}, errors.New("invalid Provider quality policy")
	}
	for _, region := range policy.RequiredTestRegions {
		if len(region) != 2 || region[0] < 'A' || region[0] > 'Z' || region[1] < 'A' || region[1] > 'Z' {
			return domain.ProviderQualityPolicy{}, errors.New("required_test_regions must contain ISO alpha-2 codes")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderQualityPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if policy.ProbeModelID != nil {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM models WHERE id=$1 AND provider_id=$2 AND enabled)`, *policy.ProbeModelID, policy.ProviderID).Scan(&valid); err != nil || !valid {
			if err == nil {
				err = errors.New("probe model does not belong to the enabled Provider")
			}
			return domain.ProviderQualityPolicy{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_quality_policies(provider_id,enabled,probe_model_id,probe_interval_seconds,
		probe_timeout_ms,evaluation_window_minutes,minimum_samples,availability_target_pct,maximum_error_rate_pct,
		maximum_429_rate_pct,maximum_ttft_ms,maximum_full_latency_ms,minimum_throughput_tps,
		minimum_output_quality_score,price_truth_tolerance_bps,required_test_regions,auto_downweight_enabled,
		auto_circuit_breaker_enabled,circuit_failure_threshold,circuit_recovery_threshold,circuit_open_seconds,
		ramp_enabled,ramp_initial_bps,ramp_step_bps,ramp_step_interval_seconds,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9::numeric,$10::numeric,$11,$12,$13::numeric,$14::numeric,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,NULLIF($26,'')::uuid,NULLIF($26,'')::uuid)
		ON CONFLICT(provider_id) DO UPDATE SET enabled=EXCLUDED.enabled,probe_model_id=EXCLUDED.probe_model_id,
		probe_interval_seconds=EXCLUDED.probe_interval_seconds,probe_timeout_ms=EXCLUDED.probe_timeout_ms,
		evaluation_window_minutes=EXCLUDED.evaluation_window_minutes,minimum_samples=EXCLUDED.minimum_samples,
		availability_target_pct=EXCLUDED.availability_target_pct,maximum_error_rate_pct=EXCLUDED.maximum_error_rate_pct,
		maximum_429_rate_pct=EXCLUDED.maximum_429_rate_pct,maximum_ttft_ms=EXCLUDED.maximum_ttft_ms,
		maximum_full_latency_ms=EXCLUDED.maximum_full_latency_ms,minimum_throughput_tps=EXCLUDED.minimum_throughput_tps,
		minimum_output_quality_score=EXCLUDED.minimum_output_quality_score,price_truth_tolerance_bps=EXCLUDED.price_truth_tolerance_bps,
		required_test_regions=EXCLUDED.required_test_regions,auto_downweight_enabled=EXCLUDED.auto_downweight_enabled,
		auto_circuit_breaker_enabled=EXCLUDED.auto_circuit_breaker_enabled,circuit_failure_threshold=EXCLUDED.circuit_failure_threshold,
		circuit_recovery_threshold=EXCLUDED.circuit_recovery_threshold,circuit_open_seconds=EXCLUDED.circuit_open_seconds,
		ramp_enabled=EXCLUDED.ramp_enabled,ramp_initial_bps=EXCLUDED.ramp_initial_bps,ramp_step_bps=EXCLUDED.ramp_step_bps,
		ramp_step_interval_seconds=EXCLUDED.ramp_step_interval_seconds,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		policy.ProviderID, policy.Enabled, policy.ProbeModelID, policy.ProbeIntervalSeconds, policy.ProbeTimeoutMS,
		policy.EvaluationWindowMinutes, policy.MinimumSamples, policy.AvailabilityTargetPct.String(), policy.MaximumErrorRatePct.String(),
		policy.Maximum429RatePct.String(), policy.MaximumTTFTMS, policy.MaximumFullLatencyMS, policy.MinimumThroughputTPS.String(),
		policy.MinimumOutputQualityScore.String(), policy.PriceTruthToleranceBPS, jsonBytes(policy.RequiredTestRegions),
		policy.AutoDownweightEnabled, policy.AutoCircuitBreakerEnabled, policy.CircuitFailureThreshold,
		policy.CircuitRecoveryThreshold, policy.CircuitOpenSeconds, policy.RampEnabled, policy.RampInitialBPS,
		policy.RampStepBPS, policy.RampStepIntervalSeconds, actor)
	if err != nil {
		return domain.ProviderQualityPolicy{}, err
	}
	if err = writeAuditTx(ctx, tx, optionalActor(actor), "provider.quality_policy_updated", "provider", policy.ProviderID, map[string]any{
		"enabled": policy.Enabled, "probe_model_id": policy.ProbeModelID, "required_test_regions": policy.RequiredTestRegions,
		"configuration_source": "PLATFORM_ADMIN"}); err != nil {
		return domain.ProviderQualityPolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderQualityPolicy{}, err
	}
	return s.ProviderQualityPolicy(ctx, policy.ProviderID)
}

func normalizeQualityRegions(regions []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(regions))
	for _, region := range regions {
		region = strings.ToUpper(strings.TrimSpace(region))
		if _, ok := seen[region]; region == "" || ok {
			continue
		}
		seen[region] = struct{}{}
		out = append(out, region)
	}
	return out
}

func optionalActor(actor string) *string {
	if strings.TrimSpace(actor) == "" {
		return nil
	}
	return &actor
}

func (s *Store) LinkSupplierProvider(ctx context.Context, providerID, supplierID, reason, actor string) (domain.SupplierProviderLink, error) {
	if strings.TrimSpace(reason) == "" {
		return domain.SupplierProviderLink{}, errors.New("supplier Provider link reason is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierProviderLink{}, err
	}
	defer tx.Rollback(ctx)
	var supplierStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM supplier_organizations WHERE id=$1 FOR SHARE`, supplierID).Scan(&supplierStatus); errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierProviderLink{}, ErrNotFound
	} else if err != nil {
		return domain.SupplierProviderLink{}, err
	}
	if supplierStatus != "APPROVED" {
		return domain.SupplierProviderLink{}, errors.New("supplier must be approved before Provider linkage")
	}
	var initialBPS int
	var qualityEnabled bool
	if err = tx.QueryRow(ctx, `SELECT ramp_initial_bps,enabled FROM provider_quality_policies WHERE provider_id=$1 FOR UPDATE`, providerID).Scan(&initialBPS, &qualityEnabled); errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierProviderLink{}, ErrNotFound
	} else if err != nil {
		return domain.SupplierProviderLink{}, err
	}
	if !qualityEnabled {
		return domain.SupplierProviderLink{}, errors.New("Provider quality policy must be enabled before supplier linkage")
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_provider_links(provider_id,supplier_id,status,linked_by,reason)
		VALUES($1,$2,'ACTIVE',NULLIF($3,'')::uuid,$4) ON CONFLICT(provider_id) DO UPDATE SET supplier_id=EXCLUDED.supplier_id,
		status='ACTIVE',linked_by=EXCLUDED.linked_by,linked_at=now(),ended_at=NULL,reason=EXCLUDED.reason`, providerID, supplierID, actor, strings.TrimSpace(reason))
	if err != nil {
		return domain.SupplierProviderLink{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE provider_quality_states SET traffic_cap_bps=$2,ramp_started_at=now(),last_ramp_step_at=now(),
		state_version=state_version+1,updated_at=now() WHERE provider_id=$1`, providerID, initialBPS)
	if err != nil {
		return domain.SupplierProviderLink{}, err
	}
	if err = writeAuditTx(ctx, tx, optionalActor(actor), "provider.supplier_linked", "provider", providerID,
		map[string]any{"supplier_id": supplierID, "traffic_cap_bps": initialBPS, "reason": strings.TrimSpace(reason)}); err != nil {
		return domain.SupplierProviderLink{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierProviderLink{}, err
	}
	return domain.SupplierProviderLink{ProviderID: providerID, SupplierID: supplierID, Status: "ACTIVE", Reason: strings.TrimSpace(reason), LinkedAt: time.Now().UTC()}, nil
}

func (s *Store) RecordProviderQualityObservation(ctx context.Context, observation domain.ProviderQualityObservation) (domain.ProviderQualityObservation, bool, error) {
	if observation.ID == "" {
		observation.ID = id.UUID()
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	if observation.IdempotencyKey == "" || observation.ProviderID == "" {
		return domain.ProviderQualityObservation{}, false, errors.New("quality observation requires idempotency_key and provider_id")
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO provider_quality_observations(id,idempotency_key,provider_id,credential_id,
		provider_attempt_id,source,region,model_id,status_code,succeeded,rate_limited,ttft_ms,full_latency_ms,
		input_tokens,output_tokens,throughput_tps,output_quality_score,response_sha256,error_class,observed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT(idempotency_key) DO NOTHING`, observation.ID, observation.IdempotencyKey, observation.ProviderID,
		observation.CredentialID, observation.ProviderAttemptID, observation.Source, observation.Region, observation.ModelID,
		observation.StatusCode, observation.Succeeded, observation.RateLimited, observation.TTFTMS, observation.FullLatencyMS,
		observation.InputTokens, observation.OutputTokens, decimalPointer(observation.ThroughputTPS),
		decimalPointer(observation.OutputQualityScore), observation.ResponseSHA256, observation.ErrorClass, observation.ObservedAt)
	if err != nil {
		return domain.ProviderQualityObservation{}, false, err
	}
	return observation, tag.RowsAffected() == 1, nil
}

func decimalPointer(value *domain.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func (s *Store) EnsureProviderQualityProbeSchedules(ctx context.Context, region string) error {
	region = strings.ToUpper(strings.TrimSpace(region))
	if len(region) != 2 {
		return errors.New("probe region must be an ISO alpha-2 code")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO provider_quality_probe_schedules(provider_id,region)
		SELECT provider_id,$1 FROM provider_quality_policies WHERE enabled AND
		(required_test_regions='[]'::jsonb OR required_test_regions ? $1)
		ON CONFLICT(provider_id,region) DO NOTHING`, region)
	return err
}

func (s *Store) ClaimProviderQualityProbe(ctx context.Context, region string, lease time.Duration) (domain.ProviderQualityProbeJob, error) {
	region = strings.ToUpper(strings.TrimSpace(region))
	if lease <= 0 {
		lease = time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderQualityProbeJob{}, err
	}
	defer tx.Rollback(ctx)
	var job domain.ProviderQualityProbeJob
	var credentialID, orgID, projectID *string
	var encrypted []byte
	err = tx.QueryRow(ctx, `SELECT q.provider_id,p.provider_type,p.base_url,q.region,qp.probe_model_id,
		COALESCE(m.provider_model_id,''),qp.probe_timeout_ms,c.id,c.encrypted_secret,c.organization_id::text,c.project_id::text
		FROM provider_quality_probe_schedules q
		JOIN provider_quality_policies qp ON qp.provider_id=q.provider_id AND qp.enabled
		JOIN providers p ON p.id=q.provider_id
		LEFT JOIN models m ON m.id=qp.probe_model_id AND m.provider_id=p.id AND m.enabled
		LEFT JOIN LATERAL (SELECT pc.id,pc.encrypted_secret,pc.organization_id,pc.project_id
			FROM provider_credentials pc WHERE pc.provider_id=p.id AND pc.credential_owner='PLATFORM'
			AND pc.status='ACTIVE' AND (pc.cooldown_until IS NULL OR pc.cooldown_until<=now())
			ORDER BY pc.current_health='HEALTHY' DESC,pc.last_success_at DESC NULLS LAST,pc.id LIMIT 1) c ON true
		WHERE q.region=$1 AND q.next_probe_at<=now() AND (q.lease_until IS NULL OR q.lease_until<=now())
		AND p.enabled AND NOT p.emergency_kill_switch AND p.commercial_status='COMMERCIAL_APPROVED'
		AND p.commercial_resale_status='APPROVED' AND (p.contract_start_at IS NULL OR p.contract_start_at<=now())
		AND (p.contract_end_at IS NULL OR p.contract_end_at>now())
		AND (p.allowed_customer_regions ? '*' OR p.allowed_customer_regions ? q.region)
		ORDER BY q.next_probe_at,q.provider_id FOR UPDATE OF q SKIP LOCKED LIMIT 1`, region).Scan(&job.ProviderID,
		&job.ProviderType, &job.ProviderBaseURL, &job.Region, &job.ProbeModelID, &job.ProbeModelName,
		&job.ProbeTimeoutMS, &credentialID, &encrypted, &orgID, &projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderQualityProbeJob{}, ErrNotFound
	}
	if err != nil {
		return domain.ProviderQualityProbeJob{}, err
	}
	job.LeaseToken = id.UUID()
	job.CredentialID, job.EncryptedCredential = credentialID, encrypted
	if orgID != nil {
		job.CredentialOrgID = *orgID
	}
	if projectID != nil {
		job.CredentialProjectID = *projectID
	}
	_, err = tx.Exec(ctx, `UPDATE provider_quality_probe_schedules SET lease_token=$3,lease_until=now()+$4::interval,
		last_started_at=now(),last_status='RUNNING',updated_at=now() WHERE provider_id=$1 AND region=$2`,
		job.ProviderID, job.Region, job.LeaseToken, intervalLiteral(lease))
	if err != nil {
		return domain.ProviderQualityProbeJob{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderQualityProbeJob{}, err
	}
	return job, nil
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%d milliseconds", duration.Milliseconds())
}

func (s *Store) CompleteProviderQualityProbe(ctx context.Context, job domain.ProviderQualityProbeJob, succeeded bool) error {
	status := "FAILED"
	if succeeded {
		status = "SUCCEEDED"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE provider_quality_probe_schedules q SET lease_token=NULL,lease_until=NULL,
		last_finished_at=now(),last_status=$4,next_probe_at=now()+make_interval(secs=>p.probe_interval_seconds),updated_at=now()
		FROM provider_quality_policies p WHERE q.provider_id=$1 AND q.region=$2 AND q.lease_token=$3 AND p.provider_id=q.provider_id`,
		job.ProviderID, job.Region, job.LeaseToken, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListEnabledProviderQualityIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider_id FROM provider_quality_policies WHERE enabled ORDER BY provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) EvaluateProviderQuality(ctx context.Context, providerID string, now time.Time) (domain.ProviderQualityState, error) {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	defer tx.Rollback(ctx)
	policy, err := scanProviderQualityPolicy(tx.QueryRow(ctx, `SELECT `+providerQualityPolicyColumns+` FROM provider_quality_policies WHERE provider_id=$1 FOR UPDATE`, providerID))
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	state, err := scanProviderQualityState(tx.QueryRow(ctx, `SELECT `+providerQualityStateColumns+` FROM provider_quality_states WHERE provider_id=$1 FOR UPDATE`, providerID))
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	windowEnd := now
	windowStart := now.Add(-time.Duration(policy.EvaluationWindowMinutes) * time.Minute)
	metrics, lastProbe, err := qualityMetrics(ctx, tx, providerID, windowStart, windowEnd, policy.RequiredTestRegions)
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	var supplierLinked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM supplier_provider_links WHERE provider_id=$1 AND status='ACTIVE')`, providerID).Scan(&supplierLinked); err != nil {
		return domain.ProviderQualityState{}, err
	}
	decision := qualitycalc.Evaluate(qualitycalc.Policy{Enabled: policy.Enabled, MinimumSamples: int64(policy.MinimumSamples),
		AvailabilityTargetPct: policy.AvailabilityTargetPct.String(), MaximumErrorRatePct: policy.MaximumErrorRatePct.String(),
		Maximum429RatePct: policy.Maximum429RatePct.String(), MaximumTTFTMS: policy.MaximumTTFTMS,
		MaximumFullLatencyMS: policy.MaximumFullLatencyMS, MinimumThroughputTPS: policy.MinimumThroughputTPS.String(),
		MinimumOutputQuality: policy.MinimumOutputQualityScore.String(), AutoDownweight: policy.AutoDownweightEnabled,
		AutoCircuitBreaker: policy.AutoCircuitBreakerEnabled, CircuitFailureThreshold: policy.CircuitFailureThreshold,
		CircuitRecoveryThreshold: policy.CircuitRecoveryThreshold, CircuitOpenDuration: time.Duration(policy.CircuitOpenSeconds) * time.Second,
		RampEnabled: policy.RampEnabled, RampInitialBPS: policy.RampInitialBPS, RampStepBPS: policy.RampStepBPS,
		RampStepInterval: time.Duration(policy.RampStepIntervalSeconds) * time.Second}, metrics,
		qualitycalc.State{CircuitState: state.CircuitState, CircuitOpenUntil: state.CircuitOpenUntil,
			ConsecutiveBreaches: state.ConsecutiveBreaches, ConsecutiveRecoveries: state.ConsecutiveRecoveries,
			TrafficCapBPS: state.TrafficCapBPS, RampStartedAt: state.RampStartedAt, LastRampStepAt: state.LastRampStepAt,
			SupplierLinked: supplierLinked}, now)
	throughput := any(nil)
	if metrics.ThroughputTPS != nil {
		throughput = *metrics.ThroughputTPS
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_quality_rollups(provider_id,window_start,window_end,measurement_count,
		availability_pct,error_rate_pct,rate_limited_pct,p95_ttft_ms,p95_full_latency_ms,throughput_tps,
		output_quality_score,price_truth_score,region_coverage_pct,grade,quality_score)
		VALUES($1,$2,$3,$4,$5::numeric,$6::numeric,$7::numeric,$8,$9,$10,$11::numeric,$12::numeric,$13::numeric,$14,$15::numeric)
		ON CONFLICT(provider_id,window_start,window_end) DO NOTHING`, providerID, windowStart, windowEnd,
		metrics.MeasurementCount, metrics.AvailabilityPct, metrics.ErrorRatePct, metrics.RateLimitedPct,
		metrics.P95TTFTMS, metrics.P95FullLatencyMS, throughput, metrics.OutputQualityScore, metrics.PriceTruthScore,
		metrics.RegionCoveragePct, decision.Grade, decision.QualityScore)
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE provider_quality_states SET grade=$2,quality_score=$3::numeric,
		routing_multiplier=$4::numeric,traffic_cap_bps=$5,circuit_state=$6,circuit_open_until=$7,
		consecutive_breaches=$8,consecutive_recoveries=$9,availability_pct=$10::numeric,error_rate_pct=$11::numeric,
		rate_limited_pct=$12::numeric,p95_ttft_ms=$13,p95_full_latency_ms=$14,throughput_tps=$15,
		output_quality_score=$16::numeric,price_truth_score=$17::numeric,region_coverage_pct=$18::numeric,
		measurement_count=$19,ramp_started_at=$20,last_ramp_step_at=$21,last_probe_at=$22,last_evaluated_at=$23,
		state_version=state_version+1,updated_at=now() WHERE provider_id=$1`, providerID, decision.Grade,
		decision.QualityScore, decision.RoutingMultiplier, decision.TrafficCapBPS, decision.CircuitState,
		decision.CircuitOpenUntil, decision.ConsecutiveBreaches, decision.ConsecutiveRecoveries,
		metrics.AvailabilityPct, metrics.ErrorRatePct, metrics.RateLimitedPct, metrics.P95TTFTMS,
		metrics.P95FullLatencyMS, throughput, metrics.OutputQualityScore, metrics.PriceTruthScore,
		metrics.RegionCoveragePct, metrics.MeasurementCount, decision.RampStartedAt, decision.LastRampStepAt,
		lastProbe, now)
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	if err = synchronizeProviderSLAEvents(ctx, tx, providerID, windowStart, windowEnd, decision); err != nil {
		return domain.ProviderQualityState{}, err
	}
	if state.Grade != decision.Grade || state.CircuitState != decision.CircuitState || state.TrafficCapBPS != decision.TrafficCapBPS || state.RoutingMultiplier.String() != decision.RoutingMultiplier {
		if err = writeAuditTx(ctx, tx, nil, "provider.quality_state_transition", "provider", providerID, map[string]any{
			"source": "PLATFORM_MEASURED", "from_grade": state.Grade, "to_grade": decision.Grade,
			"from_circuit": state.CircuitState, "to_circuit": decision.CircuitState,
			"routing_multiplier": decision.RoutingMultiplier, "traffic_cap_bps": decision.TrafficCapBPS,
			"measurement_count": metrics.MeasurementCount}); err != nil {
			return domain.ProviderQualityState{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderQualityState{}, err
	}
	return s.ProviderQualityState(ctx, providerID)
}

func qualityMetrics(ctx context.Context, tx pgx.Tx, providerID string, start, end time.Time, requiredRegions []string) (qualitycalc.Metrics, *time.Time, error) {
	var metrics qualitycalc.Metrics
	var throughput *string
	var lastProbe *time.Time
	err := tx.QueryRow(ctx, `SELECT count(*),
		COALESCE((count(*) FILTER (WHERE succeeded))::numeric*100/NULLIF(count(*),0),0)::text,
		COALESCE((count(*) FILTER (WHERE NOT succeeded AND NOT rate_limited))::numeric*100/NULLIF(count(*),0),0)::text,
		COALESCE((count(*) FILTER (WHERE rate_limited))::numeric*100/NULLIF(count(*),0),0)::text,
		percentile_disc(0.95) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL),
		percentile_disc(0.95) WITHIN GROUP (ORDER BY full_latency_ms) FILTER (WHERE full_latency_ms IS NOT NULL),
		avg(throughput_tps)::text,COALESCE(avg(output_quality_score),0)::text,
		max(observed_at) FILTER (WHERE source IN ('SCHEDULED_HEALTH','SYNTHETIC_QUALITY','REGION_PROBE'))
		FROM provider_quality_observations WHERE provider_id=$1 AND observed_at>=$2 AND observed_at<$3`, providerID, start, end).Scan(
		&metrics.MeasurementCount, &metrics.AvailabilityPct, &metrics.ErrorRatePct, &metrics.RateLimitedPct,
		&metrics.P95TTFTMS, &metrics.P95FullLatencyMS, &throughput, &metrics.OutputQualityScore, &lastProbe)
	if err != nil {
		return metrics, nil, err
	}
	metrics.ThroughputTPS = throughput
	if len(requiredRegions) == 0 {
		metrics.RegionCoveragePct = "100.0000"
	} else {
		var covered int64
		if err = tx.QueryRow(ctx, `SELECT count(DISTINCT region) FROM provider_quality_observations
			WHERE provider_id=$1 AND observed_at>=$2 AND observed_at<$3 AND succeeded
			AND source IN ('SCHEDULED_HEALTH','SYNTHETIC_QUALITY','REGION_PROBE') AND region=ANY($4)`,
			providerID, start, end, requiredRegions).Scan(&covered); err != nil {
			return metrics, nil, err
		}
		metrics.RegionCoveragePct = new(big.Rat).Mul(new(big.Rat).SetFrac(big.NewInt(covered), big.NewInt(int64(len(requiredRegions)))), big.NewRat(100, 1)).FloatString(4)
	}
	var priceTotal, priceMatches int64
	if err = tx.QueryRow(ctx, `WITH latest AS (SELECT DISTINCT ON (model_id) result FROM provider_price_verifications
		WHERE provider_id=$1 AND observed_at<$2 AND (expires_at IS NULL OR expires_at>$2)
		ORDER BY model_id,observed_at DESC,id DESC) SELECT count(*),count(*) FILTER (WHERE result='MATCH') FROM latest`,
		providerID, end).Scan(&priceTotal, &priceMatches); err != nil {
		return metrics, nil, err
	}
	metrics.PriceTruthScore = "0.0000"
	if priceTotal > 0 {
		metrics.PriceTruthScore = new(big.Rat).Mul(new(big.Rat).SetFrac(big.NewInt(priceMatches), big.NewInt(priceTotal)), big.NewRat(100, 1)).FloatString(4)
	}
	return metrics, lastProbe, nil
}

func synchronizeProviderSLAEvents(ctx context.Context, tx pgx.Tx, providerID string, start, end time.Time, decision qualitycalc.Decision) error {
	metrics := []string{"AVAILABILITY", "ERROR_RATE", "RATE_LIMITED_RATE", "TTFT", "FULL_LATENCY", "THROUGHPUT", "OUTPUT_QUALITY", "PRICE_TRUTH", "REGION_COVERAGE"}
	for _, metric := range metrics {
		key := "provider-quality:" + providerID + ":" + metric
		breach, breached := decision.Breaches[metric]
		if breached {
			severity := "WARNING"
			if breach.Critical {
				severity = "CRITICAL"
			}
			_, err := tx.Exec(ctx, `INSERT INTO provider_sla_events(provider_id,metric,severity,dedupe_key,observed_value,
				threshold_value,window_start,window_end,details) SELECT $1,$2,$3,$4,$5::numeric,$6::numeric,$7,$8,$9
				WHERE NOT EXISTS(SELECT 1 FROM provider_sla_events WHERE dedupe_key=$4 AND status='OPEN')`,
				providerID, metric, severity, key, breach.Observed, breach.Threshold, start, end,
				jsonBytes(map[string]any{"source": "PLATFORM_MEASURED", "grade": decision.Grade}))
			if err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE provider_sla_events SET status='RESOLVED',resolved_at=$2
			WHERE dedupe_key=$1 AND status='OPEN'`, key, end); err != nil {
			return err
		}
	}
	circuitKey := "provider-quality:" + providerID + ":CIRCUIT_BREAKER"
	if decision.CircuitState == "OPEN" {
		_, err := tx.Exec(ctx, `INSERT INTO provider_sla_events(provider_id,metric,severity,dedupe_key,window_start,window_end,details)
			SELECT $1,'CIRCUIT_BREAKER','CRITICAL',$2,$3,$4,$5 WHERE NOT EXISTS(
			SELECT 1 FROM provider_sla_events WHERE dedupe_key=$2 AND status='OPEN')`, providerID, circuitKey, start, end,
			jsonBytes(map[string]any{"source": "PLATFORM_MEASURED", "circuit_state": decision.CircuitState}))
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE provider_sla_events SET status='RESOLVED',resolved_at=$2 WHERE dedupe_key=$1 AND status='OPEN'`, circuitKey, end)
	return err
}

func (s *Store) ResetProviderQualityCircuit(ctx context.Context, providerID, reason, actor string) (domain.ProviderQualityState, error) {
	if strings.TrimSpace(reason) == "" {
		return domain.ProviderQualityState{}, errors.New("circuit reset reason is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE provider_quality_states SET circuit_state='HALF_OPEN',circuit_open_until=NULL,
		consecutive_breaches=0,consecutive_recoveries=0,state_version=state_version+1,updated_at=now() WHERE provider_id=$1`, providerID)
	if err != nil {
		return domain.ProviderQualityState{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.ProviderQualityState{}, ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, optionalActor(actor), "provider.quality_circuit_reset", "provider", providerID,
		map[string]any{"reason": strings.TrimSpace(reason), "target_state": "HALF_OPEN"}); err != nil {
		return domain.ProviderQualityState{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderQualityState{}, err
	}
	return s.ProviderQualityState(ctx, providerID)
}

func (s *Store) CreateProviderPriceVerification(ctx context.Context, verification domain.ProviderPriceVerification, actor string) (domain.ProviderPriceVerification, bool, error) {
	verification.ProviderID = strings.TrimSpace(verification.ProviderID)
	verification.ModelID = strings.TrimSpace(verification.ModelID)
	verification.SourceType = strings.ToUpper(strings.TrimSpace(verification.SourceType))
	verification.Currency = strings.ToUpper(strings.TrimSpace(verification.Currency))
	verification.EvidenceSHA256 = strings.ToLower(strings.TrimSpace(verification.EvidenceSHA256))
	if verification.IdempotencyKey == "" || verification.ProviderID == "" || verification.ModelID == "" || len(verification.EvidenceSHA256) != 64 || verification.Unit <= 0 {
		return domain.ProviderPriceVerification{}, false, errors.New("invalid Provider price verification")
	}
	fingerprint := providerPriceVerificationFingerprint(verification)
	if verification.ObservedAt.IsZero() {
		verification.ObservedAt = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderPriceVerification{}, false, err
	}
	defer tx.Rollback(ctx)
	var existingID, existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,request_fingerprint FROM provider_price_verifications WHERE idempotency_key=$1 FOR UPDATE`, verification.IdempotencyKey).Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return domain.ProviderPriceVerification{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.ProviderPriceVerification{}, false, err
		}
		out, loadErr := s.ProviderPriceVerificationByID(ctx, existingID)
		return out, false, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderPriceVerification{}, false, err
	}
	var priceBookID, input, cached, output, fixed, currency string
	var unit int64
	err = tx.QueryRow(ctx, `SELECT id,input_token_cost::text,cached_input_token_cost::text,output_token_cost::text,
		request_fixed_cost::text,currency,unit FROM provider_cost_price_book WHERE provider_id=$1 AND model_id=$2
		AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=$3 AND (expires_at IS NULL OR expires_at>$3)
		ORDER BY effective_at DESC,id DESC LIMIT 1`, verification.ProviderID, verification.ModelID, verification.ObservedAt).Scan(
		&priceBookID, &input, &cached, &output, &fixed, &currency, &unit)
	verification.Result = "UNVERIFIABLE"
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderPriceVerification{}, false, err
	}
	if err == nil {
		verification.PriceBookID = &priceBookID
		deviation := maximumPriceDeviationBPS([]string{input, cached, output, fixed}, []string{
			verification.ObservedInputTokenCost, verification.ObservedCachedInputTokenCost,
			verification.ObservedOutputTokenCost, verification.ObservedRequestFixedCost})
		verification.MaximumDeviationBPS = &deviation
		var tolerance int64
		if err = tx.QueryRow(ctx, `SELECT price_truth_tolerance_bps FROM provider_quality_policies WHERE provider_id=$1`, verification.ProviderID).Scan(&tolerance); err != nil {
			return domain.ProviderPriceVerification{}, false, err
		}
		verification.Result = "MISMATCH"
		if strings.EqualFold(currency, verification.Currency) && unit == verification.Unit && deviation <= tolerance {
			verification.Result = "MATCH"
		}
	}
	verification.ID = id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO provider_price_verifications(id,idempotency_key,request_fingerprint,provider_id,
		model_id,price_book_id,source_type,source_reference,evidence_sha256,observed_input_token_cost,
		observed_cached_input_token_cost,observed_output_token_cost,observed_request_fixed_cost,currency,unit,result,
		maximum_deviation_bps,observed_at,expires_at,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::numeric,
		$11::numeric,$12::numeric,$13::numeric,$14,$15,$16,$17,$18,$19,NULLIF($20,'')::uuid)`, verification.ID,
		verification.IdempotencyKey, fingerprint, verification.ProviderID, verification.ModelID, verification.PriceBookID,
		verification.SourceType, strings.TrimSpace(verification.SourceReference), verification.EvidenceSHA256,
		verification.ObservedInputTokenCost, verification.ObservedCachedInputTokenCost, verification.ObservedOutputTokenCost,
		verification.ObservedRequestFixedCost, verification.Currency, verification.Unit, verification.Result,
		verification.MaximumDeviationBPS, verification.ObservedAt, verification.ExpiresAt, actor)
	if err != nil {
		return domain.ProviderPriceVerification{}, false, err
	}
	if err = writeAuditTx(ctx, tx, optionalActor(actor), "provider.price_verified", "provider", verification.ProviderID,
		map[string]any{"verification_id": verification.ID, "model_id": verification.ModelID, "result": verification.Result,
			"source_type": verification.SourceType, "evidence_sha256": verification.EvidenceSHA256}); err != nil {
		return domain.ProviderPriceVerification{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderPriceVerification{}, false, err
	}
	out, err := s.ProviderPriceVerificationByID(ctx, verification.ID)
	return out, true, err
}

func providerPriceVerificationFingerprint(value domain.ProviderPriceVerification) string {
	raw := strings.Join([]string{value.ProviderID, value.ModelID, value.SourceType, strings.TrimSpace(value.SourceReference),
		value.EvidenceSHA256, value.ObservedInputTokenCost, value.ObservedCachedInputTokenCost,
		value.ObservedOutputTokenCost, value.ObservedRequestFixedCost, value.Currency, fmt.Sprint(value.Unit),
		value.ObservedAt.UTC().Format(time.RFC3339Nano), nullableTimeString(value.ExpiresAt)}, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func nullableTimeString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func maximumPriceDeviationBPS(reference, observed []string) int64 {
	maximum := int64(0)
	for index := range reference {
		left, leftOK := new(big.Rat).SetString(reference[index])
		right, rightOK := new(big.Rat).SetString(observed[index])
		if !leftOK || !rightOK || left.Sign() < 0 || right.Sign() < 0 {
			return 1<<62 - 1
		}
		if left.Sign() == 0 {
			if right.Sign() > 0 {
				return 1<<62 - 1
			}
			continue
		}
		difference := new(big.Rat).Sub(right, left)
		if difference.Sign() < 0 {
			difference.Neg(difference)
		}
		bps := new(big.Rat).Mul(new(big.Rat).Quo(difference, left), big.NewRat(10000, 1))
		quotient := new(big.Int).Quo(bps.Num(), bps.Denom())
		if new(big.Int).Mod(bps.Num(), bps.Denom()).Sign() != 0 {
			quotient.Add(quotient, big.NewInt(1))
		}
		if quotient.IsInt64() && quotient.Int64() > maximum {
			maximum = quotient.Int64()
		}
	}
	return maximum
}

const providerPriceVerificationColumns = `id,provider_id,model_id,price_book_id,source_type,source_reference,evidence_sha256,
	observed_input_token_cost::text,observed_cached_input_token_cost::text,observed_output_token_cost::text,
	observed_request_fixed_cost::text,currency,unit,result,maximum_deviation_bps,observed_at,expires_at,created_at`

func scanProviderPriceVerification(row pgx.Row) (domain.ProviderPriceVerification, error) {
	var out domain.ProviderPriceVerification
	err := row.Scan(&out.ID, &out.ProviderID, &out.ModelID, &out.PriceBookID, &out.SourceType, &out.SourceReference,
		&out.EvidenceSHA256, &out.ObservedInputTokenCost, &out.ObservedCachedInputTokenCost,
		&out.ObservedOutputTokenCost, &out.ObservedRequestFixedCost, &out.Currency, &out.Unit, &out.Result,
		&out.MaximumDeviationBPS, &out.ObservedAt, &out.ExpiresAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func (s *Store) ProviderPriceVerificationByID(ctx context.Context, verificationID string) (domain.ProviderPriceVerification, error) {
	return scanProviderPriceVerification(s.pool.QueryRow(ctx, `SELECT `+providerPriceVerificationColumns+` FROM provider_price_verifications WHERE id=$1`, verificationID))
}

func (s *Store) ListProviderPriceVerifications(ctx context.Context, providerID string, limit int) ([]domain.ProviderPriceVerification, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+providerPriceVerificationColumns+` FROM provider_price_verifications
		WHERE ($1='' OR provider_id=NULLIF($1,'')::uuid) ORDER BY observed_at DESC,id DESC LIMIT $2`, providerID, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProviderPriceVerification
	for rows.Next() {
		item, scanErr := scanProviderPriceVerification(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListProviderSLAEvents(ctx context.Context, providerID, status string, limit int) ([]domain.ProviderSLAEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.provider_id,p.name,e.metric,e.severity,e.status,e.observed_value::text,
		e.threshold_value::text,e.window_start,e.window_end,e.details,e.started_at,e.resolved_at,e.created_at
		FROM provider_sla_events e JOIN providers p ON p.id=e.provider_id
		WHERE ($1='' OR e.provider_id=NULLIF($1,'')::uuid) AND ($2='' OR e.status=$2)
		ORDER BY e.started_at DESC,e.id DESC LIMIT $3`, providerID, strings.ToUpper(strings.TrimSpace(status)), clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProviderSLAEvent
	for rows.Next() {
		var item domain.ProviderSLAEvent
		var observed, threshold *string
		var details []byte
		if err = rows.Scan(&item.ID, &item.ProviderID, &item.ProviderName, &item.Metric, &item.Severity, &item.Status,
			&observed, &threshold, &item.WindowStart, &item.WindowEnd, &details, &item.StartedAt, &item.ResolvedAt,
			&item.CreatedAt); err != nil {
			return nil, err
		}
		if observed != nil {
			value := domain.Decimal(*observed)
			item.ObservedValue = &value
		}
		if threshold != nil {
			value := domain.Decimal(*threshold)
			item.ThresholdValue = &value
		}
		_ = json.Unmarshal(details, &item.Details)
		out = append(out, item)
	}
	return out, rows.Err()
}
