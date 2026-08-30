package provisioning

import (
	"context"
	"testing"
	"time"

	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

type acceptanceWorkerStore struct {
	binding           domain.ProviderAccountBinding
	encryptedSecret   []byte
	completed         int
	externalReference string
	failed            bool
}

func (value *acceptanceWorkerStore) ClaimProviderProvisioningJobs(context.Context, string, time.Duration, int) ([]domain.ProviderProvisioningJob, error) {
	return nil, nil
}
func (value *acceptanceWorkerStore) ProviderAccountBindingByID(context.Context, string) (domain.ProviderAccountBinding, error) {
	return value.binding, nil
}
func (value *acceptanceWorkerStore) SaveProviderBindingProvisioned(_ context.Context, _, _ string, completion store.ProviderBindingCompletion) (domain.ProviderAccountBinding, error) {
	value.encryptedSecret = append([]byte(nil), completion.EncryptedSecret...)
	value.binding.Status = "ACTIVE"
	value.binding.ExternalAccountID = completion.ExternalAccountID
	value.binding.ExternalProjectID = completion.ExternalProjectID
	credentialID := completion.CredentialID
	value.binding.CredentialID = &credentialID
	return value.binding, nil
}
func (value *acceptanceWorkerStore) CompleteProviderProvisioningJob(_ context.Context, _, _, externalReference string, _ map[string]any) error {
	value.completed++
	value.externalReference = externalReference
	return nil
}
func (value *acceptanceWorkerStore) FailProviderProvisioningJob(context.Context, string, string, string, string, time.Time, bool) error {
	value.failed = true
	return nil
}

func TestWorkerPaidOrderEnsuresBindingEncryptsCredentialAndAllocates(t *testing.T) {
	t.Parallel()
	amount, err := domain.ParseDecimal("7.500000000000")
	if err != nil {
		t.Fatal(err)
	}
	mock := NewMockEnterprise(true)
	registry := NewRegistry()
	registry.Register("mock_enterprise", mock)
	vault, err := secretcrypto.NewVault([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	workerStore := &acceptanceWorkerStore{binding: domain.ProviderAccountBinding{ID: "binding-paid", OrganizationID: "organization-paid",
		UserID: "user-paid", ProviderID: "provider-paid", ProviderType: "mock_enterprise", ProvisioningMode: "MOCK_ENTERPRISE", Status: "PENDING"}}
	worker := NewWorker(workerStore, vault, registry, WorkerConfig{})
	job := domain.ProviderProvisioningJob{ID: "job-paid", BindingID: workerStore.binding.ID, OrganizationID: workerStore.binding.OrganizationID,
		UserID: workerStore.binding.UserID, ProviderID: workerStore.binding.ProviderID, ProviderType: "mock_enterprise",
		Operation: "ALLOCATE_CREDIT", IdempotencyKey: "recharge:paid-one", Status: "PROCESSING", Amount: &amount, Currency: "USD",
		Attempts: 1, MaxAttempts: 5, ClaimToken: "claim-paid"}
	if err = worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if workerStore.failed || workerStore.completed != 1 || workerStore.externalReference == "" || len(workerStore.encryptedSecret) == 0 {
		t.Fatalf("store=%+v", workerStore)
	}
	balance, currency, err := mock.Balance(workerStore.binding.ID)
	if err != nil || balance != "7.500000000000" || currency != "USD" {
		t.Fatalf("balance=%s currency=%s err=%v", balance, currency, err)
	}
	job.ID, job.ClaimToken = "job-paid-replay", "claim-paid-replay"
	if err = worker.process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	after, _, err := mock.Balance(workerStore.binding.ID)
	if err != nil || after != balance {
		t.Fatalf("payment replay changed upstream balance before=%s after=%s err=%v", balance, after, err)
	}
	response, err := mock.TestFreeModel(workerStore.binding.ID, workerStore.binding.UserID, "closed loop")
	if err != nil || response != "mock-free: closed loop" {
		t.Fatalf("response=%q err=%v", response, err)
	}
}
