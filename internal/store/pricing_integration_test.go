package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestCommercialPricingIntegration(t *testing.T) {
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
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET commercial_status='COMMERCIAL_APPROVED',commercial_resale_status='APPROVED',
		allowed_customer_regions='["*"]'::jsonb,data_processing_regions='["*"]'::jsonb,emergency_kill_switch=false WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	modelID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,model_type,enabled) VALUES($1,$2,'pricing-integration-model','Pricing Integration','text',true)`, modelID, providerID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = s.CreateModelPrice(ctx, domain.ModelPrice{ModelID: modelID, EffectiveFrom: now.Add(-4 * time.Hour), InputPrice: domain.Decimal("1.000000000001"), CachedInputPrice: domain.Decimal("0.250000000001"), OutputPrice: domain.Decimal("2.000000000001"), Currency: "USD", Unit: 1_000_000, Source: "reference-cost-integration"}); err != nil {
		t.Fatal(err)
	}
	groupID, routeID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO credential_groups(id,provider_id,name) VALUES($1,$2,$3)`, groupID, providerID, "pricing-reference-group-"+groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_routes(id,alias,provider_id,upstream_model,credential_group_id,enabled)
		VALUES($1,$2,$3,'pricing-integration-model',$4,true)`, routeID, "pricing-reference-route-"+routeID, providerID, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO project_model_routes(id,project_id,model_route_id,alias,enabled)
		VALUES($1,$2,$3,$4,true)`, id.UUID(), domain.LegacyProjectID, routeID, "pricing-reference-project-route-"+routeID); err != nil {
		t.Fatal(err)
	}
	referenceCost, err := s.CalculateProjectReferenceCost(ctx, domain.LegacyProjectID, 1_000_000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	comparison, compareErr := referenceCost.Compare(domain.MustDecimal("1.000000000001"))
	if compareErr != nil || comparison != 0 {
		t.Fatalf("exact project reference cost=%s", referenceCost)
	}
	cost, err := s.CreateProviderCostPriceBook(ctx, domain.ProviderCostPriceBook{ProviderID: providerID, ModelID: modelID, InputTokenCost: "1", CachedInputTokenCost: "0.25", OutputTokenCost: "2", RequestFixedCost: "0", Currency: "USD", Unit: 1_000_000, EffectiveAt: now.Add(-3 * time.Hour), Source: "integration", ApprovalStatus: "APPROVED"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cost.InputTokenCost != "1.000000000000" && cost.InputTokenCost != "1" {
		t.Fatalf("cost=%s", cost.InputTokenCost)
	}
	defaultRetail := domain.CustomerRetailPriceBook{ProviderID: providerID, ModelID: modelID, InputTokenPrice: "2", CachedInputTokenPrice: "0.5", OutputTokenPrice: "4", RequestFixedPrice: "0", Currency: "USD", Unit: 1_000_000, EffectiveAt: now.Add(-2 * time.Hour), Source: "system-default"}
	if _, err = s.CreateCustomerRetailPriceBook(ctx, defaultRetail, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	quote, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: domain.LegacyOrganizationID, ProviderID: providerID, Model: "pricing-integration-model", InputTokens: 1_000_000, ExchangeRate: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if quote.ProviderCostAmount != "1" || quote.RetailAmount != "2" || quote.FinalAmount != "2" {
		t.Fatalf("initial quote=%+v", quote)
	}

	low := defaultRetail
	low.InputTokenPrice = "0.5"
	low.Source = "negative-margin"
	if _, err = s.CreateCustomerRetailPriceBook(ctx, low, nil, false, ""); !errors.Is(err, ErrNegativeMargin) {
		t.Fatalf("negative margin error=%v", err)
	}
	if _, err = s.CreateCustomerRetailPriceBook(ctx, low, nil, true, ""); !errors.Is(err, ErrForceOverrideConfirmation) {
		t.Fatalf("override confirmation error=%v", err)
	}
	low.Source = "confirmed-negative-margin"
	forced, err := s.CreateCustomerRetailPriceBook(ctx, low, nil, true, "CONFIRM_NEGATIVE_MARGIN_OVERRIDE")
	if err != nil {
		t.Fatal(err)
	}
	if forced.ApprovalStatus != "FORCED_APPROVED" {
		t.Fatalf("forced approval status=%s", forced.ApprovalStatus)
	}
	var forcedAuditCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='pricing.negative_margin_override' AND resource_id=$1`, forced.ID).Scan(&forcedAuditCount); err != nil {
		t.Fatal(err)
	}
	if forcedAuditCount != 1 {
		t.Fatalf("forced override audit count=%d", forcedAuditCount)
	}
	currencyMismatch := defaultRetail
	currencyMismatch.Currency = "EUR"
	currencyMismatch.Source = "currency-mismatch"
	if _, err = s.CreateCustomerRetailPriceBook(ctx, currencyMismatch, nil, false, ""); !errors.Is(err, ErrPricingCurrencyMismatch) {
		t.Fatalf("currency mismatch error=%v", err)
	}

	orgID := domain.LegacyOrganizationID
	customer := defaultRetail
	customer.OrganizationID = &orgID
	customer.InputTokenPrice = "3"
	customer.Source = "customer"
	if _, err = s.CreateCustomerRetailPriceBook(ctx, customer, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	qCustomer, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if qCustomer.RetailAmount != "3" {
		t.Fatalf("customer priority=%s", qCustomer.RetailAmount)
	}
	plan := domain.OrganizationPricePlan{OrganizationID: orgID, Name: "Subscription", PlanType: "SUBSCRIPTION", ProviderID: providerID, ModelID: modelID, InputTokenPrice: "4", CachedInputTokenPrice: "1", OutputTokenPrice: "8", RequestFixedPrice: "0", Currency: "USD", Unit: 1_000_000, EffectiveAt: now.Add(-time.Hour), Source: "subscription"}
	if _, err = s.CreateOrganizationPricePlan(ctx, plan, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	qPlan, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if qPlan.RetailAmount != "4" {
		t.Fatalf("subscription priority=%s", qPlan.RetailAmount)
	}
	plan.Name = "Override"
	plan.PlanType = "ORGANIZATION_OVERRIDE"
	plan.InputTokenPrice = "5"
	plan.CachedInputTokenPrice = "1.25"
	plan.OutputTokenPrice = "10"
	plan.Source = "override"
	if _, err = s.CreateOrganizationPricePlan(ctx, plan, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	qOverride, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if qOverride.RetailAmount != "5" {
		t.Fatalf("override priority=%s", qOverride.RetailAmount)
	}

	promotionCredit, err := s.CreatePromotionCredit(ctx, domain.PromotionCredit{OrganizationID: orgID, Currency: "USD", AmountGranted: "0.25", Source: "integration", IdempotencyKey: "promotion-integration"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	promotionRetry, err := s.CreatePromotionCredit(ctx, domain.PromotionCredit{OrganizationID: orgID, Currency: "USD", AmountGranted: "0.25", Source: "integration", IdempotencyKey: "promotion-integration"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if promotionRetry.ID != promotionCredit.ID {
		t.Fatal("promotion idempotency returned a different grant")
	}
	var promotionAuditCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='promotion_credit.granted' AND resource_id=$1`, promotionCredit.ID).Scan(&promotionAuditCount); err != nil {
		t.Fatal(err)
	}
	if promotionAuditCount != 1 {
		t.Fatalf("promotion grant audit count=%d", promotionAuditCount)
	}
	settlementQuote, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	userID, keyID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES($1,$2,'synthetic-hash','Pricing User','USER','ACTIVE',now())`, userID, "pricing-"+userID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status) VALUES($1,$2,$3,'DEVELOPER','ACTIVE')`, orgID, domain.LegacyProjectID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO api_keys(id,user_id,organization_id,project_id,name,environment,key_prefix,key_hash,status,allowed_models) VALUES($1,$2,$3,$4,'Pricing key','test',$5,$6,'ACTIVE','[]')`, keyID, userID, orgID, domain.LegacyProjectID, "rdk_test_pricing", []byte("01234567890123456789012345678901")); err != nil {
		t.Fatal(err)
	}
	requestID := "pricing-request-" + id.UUID()
	err = s.InsertScopedRequestLog(ctx, domain.RequestLog{RequestID: requestID, UserID: userID, APIKeyID: keyID, OrganizationID: orgID, ProjectID: domain.LegacyProjectID, ProviderID: providerID, RequestedModel: "pricing-integration-model", ResolvedModel: "pricing-integration-model", Endpoint: "/responses", StatusCode: 200, InputTokens: 1_000_000, TotalTokens: 1_000_000, EstimatedCost: domain.Decimal("999.99"), PricingVersionID: settlementQuote.PricingVersionID, PromotionAmount: settlementQuote.PromotionAmount, ExchangeRate: "1", TaxRate: "0", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	var providerCost, sale, promotion, finalAmount, margin, walletBalance, transactionAmount string
	if err = s.pool.QueryRow(ctx, `SELECT provider_cost_amount::text,customer_sale_amount::text,promotion_amount::text,final_user_amount::text,platform_gross_margin::text FROM usage_price_snapshot WHERE request_id=$1`, requestID).Scan(&providerCost, &sale, &promotion, &finalAmount, &margin); err != nil {
		t.Fatal(err)
	}
	if providerCost != "1.000000000000" || sale != "5.000000000000" || promotion != "0.250000000000" || finalAmount != "4.750000000000" || margin != "4.000000000000" {
		t.Fatalf("snapshot cost=%s sale=%s promotion=%s final=%s margin=%s", providerCost, sale, promotion, finalAmount, margin)
	}
	if err = s.pool.QueryRow(ctx, `SELECT available_balance::text FROM wallets WHERE organization_id=$1`, orgID).Scan(&walletBalance); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT amount::text FROM wallet_transactions WHERE idempotency_key=$1`, `usage:`+requestID).Scan(&transactionAmount); err != nil {
		t.Fatal(err)
	}
	if walletBalance != "-4.750000000000" || transactionAmount != "-4.750000000000" {
		t.Fatalf("wallet=%s transaction=%s", walletBalance, transactionAmount)
	}
	var usageChargeAuditCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='wallet.usage_charge' AND after_state->>'request_id'=$1`, requestID).Scan(&usageChargeAuditCount); err != nil {
		t.Fatal(err)
	}
	if usageChargeAuditCount != 1 {
		t.Fatalf("usage charge audit count=%d", usageChargeAuditCount)
	}
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, orgID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	const walletWorkers = 10
	walletIDs := make(chan string, walletWorkers)
	walletErrs := make(chan error, walletWorkers)
	var walletWG sync.WaitGroup
	for range walletWorkers {
		walletWG.Add(1)
		go func() {
			defer walletWG.Done()
			transaction, e := s.CreateWalletTransaction(ctx, domain.WalletTransaction{WalletID: walletID, TransactionType: "TOPUP", Amount: domain.Decimal("10"), IdempotencyKey: "concurrent-topup", Reference: "integration"})
			if e != nil {
				walletErrs <- e
				return
			}
			walletIDs <- transaction.ID
		}()
	}
	walletWG.Wait()
	close(walletErrs)
	close(walletIDs)
	for e := range walletErrs {
		t.Fatal(e)
	}
	oneID := ""
	for transactionID := range walletIDs {
		if oneID == "" {
			oneID = transactionID
		} else if oneID != transactionID {
			t.Fatal("idempotent topup returned mixed transaction ids")
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT available_balance::text FROM wallets WHERE id=$1`, walletID).Scan(&walletBalance); err != nil {
		t.Fatal(err)
	}
	if walletBalance != "5.250000000000" {
		t.Fatalf("concurrent idempotent balance=%s", walletBalance)
	}
	if _, err = s.CreateWalletTransaction(ctx, domain.WalletTransaction{WalletID: walletID, TransactionType: "TOPUP", Amount: domain.Decimal("11"), IdempotencyKey: "concurrent-topup", Reference: "integration"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("wallet idempotency conflict error=%v", err)
	}
	var topupAuditCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='wallet.transaction.committed' AND resource_id=$1 AND after_state->>'idempotency_key'='concurrent-topup'`, walletID).Scan(&topupAuditCount); err != nil {
		t.Fatal(err)
	}
	if topupAuditCount != 1 {
		t.Fatalf("idempotent topup audit count=%d", topupAuditCount)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE usage_price_snapshot SET final_user_amount=0 WHERE request_id=$1`, requestID); err == nil {
		t.Fatal("immutable usage snapshot accepted update")
	}

	const workers = 12
	versions := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, e := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
			if e != nil {
				errs <- e
				return
			}
			if q.RetailAmount != "5" {
				errs <- errors.New("mixed retail version")
				return
			}
			versions <- q.PricingVersionID
		}()
	}
	wg.Wait()
	close(errs)
	close(versions)
	for e := range errs {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for version := range versions {
		if seen[version] {
			t.Fatalf("duplicate version %s", version)
		}
		seen[version] = true
	}
	if len(seen) != workers {
		t.Fatalf("versions=%d", len(seen))
	}
	plan.Name = "New override"
	plan.InputTokenPrice = "6"
	plan.CachedInputTokenPrice = "1.5"
	plan.OutputTokenPrice = "12"
	plan.Source = "new-override"
	plan.EffectiveAt = time.Now().UTC().Add(-time.Second)
	if _, err = s.CreateOrganizationPricePlan(ctx, plan, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	changed, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if changed.RetailAmount != "6" {
		t.Fatalf("changed retail=%s", changed.RetailAmount)
	}
	var historicalFinal string
	if err = s.pool.QueryRow(ctx, `SELECT final_user_amount::text FROM usage_price_snapshot WHERE request_id=$1`, requestID).Scan(&historicalFinal); err != nil {
		t.Fatal(err)
	}
	if historicalFinal != "4.750000000000" {
		t.Fatalf("historical final changed to %s", historicalFinal)
	}

	updates := []struct {
		input, cached, output, source string
	}{
		{input: "7", cached: "1.75", output: "14", source: "concurrent-seven"},
		{input: "8", cached: "2", output: "16", source: "concurrent-eight"},
	}
	const concurrentQuotes = 24
	start := make(chan struct{})
	concurrentErrs := make(chan error, len(updates)+concurrentQuotes)
	concurrentVersions := make(chan string, concurrentQuotes)
	var concurrentWG sync.WaitGroup
	for _, update := range updates {
		update := update
		concurrentWG.Add(1)
		go func() {
			defer concurrentWG.Done()
			<-start
			candidate := plan
			candidate.Name = update.source
			candidate.InputTokenPrice = update.input
			candidate.CachedInputTokenPrice = update.cached
			candidate.OutputTokenPrice = update.output
			candidate.Source = update.source
			candidate.EffectiveAt = time.Now().UTC().Add(-time.Second)
			_, publishErr := s.CreateOrganizationPricePlan(ctx, candidate, nil, false, "")
			if publishErr != nil {
				concurrentErrs <- publishErr
			}
		}()
	}
	for range concurrentQuotes {
		concurrentWG.Add(1)
		go func() {
			defer concurrentWG.Done()
			<-start
			q, quoteErr := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: orgID, ProviderID: providerID, Model: modelID, InputTokens: 1_000_000})
			if quoteErr != nil {
				concurrentErrs <- quoteErr
				return
			}
			concurrentVersions <- q.PricingVersionID
		}()
	}
	close(start)
	concurrentWG.Wait()
	close(concurrentErrs)
	close(concurrentVersions)
	for concurrentErr := range concurrentErrs {
		t.Fatal(concurrentErr)
	}
	allowedVersions := map[string]bool{
		"6.000000000000|1.500000000000|12.000000000000": true,
		"7.000000000000|1.750000000000|14.000000000000": true,
		"8.000000000000|2.000000000000|16.000000000000": true,
	}
	for pricingVersionID := range concurrentVersions {
		var inputPrice, cachedPrice, outputPrice string
		if err = s.pool.QueryRow(ctx, `SELECT retail_input_token_price::text,retail_cached_input_token_price::text,retail_output_token_price::text FROM model_price_version WHERE id=$1`, pricingVersionID).Scan(&inputPrice, &cachedPrice, &outputPrice); err != nil {
			t.Fatal(err)
		}
		if !allowedVersions[inputPrice+"|"+cachedPrice+"|"+outputPrice] {
			t.Fatalf("mixed concurrent price version input=%s cached=%s output=%s", inputPrice, cachedPrice, outputPrice)
		}
	}
}
