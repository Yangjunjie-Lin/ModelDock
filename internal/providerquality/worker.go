package providerquality

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/providers"
	provideropenai "github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/store"
)

const (
	probePrompt   = "Return exactly this ASCII text and nothing else: MODELDock quality probe OK"
	probeExpected = "MODELDock quality probe OK"
)

type qualityStore interface {
	EnsureProviderQualityProbeSchedules(context.Context, string) error
	ClaimProviderQualityProbe(context.Context, string, time.Duration) (domain.ProviderQualityProbeJob, error)
	CompleteProviderQualityProbe(context.Context, domain.ProviderQualityProbeJob, bool) error
	RecordProviderQualityObservation(context.Context, domain.ProviderQualityObservation) (domain.ProviderQualityObservation, bool, error)
	ListEnabledProviderQualityIDs(context.Context) ([]string, error)
	EvaluateProviderQuality(context.Context, string, time.Time) (domain.ProviderQualityState, error)
}

type credentialVault interface {
	Decrypt([]byte, string) (string, error)
}

type providerRegistry interface {
	Resolve(string) (providers.Provider, error)
}

type Config struct {
	Region       string
	PollInterval time.Duration
	Lease        time.Duration
	BatchSize    int
	Logger       *slog.Logger
}

type Worker struct {
	store    qualityStore
	vault    credentialVault
	registry providerRegistry
	config   Config
}

func NewWorker(database *store.Store, vault *secretcrypto.Vault, registry *providers.Registry, config Config) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = 30 * time.Second
	}
	if config.Lease <= 0 {
		config.Lease = 2 * time.Minute
	}
	if config.BatchSize <= 0 || config.BatchSize > 100 {
		config.BatchSize = 20
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	config.Region = strings.ToUpper(strings.TrimSpace(config.Region))
	return &Worker{store: database, vault: vault, registry: registry, config: config}
}

func (w *Worker) Run(ctx context.Context) {
	if len(w.config.Region) != 2 {
		w.config.Logger.Warn("provider_quality_worker_disabled", "reason", "probe region is not configured")
		return
	}
	w.runCycle(ctx)
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runCycle(ctx)
		}
	}
}

func (w *Worker) runCycle(ctx context.Context) {
	if err := w.store.EnsureProviderQualityProbeSchedules(ctx, w.config.Region); err != nil {
		w.config.Logger.Error("provider_quality_schedule_failed", "region", w.config.Region, "error", err)
		return
	}
	for index := 0; index < w.config.BatchSize; index++ {
		job, err := w.store.ClaimProviderQualityProbe(ctx, w.config.Region, w.config.Lease)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			w.config.Logger.Error("provider_quality_claim_failed", "region", w.config.Region, "error", err)
			break
		}
		succeeded := w.runProbe(ctx, job)
		if err = w.store.CompleteProviderQualityProbe(ctx, job, succeeded); err != nil {
			w.config.Logger.Error("provider_quality_complete_failed", "provider_id", job.ProviderID, "region", job.Region, "error", err)
		}
	}
	providerIDs, err := w.store.ListEnabledProviderQualityIDs(ctx)
	if err != nil {
		w.config.Logger.Error("provider_quality_list_failed", "error", err)
		return
	}
	for _, providerID := range providerIDs {
		if _, err = w.store.EvaluateProviderQuality(ctx, providerID, time.Now().UTC()); err != nil {
			w.config.Logger.Error("provider_quality_evaluation_failed", "provider_id", providerID, "error", err)
		}
	}
}

