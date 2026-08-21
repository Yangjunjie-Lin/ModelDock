package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/config"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/store"
)

func TestPublicCommercialRoutesAndConfigContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPublicCommercialRoutes(router, Dependencies{Config: config.Config{
		RegistrationMode: "PUBLIC", PaymentAllowedRegions: []string{"CN", "SG"},
		PublicSupportEmail: "support@example.invalid", PublicEnterpriseEmail: "enterprise@example.invalid",
	}})
	wanted := map[string]bool{
		"GET /api/public/config":            false,
		"GET /api/public/catalog/providers": false,
		"GET /api/public/catalog/models":    false,
		"GET /api/public/pricing":           false,
		"GET /api/public/status":            false,
		"POST /api/public/funnel/events":    false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
		if len(route.Path) >= 3 && route.Path[:3] == "/v1" {
			t.Fatalf("public commercial route changed /v1: %s", key)
		}
	}
	for route, found := range wanted {
		if !found {
			t.Errorf("route not registered: %s", route)
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/public/config", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("config status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		SupportedRegions    []string `json:"supported_regions"`
		SupportedCurrencies []string `json:"supported_currencies"`
		APIKeyPrefixes      []string `json:"api_key_prefixes"`
		SupportEmail        string   `json:"support_email"`
		EnterpriseEmail     string   `json:"enterprise_email"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.SupportedRegions) != 2 || body.SupportedRegions[0] != "CN" || body.SupportedRegions[1] != "SG" {
		t.Fatalf("supported regions=%v", body.SupportedRegions)
	}
	if body.SupportedCurrencies == nil || len(body.SupportedCurrencies) != 0 {
		t.Fatalf("unconfigured currencies must be an explicit empty array: %#v", body.SupportedCurrencies)
	}
	if len(body.APIKeyPrefixes) != 2 || body.APIKeyPrefixes[0] != "rdk_live_" {
		t.Fatalf("API key compatibility prefixes=%v", body.APIKeyPrefixes)
	}
	if body.SupportEmail != "support@example.invalid" || body.EnterpriseEmail != "enterprise@example.invalid" {
		t.Fatalf("public contact configuration support=%q enterprise=%q", body.SupportEmail, body.EnterpriseEmail)
	}
}

func TestCommercialAnonymousHashDoesNotRetainIdentifier(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 32)
	anonymousID := "browser-session-identifier-0001"
	first := commercialAnonymousHash(secret, anonymousID)
	second := commercialAnonymousHash(secret, anonymousID)
	if len(first) != sha256Size || !bytes.Equal(first, second) {
		t.Fatalf("unexpected HMAC output length=%d", len(first))
	}
	if bytes.Contains(first, []byte(anonymousID)) {
		t.Fatal("anonymous identifier appeared in its stored digest")
	}
	if bytes.Equal(first, commercialAnonymousHash(secret, anonymousID+"-different")) {
		t.Fatal("different anonymous identifiers produced the same digest")
	}
}

func TestFunnelStageConversionUsesNullForZeroDenominator(t *testing.T) {
	stages := funnelStagesWithConversion(map[string]int64{
		store.FunnelHomepageVisited: 10,
		store.FunnelRegistered:      5,
		store.FunnelEmailVerified:   0,
		store.FunnelAPIKeyCreated:   1,
	})
	if got, ok := stages[1]["conversion_from_previous"].(*float64); !ok || got == nil || *got != 0.5 {
		t.Fatalf("registration conversion=%#v", stages[1]["conversion_from_previous"])
	}
	if stages[3]["conversion_from_previous"] != (*float64)(nil) {
		t.Fatalf("zero-denominator conversion must be null: %#v", stages[3]["conversion_from_previous"])
	}
}

func TestPaymentFeeValidation(t *testing.T) {
	for _, valid := range []struct {
		kind   string
		fixed  string
		rateBP int
	}{{"NONE", "0", 0}, {"FIXED", "1.25", 0}, {"PERCENT", "0", 25}, {"FIXED_PLUS_PERCENT", "1", 25}} {
		if !validPaymentFee(valid.kind, valid.fixed, valid.rateBP) {
			t.Errorf("expected valid fee: %+v", valid)
		}
	}
	if validPaymentFee("NONE", "1", 0) || validPaymentFee("PERCENT", "0", 0) {
		t.Fatal("invalid fee combination was accepted")
	}
}

func TestPublicPaymentRegionRequiresResolvedApprovedChannel(t *testing.T) {
	registry := payment.NewRegistry()
	registry.Register(payment.NewSandbox(payment.SandboxConfig{
		Enabled: true, Secret: bytes.Repeat([]byte{0x42}, 32), AllowedRegions: []string{"CN"},
	}))
	approved := []store.PublicPaymentFee{{
		FeeCategory: "PAYMENT_CHANNEL", PaymentProvider: "sandbox", LegalReviewStatus: "APPROVED",
	}}
	if !publicPaymentRegionSupported(registry, []string{"CN"}, "CN", approved) {
		t.Fatal("resolved approved payment channel was rejected")
	}
	if publicPaymentRegionSupported(registry, []string{"CN"}, "US", approved) {
		t.Fatal("unconfigured payment region was accepted")
	}
	pending := append([]store.PublicPaymentFee(nil), approved...)
	pending[0].LegalReviewStatus = "PENDING"
	if publicPaymentRegionSupported(registry, []string{"CN"}, "CN", pending) {
		t.Fatal("pending fee evidence authorized payment")
	}
	if publicPaymentRegionSupported(nil, []string{"CN"}, "CN", approved) {
		t.Fatal("missing payment registry authorized payment")
	}
}

const sha256Size = 32
