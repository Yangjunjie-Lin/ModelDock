package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/observability"
)

func TestRequestMiddlewarePropagatesTraceAndStructuredCorrelation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	metrics := &observability.Metrics{}
	router := gin.New()
	router.Use(requestMiddleware(logger, metrics, "gateway"))
	router.GET("/trace", func(c *gin.Context) {
		trace, ok := observability.FromContext(c.Request.Context())
		if !ok {
			t.Fatal("trace missing from request context")
		}
		c.JSON(http.StatusOK, gin.H{"trace_id": trace.TraceID})
	})

	request := httptest.NewRequest(http.MethodGet, "/trace", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Request-Id") == "" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	parsed, ok := observability.ParseTraceparent(response.Header().Get("traceparent"))
	if !ok || parsed.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("response traceparent=%q", response.Header().Get("traceparent"))
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["request_id"] != response.Header().Get("X-Request-Id") || entry["trace_id"] != parsed.TraceID || entry["component"] != "gateway" {
		t.Fatalf("log correlation=%v", entry)
	}
}
