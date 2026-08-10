package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/config"
)

func csrfTestEngine(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	manager, err := auth.NewManager(bytes.Repeat([]byte("s"), 32), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := manager.Issue("user", "u@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.POST("/mutate", controlAuth(Dependencies{Auth: manager, Config: config.Config{AllowedOrigins: []string{"http://localhost:3001"}}}), func(c *gin.Context) { c.Status(204) })
	return r, raw
}
func TestCookieAuthRejectsMissingCSRFEvidence(t *testing.T) {
	r, session := csrfTestEngine(t)
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	req.AddCookie(&http.Cookie{Name: "relayedock_session", Value: session})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}
func TestCookieAuthAcceptsDoubleSubmit(t *testing.T) {
	r, session := csrfTestEngine(t)
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	req.AddCookie(&http.Cookie{Name: "relayedock_session", Value: session})
	req.AddCookie(&http.Cookie{Name: "relayedock_csrf", Value: "csrf-value"})
	req.Header.Set("X-CSRF-Token", "csrf-value")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCookieAuthRejectsTrustedOriginWithoutDoubleSubmit(t *testing.T) {
	r, session := csrfTestEngine(t)
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	req.AddCookie(&http.Cookie{Name: "relayedock_session", Value: session})
	req.Header.Set("Origin", "http://localhost:3001")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestBearerAuthDoesNotRequireCSRF(t *testing.T) {
	r, session := csrfTestEngine(t)
	req := httptest.NewRequest(http.MethodPost, "/mutate", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d", w.Code)
	}
}
