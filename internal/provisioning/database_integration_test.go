package provisioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/store"
)

func TestProviderPaymentProvisioningDatabaseClosedLoop(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	organizationID, userID := id.UUID(), id.UUID()
	suffix := strings.ReplaceAll(organizationID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,status)
		VALUES($1,'Provisioning Closed Loop',$2,'ACTIVE')`, organizationID, "provisioning-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Provisioning Closed Loop','USER','ACTIVE',now())`, userID,
		"closed-loop-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,'MEMBER','ACTIVE')`, organizationID, userID); err != nil {
		t.Fatal(err)
	}
	provider, err := dataStore.CreateProvider(ctx, domain.Provider{Name: "Mock Enterprise Closed Loop",
		Slug: "mock-closed-loop-" + suffix, ProviderType: "mock_enterprise", BaseURL: "http://mock.invalid/v1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	order, _, err := dataStore.CreateRechargeOrder(ctx, store.CreateRechargeOrderRequest{
		PlatformOrderNo: "RO_" + suffix, OrganizationID: organizationID, PaymentProvider: "sandbox",
		Amount: domain.Decimal("3.000000000000"), Currency: "USD", Region: "CN", IdempotencyKey: "closed-loop-" + suffix,
		ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedBy: &userID, TargetProviderID: provider.ID,
		TargetProvisioningMode: "MOCK_ENTERPRISE"})
	if err != nil {
		t.Fatal(err)
	}
	order, err = dataStore.MarkRechargePending(ctx, order.ID, "sbx_"+order.PlatformOrderNo, map[string]any{"mode": "test"})
	if err != nil {
		t.Fatal(err)
	}
	eventID := "evt_" + id.UUID()
	bodyHash := sha256.Sum256([]byte(eventID))
	order, _, err = dataStore.RecordVerifiedPaymentWebhook(ctx, "sandbox", payment.VerifiedWebhook{
		ProviderEventID: eventID, ProviderOrderNo: order.ProviderOrderNo, PlatformOrderNo: order.PlatformOrderNo,
		EventType: "payment.paid", Status: "PAID", Amount: order.Amount, Currency: order.Currency,
		ProviderTimestamp: time.Now().UTC(), ReplayKey: "closed-loop-" + suffix}, hex.EncodeToString(bodyHash[:]))
	if err != nil {
		t.Fatal(err)
	}
	if _, replayed, creditErr := dataStore.CreditPaidRecharge(ctx, order.ID); creditErr != nil || replayed {
		t.Fatalf("first credit replayed=%v err=%v", replayed, creditErr)
	}
	if _, replayed, creditErr := dataStore.CreditPaidRecharge(ctx, order.ID); creditErr != nil || !replayed {
		t.Fatalf("second credit replayed=%v err=%v", replayed, creditErr)
	}

	vault, err := secretcrypto.NewVault([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	mock := NewMockEnterprise(true)
	registry := NewRegistry()
	registry.Register("mock_enterprise", mock)
	worker := NewWorker(dataStore, vault, registry, WorkerConfig{WorkerID: "database-integration-" + suffix,
		PollInterval: time.Hour, Lease: time.Minute, BatchSize: 10})
	worker.runOnce(ctx)

	bindings, err := dataStore.ListProviderAccountBindings(ctx, organizationID, userID, provider.ID, 10, 0)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	binding := bindings[0]
	if binding.Status != "ACTIVE" || binding.ExternalProjectID == "" || binding.CredentialID == nil ||
		binding.AllocatedAmount.String() != "3.000000000000" || binding.Currency != "USD" {
		t.Fatalf("binding=%+v", binding)
	}
	jobs, err := dataStore.ListProviderProvisioningJobs(ctx, organizationID, userID, "", 10, 0)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "SUCCEEDED" || jobs[0].Operation != "ALLOCATE_CREDIT" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	var credentialID, secretLast4, credentialOwner, credentialOrganizationID string
	var credentialProjectID *string
	var memberFilters []byte
	var encryptedSecret []byte
	if err = pool.QueryRow(ctx, `SELECT id,encrypted_secret,secret_last4,credential_owner,organization_id,project_id,member_filters
		FROM provider_credentials WHERE id=$1`, *binding.CredentialID).Scan(&credentialID, &encryptedSecret, &secretLast4,
		&credentialOwner, &credentialOrganizationID, &credentialProjectID, &memberFilters); err != nil {
		t.Fatal(err)
	}
	plainSecret, err := vault.Decrypt(encryptedSecret, credentialID)
	if err != nil || !strings.HasPrefix(plainSecret, "mock-secret-") || secretcrypto.Last4(plainSecret) != secretLast4 ||
		credentialOwner != domain.CredentialOwnerPlatform || credentialOrganizationID != organizationID || credentialProjectID != nil ||
		!strings.Contains(string(memberFilters), userID) {
		t.Fatalf("credential owner=%s organization=%s project=%v filters=%s last4=%s decrypted=%v err=%v",
			credentialOwner, credentialOrganizationID, credentialProjectID, memberFilters, secretLast4, plainSecret != "", err)
	}
	var allocations int
	var allocatedAmount, allocationCurrency string
	if err = pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(amount),0)::text,COALESCE(max(currency),'')
		FROM provider_credit_allocation WHERE binding_id=$1`, binding.ID).Scan(&allocations, &allocatedAmount, &allocationCurrency); err != nil {
		t.Fatal(err)
	}
	if allocations != 1 || allocatedAmount != "3.000000000000" || allocationCurrency != "USD" {
		t.Fatalf("allocations=%d amount=%s currency=%s", allocations, allocatedAmount, allocationCurrency)
	}
	upstreamBalance, upstreamCurrency, err := mock.Balance(binding.ID)
	if err != nil || upstreamBalance != "3.000000000000" || upstreamCurrency != "USD" {
		t.Fatalf("upstream balance=%s currency=%s err=%v", upstreamBalance, upstreamCurrency, err)
	}
	response, err := mock.TestFreeModel(binding.ID, userID, "database closed loop")
	if err != nil || response != "mock-free: database closed loop" {
		t.Fatalf("response=%q err=%v", response, err)
	}

	worker.runOnce(ctx)
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM provider_credit_allocation WHERE binding_id=$1`, binding.ID).Scan(&allocations); err != nil {
		t.Fatal(err)
	}
	afterReplay, _, err := mock.Balance(binding.ID)
	if err != nil || allocations != 1 || afterReplay != upstreamBalance {
		t.Fatalf("replay allocations=%d before=%s after=%s err=%v", allocations, upstreamBalance, afterReplay, err)
	}
}
