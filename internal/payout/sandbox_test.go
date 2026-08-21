package payout

import (
	"context"
	"errors"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestSandboxPayoutIsIdempotentAndRegionGated(t *testing.T) {
	adapter := NewSandbox(SandboxConfig{Enabled: true, Secret: []byte("test-only-sandbox-secret-32-bytes-long"), AllowedRegions: []string{"US"}})
	registry := NewRegistry()
	registry.Register(adapter)
	resolved, err := registry.Resolve("sandbox", "US")
	if err != nil {
		t.Fatal(err)
	}
	request := Request{SettlementID: "settlement", IdempotencyKey: "payout-once", Amount: domain.Decimal("1.25"), Currency: "USD", Region: "US", Destination: "synthetic-destination"}
	first, err := resolved.Send(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolved.Send(context.Background(), request)
	if err != nil || first.ProviderReference != second.ProviderReference {
		t.Fatalf("payout replay changed: first=%+v second=%+v err=%v", first, second, err)
	}
	if _, err = registry.Resolve("sandbox", "CN"); !errors.Is(err, ErrRegionNotAllowed) {
		t.Fatalf("unexpected region result: %v", err)
	}
}
