package pricesync

import (
	"errors"
	"net"
	"testing"
)

func TestParseCSVPreservesExactDecimals(t *testing.T) {
	body := []byte("model_id,input_token_cost,cached_input_token_cost,output_token_cost,request_fixed_cost,currency,unit,effective_at\n11111111-1111-1111-1111-111111111111,0.000000000001,0,2.5,0,USD,1000000,2026-08-14T00:00:00Z\n")
	changes, err := ParseCSV(body, "22222222-2222-2222-2222-222222222222", "batch", "sha256:test")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].InputTokenCost != "0.000000000001" || changes[0].IdempotencyKey != "batch:001" {
		t.Fatalf("changes=%+v", changes)
	}
}

func TestIsPublicIPRejectsNonGlobalRanges(t *testing.T) {
	for _, raw := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "2001:db8::1", "fc00::1"} {
		if isPublicIP(net.ParseIP(raw)) {
			t.Fatalf("non-global address %s was accepted", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(raw)) {
			t.Fatalf("global address %s was rejected", raw)
		}
	}
}

func TestParseCSVRejectsFormulaAndExcessPrecision(t *testing.T) {
	for _, value := range []string{"=1+1", "0.0000000000001"} {
		body := []byte("model_id,input_token_cost,cached_input_token_cost,output_token_cost,request_fixed_cost,currency,unit,effective_at\n11111111-1111-1111-1111-111111111111," + value + ",0,2.5,0,USD,1000000,2026-08-14T00:00:00Z\n")
		if _, err := ParseCSV(body, "provider", "batch", "source"); err == nil {
			t.Fatalf("value %q was accepted", value)
		}
	}
}

func TestValidateSourceRejectsPrivateAndUnlistedHosts(t *testing.T) {
	if _, _, _, err := validateSource(t.Context(), "http://example.com/prices", []string{"example.com"}); !errors.Is(err, ErrUnsafeSource) {
		t.Fatalf("http error=%v", err)
	}
	if _, _, _, err := validateSource(t.Context(), "https://127.0.0.1/prices", []string{"127.0.0.1"}); !errors.Is(err, ErrUnsafeSource) {
		t.Fatalf("private error=%v", err)
	}
	if _, _, _, err := validateSource(t.Context(), "https://example.com/prices", []string{"prices.example.com"}); !errors.Is(err, ErrUnsafeSource) {
		t.Fatalf("allowlist error=%v", err)
	}
}
