package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestBYOKServiceFeeSnapshotIntegration(t *testing.T) {
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
	providerID, modelID, pricingVersionID := fundingFixture(t, ctx, s)
	policy, err := s.CreateBYOKServiceFeePolicy(ctx, domain.BYOKServiceFeePolicy{ProviderID: &providerID, FixedFee: "0.01",
		InputTokenFee: "0.2", CachedInputTokenFee: "0.1", OutputTokenFee: "0.4", Currency: "USD", Unit: 1_000_000,
		EffectiveAt: time.Now().UTC().Add(-time.Minute)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, domain.LegacyOrganizationID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	operationID, requestID := id.UUID(), "byok-snapshot-"+id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,
		request_fingerprint,pricing_version_id,status,currency,maximum_amount,settled_amount,estimated_input_tokens,max_output_tokens,actual_input_tokens,actual_cached_input_tokens,
		actual_output_tokens,credential_owner,platform_service_fee,byok_service_fee_policy_id,byok_fixed_fee,byok_input_token_fee,
		byok_cached_input_token_fee,byok_output_token_fee,byok_fee_unit)
		VALUES($1,$2,$3,$4,$5,$5,$5,$6,'SETTLED','USD',1,0.05,100000,50000,100000,20000,50000,'CUSTOMER',0.05,$7,0.01,0.2,0.1,0.4,1000000)`,
		operationID, walletID, domain.LegacyOrganizationID, domain.LegacyProjectID, requestID, pricingVersionID, policy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,organization_id,project_id,provider_id,requested_model,resolved_model,
		endpoint,status_code,input_tokens,cached_input_tokens,output_tokens,total_tokens,latency_ms,pricing_version_id,funding_operation_id)
		VALUES($1,$2,$3,$4,$5,$6,$6,'/v1/chat/completions',200,100000,20000,50000,150000,1,$7,$8)`, id.UUID(), requestID,
		domain.LegacyOrganizationID, domain.LegacyProjectID, providerID, "funding-"+modelID, pricingVersionID, operationID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	logEntry := domain.RequestLog{RequestID: requestID, OrganizationID: domain.LegacyOrganizationID, ProjectID: domain.LegacyProjectID,
		ProviderID: providerID, ResolvedModel: "funding-" + modelID, StatusCode: 200, InputTokens: 100000, CachedInputTokens: 20000,
		OutputTokens: 50000, PricingVersionID: pricingVersionID, FundingOperationID: operationID}
	if err = settlePricingUsage(ctx, tx, logEntry, domain.LegacyOrganizationID, domain.LegacyProjectID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var owner, providerCost, sale, finalAmount, fee, snapshotPolicy string
	if err = s.pool.QueryRow(ctx, `SELECT credential_owner,provider_cost_amount::text,customer_sale_amount::text,final_user_amount::text,
		platform_service_fee::text,byok_service_fee_policy_id::text FROM usage_price_snapshot WHERE request_id=$1`, requestID).
		Scan(&owner, &providerCost, &sale, &finalAmount, &fee, &snapshotPolicy); err != nil {
		t.Fatal(err)
	}
	if owner != "CUSTOMER" || providerCost != "0.000000000000" || sale != "0.050000000000" || finalAmount != "0.050000000000" ||
		fee != "0.050000000000" || snapshotPolicy != policy.ID {
		t.Fatalf("snapshot owner=%s provider=%s sale=%s final=%s fee=%s policy=%s", owner, providerCost, sale, finalAmount, fee, snapshotPolicy)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE byok_service_fee_policies SET fixed_fee=99 WHERE id=$1`, policy.ID); err == nil {
		t.Fatal("immutable BYOK fee terms accepted an update")
	}
}
