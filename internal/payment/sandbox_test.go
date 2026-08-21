package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestSandboxVerifyWebhookSignatureAndTimestamp(t *testing.T) {
	secret := []byte("sandbox-test-secret-32-bytes-long!!")
	adapter := NewSandbox(SandboxConfig{Enabled: true, Secret: secret, AllowedRegions: []string{"CN"}, TimestampSkew: 5 * time.Minute})
	now := time.Unix(1_800_000_000, 0).UTC()
	body := []byte(`{"event_id":"evt_test","event_type":"payment.paid","platform_order_no":"RO_TEST","provider_order_no":"sbx_RO_TEST","status":"PAID","amount":"12.340000000000","currency":"USD"}`)
	timestamp := fmt.Sprint(now.Unix())
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	verified, err := adapter.VerifyWebhook(context.Background(), WebhookRequest{Body: body, Timestamp: timestamp,
		Signature: hex.EncodeToString(mac.Sum(nil)), EventID: "evt_test", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != "PAID" || verified.Amount.String() != "12.340000000000" || verified.ReplayKey != "evt_test" {
		t.Fatalf("verified=%+v", verified)
	}
	if _, err = adapter.VerifyWebhook(context.Background(), WebhookRequest{Body: body, Timestamp: timestamp,
		Signature: "00", EventID: "evt_test", Now: now}); err != ErrInvalidSignature {
		t.Fatalf("invalid signature err=%v", err)
	}
	if _, err = adapter.VerifyWebhook(context.Background(), WebhookRequest{Body: body, Timestamp: timestamp,
		Signature: hex.EncodeToString(mac.Sum(nil)), EventID: "evt_test", Now: now.Add(6 * time.Minute)}); err != ErrTimestampInvalid {
		t.Fatalf("stale timestamp err=%v", err)
	}
}

func TestRegistryEnforcesSwitchContractAndRegion(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewSandbox(SandboxConfig{Enabled: true, Secret: []byte("sandbox-test-secret-32-bytes-long!!"), AllowedRegions: []string{"CN"}}))
	if _, err := registry.Resolve("sandbox", "CN"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve("sandbox", "US"); err != ErrRegionNotAllowed {
		t.Fatalf("region err=%v", err)
	}
	registry.Register(NewManualTransfer(false, []string{"CN"}))
	if _, err := registry.Resolve("manual_transfer", "CN"); err != ErrProviderDisabled {
		t.Fatalf("disabled err=%v", err)
	}
}

func TestReconcileRequiresIndependentProviderEvidence(t *testing.T) {
	manual := NewManualTransfer(true, []string{"CN"})
	if manual.Capabilities().SupportsReconcile {
		t.Fatal("manual transfer must not advertise an independent provider reconciliation API")
	}
	if _, err := manual.ReconcilePayment(context.Background(), ReconcileRequest{LocalStatus: "PAID",
		Amount: "1.000000000000", Currency: "USD"}); err != ErrReconcileUnsupported {
		t.Fatalf("manual reconciliation error=%v", err)
	}

	sandbox := NewSandbox(SandboxConfig{Enabled: true, Secret: []byte("sandbox-test-secret-32-bytes-long!!"), AllowedRegions: []string{"CN"}})
	if _, err := sandbox.ReconcilePayment(context.Background(), ReconcileRequest{ProviderOrderNo: "sbx_unknown",
		LocalStatus: "PAID", Amount: "1.000000000000", Currency: "USD"}); err != ErrReconcileUnsupported {
		t.Fatalf("sandbox local fallback was accepted: %v", err)
	}
}
