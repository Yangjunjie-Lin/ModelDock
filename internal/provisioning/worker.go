package provisioning

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/store"
)

type WorkerStore interface {
	ClaimProviderProvisioningJobs(context.Context, string, time.Duration, int) ([]domain.ProviderProvisioningJob, error)
	ProviderAccountBindingByID(context.Context, string) (domain.ProviderAccountBinding, error)
	SaveProviderBindingProvisioned(context.Context, string, string, store.ProviderBindingCompletion) (domain.ProviderAccountBinding, error)
	CompleteProviderProvisioningJob(context.Context, string, string, string, map[string]any) error
	FailProviderProvisioningJob(context.Context, string, string, string, string, time.Time, bool) error
}

type WorkerConfig struct {
	WorkerID     string
	PollInterval time.Duration
	Lease        time.Duration
	BatchSize    int
	Logger       *slog.Logger
}

type Worker struct {
	store    WorkerStore
	vault    *secretcrypto.Vault
	registry *Registry
	config   WorkerConfig
}

func NewWorker(store WorkerStore, vault *secretcrypto.Vault, registry *Registry, config WorkerConfig) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
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
	return &Worker{store: store, vault: vault, registry: registry, config: config}
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.store == nil || worker.vault == nil || worker.registry == nil {
		return
	}
	ticker := time.NewTicker(worker.config.PollInterval)
	defer ticker.Stop()
	worker.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.runOnce(ctx)
		}
	}
}

func (worker *Worker) runOnce(ctx context.Context) {
	jobs, err := worker.store.ClaimProviderProvisioningJobs(ctx, worker.config.WorkerID, worker.config.Lease, worker.config.BatchSize)
	if err != nil {
		worker.config.Logger.Error("provider_provisioning_claim_failed", "error", err)
		return
	}
	for _, job := range jobs {
		if err = worker.process(ctx, job); err != nil {
			worker.config.Logger.Error("provider_provisioning_job_failed", "job_id", job.ID, "provider_id", job.ProviderID,
				"operation", job.Operation, "error", err)
		}
	}
}

