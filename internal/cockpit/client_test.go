package cockpit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPoolReadsOnlySanitizedSnapshot(t *testing.T) {
	generated := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "accounts.json")
	value := snapshot{Source: "cockpit-local-sidecar", GeneratedAt: &generated, Accounts: []Account{{ID: "acct-a1b2", EmailMasked: "a***@example.test", Plan: "pro", RemainingPercent: 13}}}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := New(Config{SnapshotPath: path}).Pool()
	if err != nil {
		t.Fatal(err)
	}
	if !pool.Configured || len(pool.Accounts) != 1 || pool.Accounts[0].RemainingPercent != 13 {
		t.Fatalf("unexpected pool: %#v", pool)
	}
}

func TestPoolRejectsSecretBearingUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`{"source":"cockpit","accounts":[],"api_key":"must-not-be-accepted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{SnapshotPath: path}).Pool(); err == nil {
		t.Fatal("snapshot with an unknown secret-bearing field was accepted")
	}
}

func TestSidecarTestUsesBearerAndReturnsNoSecret(t *testing.T) {
	const secret = "sidecar-test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"RELAYDOCK_COCKPIT_OK"}`))
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, APIKey: secret, TestModel: "gpt-test", HTTPClient: server.Client()})
	result, err := client.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Model != "gpt-test" || result.Message == secret {
		t.Fatalf("unexpected sanitized result: %#v", result)
	}
}

func TestSidecarTestFailsClosedWithoutConfiguration(t *testing.T) {
	if _, err := New(Config{}).Test(context.Background()); err != ErrTestNotConfigured {
		t.Fatalf("error = %v, want ErrTestNotConfigured", err)
	}
}
