package backend_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/providers"
	"github.com/relayedock/relayedock/internal/providers/openai"
)

func TestOpenAIAdapterUsesOnlyConstructedCredentialHeaders(t *testing.T) {
	seen := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Internal-Credential-ID", "must-not-forward")
		_, _ = io.WriteString(w, `{"id":"resp_1","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)
	}))
	defer server.Close()
	adapter := openai.New(server.Client())
	resp, err := adapter.Forward(context.Background(), providers.ForwardRequest{BaseURL: server.URL + "/v1", Path: "/responses", Body: bytes.NewBufferString(`{"model":"gpt-test"}`), ContentType: "application/json", Accept: "text/event-stream", ClientRequestID: "client-123", Credential: providers.Credential{Secret: "sk-authorized", OrganizationID: "org_1", ProjectID: "proj_1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	req := <-seen
	if req.URL.Path != "/v1/responses" || req.Method != "POST" {
		t.Fatalf("unexpected target %s %s", req.Method, req.URL.Path)
	}
	if req.Header.Get("Authorization") != "Bearer sk-authorized" || req.Header.Get("OpenAI-Organization") != "org_1" || req.Header.Get("OpenAI-Project") != "proj_1" {
		t.Fatalf("credential headers missing: %#v", req.Header)
	}
	if req.Header.Get("Cookie") != "" || req.Header.Get("X-Internal-Credential-ID") != "" {
		t.Fatal("unapproved header reached upstream")
	}
	out := http.Header{}
	openai.ForwardResponseHeader(out, resp.Header)
	if out.Get("Content-Type") != "application/json" {
		t.Fatal("content type was not copied")
	}
	if out.Get("X-Internal-Credential-ID") != "" {
		t.Fatal("internal upstream header leaked")
	}
}

func TestOpenAIModelsAndStreamingAreNotBuffered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": "gpt-test", "object": "model", "owned_by": "openai"}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	}))
	defer server.Close()
	adapter := openai.New(server.Client())
	models, err := adapter.ListModels(context.Background(), server.URL+"/v1", providers.Credential{Secret: "sk-test"})
	if err != nil || len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("models: %#v %v", models, err)
	}
	started := time.Now()
	resp, err := adapter.Forward(context.Background(), providers.ForwardRequest{BaseURL: server.URL + "/v1", Path: "/responses", Body: strings.NewReader(`{}`), Credential: providers.Credential{Secret: "sk-test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "data: first\n" {
		t.Fatalf("first chunk %q", line)
	}
	if time.Since(started) > 200*time.Millisecond {
		t.Fatal("first SSE event was buffered until the second event")
	}
}

func TestOpenAIAdapterHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := openai.New(server.Client()).Forward(ctx, providers.ForwardRequest{BaseURL: server.URL, Path: "/responses", Body: strings.NewReader(`{}`), Credential: providers.Credential{Secret: "sk-test"}})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
		close(release)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("upstream request ignored cancellation")
	}
}
