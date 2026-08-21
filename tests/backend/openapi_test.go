package backend_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIDocumentParsesAndIncludesPricing(t *testing.T) {
	raw, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI    string         `yaml:"openapi"`
		Paths      map[string]any `yaml:"paths"`
		Components struct {
			Schemas   map[string]any `yaml:"schemas"`
			Responses map[string]any `yaml:"responses"`
		} `yaml:"components"`
	}
	if err = yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var root any
	if err = yaml.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	assertLocalOpenAPIRefsResolve(t, root, root)
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%q", document.OpenAPI)
	}
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/api/pricing/quote", "/api/admin/pricing/quote", "/api/admin/pricing/provider-cost-price-books",
		"/api/public/config", "/api/public/catalog/models", "/api/public/catalog/providers", "/api/public/pricing", "/api/public/status", "/api/public/funnel/events",
		"/api/console/onboarding", "/api/console/models", "/api/admin/funnel/summary", "/api/admin/public/commercial-terms", "/api/admin/public/payment-fees",
		"/api/admin/pricing/provider-cost-changes/manual", "/api/admin/pricing/provider-cost-changes/fetch", "/api/admin/pricing/provider-cost-changes/import-csv",
		"/api/admin/pricing/byok-service-fee-policies",
		"/api/admin/wallets/{walletID}/funding-operations", "/api/admin/wallets/{walletID}/journals", "/api/admin/funding-operations/{operationID}/late-usage", "/api/admin/funding-operations/{operationID}/reversals",
		"/api/payments/webhooks/{provider}", "/api/console/organizations/{organizationID}/recharge-orders", "/api/admin/recharge-orders/{orderID}/refunds",
		"/api/admin/subscription-plans", "/api/admin/organizations/{organizationID}/subscription",
		"/api/console/organizations/{organizationID}/subscription/change", "/api/console/organizations/{organizationID}/subscription-invoices",
		"/api/console/organizations/{organizationID}/finance/balance", "/api/console/organizations/{organizationID}/finance/usage",
		"/api/console/organizations/{organizationID}/finance/monthly-statements", "/api/console/organizations/{organizationID}/refund-applications",
		"/api/console/organizations/{organizationID}/invoice-applications", "/api/console/organizations/{organizationID}/finance/export",
		"/api/admin/finance/payment-orders", "/api/admin/finance/anomalous-orders", "/api/admin/finance/ledger-entries",
		"/api/admin/finance/refund-applications", "/api/admin/finance/refund-applications/{applicationID}/decision",
		"/api/admin/finance/refund-applications/{applicationID}/process",
		"/api/admin/finance/invoice-applications", "/api/admin/finance/invoice-applications/{applicationID}/decision",
		"/api/admin/finance/invoice-applications/export",
		"/api/admin/finance/reports", "/api/admin/finance/accounting-export", "/api/admin/finance/provider-statements",
		"/api/admin/finance/reconciliation/runs", "/api/admin/finance/reconciliation/cases",
		"/api/admin/finance/reconciliation/cases/{caseID}/resolve",
		"/api/admin/risk/events", "/api/admin/risk/{subjectType}/{subjectID}", "/api/admin/api-keys/leak-check", "/api/admin/api-keys/{keyID}/freeze", "/api/admin/content-policies", "/api/admin/manual-reviews", "/api/admin/manual-reviews/{id}",
		"/api/admin/reports", "/api/admin/lifecycle/jobs", "/api/console/reports", "/api/console/privacy/{subjectType}/{subjectID}",
		"/api/console/privacy/{subjectType}/{subjectID}/jobs", "/api/console/privacy/jobs/{jobID}/export",
		"/status", "/api/status", "/api/admin/observability", "/api/admin/observability/requests/{requestID}",
		"/api/admin/status/summary", "/api/admin/status/events", "/api/admin/status/events/{id}/resolve", "/api/console/status",
		"/api/admin/support/tickets", "/api/admin/support/tickets/{id}", "/api/admin/support/tickets/{id}/messages",
		"/api/console/support/tickets", "/api/console/support/tickets/{id}", "/api/console/support/tickets/{id}/messages",
		"/api/console/suppliers", "/api/console/suppliers/{supplierID}", "/api/console/suppliers/{supplierID}/submit", "/api/console/suppliers/{supplierID}/exit",
		"/api/console/suppliers/{supplierID}/endpoints", "/api/console/suppliers/{supplierID}/endpoints/{endpointID}/verify", "/api/console/suppliers/{supplierID}/credentials",
		"/api/console/suppliers/{supplierID}/residency", "/api/console/suppliers/{supplierID}/questionnaires", "/api/console/suppliers/{supplierID}/models", "/api/console/suppliers/{supplierID}/prices",
		"/api/admin/suppliers", "/api/admin/suppliers/{supplierID}", "/api/admin/suppliers/{supplierID}/compliance", "/api/admin/suppliers/{supplierID}/review", "/api/admin/suppliers/{supplierID}/status", "/api/admin/supplier-evidence/{type}/{id}",
		"/api/admin/provider-quality", "/api/admin/provider-quality/sla-events", "/api/admin/provider-quality/price-verifications",
		"/api/admin/providers/{id}/quality-policy", "/api/admin/providers/{id}/quality/evaluate", "/api/admin/providers/{id}/quality/circuit-reset", "/api/admin/providers/{id}/supplier-link",
		"/api/admin/marketplace/launch-reviews", "/api/admin/marketplace/launch-reviews/{id}",
		"/api/admin/marketplace/providers/{id}/launch-reviews", "/api/admin/marketplace/launch-reviews/{id}/evaluate",
		"/api/admin/marketplace/launch-reviews/{id}/gates/{gateCode}", "/api/admin/marketplace/launch-reviews/{id}/approve",
		"/api/admin/marketplace/providers/{id}/lifecycle", "/api/admin/marketplace/providers/{id}/lifecycle-events",
		"/api/admin/marketplace/payout-readiness/{supplierID}"} {
		if _, ok := document.Paths[path]; !ok {
			t.Fatalf("OpenAPI path %s is missing", path)
		}
	}
	marketplaceReviewProperties := openAPIMapping(t, openAPIMapping(t, document.Components.Schemas["MarketplaceLaunchReview"], "MarketplaceLaunchReview")["properties"], "MarketplaceLaunchReview.properties")
	for _, field := range []string{"listing_fingerprint_sha256", "gates", "passed_gate_count", "gate_count"} {
		if _, ok := marketplaceReviewProperties[field]; !ok {
			t.Fatalf("MarketplaceLaunchReview.%s is missing", field)
		}
	}

	publicConfigProperties := openAPIMapping(t, openAPIMapping(t, document.Components.Schemas["PublicCommercialConfig"], "PublicCommercialConfig")["properties"], "PublicCommercialConfig.properties")
	for _, field := range []string{"support_email", "enterprise_email"} {
		if _, ok := publicConfigProperties[field]; !ok {
			t.Fatalf("PublicCommercialConfig.%s is missing", field)
		}
	}
	publicTokenPriceProperties := openAPIMapping(t, openAPIMapping(t, document.Components.Schemas["PublicTokenPrice"], "PublicTokenPrice")["properties"], "PublicTokenPrice.properties")
	for _, field := range []string{"provider_name", "provider_slug", "model_name", "provider_model_id", "availability"} {
		if _, ok := publicTokenPriceProperties[field]; !ok {
			t.Fatalf("PublicTokenPrice.%s is missing", field)
		}
	}
	publicPricingProperties := openAPIMapping(t, openAPIMapping(t, document.Components.Schemas["PublicPricing"], "PublicPricing")["properties"], "PublicPricing.properties")
	if _, ok := publicPricingProperties["payment_region_supported"]; !ok {
		t.Fatal("PublicPricing.payment_region_supported is missing")
	}
	consoleModelProperties := openAPIMapping(t, openAPIMapping(t, document.Components.Schemas["ConsoleModel"], "ConsoleModel")["properties"], "ConsoleModel.properties")
	for _, field := range []string{"id_alias", "provider_id", "upstream_model"} {
		if _, ok := consoleModelProperties[field]; !ok {
			t.Fatalf("ConsoleModel.%s is missing", field)
		}
	}
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/embeddings"} {
		operation := openAPIMapping(t, openAPIMapping(t, document.Paths[path], path)["post"], path+".post")
		responses := openAPIMapping(t, operation["responses"], path+".post.responses")
		for _, status := range []string{"402", "409"} {
			if _, ok := responses[status]; !ok {
				t.Fatalf("%s response %s is missing", path, status)
			}
		}
	}
	idempotencyResponse := openAPIMapping(t, document.Components.Responses["GatewayIdempotencyConflict"], "GatewayIdempotencyConflict")
	headers := openAPIMapping(t, idempotencyResponse["headers"], "GatewayIdempotencyConflict.headers")
	if _, ok := headers["X-RelayDock-Original-Request-Id"]; !ok {
		t.Fatal("GatewayIdempotencyConflict does not document X-RelayDock-Original-Request-Id")
	}
}

func assertLocalOpenAPIRefsResolve(t *testing.T, root, value any) {
	t.Helper()
	switch current := value.(type) {
	case map[string]any:
		if rawRef, ok := current["$ref"].(string); ok && strings.HasPrefix(rawRef, "#/") {
			resolved := root
			for _, part := range strings.Split(strings.TrimPrefix(rawRef, "#/"), "/") {
				part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
				mapping, mappingOK := resolved.(map[string]any)
				if !mappingOK {
					t.Fatalf("OpenAPI local ref %s traverses a non-object at %s", rawRef, part)
				}
				resolved, ok = mapping[part]
				if !ok {
					t.Fatalf("OpenAPI local ref %s is unresolved at %s", rawRef, part)
				}
			}
		}
		for _, child := range current {
			assertLocalOpenAPIRefsResolve(t, root, child)
		}
	case []any:
		for _, child := range current {
			assertLocalOpenAPIRefsResolve(t, root, child)
		}
	}
}

func openAPIMapping(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	mapping, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %T", label, value)
	}
	return mapping
}
