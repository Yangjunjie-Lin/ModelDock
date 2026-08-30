package provisioning

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type mockAccount struct {
	bindingID, organizationID, userID, providerID string
	externalAccountID, externalProjectID, secret  string
	credit, currency                              string
	allocations                                   map[string]AllocationResult
}

// MockEnterprise is an isolated, no-charge Provisioner used for acceptance
// tests and local demonstrations. It never contacts or impersonates a real
// provider and is disabled in runtime configuration by default.
type MockEnterprise struct {
	mu       sync.Mutex
	enabled  bool
	accounts map[string]*mockAccount
}

func NewMockEnterprise(enabled bool) *MockEnterprise {
	return &MockEnterprise{enabled: enabled, accounts: make(map[string]*mockAccount)}
}

func (mock *MockEnterprise) Capabilities() Capability {
	reason := "Local acceptance adapter is disabled."
	if mock.enabled {
		reason = "Local-only enterprise simulator; no external account or payment is created."
	}
	return Capability{ProviderType: "mock_enterprise", Mode: "MOCK_ENTERPRISE", Enabled: mock.enabled,
		SupportsAutomaticBinding: true, SupportsAutomaticCredit: true, SupportsRefresh: true,
		FreeTestModel: "mock-free", Reason: reason}
}

func (mock *MockEnterprise) EnsureBinding(_ context.Context, request BindingRequest) (BindingResult, error) {
	if !mock.enabled {
		return BindingResult{}, ErrAutomaticUnsupported
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if existing := mock.accounts[request.BindingID]; existing != nil {
		if existing.userID != request.UserID || existing.providerID != request.ProviderID || existing.organizationID != request.OrganizationID {
			return BindingResult{}, ErrBindingMismatch
		}
		return mock.bindingResult(existing), nil
	}
	short := strings.ReplaceAll(request.BindingID, "-", "")
	if len(short) > 12 {
		short = short[:12]
	}
	account := &mockAccount{bindingID: request.BindingID, organizationID: request.OrganizationID, userID: request.UserID,
		providerID: request.ProviderID, externalAccountID: "mock-account-" + short, externalProjectID: "mock-project-" + short,
		secret: "mock-secret-" + short, credit: "0", allocations: make(map[string]AllocationResult)}
	mock.accounts[request.BindingID] = account
	return mock.bindingResult(account), nil
}

func (mock *MockEnterprise) bindingResult(account *mockAccount) BindingResult {
	return BindingResult{ExternalAccountID: account.externalAccountID, ExternalProjectID: account.externalProjectID,
		CredentialSecret: account.secret, CredentialType: "api_key", CredentialName: "Mock enterprise service account",
		Metadata: map[string]any{"environment": "local_acceptance", "provider_managed": false}}
}

func (mock *MockEnterprise) AllocateCredit(_ context.Context, request AllocationRequest) (AllocationResult, error) {
	if !mock.enabled {
		return AllocationResult{}, ErrAutomaticUnsupported
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	account := mock.accounts[request.BindingID]
	if account == nil || account.userID != request.UserID || account.providerID != request.ProviderID || account.organizationID != request.OrganizationID {
		return AllocationResult{}, ErrBindingMismatch
	}
	if result, ok := account.allocations[request.IdempotencyKey]; ok {
		return result, nil
	}
	if account.currency != "" && account.currency != request.Currency {
		return AllocationResult{}, fmt.Errorf("mock enterprise currency mismatch: %s", account.currency)
	}
	current, err := domainDecimal(account.credit)
	if err != nil {
		return AllocationResult{}, err
	}
	next, err := current.Add(request.Amount)
	if err != nil {
		return AllocationResult{}, err
	}
	account.credit, account.currency = next.String(), request.Currency
	result := AllocationResult{ExternalReference: "mock-allocation-" + strings.ReplaceAll(request.IdempotencyKey, ":", "-"),
		Metadata: map[string]any{"credit_after": account.credit, "currency": account.currency}}
	account.allocations[request.IdempotencyKey] = result
	return result, nil
}

func (mock *MockEnterprise) RefreshBinding(_ context.Context, request BindingRequest) (BindingResult, error) {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	account := mock.accounts[request.BindingID]
	if account == nil || account.userID != request.UserID || account.providerID != request.ProviderID || account.organizationID != request.OrganizationID {
		return BindingResult{}, ErrBindingMismatch
	}
	return mock.bindingResult(account), nil
}

// TestFreeModel closes the local acceptance loop without spending or reducing
// allocated credit. Production model traffic continues through the gateway.
func (mock *MockEnterprise) TestFreeModel(bindingID, userID, prompt string) (string, error) {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	account := mock.accounts[bindingID]
	if account == nil || account.userID != userID {
		return "", ErrBindingMismatch
	}
	return "mock-free: " + prompt, nil
}

func (mock *MockEnterprise) Balance(bindingID string) (string, string, error) {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	account := mock.accounts[bindingID]
	if account == nil {
		return "", "", ErrBindingMismatch
	}
	return account.credit, account.currency, nil
}
