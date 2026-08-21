package migrations

import _ "embed"

// Migration is one immutable, ordered database migration.  Version values are
// persisted by the store in schema_migrations only after the SQL transaction
// commits successfully.
type Migration struct {
	Version int64
	Name    string
	SQL     string
}

//go:embed 0001_core.sql
var coreSchema string

//go:embed 0002_v2.sql
var v2Schema string

//go:embed 0003_v2_statuses.sql
var v2StatusesSchema string

//go:embed 0004_project_route_soft_delete.sql
var projectRouteSoftDeleteSchema string

//go:embed 0005_openai_compatible_providers.sql
var openAICompatibleProvidersSchema string

//go:embed 0006_modeldock.sql
var modelDockSchema string

//go:embed 0007_accounts.sql
var accountsSchema string

//go:embed 0008_pricing.sql
var pricingSchema string

//go:embed 0009_funding_ledger.sql
var fundingLedgerSchema string

//go:embed 0010_payment_orders.sql
var paymentOrdersSchema string

//go:embed 0011_subscriptions.sql
var subscriptionsSchema string

//go:embed 0012_financial_close.sql
var financialCloseSchema string

//go:embed 0013_financial_close_hardening.sql
var financialCloseHardeningSchema string

//go:embed 0014_provider_commercial_governance.sql
var providerCommercialGovernanceSchema string

//go:embed 0015_provider_pricing_hardening.sql
var providerPricingHardeningSchema string

//go:embed 0016_public_operations_governance.sql
var publicOperationsGovernanceSchema string

//go:embed 0017_observability_support.sql
var observabilitySupportSchema string

//go:embed 0018_beta_runtime_hardening.sql
var betaRuntimeHardeningSchema string

//go:embed 0019_public_commercial_onboarding.sql
var publicCommercialOnboardingSchema string

//go:embed 0020_supplier_onboarding.sql
var supplierOnboardingSchema string

//go:embed 0021_provider_quality.sql
var providerQualitySchema string

//go:embed 0022_supplier_settlement.sql
var supplierSettlementSchema string

//go:embed 0023_marketplace_launch_acceptance.sql
var marketplaceLaunchAcceptanceSchema string

//go:embed 0024_exact_money_and_release_evidence.sql
var exactMoneyAndReleaseEvidenceSchema string

// All is ordered oldest to newest.  Never edit a released migration; append a
// new entry instead so checksum validation can detect binary/schema drift.
var All = []Migration{
	{Version: 1, Name: "core", SQL: coreSchema},
	{Version: 2, Name: "v2", SQL: v2Schema},
	{Version: 3, Name: "v2_statuses", SQL: v2StatusesSchema},
	{Version: 4, Name: "project_route_soft_delete", SQL: projectRouteSoftDeleteSchema},
	{Version: 5, Name: "openai_compatible_providers", SQL: openAICompatibleProvidersSchema},
	{Version: 6, Name: "modeldock", SQL: modelDockSchema},
	{Version: 7, Name: "accounts", SQL: accountsSchema},
	{Version: 8, Name: "pricing", SQL: pricingSchema},
	{Version: 9, Name: "funding_ledger", SQL: fundingLedgerSchema},
	{Version: 10, Name: "payment_orders", SQL: paymentOrdersSchema},
	{Version: 11, Name: "subscriptions", SQL: subscriptionsSchema},
	{Version: 12, Name: "financial_close", SQL: financialCloseSchema},
	{Version: 13, Name: "financial_close_hardening", SQL: financialCloseHardeningSchema},
	{Version: 14, Name: "provider_commercial_governance", SQL: providerCommercialGovernanceSchema},
	{Version: 15, Name: "provider_pricing_hardening", SQL: providerPricingHardeningSchema},
	{Version: 16, Name: "public_operations_governance", SQL: publicOperationsGovernanceSchema},
	{Version: 17, Name: "observability_support", SQL: observabilitySupportSchema},
	{Version: 18, Name: "beta_runtime_hardening", SQL: betaRuntimeHardeningSchema},
	{Version: 19, Name: "public_commercial_onboarding", SQL: publicCommercialOnboardingSchema},
	{Version: 20, Name: "supplier_onboarding", SQL: supplierOnboardingSchema},
	{Version: 21, Name: "provider_quality", SQL: providerQualitySchema},
	{Version: 22, Name: "supplier_settlement", SQL: supplierSettlementSchema},
	{Version: 23, Name: "marketplace_launch_acceptance", SQL: marketplaceLaunchAcceptanceSchema},
	{Version: 24, Name: "exact_money_and_release_evidence", SQL: exactMoneyAndReleaseEvidenceSchema},
}
