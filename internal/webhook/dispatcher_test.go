package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeliverSignsExactBodyAndRejectsRedirects(t *testing.T) {
	secret := "unit-test-webhook-secret"
	body := []byte(`{"type":"webhook.test"}`)
	var signature, timestamp string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature = r.Header.Get("X-RelayDock-Signature")
		timestamp = r.Header.Get("X-RelayDock-Timestamp")
		got, _ := io.ReadAll(r.Body)
		if string(got) != string(body) || r.Header.Get("X-RelayDock-Event") != "webhook.test" {
			t.Fatalf("unexpected webhook request")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dispatcher := New(Config{AllowHTTP: true, AllowPrivateNetwork: true})
	dispatcher.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	result, err := dispatcher.Deliver(context.Background(), Delivery{ID: "delivery-1", EventType: "webhook.test", URL: server.URL, Secret: secret, Body: body})
	if err != nil || result.HTTPStatus != http.StatusNoContent {
		t.Fatalf("deliver: result=%+v err=%v", result, err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	want := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if signature != want {
		t.Fatalf("signature=%q want=%q", signature, want)
	}
}

func TestValidateTargetBlocksUnsafeTargetsByDefault(t *testing.T) {
	dispatcher := New(Config{})
	for _, target := range []string{"http://example.com/hook", "https://127.0.0.1/hook", "https://user:pass@example.com/hook"} {
		if err := dispatcher.ValidateTarget(context.Background(), target); err == nil {
			t.Fatalf("expected %q to be rejected", target)
		}
	}
}
