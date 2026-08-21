package server

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMarketplaceLaunchRoutesAndStatuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerMarketplaceLaunchAdminRoutes(router.Group("/api/admin"), Dependencies{})
	want := map[string]bool{
		"GET /api/admin/marketplace/launch-reviews":                     false,
		"POST /api/admin/marketplace/providers/:id/launch-reviews":      false,
		"POST /api/admin/marketplace/launch-reviews/:id/evaluate":       false,
		"PUT /api/admin/marketplace/launch-reviews/:id/gates/:gateCode": false,
		"POST /api/admin/marketplace/launch-reviews/:id/approve":        false,
		"POST /api/admin/marketplace/providers/:id/lifecycle":           false,
		"GET /api/admin/marketplace/providers/:id/lifecycle-events":     false,
		"GET /api/admin/marketplace/payout-readiness/:supplierID":       false,
		"PUT /api/admin/marketplace/payout-readiness/:supplierID":       false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("route %s was not registered", route)
		}
	}
	for _, status := range []string{"DRAFT", "REVIEW", "CANARY", "ACTIVE", "SUSPENDED", "REJECTED", "EXITED"} {
		if !validMarketplaceStatus(status) {
			t.Errorf("Marketplace status %s was rejected", status)
		}
	}
	if validMarketplaceStatus("SELF_APPROVED") {
		t.Fatal("unreviewed Marketplace status was accepted")
	}
}
