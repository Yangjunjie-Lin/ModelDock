package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProviderPublicAvailabilityUsesCommercialAndRegionGates(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	provider := PublicProvider{
		Enabled: true, CommercialStatus: "COMMERCIAL_APPROVED", CommercialResaleStatus: "APPROVED",
		AllowedCustomerRegions: []string{"CN", "SG"}, ProhibitedRegions: []string{"US"},
	}
	if got := providerPublicAvailability(provider, "CN", now); !got.Available || got.ReasonCode != "AVAILABLE" {
		t.Fatalf("available provider result=%+v", got)
	}
	if got := providerPublicAvailability(provider, "US", now); got.Available || got.ReasonCode != "REGION_NOT_ALLOWED" {
		t.Fatalf("unlisted region result=%+v", got)
	}
	provider.AllowedCustomerRegions = []string{"*"}
	if got := providerPublicAvailability(provider, "US", now); got.Available || got.ReasonCode != "REGION_PROHIBITED" {
		t.Fatalf("prohibited region result=%+v", got)
	}
	provider.ProhibitedRegions = nil
	provider.EmergencyKillSwitch = true
	if got := providerPublicAvailability(provider, "CN", now); got.Available || got.ReasonCode != "EMERGENCY_KILL_SWITCH" {
		t.Fatalf("kill switch result=%+v", got)
	}
	provider.EmergencyKillSwitch = false
	provider.contractStartAt = timePtr(now.Add(time.Hour))
	if got := providerPublicAvailability(provider, "CN", now); got.Available || got.ReasonCode != "CONTRACT_NOT_STARTED" {
		t.Fatalf("future contract result=%+v", got)
	}
}

func TestPublicProviderComponentStatusDoesNotOverstateReadiness(t *testing.T) {
	tests := []struct {
		name            string
		enabled         bool
		killed          bool
		pricingDisabled bool
		commercial      string
		resale          string
		schedulable     int64
		wantStatus      string
	}{{"disabled", false, false, false, "COMMERCIAL_APPROVED", "APPROVED", 1, "MAJOR_OUTAGE"},
		{"contract pending", true, false, false, "CONTRACT_PENDING", "APPROVED", 1, "DEGRADED"},
		{"resale pending", true, false, false, "COMMERCIAL_APPROVED", "PENDING", 1, "DEGRADED"},
		{"pricing disabled", true, false, true, "COMMERCIAL_APPROVED", "APPROVED", 1, "DEGRADED"},
		{"no credential", true, false, false, "COMMERCIAL_APPROVED", "APPROVED", 0, "DEGRADED"},
		{"schedulable", true, false, false, "COMMERCIAL_APPROVED", "APPROVED", 1, "OPERATIONAL"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := publicProviderComponentStatus(test.enabled, test.killed, test.pricingDisabled, test.commercial, test.resale, test.schedulable)
			if status != test.wantStatus || strings.TrimSpace(message) == "" {
				t.Fatalf("status=%q message=%q want=%q", status, message, test.wantStatus)
			}
		})
	}
}

func TestApprovedPublicEvidenceFingerprintIgnoresServerReviewTimestamp(t *testing.T) {
	firstReview := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	secondReview := firstReview.Add(time.Minute)
	terms := PublishCommercialTermsRequest{
		Region: "CN", Currency: "CNY", TaxDisclosure: "Synthetic disclosure", RefundSummary: "Synthetic refund summary",
		RefundPolicyURL: "/legal/refunds", BonusCreditAmount: "0", BonusNonRefundable: true,
		EffectiveAt: firstReview.Add(-time.Hour), LegalReviewStatus: "APPROVED", ReviewedAt: &firstReview,
		IdempotencyKey: "terms-idempotency", CreatedBy: "reviewer-id",
	}
	first, err := publicCommercialTermsFingerprint(terms)
	if err != nil {
		t.Fatal(err)
	}
	terms.ReviewedAt = &secondReview
	second, err := publicCommercialTermsFingerprint(terms)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("server review timestamp changed terms fingerprint: %s != %s", first, second)
	}

	fee := PublishPaymentFeeRequest{
		FeeCategory: "PAYMENT_CHANNEL", PaymentProvider: "sandbox", Region: "CN", Currency: "CNY",
		FeeKind: "NONE", FixedAmount: "0", Description: "Synthetic fee disclosure",
		EffectiveAt: firstReview.Add(-time.Hour), LegalReviewStatus: "APPROVED", ReviewedAt: &firstReview,
		IdempotencyKey: "fee-idempotency", CreatedBy: "reviewer-id",
	}
	first, err = publicPaymentFeeFingerprint(fee)
	if err != nil {
		t.Fatal(err)
	}
	fee.ReviewedAt = &secondReview
	second, err = publicPaymentFeeFingerprint(fee)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("server review timestamp changed fee fingerprint: %s != %s", first, second)
	}
}

func TestPublicEvidenceFingerprintIsStableAndPayloadSensitive(t *testing.T) {
	request := PublishPaymentFeeRequest{
		FeeCategory: "PAYMENT_CHANNEL", PaymentProvider: "sandbox", Region: "CN", Currency: "CNY",
		FeeKind: "PERCENT", FixedAmount: "0", RateBPS: 25, IdempotencyKey: "fee-fingerprint",
	}
	first, err := publicEvidenceFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := publicEvidenceFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("fingerprint stability first=%q second=%q", first, second)
	}
	request.RateBPS++
	changed, err := publicEvidenceFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("changed fee payload reused its fingerprint")
	}
}

func TestPublicCatalogJSONExcludesProviderSecretsAndCosts(t *testing.T) {
	raw, err := json.Marshal(struct {
		Provider PublicProvider `json:"provider"`
		Model    PublicModel    `json:"model"`
	}{Provider: PublicProvider{ID: "provider", Name: "Provider"}, Model: PublicModel{ID: "model", ProviderID: "provider"}})
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	for _, forbidden := range []string{"base_url", "credential", "provider_cost", "cost_limit", "rate_limit"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("public catalog JSON exposed %q: %s", forbidden, payload)
		}
	}
}

func timePtr(value time.Time) *time.Time { return &value }
