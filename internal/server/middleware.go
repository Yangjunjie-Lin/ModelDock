package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/id"
)

const claimsKey = "relayedock.claims"

type controlCookies struct {
	Session string
	Refresh string
	CSRF    string
}

func controlCookieNames(realm string) controlCookies {
	prefix := "relayedock"
	if realm == "admin" || realm == "console" {
		prefix += "_" + realm
	}
	return controlCookies{Session: prefix + "_session", Refresh: prefix + "_refresh", CSRF: prefix + "_csrf"}
}

func controlRealm(path string) string {
	if strings.HasPrefix(path, "/api/admin/") {
		return "admin"
	}
	if strings.HasPrefix(path, "/api/console/") {
		return "console"
	}
	return "shared"
}

func requestMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		requestID := id.RequestID()
		c.Set("request_id", requestID)
		c.Header("X-Request-Id", requestID)
		c.Header("X-RelayDock-Request-Id", requestID)
		c.Next()
		logger.Info("http_request", "request_id", requestID, "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "latency_ms", time.Since(start).Milliseconds(), "client_ip", c.ClientIP())
	}
}

func recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.Error("panic_recovered", "error", recovered, "request_id", requestID(c))
		openAIError(c, http.StatusInternalServerError, "internal_error", "The server encountered an unexpected error.")
	})
}

func cors(origins []string) gin.HandlerFunc {
	allowed := map[string]struct{}{}
	for _, v := range origins {
		allowed[v] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if _, ok := allowed[origin]; origin != "" && ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-Client-Request-Id")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			if _, ok := allowed[origin]; !ok {
				c.AbortWithStatus(http.StatusForbidden)
			} else {
				c.AbortWithStatus(http.StatusNoContent)
			}
			return
		}
		c.Next()
	}
}

func controlAuth(d Dependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := ""
		cookieAuth := false
		cookies := controlCookieNames(controlRealm(c.Request.URL.Path))
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			raw = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		} else if value, err := c.Cookie(cookies.Session); err == nil {
			raw = value
			cookieAuth = true
		}
		if raw == "" {
			openAIError(c, http.StatusUnauthorized, "invalid_session", "Authentication is required.")
			c.Abort()
			return
		}
		claims, err := d.Auth.Parse(raw)
		if err != nil {
			openAIError(c, http.StatusUnauthorized, "invalid_session", err.Error())
			c.Abort()
			return
		}
		if d.Store != nil {
			user, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
			if err != nil || user.Status != "ACTIVE" {
				openAIError(c, http.StatusUnauthorized, "invalid_session", "The session user is unavailable or disabled.")
				c.Abort()
				return
			}
			claims.Role = user.Role
			claims.Email = user.Email
		}
		if cookieAuth && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			if !csrfAllowed(c, cookies.CSRF) {
				openAIError(c, http.StatusForbidden, "csrf_failed", "CSRF validation failed.")
				c.Abort()
				return
			}
		}
		c.Set(claimsKey, claims)
		c.Next()
	}
}

func csrfAllowed(c *gin.Context, cookieName string) bool {
	cookie, _ := c.Cookie(cookieName)
	header := c.GetHeader("X-CSRF-Token")
	return cookie != "" && header != "" && len(cookie) == len(header) && subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) == 1
}

func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := claimsFrom(c)
		if claims == nil || (claims.Role != "ADMIN" && claims.Role != "SUPER_ADMIN") {
			openAIError(c, http.StatusForbidden, "insufficient_permissions", "Administrator access is required.")
			c.Abort()
			return
		}
		c.Next()
	}
}
func claimsFrom(c *gin.Context) *auth.Claims {
	v, ok := c.Get(claimsKey)
	if !ok {
		return nil
	}
	claims, _ := v.(*auth.Claims)
	return claims
}
func requestID(c *gin.Context) string { v, _ := c.Get("request_id"); s, _ := v.(string); return s }
func openAIError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "relayedock_error", "param": nil, "code": code}})
}