func (worker *Worker) process(ctx context.Context, job domain.ProviderProvisioningJob) error {
	adapter, err := worker.registry.Resolve(job.ProviderType)
	if err != nil {
		return worker.fail(ctx, job, "adapter_not_registered", err, true)
	}
	capability := adapter.Capabilities()
	if !capability.Enabled {
		return worker.fail(ctx, job, "adapter_disabled", ErrAutomaticUnsupported, true)
	}
	if job.Operation == "ALLOCATE_CREDIT" && !capability.SupportsAutomaticCredit {
		return worker.fail(ctx, job, "automatic_credit_unsupported", ErrAutomaticUnsupported, true)
	}
	if job.Operation == "ENSURE_BINDING" && !capability.SupportsAutomaticBinding {
		return worker.fail(ctx, job, "automatic_binding_unsupported", ErrAutomaticUnsupported, true)
	}
	if job.Operation == "REFRESH_BINDING" && !capability.SupportsRefresh {
		return worker.fail(ctx, job, "refresh_unsupported", ErrAutomaticUnsupported, true)
	}
	binding, err := worker.store.ProviderAccountBindingByID(ctx, job.BindingID)
	if err != nil {
		return worker.fail(ctx, job, "binding_not_found", err, true)
	}
	request := BindingRequest{BindingID: binding.ID, OrganizationID: binding.OrganizationID, UserID: binding.UserID,
		ProviderID: binding.ProviderID, IdempotencyKey: job.IdempotencyKey, ExternalAccountID: binding.ExternalAccountID,
		ExternalProjectID: binding.ExternalProjectID}
	if job.Operation == "REFRESH_BINDING" {
		result, refreshErr := adapter.RefreshBinding(ctx, request)
		if refreshErr != nil {
			return worker.fail(ctx, job, "refresh_failed", refreshErr, false)
		}
		if _, err = worker.persistBinding(ctx, job, result); err != nil {
			return worker.fail(ctx, job, "persist_binding_failed", err, false)
		}
		return worker.store.CompleteProviderProvisioningJob(ctx, job.ID, job.ClaimToken, result.ExternalProjectID, result.Metadata)
	}
	if binding.Status != "ACTIVE" || binding.ExternalProjectID == "" {
		if !capability.SupportsAutomaticBinding {
			return worker.fail(ctx, job, "automatic_binding_unsupported", ErrAutomaticUnsupported, true)
		}
		result, bindingErr := adapter.EnsureBinding(ctx, request)
		if bindingErr != nil {
			return worker.fail(ctx, job, "ensure_binding_failed", bindingErr, false)
		}
		binding, err = worker.persistBinding(ctx, job, result)
		if err != nil {
			return worker.fail(ctx, job, "persist_binding_failed", err, false)
		}
	}
	if job.Operation == "ENSURE_BINDING" {
		return worker.store.CompleteProviderProvisioningJob(ctx, job.ID, job.ClaimToken, binding.ExternalProjectID,
			map[string]any{"binding_status": binding.Status})
	}
	if job.Amount == nil {
		return worker.fail(ctx, job, "allocation_amount_missing", errors.New("allocation amount is missing"), true)
	}
	allocation, err := adapter.AllocateCredit(ctx, AllocationRequest{BindingID: binding.ID, OrganizationID: binding.OrganizationID,
		UserID: binding.UserID, ProviderID: binding.ProviderID, ExternalAccountID: binding.ExternalAccountID,
		ExternalProjectID: binding.ExternalProjectID, IdempotencyKey: job.IdempotencyKey, Amount: *job.Amount, Currency: job.Currency})
	if err != nil {
		return worker.fail(ctx, job, "allocation_failed", err, false)
	}
	return worker.store.CompleteProviderProvisioningJob(ctx, job.ID, job.ClaimToken, allocation.ExternalReference, allocation.Metadata)
}

func (worker *Worker) persistBinding(ctx context.Context, job domain.ProviderProvisioningJob, result BindingResult) (domain.ProviderAccountBinding, error) {
	completion := store.ProviderBindingCompletion{ExternalAccountID: result.ExternalAccountID, ExternalProjectID: result.ExternalProjectID,
		Metadata: result.Metadata, CredentialName: result.CredentialName, CredentialType: result.CredentialType}
	if result.CredentialSecret != "" {
		completion.CredentialID = id.UUID()
		encrypted, err := worker.vault.Encrypt(result.CredentialSecret, completion.CredentialID)
		if err != nil {
			return domain.ProviderAccountBinding{}, err
		}
		completion.EncryptedSecret = encrypted
		completion.SecretLast4 = secretcrypto.Last4(result.CredentialSecret)
	}
	return worker.store.SaveProviderBindingProvisioned(ctx, job.ID, job.ClaimToken, completion)
}

func (worker *Worker) fail(ctx context.Context, job domain.ProviderProvisioningJob, code string, failure error, terminal bool) error {
	if errors.Is(failure, ErrAutomaticUnsupported) || errors.Is(failure, ErrBindingMismatch) {
		terminal = true
	}
	if job.Attempts >= job.MaxAttempts {
		terminal = true
	}
	detail := "provider provisioning failed"
	if failure != nil {
		detail = strings.TrimSpace(failure.Error())
	}
	retryAt := time.Now().UTC().Add(time.Duration(maxProvisioning(job.Attempts, 1)) * 15 * time.Second)
	if err := worker.store.FailProviderProvisioningJob(ctx, job.ID, job.ClaimToken, code, detail, retryAt, terminal); err != nil {
		return err
	}
	return failure
}

func maxProvisioning(left, right int) int {
	if left > right {
		return left
	}
	return right
}
