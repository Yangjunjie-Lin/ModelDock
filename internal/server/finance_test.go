package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/domain"
)

func TestFinanceRoutesAreRegisteredInExpectedRealm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAdminFinanceRoutes(router.Group("/api/admin"), Dependencies{})
	registerConsoleFinanceRoutes(router.Group("/api/console"), Dependencies{})

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range []string{
		"GET /api/console/organizations/:organizationID/finance/balance",
		"GET /api/console/organizations/:organizationID/finance/usage",
		"GET /api/console/organizations/:organizationID/finance/monthly-statements",
		"GET /api/console/organizations/:organizationID/refund-applications",
		"POST /api/console/organizations/:organizationID/refund-applications",
		"GET /api/console/organizations/:organizationID/invoice-applications",
		"POST /api/console/organizations/:organizationID/invoice-applications",
		"GET /api/console/organizations/:organizationID/finance/export",
		"GET /api/admin/finance/payment-orders",
		"GET /api/admin/finance/anomalous-orders",
		"GET /api/admin/finance/ledger-entries",
		"GET /api/admin/finance/refund-applications",
		"POST /api/admin/finance/refund-applications/:applicationID/decision",
		"GET /api/admin/finance/invoice-applications",
		"POST /api/admin/finance/invoice-applications/:applicationID/decision",
		"GET /api/admin/finance/invoice-applications/export",
		"GET /api/admin/finance/reports",
		"GET /api/admin/finance/accounting-export",
		"POST /api/admin/finance/provider-statements",
		"GET /api/admin/finance/reconciliation-runs",
		"POST /api/admin/finance/reconciliation-runs",
		"GET /api/admin/finance/reconciliation-cases",
		"POST /api/admin/finance/reconciliation-cases/:caseID/resolve",
		"GET /api/admin/finance/reconciliation/runs",
		"POST /api/admin/finance/reconciliation/runs",
		"GET /api/admin/finance/reconciliation/cases",
		"POST /api/admin/finance/reconciliation/cases/:caseID/resolve",
	} {
		if _, ok := routes[route]; !ok {
			t.Errorf("finance API contract has no registered route: %s", route)
		}
	}

	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/v1") {
			t.Fatalf("finance control-plane route leaked into /v1: %s %s", route.Method, route.Path)
		}
		if strings.Contains(route.Path, "provider-statements") && !strings.HasPrefix(route.Path, "/api/admin/") {
			t.Fatalf("Provider statement import leaked outside administrator realm: %s", route.Path)
		}
		if strings.Contains(route.Path, "reconciliation-cases") && !strings.HasPrefix(route.Path, "/api/admin/") {
			t.Fatalf("reconciliation correction leaked outside administrator realm: %s", route.Path)
		}
	}
}

func TestFinanceDecisionRejectsInvalidInputBeforeStoreAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(claimsKey, &auth.Claims{Role: "ADMIN", RegisteredClaims: jwt.RegisteredClaims{Subject: "00000000-0000-4000-8000-000000000001"}})
		c.Next()
	})
	registerAdminFinanceRoutes(router.Group("/api/admin"), Dependencies{})

	tests := []struct {
		name string
		path string
		body string
	}{
		{"invalid application id", "/api/admin/finance/refund-applications/not-a-uuid/decision", `{"decision":"APPROVE","reason":"valid evidence","idempotency_key":"key"}`},
		{"missing reason", "/api/admin/finance/invoice-applications/00000000-0000-4000-8000-000000000001/decision", `{"decision":"APPROVE","reason":"","idempotency_key":"key"}`},
		{"reversal without journal", "/api/admin/finance/reconciliation-cases/00000000-0000-4000-8000-000000000001/resolve", `{"action":"REVERSE_JOURNAL","reason":"valid evidence","idempotency_key":"key"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestFinanceDecimalValidationUsesNumericScale(t *testing.T) {
	for _, value := range []string{"0.01", "1", "999999999999999999.999999999999"} {
		if !validPositiveFinanceDecimal(value) {
			t.Errorf("expected valid positive NUMERIC(30,12): %s", value)
		}
	}
	for _, value := range []string{"0", "-1", "+1", "1e3", "NaN", "1.1234567890123", "1000000000000000000", "9999999999999999999.999999999999"} {
		if validPositiveFinanceDecimal(value) {
			t.Errorf("expected invalid positive NUMERIC(30,12): %s", value)
		}
	}
}

func TestOrganizationFinanceRoleOrderSeparatesViewerAndMember(t *testing.T) {
	if organizationRoleRank("VIEWER") >= organizationRoleRank("MEMBER") || organizationRoleRank("MEMBER") >= organizationRoleRank("ADMIN") || organizationRoleRank("ADMIN") >= organizationRoleRank("OWNER") {
		t.Fatal("finance role order would allow a read-only role to mutate financial applications")
	}
}

func TestFinanceCSVNeutralizesEveryCell(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	financeCSV(context, "../../unsafe.csv", []string{"label", "amount"}, func(write func([]string) error) error {
		return write([]string{"=HYPERLINK(\"https://example.invalid\")", "1.25"})
	})
	if !strings.Contains(recorder.Body.String(), `'=`) {
		t.Fatalf("CSV formula was not neutralized: %q", recorder.Body.String())
	}
	if disposition := recorder.Header().Get("Content-Disposition"); strings.Contains(disposition, "/") || strings.Contains(disposition, `\`) {
		t.Fatalf("unsafe filename in Content-Disposition: %q", disposition)
	}
}

func TestInvoiceExportRequiresAuthenticatedActorBeforeStoreAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(claimsKey, &auth.Claims{Role: "ADMIN"})
		c.Next()
	})
	registerAdminFinanceRoutes(router.Group("/api/admin"), Dependencies{})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/finance/invoice-applications/export", nil)
	request.Header.Set("Idempotency-Key", "authenticated-actor-contract")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestConsoleFinanceSanitizesProviderCommercialTerms(t *testing.T) {
	usage := sanitizeConsoleFinanceUsage([]domain.FinanceUsageDetail{{ProviderCost: "1.25", GrossMargin: "2.75", ProviderCurrency: "USD"}})
	if usage[0].ProviderCost != "" || usage[0].GrossMargin != "" || usage[0].ProviderCurrency != "" {
		t.Fatalf("Provider commercial terms remained in console usage: %#v", usage[0])
	}
	statements := sanitizeConsoleMonthlyStatements([]domain.MonthlyStatement{{ProviderCost: "1.25", GrossMargin: "2.75"}})
	if statements[0].ProviderCost != "" || statements[0].GrossMargin != "" {
		t.Fatalf("Provider commercial terms remained in console statement: %#v", statements[0])
	}
	payload, err := json.Marshal(gin.H{"usage": usage[0], "statement": statements[0]})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"provider_cost", "gross_margin", "provider_currency"} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("console JSON exposed commercial field %q: %s", forbidden, payload)
		}
	}
}
