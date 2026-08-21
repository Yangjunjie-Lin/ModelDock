package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStartupAndDrainingProbes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	started, draining := &atomic.Bool{}, &atomic.Bool{}
	router := gin.New()
	registerHealth(router, Dependencies{StartupComplete: started, Draining: draining})

	assertProbeStatus(t, router, "/startupz", http.StatusServiceUnavailable)
	started.Store(true)
	assertProbeStatus(t, router, "/startupz", http.StatusOK)
	draining.Store(true)
	assertProbeStatus(t, router, "/readyz", http.StatusServiceUnavailable)
	assertProbeStatus(t, router, "/healthz", http.StatusOK)
}

func assertProbeStatus(t *testing.T, handler http.Handler, path string, expected int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != expected {
		t.Fatalf("%s status=%d want=%d body=%s", path, recorder.Code, expected, recorder.Body.String())
	}
}
