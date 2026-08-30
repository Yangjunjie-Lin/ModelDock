package server

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProviderBindingSelfServicePolicy(t *testing.T) {
	credentialID := "credential-id"
	emptyCredentialID := " "
	tests := []struct {
		name       string
		automatic  bool
		mode       string
		credential *string
		allowed    bool
	}{
		{name: "automatic official binding", automatic: true, mode: "OFFICIAL_ENTERPRISE", allowed: true},
		{name: "organization BYOK", mode: "BYOK", credential: &credentialID, allowed: true},
		{name: "BYOK requires credential", mode: "BYOK", allowed: false},
		{name: "BYOK rejects blank credential", mode: "BYOK", credential: &emptyCredentialID, allowed: false},
		{name: "manual binding is admin reviewed", mode: "MANUAL", credential: &credentialID, allowed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := providerBindingSelfServiceAllowed(test.automatic, test.mode, test.credential); got != test.allowed {
				t.Fatalf("allowed=%v want=%v", got, test.allowed)
			}
		})
	}
}

func TestProviderAccountRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAdminProviderAccountRoutes(router.Group("/api/admin"), Dependencies{})
	registerConsoleProviderAccountRoutes(router.Group("/api/console"), Dependencies{})
	want := map[string]bool{
		"GET /api/admin/provider-provisioning/capabilities":                         false,
		"GET /api/admin/provider-accounts":                                          false,
		"POST /api/admin/provider-accounts":                                         false,
		"GET /api/admin/provider-provisioning/jobs":                                 false,
		"POST /api/admin/provider-provisioning/jobs/:jobID/retry":                   false,
		"GET /api/console/provider-provisioning/capabilities":                       false,
		"GET /api/console/organizations/:organizationID/provider-accounts":          false,
		"POST /api/console/organizations/:organizationID/provider-accounts":         false,
		"GET /api/console/organizations/:organizationID/provider-provisioning/jobs": false,
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
