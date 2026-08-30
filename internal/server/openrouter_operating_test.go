package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
)

func TestOperatingRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPublicOperatingRoutes(router, Dependencies{})
	registerSCIMRoutes(router, Dependencies{})
	registerAdminOperatingRoutes(router.Group("/api/admin"), Dependencies{})
	registerConsoleOperatingRoutes(router.Group("/api/console"), Dependencies{})
	want := map[string]bool{
		"GET /api/public/provider-quality":                                         false,
		"GET /api/public/catalog/provider-capabilities":                            false,
		"GET /api/console/projects/:projectID/workspace-settings":                  false,
		"PUT /api/console/projects/:projectID/workspace-settings":                  false,
		"GET /api/console/organizations/:organizationID/enterprise-identity":       false,
		"PUT /api/console/organizations/:organizationID/enterprise-identity":       false,
		"POST /api/console/organizations/:organizationID/enterprise-identity/test": false,
		"GET /api/admin/provider-capabilities":                                     false,
		"POST /api/admin/provider-capabilities":                                    false,
		"GET /scim/v2/:organizationID/Users":                                       false,
		"POST /scim/v2/:organizationID/Users":                                      false,
		"PATCH /scim/v2/:organizationID/Users/:resourceID":                         false,
		"GET /scim/v2/:organizationID/Groups":                                      false,
		"PATCH /scim/v2/:organizationID/Groups/:resourceID":                        false,
		"GET /scim/v2/:organizationID/ServiceProviderConfig":                       false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("missing route %s", route)
		}
	}
}

func TestWorkspacePolicyMergeCannotRelaxPrivacyOrCapacity(t *testing.T) {
	workspace := domain.RequestProviderPolicy{Only: []string{"openai", "anthropic"}, ProcessingRegions: []string{"gb", "de"},
		DataCollection: "deny", ZDR: true, UseSharedCapacity: boolPtr(false), AllowFallbacks: boolPtr(false),
		MaxInputPrice: decimalPtr("5")}
	request := domain.RequestProviderPolicy{Only: []string{"anthropic", "google"}, ProcessingRegions: []string{"de", "us"},
		DataCollection: "allow", UseSharedCapacity: boolPtr(true), AllowFallbacks: boolPtr(true), MaxInputPrice: decimalPtr("9")}
	merged := mergeProviderPolicies(workspace, request, []string{"de", "fr"}, map[string]any{"data_collection": "deny"})
	if strings.Join(merged.Only, ",") != "anthropic" || strings.Join(merged.ProcessingRegions, ",") != "de" {
		t.Fatalf("restrictive lists were relaxed: %+v", merged)
	}
	if merged.DataCollection != "deny" || !merged.ZDR || merged.UseSharedCapacity == nil || *merged.UseSharedCapacity ||
		merged.AllowFallbacks == nil || *merged.AllowFallbacks || merged.MaxInputPrice == nil || merged.MaxInputPrice.String() != "5" {
		t.Fatalf("privacy, capacity, or price ceiling was relaxed: %+v", merged)
	}
}

func TestRequestProviderPolicyUsesExactNumericParsing(t *testing.T) {
	policy, err := requestProviderPolicy(map[string]any{"provider": map[string]any{
		"max_price":                map[string]any{"prompt": json.Number("0.000000000001"), "completion": "15.25"},
		"preferred_min_throughput": json.Number("12.5"),
		"preferred_max_latency":    json.Number("1.2349"),
	}})
	if err != nil {
		t.Fatalf("requestProviderPolicy failed: %v", err)
	}
	if policy.MaxInputPrice == nil || policy.MaxInputPrice.String() != "0.000000000001" ||
		policy.MaxOutputPrice == nil || policy.MaxOutputPrice.String() != "15.25" ||
		policy.PreferredMinThroughput == nil || policy.PreferredMinThroughput.String() != "12.5" ||
		policy.PreferredMaxLatencyMS == nil || *policy.PreferredMaxLatencyMS != 1234 {
		t.Fatalf("provider policy lost decimal precision: %+v", policy)
	}
}

func TestOIDCIssuerValidationRejectsPrivateTargets(t *testing.T) {
	for _, value := range []string{"http://id.example.com", "https://localhost", "https://127.0.0.1", "https://10.2.3.4", "https://id.example.com?redirect=x"} {
		if err := validateOIDCIssuer(value); err == nil {
			t.Errorf("validateOIDCIssuer(%q) accepted an unsafe target", value)
		}
	}
	if err := validateOIDCIssuer("https://id.example.com/tenant"); err != nil {
		t.Fatalf("public HTTPS issuer rejected: %v", err)
	}
}

func TestSCIMFilterAndPatchHelpers(t *testing.T) {
	attribute, value, err := parseSCIMFilter(`userName eq "person@example.com"`)
	if err != nil || attribute != "username" || value != "person@example.com" {
		t.Fatalf("filter parse failed: attribute=%q value=%q err=%v", attribute, value, err)
	}
	active := true
	user := scimUser{UserName: "person@example.com", Active: &active}
	request := scimPatchRequest{Schemas: []string{scimPatchSchema}, Operations: []scimPatchOperation{
		{Op: "replace", Path: "active", Value: json.RawMessage(`false`)},
		{Op: "replace", Path: "displayName", Value: json.RawMessage(`"New Name"`)},
	}}
	if err = applySCIMUserPatch(&user, request); err != nil || user.Active == nil || *user.Active || user.DisplayName != "New Name" {
		t.Fatalf("User patch failed: %+v err=%v", user, err)
	}
	group := scimGroup{Members: []scimMember{{Value: "one"}, {Value: "two"}}}
	groupPatch := scimPatchRequest{Operations: []scimPatchOperation{{Op: "remove", Path: `members[value eq "one"]`}}}
	if err = applySCIMGroupPatch(&group, groupPatch); err != nil || len(group.Members) != 1 || group.Members[0].Value != "two" {
		t.Fatalf("Group patch failed: %+v err=%v", group, err)
	}
}

func TestSCIMJSONContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	scimJSON(context, http.StatusOK, gin.H{"ok": true})
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/scim+json") {
		t.Fatalf("unexpected SCIM content type %q", recorder.Header().Get("Content-Type"))
	}
}

func decimalPtr(value string) *domain.Decimal {
	decimal := domain.Decimal(value)
	return &decimal
}