func (w *Worker) runProbe(parent context.Context, job domain.ProviderQualityProbeJob) bool {
	status := 0
	credentialID := job.CredentialID
	if credentialID == nil || len(job.EncryptedCredential) == 0 {
		w.record(job, domain.ProviderQualityObservation{IdempotencyKey: job.LeaseToken + ":health", ProviderID: job.ProviderID,
			CredentialID: credentialID, Source: "SCHEDULED_HEALTH", Region: &job.Region, Succeeded: false,
			ErrorClass: "no_eligible_platform_credential", ObservedAt: time.Now().UTC()})
		return false
	}
	secret, err := w.vault.Decrypt(job.EncryptedCredential, *credentialID)
	if err != nil {
		w.record(job, domain.ProviderQualityObservation{IdempotencyKey: job.LeaseToken + ":health", ProviderID: job.ProviderID,
			CredentialID: credentialID, Source: "SCHEDULED_HEALTH", Region: &job.Region, Succeeded: false,
			ErrorClass: "credential_decryption_failed", ObservedAt: time.Now().UTC()})
		return false
	}
	adapter, err := w.registry.Resolve(job.ProviderType)
	if err != nil {
		w.record(job, domain.ProviderQualityObservation{IdempotencyKey: job.LeaseToken + ":health", ProviderID: job.ProviderID,
			CredentialID: credentialID, Source: "SCHEDULED_HEALTH", Region: &job.Region, Succeeded: false,
			ErrorClass: "provider_adapter_unavailable", ObservedAt: time.Now().UTC()})
		return false
	}
	credential := providers.Credential{Secret: secret, OrganizationID: job.CredentialOrgID, ProjectID: job.CredentialProjectID}
	timeout := time.Duration(job.ProbeTimeoutMS) * time.Millisecond
	probeCtx, cancel := context.WithTimeout(parent, timeout)
	started := time.Now()
	err = adapter.HealthCheck(probeCtx, job.ProviderBaseURL, credential)
	latency := time.Since(started).Milliseconds()
	cancel()
	if err != nil {
		status = providerHTTPStatus(err)
	}
	healthStatus := nullableStatus(status)
	w.record(job, domain.ProviderQualityObservation{IdempotencyKey: job.LeaseToken + ":health", ProviderID: job.ProviderID,
		CredentialID: credentialID, Source: "SCHEDULED_HEALTH", Region: &job.Region, StatusCode: healthStatus,
		Succeeded: err == nil, RateLimited: status == http.StatusTooManyRequests, FullLatencyMS: &latency,
		ErrorClass: classifyProbeError(err, status), ObservedAt: time.Now().UTC()})
	if err != nil || job.ProbeModelID == nil || job.ProbeModelName == "" {
		return err == nil
	}

	body, marshalErr := json.Marshal(map[string]any{
		"model": job.ProbeModelName, "stream": true, "stream_options": map[string]any{"include_usage": true},
		"temperature": 0, "max_tokens": 32,
		"messages": []map[string]string{{"role": "user", "content": probePrompt}},
	})
	if marshalErr != nil {
		return false
	}
	probeCtx, cancel = context.WithTimeout(parent, timeout)
	started = time.Now()
	response, err := adapter.CreateStreamCompletion(probeCtx, providers.ForwardRequest{BaseURL: job.ProviderBaseURL,
		Body: bytes.NewReader(body), ContentType: "application/json", Accept: "text/event-stream", Credential: credential})
	if err != nil {
		latency = time.Since(started).Milliseconds()
		cancel()
		status = providerHTTPStatus(err)
		w.record(job, domain.ProviderQualityObservation{IdempotencyKey: job.LeaseToken + ":synthetic", ProviderID: job.ProviderID,
			CredentialID: credentialID, Source: "SYNTHETIC_QUALITY", Region: &job.Region, ModelID: job.ProbeModelID,
			StatusCode: nullableStatus(status), Succeeded: false, RateLimited: status == http.StatusTooManyRequests,
			FullLatencyMS: &latency, ErrorClass: classifyProbeError(err, status), ObservedAt: time.Now().UTC()})
		return false
	}
	defer response.Body.Close()
	status = response.StatusCode
	result := measureSyntheticStream(response.Body, started)
	cancel()
	if status < 200 || status >= 300 {
		result.Succeeded = false
		result.ErrorClass = classifyProbeError(nil, status)
	}
	result.IdempotencyKey = job.LeaseToken + ":synthetic"
	result.ProviderID = job.ProviderID
	result.CredentialID = credentialID
	result.Source = "SYNTHETIC_QUALITY"
	result.Region = &job.Region
	result.ModelID = job.ProbeModelID
	result.StatusCode = &status
	result.RateLimited = status == http.StatusTooManyRequests
	w.record(job, result)
	return result.Succeeded
}

