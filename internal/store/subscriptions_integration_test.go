package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestSubscriptionsIntegration(t *testing.T) {
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

	userID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status)
		VALUES($1,$2,'integration-hash','Subscription Integration','USER','ACTIVE')`, userID, "subscription-"+userID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	organization, err := s.CreateOrganization(ctx, domain.Organization{Name: "Subscription Integration", Slug: "subscription-" + userID[:8], Status: "ACTIVE"}, userID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, domain.Project{OrganizationID: organization.ID, Name: "Integration", Slug: "integration", Status: "ACTIVE"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetProjectMember(ctx, domain.ProjectMembership{OrganizationID: organization.ID, ProjectID: project.ID, UserID: userID, Role: "ADMIN", Status: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}

	// Free allows exactly two active keys. Serializable organization locking
	// prevents concurrent callers from all observing the same stale count.
	const workers = 12
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	created := make(chan domain.APIKey, workers)
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			key, createErr := s.CreateProjectAPIKey(ctx, domain.APIKey{UserID: userID, OrganizationID: organization.ID,
				ProjectID: project.ID, Name: fmt.Sprintf("concurrent-%02d", index), Environment: "test",
				KeyPrefix: fmt.Sprintf("rdk_test_%02d", index), KeyHash: []byte(fmt.Sprintf("subscription-key-hash-%02d", index)),
				Status: "ACTIVE", RateLimitRPM: 60, RateLimitTPM: 100000, AllowedModels: []string{}})
			if createErr != nil {
				errorsFound <- createErr
				return
			}
			created <- key
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	close(created)
	successes, rejected := 0, 0
	for range created {
		successes++
	}
	for createErr := range errorsFound {
		if !errors.Is(createErr, ErrEntitlementExceeded) {
			t.Fatal(createErr)
		}
		rejected++
	}
	if successes != 2 || rejected != workers-2 {
		t.Fatalf("concurrent Free API keys successes=%d rejected=%d", successes, rejected)
	}

	developerVersionID := "11000000-0000-4000-8000-000000000002"
	subscription, invoice, err := s.ChangeSubscription(ctx, SubscriptionChangeRequest{OrganizationID: organization.ID,
		PlanVersionID: developerVersionID, Mode: "IMMEDIATE", IdempotencyKey: "developer-upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	if subscription.Status != "PAST_DUE" || invoice == nil || invoice.Status != "OPEN" || invoice.TotalAmount.String() != "29.000000000000" {
		t.Fatalf("initial paid subscription=%+v invoice=%+v", subscription, invoice)
	}
	effective, err := s.EffectiveEntitlements(ctx, organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if effective.PlanSlug != "free" {
		t.Fatalf("unpaid plan granted entitlements: %+v", effective)
	}

	var walletAvailable, walletReserved string
	var walletTransactions int
	if err = s.pool.QueryRow(ctx, `SELECT available_balance::text,reserved_balance::text FROM wallets WHERE organization_id=$1`, organization.ID).Scan(&walletAvailable, &walletReserved); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM wallet_transactions transaction JOIN wallets wallet ON wallet.id=transaction.wallet_id WHERE wallet.organization_id=$1`, organization.ID).Scan(&walletTransactions); err != nil {
		t.Fatal(err)
	}
	const paymentWorkers = 10
	paidResults := make(chan domain.SubscriptionInvoice, paymentWorkers)
	paymentErrors := make(chan error, paymentWorkers)
	wait = sync.WaitGroup{}
	for range paymentWorkers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			paidInvoice, paymentErr := s.PaySubscriptionInvoice(ctx, SubscriptionPaymentRequest{InvoiceID: invoice.ID,
				PaymentProvider: "manual_contract_test", ProviderPaymentReference: "synthetic-subscription-payment",
				IdempotencyKey: "developer-payment"})
			if paymentErr != nil {
				paymentErrors <- paymentErr
				return
			}
			paidResults <- paidInvoice
		}()
	}
	wait.Wait()
	close(paidResults)
	close(paymentErrors)
	for paymentErr := range paymentErrors {
		t.Fatal(paymentErr)
	}
	var paid domain.SubscriptionInvoice
	paidCount := 0
	for paidInvoice := range paidResults {
		paid = paidInvoice
		paidCount++
	}
	if paidCount != paymentWorkers {
		t.Fatalf("idempotent concurrent payments=%d want %d", paidCount, paymentWorkers)
	}
	if paid.Status != "PAID" || paid.LedgerJournalID == nil {
		t.Fatalf("paid invoice=%+v", paid)
	}
	var subscriptionPaymentJournals int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_journal WHERE subscription_invoice_id=$1`, invoice.ID).Scan(&subscriptionPaymentJournals); err != nil {
		t.Fatal(err)
	}
	if subscriptionPaymentJournals != 1 {
		t.Fatalf("concurrent payment journals=%d want 1", subscriptionPaymentJournals)
	}
	var availableAfter, reservedAfter string
	var transactionsAfter int
	if err = s.pool.QueryRow(ctx, `SELECT available_balance::text,reserved_balance::text FROM wallets WHERE organization_id=$1`, organization.ID).Scan(&availableAfter, &reservedAfter); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM wallet_transactions transaction JOIN wallets wallet ON wallet.id=transaction.wallet_id WHERE wallet.organization_id=$1`, organization.ID).Scan(&transactionsAfter); err != nil {
		t.Fatal(err)
	}
	if availableAfter != walletAvailable || reservedAfter != walletReserved || transactionsAfter != walletTransactions {
		t.Fatalf("subscription payment changed Token wallet before=%s/%s/%d after=%s/%s/%d", walletAvailable, walletReserved, walletTransactions, availableAfter, reservedAfter, transactionsAfter)
	}
	var journalWallet *string
	var debitAccount, creditAccount string
	if err = s.pool.QueryRow(ctx, `SELECT journal.wallet_id,
		max(account.account_key) FILTER (WHERE entry.entry_side='DEBIT'),
		max(account.account_key) FILTER (WHERE entry.entry_side='CREDIT')
		FROM ledger_journal journal JOIN ledger_journal_entry entry ON entry.journal_id=journal.id
		JOIN ledger_account account ON account.id=entry.account_id WHERE journal.id=$1 GROUP BY journal.id,journal.wallet_id`, *paid.LedgerJournalID).
		Scan(&journalWallet, &debitAccount, &creditAccount); err != nil {
		t.Fatal(err)
	}
	if journalWallet != nil || debitAccount != "system:subscription-cash:USD" || creditAccount != "system:subscription-revenue:USD" {
		t.Fatalf("subscription journal mixed with wallet: wallet=%v debit=%s credit=%s", journalWallet, debitAccount, creditAccount)
	}
	effective, err = s.EffectiveEntitlements(ctx, organization.ID)
	if err != nil || effective.PlanSlug != "developer" || effective.APIKeyCount != 10 || effective.TokenBillingMode != "METERED_SEPARATE" {
		t.Fatalf("paid entitlements=%+v err=%v", effective, err)
	}

	// A new frozen template version does not rewrite the invoice's version or
	// immutable commercial snapshot.
	allEntitlements := subscriptionTestEntitlements()
	newVersion, err := s.CreatePlanVersion(ctx, domain.PlanVersion{SubscriptionPlanID: "10000000-0000-4000-8000-000000000002",
		BillingInterval: "MONTHLY", SubscriptionFee: domain.Decimal("39"), Currency: "USD", TrialDays: 7,
		GracePeriodDays: 7, TokenBillingMode: "METERED_SEPARATE", EffectiveAt: time.Now().UTC(), Entitlements: allEntitlements})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FreezePlanVersion(ctx, newVersion.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE plan_version SET subscription_fee=40 WHERE id=$1`, newVersion.ID); err == nil {
		t.Fatal("frozen plan version was mutable")
	}
	var invoiceVersion, snapshotFee string
	if err = s.pool.QueryRow(ctx, `SELECT plan_version_id,plan_snapshot->>'subscription_fee' FROM subscription_invoice WHERE id=$1`, invoice.ID).Scan(&invoiceVersion, &snapshotFee); err != nil {
		t.Fatal(err)
	}
	if invoiceVersion != developerVersionID || snapshotFee != "29.000000000000" {
		t.Fatalf("historical invoice changed version=%s snapshot fee=%s", invoiceVersion, snapshotFee)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE subscription_invoice SET total_amount=30 WHERE id=$1`, invoice.ID); err == nil {
		t.Fatal("historical subscription invoice was mutable")
	}

	// Developer allows more keys. Immediate cancellation preserves them and all
	// accounting evidence while activating a separate Free fallback.
	for index := 2; index < 5; index++ {
		if _, err = s.CreateProjectAPIKey(ctx, domain.APIKey{UserID: userID, OrganizationID: organization.ID, ProjectID: project.ID,
			Name: fmt.Sprintf("retained-%d", index), Environment: "test", KeyPrefix: fmt.Sprintf("rdk_test_retained_%d", index),
			KeyHash: []byte(fmt.Sprintf("retained-hash-%d", index)), Status: "ACTIVE", RateLimitRPM: 60, RateLimitTPM: 100000}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CancelSubscription(ctx, SubscriptionCancelRequest{OrganizationID: organization.ID, Mode: "IMMEDIATE", IdempotencyKey: "cancel-developer"}); err != nil {
		t.Fatal(err)
	}
	var retainedKeys, retainedInvoices, retainedJournals int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE organization_id=$1`, organization.ID).Scan(&retainedKeys); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM subscription_invoice WHERE organization_id=$1`, organization.ID).Scan(&retainedInvoices); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_journal WHERE subscription_invoice_id=$1`, invoice.ID).Scan(&retainedJournals); err != nil {
		t.Fatal(err)
	}
	if retainedKeys != 5 || retainedInvoices != 1 || retainedJournals != 1 {
		t.Fatalf("cancellation deleted data keys=%d invoices=%d journals=%d", retainedKeys, retainedInvoices, retainedJournals)
	}
	if _, err = s.CreateProjectAPIKey(ctx, domain.APIKey{UserID: userID, OrganizationID: organization.ID, ProjectID: project.ID,
		Name: "bypass", Environment: "test", KeyPrefix: "rdk_test_bypass", KeyHash: []byte("bypass-hash"),
		Status: "ACTIVE", RateLimitRPM: 60, RateLimitTPM: 100000}); !errors.Is(err, ErrEntitlementExceeded) {
		t.Fatalf("direct store call bypassed downgraded key entitlement: %v", err)
	}
	var retainedKeyID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM api_keys WHERE organization_id=$1 ORDER BY created_at,id LIMIT 1`, organization.ID).Scan(&retainedKeyID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAPIKeyStatus(ctx, retainedKeyID, "DISABLED"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAPIKeyStatus(ctx, retainedKeyID, "ACTIVE"); !errors.Is(err, ErrEntitlementExceeded) {
		t.Fatalf("status reactivation bypassed downgraded key entitlement: %v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE api_keys SET status='ACTIVE' WHERE id=$1`, retainedKeyID); err != nil {
		t.Fatal(err)
	}

	// Exercise renewal failure, grace, expiration, and non-destructive Free
	// fallback on a second paid lifecycle.
	second, secondInvoice, err := s.ChangeSubscription(ctx, SubscriptionChangeRequest{OrganizationID: organization.ID,
		PlanVersionID: developerVersionID, Mode: "IMMEDIATE", IdempotencyKey: "developer-second"})
	if err != nil || secondInvoice == nil {
		t.Fatalf("second subscription=%+v invoice=%+v err=%v", second, secondInvoice, err)
	}
	if _, err = s.PaySubscriptionInvoice(ctx, SubscriptionPaymentRequest{InvoiceID: secondInvoice.ID, PaymentProvider: "manual_contract_test",
		ProviderPaymentReference: "synthetic-subscription-payment-2", IdempotencyKey: "developer-payment-2"}); err != nil {
		t.Fatal(err)
	}
	pastEnd := time.Now().UTC().Add(-time.Minute)
	if _, err = s.pool.Exec(ctx, `UPDATE organization_subscription SET current_period_start=$2::timestamptz-interval '1 month',current_period_end=$2::timestamptz WHERE id=$1`, second.ID, pastEnd); err != nil {
		t.Fatal(err)
	}
	if count, processErr := s.ProcessSubscriptionLifecycle(ctx, time.Now().UTC(), 100); processErr != nil || count != 1 {
		t.Fatalf("renewal lifecycle count=%d err=%v", count, processErr)
	}
	current, err := s.CurrentSubscription(ctx, organization.ID)
	if err != nil || current.Status != "PAST_DUE" {
		t.Fatalf("renewal status=%+v err=%v", current, err)
	}
	if count, processErr := s.ProcessSubscriptionLifecycle(ctx, time.Now().UTC(), 100); processErr != nil || count != 1 {
		t.Fatalf("grace lifecycle count=%d err=%v", count, processErr)
	}
	current, err = s.CurrentSubscription(ctx, organization.ID)
	if err != nil || current.Status != "GRACE_PERIOD" {
		t.Fatalf("grace status=%+v err=%v", current, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE organization_subscription SET grace_period_end=now()-interval '1 second' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if count, processErr := s.ProcessSubscriptionLifecycle(ctx, time.Now().UTC(), 100); processErr != nil || count != 1 {
		t.Fatalf("expiry lifecycle count=%d err=%v", count, processErr)
	}
	current, err = s.CurrentSubscription(ctx, organization.ID)
	if err != nil || current.PlanSlug != "free" || current.Status != "ACTIVE" {
		t.Fatalf("expiry fallback=%+v err=%v", current, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE organization_id=$1`, organization.ID).Scan(&retainedKeys); err != nil || retainedKeys != 5 {
		t.Fatalf("expiry deleted API keys count=%d err=%v", retainedKeys, err)
	}
}

func subscriptionTestEntitlements() []domain.PlanEntitlement {
	integer := func(key string, value int64) domain.PlanEntitlement {
		return domain.PlanEntitlement{EntitlementKey: key, ValueType: "INTEGER", IntegerValue: &value}
	}
	boolean := func(key string, value bool) domain.PlanEntitlement {
		return domain.PlanEntitlement{EntitlementKey: key, ValueType: "BOOLEAN", BooleanValue: &value}
	}
	stringValue := func(key, value string) domain.PlanEntitlement {
		return domain.PlanEntitlement{EntitlementKey: key, ValueType: "STRING", StringValue: &value}
	}
	return []domain.PlanEntitlement{
		integer("api_key_count", 20), integer("organization_member_count", 10), integer("concurrency", 10),
		integer("requests_per_minute", 1000), integer("log_retention_days", 60), boolean("advanced_routing", true),
		boolean("cost_analysis", true), boolean("custom_budget", true), integer("webhook_count", 10),
		boolean("priority_support", false), stringValue("sla_level", "STANDARD"),
	}
}
