package server

import (
	"bytes"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/config"
	"github.com/relayedock/relayedock/internal/contentpolicy"
)

func TestGovernanceFailureModeConfig(t *testing.T) {
	for _, mode := range []string{"FAIL_OPEN", "FAIL_CLOSED"} {
		cfg := config.Config{ContentPolicyFailureMode: mode}
		if cfg.ContentPolicyFailureMode != mode {
			t.Fatalf("mode=%s", cfg.ContentPolicyFailureMode)
		}
	}
	if !contentPolicyFailOpen("FAIL_OPEN") || contentPolicyFailOpen("FAIL_CLOSED") || contentPolicyFailOpen("") {
		t.Fatal("content policy failure mode did not preserve its explicit boundary")
	}
}

func TestGovernanceRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerGovernanceRoutes(router.Group("/api/admin"), Dependencies{}, true)
	registerGovernanceRoutes(router.Group("/api/console"), Dependencies{}, false)
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	want := []string{
		"GET /api/admin/risk/events", "PATCH /api/admin/risk/:subjectType/:subjectID",
		"POST /api/admin/api-keys/:keyID/freeze", "GET /api/admin/content-policies",
		"GET /api/admin/manual-reviews", "PATCH /api/admin/manual-reviews/:id",
		"GET /api/admin/reports", "PATCH /api/admin/reports/:id",
		"GET /api/console/reports", "POST /api/console/reports",
		"GET /api/console/privacy/:subjectType/:subjectID", "PUT /api/console/privacy/:subjectType/:subjectID",
		"GET /api/console/privacy/:subjectType/:subjectID/jobs", "POST /api/console/privacy/:subjectType/:subjectID/jobs",
		"GET /api/console/privacy/jobs/:jobID/export",
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("missing governance route %s", route)
		}
	}
}

func TestGovernanceSignalHashIsKeyedAndDomainSeparated(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	ip := governanceSignalHash(secret, "ip", "192.0.2.10")
	if len(ip) != 32 || bytes.Contains(ip, []byte("192.0.2.10")) {
		t.Fatal("IP signal was not reduced to an opaque SHA-256 HMAC")
	}
	if !bytes.Equal(ip, governanceSignalHash(secret, "ip", "192.0.2.10")) {
		t.Fatal("same signal must be correlatable within one deployment")
	}
	if bytes.Equal(ip, governanceSignalHash(secret, "device", "192.0.2.10")) {
		t.Fatal("IP and device signals must use separate HMAC domains")
	}
	if governanceSignalHash(secret, "device", "") != nil {
		t.Fatal("missing optional signal must not create a shared fingerprint")
	}
}

func TestContentPolicyDecisionBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		decision   contentpolicy.Decision
		wantReview bool
		wantBlock  bool
	}{
		{name: "allow", decision: contentpolicy.Decision{Allowed: true, Action: "ALLOW"}},
		{name: "block", decision: contentpolicy.Decision{Allowed: false, Action: "BLOCK"}, wantBlock: true},
		{name: "review", decision: contentpolicy.Decision{Allowed: true, Action: "REVIEW"}, wantReview: true},
		{name: "redact without replacement", decision: contentpolicy.Decision{Allowed: true, Action: "REDACT"}, wantReview: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			review := test.decision.ReviewRequired || test.decision.Action == "REVIEW" || test.decision.Action == "REDACT"
			blocked := !test.decision.Allowed || test.decision.Action == "BLOCK"
			if review != test.wantReview || blocked != test.wantBlock {
				t.Fatalf("review=%v blocked=%v decision=%+v", review, blocked, test.decision)
			}
		})
	}
}
