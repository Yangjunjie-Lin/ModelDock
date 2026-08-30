package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/payment"
)

func TestProviderAccountPaymentAllocationIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	organizationID, _ := paymentFixture(t, ctx, s)
	userID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Provisioning Integration','USER','ACTIVE',now())`, userID,
		"provisioning-"+stringsNoDash(userID)+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,'MEMBER','ACTIVE')`, organizationID, userID); err != nil {
		t.Fatal(err)
	}
	provider, err := s.CreateProvider(ctx, domain.Provider{Name: "Mock Enterprise Integration", Slug: "mock-enterprise-" + stringsNoDash(id.UUID()),
		ProviderType: "mock_enterprise", BaseURL: "http://mock.invalid/v1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conflictProvider, err := s.CreateProvider(ctx, domain.Provider{Name: "Mock Enterprise Conflict", Slug: "mock-conflict-" + stringsNoDash(id.UUID()),
		ProviderType: "mock_enterprise", BaseURL: "http://mock.invalid/v1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.CreateProviderAccountBinding(ctx, CreateProviderAccountBindingRequest{OrganizationID: organizationID,
		UserID: userID, ProviderID: conflictProvider.ID, ProvisioningMode: "MANUAL", ExternalProjectID: "reviewed-project",
		IdempotencyKey: "manual-conflict", CreatedBy: &userID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateRechargeOrder(ctx, CreateRechargeOrderRequest{PlatformOrderNo: "RO_" + stringsNoDash(id.UUID()),
		OrganizationID: organizationID, PaymentProvider: "sandbox", Amount: domain.Decimal("1.000000000000"), Currency: "USD",
		Region: "CN", IdempotencyKey: "provider-mode-conflict", ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedBy: &userID,
		TargetProviderID: conflictProvider.ID, TargetProvisioningMode: "MOCK_ENTERPRISE"}); err != ErrBindingMode {
		t.Fatalf("provider mode conflict err=%v", err)
	}
	replayProvider, err := s.CreateProvider(ctx, domain.Provider{Name: "Mock Enterprise Binding Replay", Slug: "mock-binding-replay-" + stringsNoDash(id.UUID()),
		ProviderType: "mock_enterprise", BaseURL: "http://mock.invalid/v1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const bindingWorkers = 12
	bindingErrors := make(chan error, bindingWorkers)
	var bindingWait sync.WaitGroup
	for workerIndex := range bindingWorkers {
		bindingWait.Add(1)
		go func() {
			defer bindingWait.Done()
			_, _, _, createErr := s.CreateProviderAccountBinding(ctx, CreateProviderAccountBindingRequest{OrganizationID: organizationID,
				UserID: userID, ProviderID: replayProvider.ID, ProvisioningMode: "MOCK_ENTERPRISE",
				IdempotencyKey: fmt.Sprintf("binding-replay-%d", workerIndex), EnqueueAutomatic: true, CreatedBy: &userID})
			if createErr != nil {
				bindingErrors <- createErr
			}
		}()
	}
	bindingWait.Wait()
	close(bindingErrors)
	for createErr := range bindingErrors {
		t.Fatal(createErr)
	}
	var replayBindings, replayJobs int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_account_binding WHERE organization_id=$1 AND user_id=$2 AND provider_id=$3),
		(SELECT count(*) FROM provider_provisioning_job j JOIN provider_account_binding b ON b.id=j.binding_id
		WHERE b.organization_id=$1 AND b.user_id=$2 AND b.provider_id=$3 AND j.operation='ENSURE_BINDING')`,
		organizationID, userID, replayProvider.ID).Scan(&replayBindings, &replayJobs); err != nil {
		t.Fatal(err)
	}
	if replayBindings != 1 || replayJobs != 1 {
		t.Fatalf("replay bindings=%d jobs=%d", replayBindings, replayJobs)
	}
	order, _, err := s.CreateRechargeOrder(ctx, CreateRechargeOrderRequest{PlatformOrderNo: "RO_" + stringsNoDash(id.UUID()),
		OrganizationID: organizationID, PaymentProvider: "sandbox", Amount: domain.Decimal("3.000000000000"), Currency: "USD",
		Region: "CN", IdempotencyKey: "provider-allocation", ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedBy: &userID,
		TargetProviderID: provider.ID, TargetProvisioningMode: "MOCK_ENTERPRISE"})
	if err != nil {
		t.Fatal(err)
	}
	var reservedMode, reservedStatus string
	if err = s.pool.QueryRow(ctx, `SELECT provisioning_mode,status FROM provider_account_binding
		WHERE organization_id=$1 AND user_id=$2 AND provider_id=$3`, organizationID, userID, provider.ID).
		Scan(&reservedMode, &reservedStatus); err != nil {
		t.Fatal(err)
	}
	if reservedMode != "MOCK_ENTERPRISE" || reservedStatus != "PENDING" {
		t.Fatalf("reserved mode=%s status=%s", reservedMode, reservedStatus)
	}
	if _, err = s.MarkRechargePending(ctx, order.ID, "sbx_"+order.PlatformOrderNo, map[string]any{"mode": "test"}); err != nil {
		t.Fatal(err)
	}
	replayKey := "provider-allocation-replay-" + stringsNoDash(id.UUID())
	order, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(),
		ProviderOrderNo: "sbx_" + order.PlatformOrderNo, PlatformOrderNo: order.PlatformOrderNo, EventType: "payment.paid", Status: "PAID",
		Amount: order.Amount, Currency: order.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: replayKey}, fmt.Sprintf("%064x", 42))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 20
	var wait sync.WaitGroup
	errorsSeen := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, creditErr := s.CreditPaidRecharge(ctx, order.ID)
			if creditErr != nil {
				errorsSeen <- creditErr
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for creditErr := range errorsSeen {
		t.Fatal(creditErr)
	}
	var bindings, jobs int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_account_binding WHERE organization_id=$1 AND user_id=$2 AND provider_id=$3),
		(SELECT count(*) FROM provider_provisioning_job j JOIN provider_account_binding b ON b.id=j.binding_id
		WHERE j.recharge_order_id=$4 AND b.user_id=$2)`, organizationID, userID, provider.ID, order.ID).Scan(&bindings, &jobs); err != nil {
		t.Fatal(err)
	}
	if bindings != 1 || jobs != 1 {
		t.Fatalf("bindings=%d jobs=%d", bindings, jobs)
	}
	otherUserID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Other User','USER','ACTIVE',now())`, otherUserID,
		"other-"+stringsNoDash(otherUserID)+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.CreateProviderAccountBinding(ctx, CreateProviderAccountBindingRequest{OrganizationID: organizationID,
		UserID: otherUserID, ProviderID: provider.ID, ProvisioningMode: "MOCK_ENTERPRISE", IdempotencyKey: "cross-user",
		EnqueueAutomatic: true, CreatedBy: &userID}); err != ErrNotFound {
		t.Fatalf("cross-organization member binding err=%v", err)
	}
}
