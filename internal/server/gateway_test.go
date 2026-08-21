package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/apikey"
)

func TestExtractUsageFromResponsesSSE(t *testing.T) {
	data := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":12,\"input_tokens_details\":{\"cached_tokens\":4},\"output_tokens\":7,\"total_tokens\":19}}}\n\n")
	usage := extractUsage(data)
	if usage.Input != 12 || usage.Cached != 4 || usage.Output != 7 || usage.Total != 19 {
		t.Fatalf("bad usage: %#v", usage)
	}
}
func TestExtractUsageFromChatCompletion(t *testing.T) {
	usage := extractUsage([]byte(`{"usage":{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens":5,"total_tokens":15}}`))
	if usage.Input != 10 || usage.Cached != 3 || usage.Output != 5 || usage.Total != 15 {
		t.Fatalf("bad usage: %#v", usage)
	}
}
func TestTailCaptureIsBounded(t *testing.T) {
	capture := newTailCapture(5)
	_, _ = capture.Write([]byte("abc"))
	_, _ = capture.Write([]byte("defgh"))
	if string(capture.Bytes()) != "defgh" {
		t.Fatalf("tail=%q", capture.Bytes())
	}
}

func TestEmbeddedAPIKeyPatternOnlyCapturesCompleteKeys(t *testing.T) {
	fixture := "rdk_test_" + strings.Repeat("A", 43)
	matches := embeddedAPIKeyPattern.FindAllSubmatch([]byte(`{"input":"prefix `+fixture+` suffix"}`), -1)
	if len(matches) != 1 || len(matches[0]) != 2 || string(matches[0][1]) != fixture || !apikey.LooksValid(string(matches[0][1])) {
		t.Fatalf("unexpected embedded key matches: %d", len(matches))
	}
	if embeddedAPIKeyPattern.MatchString("rdk_test_" + strings.Repeat("A", 42)) {
		t.Fatal("truncated key matched leak detector")
	}
	if embeddedAPIKeyPattern.MatchString("x" + fixture + "y") {
		t.Fatal("key embedded in a longer token matched leak detector")
	}
}
func TestOpenAICompatibleErrorShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { openAIError(c, 429, "rate_limit_exceeded", "slow down") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 429 {
		t.Fatalf("status %d", w.Code)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   any    `json:"param"`
		} `json:"error"`
	}
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "rate_limit_exceeded" || payload.Error.Type != "relayedock_error" || payload.Error.Message != "slow down" {
		t.Fatalf("payload %#v", payload)
	}
}

func TestProviderClientRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set("request_id", "rd_req_internal")
	if value, ok := providerClientRequestID(c); !ok || value != "rd_req_internal" {
		t.Fatalf("fallback value=%q ok=%v", value, ok)
	}
	c.Request.Header.Set("X-Client-Request-Id", "client-correlation-id")
	if value, ok := providerClientRequestID(c); !ok || value != "client-correlation-id" {
		t.Fatalf("forwarded value=%q ok=%v", value, ok)
	}
	c.Request.Header.Set("X-Client-Request-Id", strings.Repeat("x", 513))
	if _, ok := providerClientRequestID(c); ok {
		t.Fatal("oversized client request ID was accepted")
	}
}

func TestGatewayIdempotencyKey(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("request_id", "req_generated")
	if key, ok := gatewayIdempotencyKey(c); !ok || key != "req_generated" {
		t.Fatalf("default key=%q ok=%v", key, ok)
	}
	c.Request.Header.Set("Idempotency-Key", "logical-request")
	if key, ok := gatewayIdempotencyKey(c); !ok || key != "logical-request" {
		t.Fatalf("header key=%q ok=%v", key, ok)
	}
	c.Request.Header.Set("Idempotency-Key", strings.Repeat("x", 201))
	if _, ok := gatewayIdempotencyKey(c); ok {
		t.Fatal("oversized key accepted")
	}
}

func TestMaximumOutputTokens(t *testing.T) {
	if got := maximumOutputTokens(map[string]any{}, "/responses"); got != 4096 {
		t.Fatalf("default=%d", got)
	}
	if got := maximumOutputTokens(map[string]any{"max_tokens": float64(123)}, "/chat/completions"); got != 123 {
		t.Fatalf("explicit=%d", got)
	}
	if got := maximumOutputTokens(map[string]any{"max_output_tokens": float64(999)}, "/embeddings"); got != 0 {
		t.Fatalf("embeddings=%d", got)
	}
}

func TestEstimatedUsage(t *testing.T) {
	usage := estimatedUsage("/responses", 100, 404)
	if usage.Input != 100 || usage.Output != 101 || usage.Total != 201 {
		t.Fatalf("usage=%+v", usage)
	}
}
