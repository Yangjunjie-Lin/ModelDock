package store

import (
	"regexp"
	"testing"
)

func TestMarketplaceLaunchGateCatalogAndFingerprint(t *testing.T) {
	seen := map[string]bool{}
	for _, gateCode := range marketplaceGateCodes {
		if seen[gateCode] {
			t.Fatalf("duplicate gate code %s", gateCode)
		}
		seen[gateCode] = true
	}
	if len(seen) != 23 {
		t.Fatalf("gate count=%d", len(seen))
	}
	for gateCode := range marketplaceManualGates {
		if !seen[gateCode] {
			t.Fatalf("manual gate %s is not in the release catalog", gateCode)
		}
	}
	first := marketplaceFingerprint("listing-a", marketplacePolicyVersion, "content-a")
	if first != marketplaceFingerprint("listing-a", marketplacePolicyVersion, "content-a") || first == marketplaceFingerprint("listing-b", marketplacePolicyVersion, "content-a") || first == marketplaceFingerprint("listing-a", marketplacePolicyVersion, "content-b") {
		t.Fatal("Marketplace launch fingerprint is not stable and request-bound")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("invalid fingerprint %q", first)
	}
}