func (w *Worker) record(job domain.ProviderQualityProbeJob, observation domain.ProviderQualityObservation) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := w.store.RecordProviderQualityObservation(ctx, observation); err != nil {
		w.config.Logger.Error("provider_quality_observation_failed", "provider_id", job.ProviderID, "source", observation.Source, "error", err)
	}
}

func measureSyntheticStream(body io.Reader, started time.Time) domain.ProviderQualityObservation {
	result := domain.ProviderQualityObservation{Succeeded: true, ObservedAt: time.Now().UTC()}
	scanner := bufio.NewScanner(io.LimitReader(body, 1<<20))
	scanner.Buffer(make([]byte, 4096), 256<<10)
	var output strings.Builder
	var firstTokenAt time.Time
	var inputTokens, outputTokens int64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event map[string]any
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.UseNumber()
		if decoder.Decode(&event) != nil {
			continue
		}
		content := streamContent(event)
		if content != "" {
			if firstTokenAt.IsZero() {
				firstTokenAt = time.Now()
			}
			if output.Len()+len(content) > 4096 {
				result.Succeeded = false
				result.ErrorClass = "synthetic_response_too_large"
				break
			}
			output.WriteString(content)
		}
		readUsage(event, &inputTokens, &outputTokens)
	}
	finished := time.Now()
	full := finished.Sub(started).Milliseconds()
	result.FullLatencyMS = &full
	if !firstTokenAt.IsZero() {
		ttft := firstTokenAt.Sub(started).Milliseconds()
		result.TTFTMS = &ttft
	}
	if scanner.Err() != nil {
		result.Succeeded = false
		result.ErrorClass = "synthetic_stream_read_failed"
	}
	trimmed := strings.TrimSpace(output.String())
	quality := domain.Decimal("0.0000")
	if trimmed == probeExpected {
		quality = domain.Decimal("100.0000")
	}
	result.OutputQualityScore = &quality
	hash := sha256.Sum256([]byte(trimmed))
	hashText := hex.EncodeToString(hash[:])
	result.ResponseSHA256 = &hashText
	result.InputTokens, result.OutputTokens = &inputTokens, &outputTokens
	if outputTokens > 0 && result.TTFTMS != nil && full > *result.TTFTMS {
		durationMS := full - *result.TTFTMS
		throughput := domain.Decimal(new(big.Rat).Mul(big.NewRat(outputTokens, durationMS), big.NewRat(1000, 1)).FloatString(6))
		result.ThroughputTPS = &throughput
	}
	return result
}

func streamContent(event map[string]any) string {
	if delta, ok := event["delta"].(string); ok {
		return delta
	}
	choices, _ := event["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	content, _ := delta["content"].(string)
	return content
}

func readUsage(event map[string]any, inputTokens, outputTokens *int64) {
	usage, _ := event["usage"].(map[string]any)
	if usage == nil {
		return
	}
	*inputTokens = integerMetric(usage, "input_tokens", "prompt_tokens")
	*outputTokens = integerMetric(usage, "output_tokens", "completion_tokens")
}

func integerMetric(values map[string]any, names ...string) int64 {
	for _, name := range names {
		if value, ok := values[name].(json.Number); ok {
			parsed, err := strconv.ParseInt(value.String(), 10, 64)
			if err == nil && parsed >= 0 {
				return parsed
			}
		}
	}
	return 0
}

func providerHTTPStatus(err error) int {
	var upstream *provideropenai.HTTPError
	if errors.As(err, &upstream) {
		return upstream.StatusCode
	}
	return 0
}

func nullableStatus(status int) *int {
	if status == 0 {
		return nil
	}
	return &status
}

func classifyProbeError(err error, status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "http_429"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "authentication_or_policy_denied"
	case status >= 500:
		return "http_5xx"
	case status >= 400:
		return "http_4xx"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case err != nil:
		return "connection_error"
	default:
		return ""
	}
}
