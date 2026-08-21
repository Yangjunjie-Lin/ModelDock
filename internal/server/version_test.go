package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/version"
)

func TestVersionEndpointPreservesCompatibilityAndAddsProvenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerHealth(router, Dependencies{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	want := map[string]string{
		"name":               "RelayDock",
		"product":            "ModelDock",
		"compatibility_name": "RelayDock",
		"version":            version.Current,
		"commit":             version.Commit,
		"build_time":         version.BuildTime,
	}
	for field, expected := range want {
		if body[field] != expected {
			t.Fatalf("%s = %q, want %q", field, body[field], expected)
		}
	}
}
