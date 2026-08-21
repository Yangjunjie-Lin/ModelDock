package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/config"
)

func TestAdministratorMFAPolicyRestrictsUnverifiedSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, err := auth.NewManager([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{Auth: manager, Config: config.Config{AdminMFARequired: true}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	engine := gin.New()
	admin := engine.Group("/api/admin")
	admin.Use(controlAuth(dependencies), requireAdmin(dependencies))
	admin.GET("/dashboard", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	admin.GET("/auth/mfa/status", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	unverified, _, err := manager.IssueVersioned("00000000-0000-0000-0000-000000000001", "admin@example.invalid", "ADMIN", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	verified, _, err := manager.IssueVersioned("00000000-0000-0000-0000-000000000001", "admin@example.invalid", "ADMIN", 0, true)
	if err != nil {
		t.Fatal(err)
	}

	assertBearerStatus(t, engine, "/api/admin/dashboard", unverified, http.StatusForbidden)
	assertBearerStatus(t, engine, "/api/admin/auth/mfa/status", unverified, http.StatusNoContent)
	assertBearerStatus(t, engine, "/api/admin/dashboard", verified, http.StatusNoContent)
}

func TestControlEngineRegistersAccountRoutesWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, err := auth.NewManager([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{Auth: manager, Config: config.Config{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_ = ControlEngine(dependencies)
}

func assertBearerStatus(t *testing.T, handler http.Handler, path, token string, want int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("GET %s status=%d body=%s, want %d", path, response.Code, response.Body.String(), want)
	}
}
