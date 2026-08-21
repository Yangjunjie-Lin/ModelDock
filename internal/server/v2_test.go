package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/scheduler"
)

func TestV2FrontendContractRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAdmin(router.Group("/api/admin"), Dependencies{})
	registerConsole(router.Group("/api/console"), Dependencies{})

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	expected := []string{
		"GET /api/admin/cockpit/accounts",
		"POST /api/admin/cockpit/refresh",
		"POST /api/admin/cockpit/test",
		"GET /api/admin/credentials/:id/tags",
		"PUT /api/admin/credentials/:id/tags",
		"GET /api/admin/organizations",
		"POST /api/admin/organizations",
		"GET /api/admin/organizations/:organizationID",
		"PUT /api/admin/organizations/:organizationID",
		"DELETE /api/admin/organizations/:organizationID",
		"GET /api/admin/organizations/:organizationID/members",
		"PUT /api/admin/organizations/:organizationID/members/:userID",
		"DELETE /api/admin/organizations/:organizationID/members/:userID",
		"GET /api/admin/organizations/:organizationID/projects",
		"POST /api/admin/organizations/:organizationID/projects",
		"GET /api/admin/projects/:projectID",
		"PUT /api/admin/projects/:projectID",
		"DELETE /api/admin/projects/:projectID",
		"GET /api/admin/projects/:projectID/members",
		"PUT /api/admin/projects/:projectID/members/:userID",
		"DELETE /api/admin/projects/:projectID/members/:userID",
		"GET /api/admin/projects/:projectID/routes",
		"POST /api/admin/projects/:projectID/routes",
		"DELETE /api/admin/projects/:projectID/routes/:routeID",
		"GET /api/admin/projects/:projectID/budgets",
		"POST /api/admin/projects/:projectID/budgets",
		"DELETE /api/admin/projects/:projectID/budgets/:policyID",
		"GET /api/admin/projects/:projectID/budget-usage",
		"GET /api/admin/projects/:projectID/budget-events",
		"GET /api/admin/projects/:projectID/webhooks",
		"POST /api/admin/projects/:projectID/webhooks",
		"DELETE /api/admin/projects/:projectID/webhooks/:webhookID",
		"POST /api/admin/projects/:projectID/webhooks/:webhookID/test",
		"GET /api/admin/projects/:projectID/webhook-deliveries",
		"POST /api/admin/projects/:projectID/webhook-deliveries/:deliveryID/retry",
		"GET /api/admin/projects/:projectID/usage/export",
		"GET /api/admin/model-routes",
		"GET /api/admin/api-keys",
		"POST /api/admin/api-keys",
		"POST /api/admin/api-keys/:keyID/rotate",
		"POST /api/admin/api-keys/:keyID/finalize",
		"POST /api/admin/alerts/:alertID/acknowledge",
		"GET /api/admin/wallets/:id/funding-operations",
		"GET /api/admin/wallets/:id/journals",
		"POST /api/admin/funding-operations/:id/late-usage",
		"POST /api/admin/funding-operations/:id/reversals",
		"GET /api/console/organizations",
		"GET /api/console/organizations/:organizationID/projects",
		"GET /api/console/overview",
		"GET /api/console/models",
		"GET /api/console/usage",
		"GET /api/console/request-logs",
		"GET /api/console/api-keys",
		"POST /api/console/api-keys",
		"DELETE /api/console/api-keys/:id",
		"POST /api/console/api-keys/:keyID/rotate",
		"POST /api/console/api-keys/:keyID/finalize",
		"GET /api/console/projects/:projectID/usage/export",
	}
	for _, contract := range expected {
		if _, ok := routes[contract]; !ok {
			t.Errorf("frontend API contract has no registered backend route: %s", contract)
		}
	}
	for _, forbidden := range []string{
		"GET /api/console/credentials/:id/tags",
		"PUT /api/console/credentials/:id/tags",
	} {
		if _, ok := routes[forbidden]; ok {
			t.Errorf("administrator-only credential tag route leaked into Console: %s", forbidden)
		}
	}
}

func TestCredentialTagRoutesRequireAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, err := auth.NewManager(bytes.Repeat([]byte("v"), 32), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := manager.Issue("admin", "admin@example.test", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	userToken, _, err := manager.Issue("user", "user@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(controlAuth(Dependencies{Auth: manager}))
	admin := api.Group("/admin")
	admin.Use(requireAdmin())
	registerCredentialTagsV2(admin, Dependencies{})

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method+" rejects missing authentication", func(t *testing.T) {
			request := httptest.NewRequest(method, "/api/admin/credentials/credential-1/tags", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
		t.Run(method+" rejects non-administrator", func(t *testing.T) {
			request := httptest.NewRequest(method, "/api/admin/credentials/credential-1/tags", nil)
			request.Header.Set("Authorization", "Bearer "+userToken)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	request := httptest.NewRequest(http.MethodPut, "/api/admin/credentials/credential-1/tags", bytes.NewBufferString(`{}`))
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("administrator reached wrong handler: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMergeCredentialConstraintsIsStableAndFailClosed(t *testing.T) {
	got := mergeCredentialConstraints(
		scheduler.CredentialConstraints{RequiredTags: []string{" Region:APAC ", "tier:prod"}, ExcludedTags: []string{"maintenance"}},
		scheduler.CredentialConstraints{RequiredTags: []string{"tier:prod", "maintenance"}, ExcludedTags: []string{" deprecated ", "maintenance"}},
	)
	if want := []string{"maintenance", "region:apac", "tier:prod"}; !reflect.DeepEqual(got.RequiredTags, want) {
		t.Fatalf("required tags = %#v, want %#v", got.RequiredTags, want)
	}
	if want := []string{"deprecated", "maintenance"}; !reflect.DeepEqual(got.ExcludedTags, want) {
		t.Fatalf("excluded tags = %#v, want %#v", got.ExcludedTags, want)
	}
}

func TestBudgetThresholdAndHardLimitBoundaries(t *testing.T) {
	tokenLimit := int64(100)
	costLimit := domain.Decimal("10")
	policy := domain.ProjectBudgetPolicy{TokenLimit: &tokenLimit, CostLimit: &costLimit, AlertThreshold: domain.Decimal("0.8")}
	if budgetThresholdReached(policy, domain.ProjectBudgetUsage{TotalTokens: 79, Cost: domain.Decimal("7.99")}) {
		t.Fatal("threshold triggered before either dimension reached 80 percent")
	}
	if !budgetThresholdReached(policy, domain.ProjectBudgetUsage{TotalTokens: 80}) {
		t.Fatal("token threshold did not trigger at the boundary")
	}
	if !budgetLimitReached(policy, domain.ProjectBudgetUsage{Cost: domain.Decimal("10")}) {
		t.Fatal("hard cost limit did not trigger at the boundary")
	}
}

func TestQueryTimeRangeTreatsEndDateAsInclusiveAndBoundsRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("GET", "/?from=2026-08-01&to=2026-08-10", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	from, to, err := queryTimeRange(context, 30*24*time.Hour, 366*24*time.Hour)
	if err != nil {
		t.Fatalf("queryTimeRange: %v", err)
	}
	if got := from.Format("2006-01-02"); got != "2026-08-01" {
		t.Fatalf("from = %s", got)
	}
	if got := to.Format("2006-01-02"); got != "2026-08-11" {
		t.Fatalf("exclusive to = %s, want next day", got)
	}
}

func TestTenantSlugValidation(t *testing.T) {
	for _, value := range []string{"production", "team-42", "a"} {
		if !validTenantSlug(value) {
			t.Fatalf("valid slug %q rejected", value)
		}
	}
	for _, value := range []string{"", "Upper", "-leading", "trailing-", "contains space"} {
		if validTenantSlug(value) {
			t.Fatalf("invalid slug %q accepted", value)
		}
	}
}

func TestMergeOrganizationUpdatePreservesOmittedCommercialPolicy(t *testing.T) {
	current := domain.Organization{
		ID: "organization-id", Name: "Current", Slug: "current", Status: "ACTIVE", BillingRegion: "US",
		Metadata: map[string]any{"owner": "customer"}, AllowedProviderIDs: []string{"provider-allowed"},
		ProhibitedProviderIDs: []string{"provider-blocked"}, RequiredDataRegions: []string{"US"},
		MinimumGrossMargin: "0.125000000000",
	}

	updated := mergeOrganizationUpdate(current, domain.Organization{BillingRegion: "CN"})
	if updated.ID != current.ID || updated.Name != current.Name || updated.Slug != current.Slug ||
		updated.Status != current.Status || updated.BillingRegion != "CN" ||
		updated.MinimumGrossMargin != current.MinimumGrossMargin ||
		!reflect.DeepEqual(updated.Metadata, current.Metadata) ||
		!reflect.DeepEqual(updated.AllowedProviderIDs, current.AllowedProviderIDs) ||
		!reflect.DeepEqual(updated.ProhibitedProviderIDs, current.ProhibitedProviderIDs) ||
		!reflect.DeepEqual(updated.RequiredDataRegions, current.RequiredDataRegions) {
		t.Fatalf("omitted organization policy was not preserved: %+v", updated)
	}

	cleared := mergeOrganizationUpdate(current, domain.Organization{
		BillingRegion: "CN", AllowedProviderIDs: []string{}, ProhibitedProviderIDs: []string{}, RequiredDataRegions: []string{},
	})
	if cleared.AllowedProviderIDs == nil || cleared.ProhibitedProviderIDs == nil || cleared.RequiredDataRegions == nil ||
		len(cleared.AllowedProviderIDs) != 0 || len(cleared.ProhibitedProviderIDs) != 0 || len(cleared.RequiredDataRegions) != 0 {
		t.Fatalf("explicit empty policy arrays were not retained: %+v", cleared)
	}
}

func TestConsoleProjectModelExposesOnlyPublicCatalogCorrelation(t *testing.T) {
	fallbackGroupID := "fallback-group-id"
	projected := consoleProjectModel(domain.ProjectModelRoute{
		Alias: "project-chat", ProviderID: "provider-id", UpstreamModel: "provider-model",
		CredentialGroupID: "credential-group-id", FallbackGroupID: &fallbackGroupID,
		ProviderBaseURL: "https://provider.example.invalid/v1", Enabled: true,
	})
	if projected["alias"] != "project-chat" || projected["provider_id"] != "provider-id" || projected["upstream_model"] != "provider-model" {
		t.Fatalf("catalog correlation fields are incomplete: %#v", projected)
	}
	for _, secretRoutingField := range []string{"provider_base_url", "credential_group_id", "fallback_group_id", "routing_config", "fallback_config"} {
		if _, exposed := projected[secretRoutingField]; exposed {
			t.Fatalf("console model exposed administrator-only %s: %#v", secretRoutingField, projected)
		}
	}
}
