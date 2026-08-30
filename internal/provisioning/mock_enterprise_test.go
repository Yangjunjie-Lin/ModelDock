package provisioning

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestMockEnterpriseSingleAccountClosedLoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	adapter := NewMockEnterprise(true)
	bindingRequest := BindingRequest{BindingID: "binding-one", OrganizationID: "organization-one", UserID: "user-one",
		ProviderID: "provider-one", IdempotencyKey: "ensure-one"}
	binding, err := adapter.EnsureBinding(ctx, bindingRequest)
	if err != nil || binding.ExternalAccountID == "" || binding.ExternalProjectID == "" || binding.CredentialSecret == "" {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	amount, err := domain.ParseDecimal("5.000000000000")
	if err != nil {
		t.Fatal(err)
	}
	allocationRequest := AllocationRequest{BindingID: bindingRequest.BindingID, OrganizationID: bindingRequest.OrganizationID,
		UserID: bindingRequest.UserID, ProviderID: bindingRequest.ProviderID, ExternalAccountID: binding.ExternalAccountID,
		ExternalProjectID: binding.ExternalProjectID, IdempotencyKey: "recharge:paid-order-one", Amount: amount, Currency: "USD"}
	const replays = 32
	var wait sync.WaitGroup
	errorsSeen := make(chan error, replays)
	references := make(chan string, replays)
	for range replays {
		wait.Add(1)
		go func() {
			defer wait.Done()
			allocation, allocateErr := adapter.AllocateCredit(ctx, allocationRequest)
			if allocateErr != nil {
				errorsSeen <- allocateErr
				return
			}
			references <- allocation.ExternalReference
		}()
	}
	wait.Wait()
	close(errorsSeen)
	close(references)
	for replayErr := range errorsSeen {
		t.Fatal(replayErr)
	}
	firstReference := ""
	for reference := range references {
		if firstReference == "" {
			firstReference = reference
		} else if reference != firstReference {
			t.Fatalf("idempotent references differ: %q != %q", reference, firstReference)
		}
	}
	balance, currency, err := adapter.Balance(bindingRequest.BindingID)
	if err != nil || balance != "5.000000000000" || currency != "USD" {
		t.Fatalf("balance=%s currency=%s err=%v", balance, currency, err)
	}
	response, err := adapter.TestFreeModel(bindingRequest.BindingID, bindingRequest.UserID, "hello")
	if err != nil || response != "mock-free: hello" {
		t.Fatalf("response=%q err=%v", response, err)
	}
	after, _, err := adapter.Balance(bindingRequest.BindingID)
	if err != nil || after != balance {
		t.Fatalf("free model changed balance before=%s after=%s err=%v", balance, after, err)
	}
	_, err = adapter.EnsureBinding(ctx, BindingRequest{BindingID: bindingRequest.BindingID, OrganizationID: bindingRequest.OrganizationID,
		UserID: "other-user", ProviderID: bindingRequest.ProviderID, IdempotencyKey: "cross-user"})
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cross-user binding err=%v", err)
	}
	allocationRequest.UserID = "other-user"
	allocationRequest.IdempotencyKey = "cross-user-allocation"
	if _, err = adapter.AllocateCredit(ctx, allocationRequest); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cross-user allocation err=%v", err)
	}
}

func TestMockEnterpriseDisabled(t *testing.T) {
	t.Parallel()
	adapter := NewMockEnterprise(false)
	if adapter.Capabilities().Enabled {
		t.Fatal("disabled mock advertised itself as enabled")
	}
	if _, err := adapter.EnsureBinding(context.Background(), BindingRequest{}); !errors.Is(err, ErrAutomaticUnsupported) {
		t.Fatalf("disabled mock err=%v", err)
	}
}
