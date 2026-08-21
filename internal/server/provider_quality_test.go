package server

import (
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestValidPriceVerificationRejectsBinaryAndUntrustedEvidence(t *testing.T) {
	valid := domain.ProviderPriceVerification{ProviderID: "provider", ModelID: "model", SourceType: "OFFICIAL_DOCUMENT",
		SourceReference: "https://pricing.example.invalid/model", EvidenceSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		ObservedInputTokenCost: "1.25", ObservedCachedInputTokenCost: "0.5", ObservedOutputTokenCost: "2.75",
		ObservedRequestFixedCost: "0", Currency: "USD", Unit: 1000000, ObservedAt: time.Now().UTC()}
	if !validPriceVerification(valid) {
		t.Fatal("valid exact-decimal verification was rejected")
	}
	invalid := valid
	invalid.ObservedInputTokenCost = "1e-3"
	if validPriceVerification(invalid) {
		t.Fatal("exponent-form price was accepted")
	}
	invalid = valid
	invalid.SourceReference = "http://127.0.0.1/pricing"
	if validPriceVerification(invalid) {
		t.Fatal("non-HTTPS local evidence reference was accepted")
	}
}
