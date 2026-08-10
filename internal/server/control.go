package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/cockpit"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/providers"
	provideropenai "github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/store"
	"github.com/relayedock/relayedock/internal/version"
)

func ControlEngine(d Dependencies) *gin.Engine {
	r := gin.New()
	r.Use(recovery(d.Logger), requestMiddleware(d.Logger), cors(d.Config.AllowedOrigins))
	registerHealth(r, d)
	r.POST("/api/auth/login", func(c *gin.Context) { loginHandler(c, d, "shared") })
	r.POST("/api/admin/auth/login", func(c *gin.Context) { loginHandler(c, d, "admin") })
	r.POST("/api/console/auth/login", func(c *gin.Context) { loginHandler(c, d, "console") })
	r.POST("/api/auth/refresh", func(c *gin.Context) { refreshHandler(c, d, "shared") })
	r.POST("/api/admin/auth/refresh", func(c *gin.Context) { refreshHandler(c, d, "admin") })
	r.POST("/api/console/auth/refresh", func(c *gin.Context) { refreshHandler(c, d, "console") })
	authenticated := r.Group("/api")
	authenticated.Use(controlAuth(d))
	authenticated.GET("/auth/me", func(c *gin.Context) { meHandler(c, d) })
	authenticated.POST("/auth/logout", func(c *gin.Context) { logoutHandler(c, d, "shared") })
	admin := authenticated.Group("/admin")
	admin.Use(requireAdmin())
	admin.POST("/auth/logout", func(c *gin.Context) { logoutHandler(c, d, "admin") })
	registerAdmin(admin, d)
	console := authenticated.Group("/console")
	console.POST("/auth/logout", func(c *gin.Context) { logoutHandler(c, d, "console") })
	registerConsole(console, d)
	return r
}

func registerHealth(r *gin.Engine, d Dependencies) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "relayedock", "version": version.Current})
	})
	r.GET("/api/version", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"name": "RelayDock", "version": version.Current}) })
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 1500*time.Millisecond)
		defer cancel()
		dbErr := d.Store.Ping(ctx)
		redisErr := d.Redis.Ping(ctx).Err()
		if dbErr != nil || redisErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "postgres": dbErr == nil, "redis": redisErr == nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "postgres": true, "redis": true})
	})
	r.GET("/metrics", func(c *gin.Context) { c.Header("Content-Type", "text/plain; version=0.0.4"); d.Metrics.Write(c.Writer) })
}

func loginHandler(c *gin.Context, d Dependencies, realm string) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		openAIError(c, 400, "invalid_request", "A valid email and password are required.")
		return
	}
	u, err := d.Store.UserByEmail(c.Request.Context(), strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil || u.Status != "ACTIVE" || !authVerify(u.PasswordHash, in.Password) {
		openAIError(c, 401, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if strings.HasPrefix(u.PasswordHash, "$2") {
		if upgraded, hashErr := auth.HashPassword(in.Password); hashErr == nil {
			d.Store.UpgradePasswordHash(c.Request.Context(), u.ID, upgraded)
		}
	}
	signed, expires, err := d.Auth.Issue(u.ID, u.Email, u.Role)
	if err != nil {
		openAIError(c, 500, "internal_error", "Could not create the session.")
		return
	}
	refreshToken, refreshExpires, err := d.Auth.IssueRefresh(u.ID, u.Email, u.Role)
	if err != nil {
		openAIError(c, 500, "internal_error", "Could not create the session.")
		return
	}
	csrf, err := newCSRF()
	if err != nil {
		openAIError(c, 500, "internal_error", "Could not create the session.")
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	accessMaxAge := int(time.Until(expires).Seconds())
	refreshMaxAge := int(time.Until(refreshExpires).Seconds())
	cookies := controlCookieNames(realm)
	c.SetCookie(cookies.Session, signed, accessMaxAge, "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.Refresh, refreshToken, refreshMaxAge, "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.CSRF, csrf, refreshMaxAge, "/", "", d.Config.CookieSecure, false)
	d.Store.TouchLogin(c.Request.Context(), u.ID)
	u.PasswordHash = ""
	c.JSON(200, gin.H{"user": u, "expires_at": expires, "csrf_token": csrf})
}

func refreshHandler(c *gin.Context, d Dependencies, realm string) {
	cookies := controlCookieNames(realm)
	if !csrfAllowed(c, cookies.CSRF) {
		openAIError(c, http.StatusForbidden, "csrf_failed", "CSRF validation failed.")
		return
	}
	raw, err := c.Cookie(cookies.Refresh)
	if err != nil || raw == "" {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "A refresh session is required.")
		return
	}
	claims, err := d.Auth.ParseRefresh(raw)
	if err != nil {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "The refresh session is invalid or expired.")
		return
	}
	u, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil || u.Status != "ACTIVE" {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "The session user is unavailable.")
		return
	}
	accessToken, accessExpires, err := d.Auth.Issue(u.ID, u.Email, u.Role)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not refresh the session.")
		return
	}
	refreshToken, refreshExpires, err := d.Auth.IssueRefresh(u.ID, u.Email, u.Role)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not refresh the session.")
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(cookies.Session, accessToken, int(time.Until(accessExpires).Seconds()), "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.Refresh, refreshToken, int(time.Until(refreshExpires).Seconds()), "/", "", d.Config.CookieSecure, true)
	c.JSON(http.StatusOK, gin.H{"expires_at": accessExpires})
}

func meHandler(c *gin.Context, d Dependencies) {
	claims := claimsFrom(c)
	u, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil {
		openAIError(c, 401, "invalid_session", "The session user no longer exists.")
		return
	}
	u.PasswordHash = ""
	c.JSON(200, gin.H{"user": u})
}
func logoutHandler(c *gin.Context, d Dependencies, realm string) {
	cookies := controlCookieNames(realm)
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(cookies.Session, "", -1, "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.Refresh, "", -1, "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.CSRF, "", -1, "/", "", d.Config.CookieSecure, false)
	c.Status(204)
}

func registerAdmin(g *gin.RouterGroup, d Dependencies) {
	registerAdminV2(g, d)
	g.GET("/cockpit/accounts", func(c *gin.Context) { cockpitPool(c, d) })
	g.POST("/cockpit/refresh", func(c *gin.Context) { cockpitPool(c, d) })
	g.POST("/cockpit/test", func(c *gin.Context) { cockpitTest(c, d) })
	g.GET("/dashboard", func(c *gin.Context) { data, err := d.Store.Dashboard(c.Request.Context(), nil); respond(c, data, err) })
	g.GET("/providers", func(c *gin.Context) { v, err := d.Store.ListProviders(c.Request.Context()); respondList(c, v, err) })
	g.POST("/providers", func(c *gin.Context) {
		var p domain.Provider
		if err := c.ShouldBindJSON(&p); err != nil || p.Name == "" || !validBaseURL(p.BaseURL) {
			openAIError(c, 400, "invalid_request", "name and an HTTP(S) base_url are required.")
			return
		}
		if p.Slug == "" {
			p.Slug = p.ProviderType
		}
		if p.Slug == "" {
			p.Slug = strings.ToLower(strings.ReplaceAll(p.Name, " ", "-"))
		}
		p.ProviderType = "openai"
		p.Enabled = true
		v, err := d.Store.CreateProvider(c.Request.Context(), p)
		audit(c, d, "provider.create", "provider", v.ID, v)
		respondCreated(c, v, err)
	})
	g.PUT("/providers/:id", func(c *gin.Context) {
		var p domain.Provider
		if err := c.ShouldBindJSON(&p); err != nil || p.Name == "" || !validBaseURL(p.BaseURL) {
			openAIError(c, 400, "invalid_request", "name and an HTTP(S) base_url are required.")
			return
		}
		p.ID = c.Param("id")
		v, err := d.Store.UpdateProvider(c.Request.Context(), p)
		audit(c, d, "provider.update", "provider", p.ID, v)
		respond(c, v, err)
	})
	g.DELETE("/providers/:id", func(c *gin.Context) {
		err := d.Store.DeleteProvider(c.Request.Context(), c.Param("id"))
		audit(c, d, "provider.delete", "provider", c.Param("id"), nil)
		respondNoContent(c, err)
	})

	g.GET("/credentials", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListCredentials(c.Request.Context(), limit, offset)
		respondList(c, v, err)
	})
	g.POST("/credentials", func(c *gin.Context) { createCredential(c, d) })
	g.POST("/credentials/import", func(c *gin.Context) { importCredentials(c, d) })
	g.POST("/credentials/bulk", func(c *gin.Context) {
		var in struct {
			CredentialIDs []string `json:"credential_ids"`
			Action        string   `json:"action"`
		}
		if c.ShouldBindJSON(&in) != nil || len(in.CredentialIDs) == 0 {
			openAIError(c, 400, "invalid_request", "credential_ids and action are required.")
			return
		}
		for _, credentialID := range in.CredentialIDs {
			action := strings.ToLower(in.Action)
			switch {
			case action == "enable":
				_ = d.Store.SetCredentialStatus(c.Request.Context(), credentialID, "ACTIVE", nil)
			case action == "disable":
				_ = d.Store.SetCredentialStatus(c.Request.Context(), credentialID, "DISABLED", nil)
			case action == "delete":
				_ = d.Store.DeleteCredential(c.Request.Context(), credentialID)
			case strings.HasPrefix(action, "move:"):
				groupID := strings.TrimSpace(strings.TrimPrefix(action, "move:"))
				if groupID == "" {
					openAIError(c, 400, "invalid_request", "A target credential group is required.")
					return
				}
				if err := d.Store.SetGroupMember(c.Request.Context(), groupID, credentialID, 100, 0); err != nil {
					respond(c, nil, err)
					return
				}
			case action == "health_check":
				cred, err := d.Store.CredentialByID(c.Request.Context(), credentialID)
				if err != nil {
					continue
				}
				provider, err := d.Store.ProviderByID(c.Request.Context(), cred.ProviderID)
				if err != nil {
					continue
				}
				secret, err := d.Vault.Decrypt(cred.EncryptedSecret, cred.ID)
				if err != nil {
					continue
				}
				ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
				err = d.OpenAI.HealthCheck(ctx, provider.BaseURL, providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)})
				cancel()
				if err == nil {
					d.Store.MarkCredentialSuccess(c.Request.Context(), credentialID)
				} else {
					var upstreamHTTP *provideropenai.HTTPError
					if errors.As(err, &upstreamHTTP) && upstreamHTTP.StatusCode == http.StatusUnauthorized {
						_ = d.Store.SetCredentialStatus(c.Request.Context(), credentialID, "AUTH_FAILED", nil)
					} else {
						d.Store.MarkCredentialFailure(c.Request.Context(), credentialID)
					}
				}
			default:
				openAIError(c, 400, "invalid_request", "Unsupported bulk action.")
				return
			}
		}
		c.JSON(200, gin.H{"updated": len(in.CredentialIDs)})
	})
	g.PUT("/credentials/:id", func(c *gin.Context) { updateCredential(c, d) })
	g.DELETE("/credentials/:id", func(c *gin.Context) {
		err := d.Store.DeleteCredential(c.Request.Context(), c.Param("id"))
		audit(c, d, "credential.delete", "provider_credential", c.Param("id"), nil)
		respondNoContent(c, err)
	})
	g.POST("/credentials/:id/test", func(c *gin.Context) { testCredential(c, d, c.Param("id")) })
	g.PATCH("/credentials/:id/status", func(c *gin.Context) {
		var in struct {
			Status string `json:"status"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "status is required.")
			return
		}
		if in.Status != "ACTIVE" && in.Status != "DISABLED" {
			openAIError(c, 400, "invalid_request", "status must be ACTIVE or DISABLED.")
			return
		}
		err := d.Store.SetCredentialStatus(c.Request.Context(), c.Param("id"), in.Status, nil)
		audit(c, d, "credential.status", "provider_credential", c.Param("id"), in)
		respond(c, gin.H{"status": in.Status}, err)
	})
	g.POST("/providers/:id/sync-models", func(c *gin.Context) { syncModels(c, d) })

	g.GET("/credential-groups", func(c *gin.Context) { v, err := d.Store.ListGroups(c.Request.Context()); respondList(c, v, err) })
	g.POST("/credential-groups", func(c *gin.Context) {
		var v domain.CredentialGroup
		if c.ShouldBindJSON(&v) != nil || v.Name == "" {
			openAIError(c, 400, "invalid_request", "name is required.")
			return
		}
		if v.ProviderID == "" {
			provider, err := d.Store.ProviderBySlug(c.Request.Context(), "openai")
			if err != nil {
				respond(c, nil, err)
				return
			}
			v.ProviderID = provider.ID
		}
		out, err := d.Store.CreateGroup(c.Request.Context(), v)
		audit(c, d, "credential_group.create", "credential_group", out.ID, out)
		respondCreated(c, out, err)
	})
	g.DELETE("/credential-groups/:id", func(c *gin.Context) {
		err := d.Store.DeleteGroup(c.Request.Context(), c.Param("id"))
		audit(c, d, "credential_group.delete", "credential_group", c.Param("id"), nil)
		respondNoContent(c, err)
	})
	g.PUT("/credential-groups/:id/members/:credentialId", func(c *gin.Context) {
		var in struct {
			Weight   int `json:"weight"`
			Priority int `json:"priority"`
		}
		_ = c.ShouldBindJSON(&in)
		if in.Weight <= 0 {
			in.Weight = 100
		}
		err := d.Store.SetGroupMember(c.Request.Context(), c.Param("id"), c.Param("credentialId"), in.Weight, in.Priority)
		audit(c, d, "credential_group.member_set", "credential_group", c.Param("id"), in)
		respond(c, gin.H{"ok": err == nil}, err)
	})
	g.DELETE("/credential-groups/:id/members/:credentialId", func(c *gin.Context) {
		respondNoContent(c, d.Store.RemoveGroupMember(c.Request.Context(), c.Param("id"), c.Param("credentialId")))
	})

	g.GET("/models", func(c *gin.Context) { v, err := d.Store.ListModels(c.Request.Context()); respondList(c, v, err) })
	g.GET("/models/:id/prices", func(c *gin.Context) {
		v, err := d.Store.ListModelPrices(c.Request.Context(), c.Param("id"))
		respondList(c, v, err)
	})
	g.POST("/models/:id/prices", func(c *gin.Context) {
		var v domain.ModelPrice
		if c.ShouldBindJSON(&v) != nil || v.InputPrice < 0 || v.CachedInputPrice < 0 || v.OutputPrice < 0 {
			openAIError(c, 400, "invalid_request", "Non-negative input, cached input, and output prices are required.")
			return
		}
		v.ModelID = c.Param("id")
		out, err := d.Store.CreateModelPrice(c.Request.Context(), v)
		audit(c, d, "model_price.create", "model", v.ModelID, out)
		respondCreated(c, out, err)
	})
	g.GET("/model-routes", func(c *gin.Context) { v, err := d.Store.ListRoutes(c.Request.Context()); respondList(c, v, err) })
	g.GET("/routes", func(c *gin.Context) { v, err := d.Store.ListRoutes(c.Request.Context()); respondList(c, v, err) })
	g.POST("/model-routes", func(c *gin.Context) {
		var v domain.ModelRoute
		if c.ShouldBindJSON(&v) != nil || v.Alias == "" || v.ProviderID == "" || v.UpstreamModel == "" || v.CredentialGroupID == "" {
			openAIError(c, 400, "invalid_request", "alias, provider_id, upstream_model, and credential_group_id are required.")
			return
		}
		if v.RoutingPolicy == "" {
			v.RoutingPolicy = "priority_weighted"
		}
		if !validRoutingPolicy(v.RoutingPolicy) {
			openAIError(c, 400, "invalid_request", "routing_policy must be priority_weighted, least_loaded, or weighted_round_robin.")
			return
		}
		v.Enabled = true
		out, err := d.Store.CreateRoute(c.Request.Context(), v)
		audit(c, d, "model_route.create", "model_route", out.ID, out)
		respondCreated(c, out, err)
	})
	g.POST("/routes", func(c *gin.Context) {
		var v domain.ModelRoute
		if c.ShouldBindJSON(&v) != nil || v.Alias == "" || v.ProviderID == "" || v.UpstreamModel == "" || v.CredentialGroupID == "" {
			openAIError(c, 400, "invalid_request", "alias, provider_id, upstream_model, and credential_group_id are required.")
			return
		}
		if v.RoutingPolicy == "" {
			v.RoutingPolicy = "priority_weighted"
		}
		if !validRoutingPolicy(v.RoutingPolicy) {
			openAIError(c, 400, "invalid_request", "routing_policy must be priority_weighted, least_loaded, or weighted_round_robin.")
			return
		}
		v.Enabled = true
		out, err := d.Store.CreateRoute(c.Request.Context(), v)
		audit(c, d, "model_route.create", "model_route", out.ID, out)
		respondCreated(c, out, err)
	})
	g.PUT("/model-routes/:id", func(c *gin.Context) {
		var v domain.ModelRoute
		if c.ShouldBindJSON(&v) != nil || v.Alias == "" || v.ProviderID == "" || v.UpstreamModel == "" || v.CredentialGroupID == "" {
			openAIError(c, 400, "invalid_request", "alias, provider_id, upstream_model, and credential_group_id are required.")
			return
		}
		if v.RoutingPolicy == "" {
			v.RoutingPolicy = "priority_weighted"
		}
		if !validRoutingPolicy(v.RoutingPolicy) {
			openAIError(c, 400, "invalid_request", "routing_policy must be priority_weighted, least_loaded, or weighted_round_robin.")
			return
		}
		v.ID = c.Param("id")
		out, err := d.Store.UpdateRoute(c.Request.Context(), v)
		audit(c, d, "model_route.update", "model_route", v.ID, out)
		respond(c, out, err)
	})
	g.DELETE("/model-routes/:id", func(c *gin.Context) {
		err := d.Store.DeleteRoute(c.Request.Context(), c.Param("id"))
		audit(c, d, "model_route.delete", "model_route", c.Param("id"), nil)
		respondNoContent(c, err)
	})

	g.GET("/api-keys", func(c *gin.Context) {
		limit, offset := page(c)
		if projectID := strings.TrimSpace(c.Query("project_id")); projectID != "" {
			v, err := d.Store.ListProjectAPIKeys(c.Request.Context(), projectID, nil, limit, offset)
			respondList(c, v, err)
			return
		}
		v, err := d.Store.ListAPIKeys(c.Request.Context(), nil, limit, offset)
		respondList(c, v, err)
	})
	g.POST("/api-keys", func(c *gin.Context) { createAPIKey(c, d, true) })
	g.PATCH("/api-keys/:id/status", func(c *gin.Context) {
		var in struct {
			Status string `json:"status"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "status is required.")
			return
		}
		err := d.Store.UpdateAPIKeyStatus(c.Request.Context(), c.Param("id"), in.Status)
		audit(c, d, "api_key.status", "api_key", c.Param("id"), in)
		respond(c, gin.H{"status": in.Status}, err)
	})
	g.DELETE("/api-keys/:id", func(c *gin.Context) {
		err := d.Store.RevokeAPIKey(c.Request.Context(), c.Param("id"), "", true)
		audit(c, d, "api_key.revoke", "api_key", c.Param("id"), nil)
		respondNoContent(c, err)
	})

	g.GET("/users", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListUsers(c.Request.Context(), limit, offset)
		for i := range v {
			v[i].PasswordHash = ""
		}
		respondList(c, v, err)
	})
	g.POST("/users", func(c *gin.Context) {
		var in struct {
			Email       string `json:"email"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		}
		if c.ShouldBindJSON(&in) != nil || in.Email == "" {
			openAIError(c, 400, "invalid_request", "email is required.")
			return
		}
		temporary := ""
		if in.Password == "" {
			temporary, _ = auth.CSRFToken()
			in.Password = temporary
		}
		v, err := d.Store.CreateUser(c.Request.Context(), in.Email, in.Password, in.DisplayName, in.Role)
		v.PasswordHash = ""
		audit(c, d, "user.create", "user", v.ID, v)
		if err != nil {
			respond(c, nil, err)
			return
		}
		if temporary != "" {
			c.JSON(201, gin.H{"user": v, "temporary_password": temporary, "warning": "The temporary password is shown once."})
			return
		}
		c.JSON(201, v)
	})
	g.PATCH("/users/:id/status", func(c *gin.Context) {
		var in struct {
			Status string `json:"status"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "status is required.")
			return
		}
		err := d.Store.UpdateUserStatus(c.Request.Context(), c.Param("id"), in.Status)
		audit(c, d, "user.status", "user", c.Param("id"), in)
		respond(c, gin.H{"status": in.Status}, err)
	})
	g.GET("/request-logs", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListRequestLogs(c.Request.Context(), nil, limit, offset)
		respondList(c, v, err)
	})
	g.GET("/usage", func(c *gin.Context) {
		v, err := d.Store.UsageSeries(c.Request.Context(), nil, days(c))
		respondList(c, v, err)
	})
	g.GET("/audit-logs", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListAuditLogs(c.Request.Context(), limit, offset)
		respondList(c, v, err)
	})
	g.GET("/alerts", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListAlerts(c.Request.Context(), limit, offset)
		respondList(c, v, err)
	})
	g.GET("/settings", func(c *gin.Context) { v, err := d.Store.GetSettings(c.Request.Context()); respond(c, v, err) })
	g.PUT("/settings", func(c *gin.Context) {
		var v map[string]any
		if c.ShouldBindJSON(&v) != nil {
			openAIError(c, 400, "invalid_request", "Invalid settings body.")
			return
		}
		err := d.Store.SetSettings(c.Request.Context(), v)
		audit(c, d, "settings.update", "settings", "global", v)
		respond(c, v, err)
	})
}

func cockpitPool(c *gin.Context, d Dependencies) {
	if d.Cockpit == nil {
		c.JSON(http.StatusOK, gin.H{"configured": false, "test_configured": false, "source": "cockpit-local-sidecar", "accounts": []any{}})
		return
	}
	pool, err := d.Cockpit.Pool()
	if err != nil {
		openAIError(c, http.StatusUnprocessableEntity, "cockpit_snapshot_invalid", "The sanitized Cockpit snapshot could not be read.")
		return
	}
	c.JSON(http.StatusOK, pool)
}

func cockpitTest(c *gin.Context, d Dependencies) {
	if d.Cockpit == nil {
		openAIError(c, http.StatusServiceUnavailable, "cockpit_test_not_configured", "Cockpit sidecar testing is not configured.")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Second)
	defer cancel()
	result, err := d.Cockpit.Test(ctx)
	if errors.Is(err, cockpit.ErrTestNotConfigured) {
		openAIError(c, http.StatusServiceUnavailable, "cockpit_test_not_configured", "Set COCKPIT_BASE_URL and COCKPIT_API_KEY to enable the live sidecar check.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusBadGateway, "cockpit_test_failed", "The Cockpit sidecar model check failed.")
		return
	}
	c.JSON(http.StatusOK, result)
}

func registerConsole(g *gin.RouterGroup, d Dependencies) {
	registerConsoleV2(g, d)
	g.GET("/overview", func(c *gin.Context) {
		uid := claimsFrom(c).Subject
		if projectID := strings.TrimSpace(c.Query("project_id")); projectID != "" {
			project, ok := requireProjectIDAccess(c, d, false, projectID, "VIEWER")
			if !ok {
				return
			}
			v, err := consoleProjectOverview(c, d, project, uid)
			respond(c, v, err)
			return
		}
		v, err := d.Store.ConsoleOverview(c.Request.Context(), uid)
		respond(c, v, err)
	})
	g.GET("/dashboard", func(c *gin.Context) {
		uid := claimsFrom(c).Subject
		v, err := d.Store.Dashboard(c.Request.Context(), &uid)
		respond(c, v, err)
	})
	g.GET("/api-keys", func(c *gin.Context) {
		uid := claimsFrom(c).Subject
		limit, offset := page(c)
		if projectID := strings.TrimSpace(c.Query("project_id")); projectID != "" {
			if _, ok := requireProjectIDAccess(c, d, false, projectID, "DEVELOPER"); !ok {
				return
			}
			v, err := d.Store.ListProjectAPIKeys(c.Request.Context(), projectID, &uid, limit, offset)
			respondList(c, v, err)
			return
		}
		v, err := d.Store.ListAPIKeys(c.Request.Context(), &uid, limit, offset)
		respondList(c, v, err)
	})
	g.POST("/api-keys", func(c *gin.Context) { createAPIKey(c, d, false) })
	g.DELETE("/api-keys/:id", func(c *gin.Context) {
		uid := claimsFrom(c).Subject
		respondNoContent(c, d.Store.RevokeAPIKey(c.Request.Context(), c.Param("id"), uid, false))
	})
	g.GET("/usage", func(c *gin.Context) {
		uid := claimsFrom(c).Subject
		if projectID := strings.TrimSpace(c.Query("project_id")); projectID != "" {
			if _, ok := requireProjectIDAccess(c, d, false, projectID, "VIEWER"); !ok {
				return
			}
			v, err := consoleProjectUsage(c.Request.Context(), d, projectID, days(c))
			respond(c, v, err)
			return
		}
		v, err := d.Store.UsageSummary(c.Request.Context(), uid, periodDays(c))
		respond(c, v, err)
	})
	projectLogs := func(c *gin.Context) {
		uid := claimsFrom(c).Subject
		limit, offset := page(c)
		var v []domain.RequestLog
		var err error
		if projectID := strings.TrimSpace(c.Query("project_id")); projectID != "" {
			if _, ok := requireProjectIDAccess(c, d, false, projectID, "VIEWER"); !ok {
				return
			}
			v, err = d.Store.ListProjectRequestLogs(c.Request.Context(), projectID, &uid, limit, offset)
		} else {
			v, err = d.Store.ListRequestLogs(c.Request.Context(), &uid, limit, offset)
		}
		sanitizeConsoleRequestLogs(v)
		respondList(c, v, err)
	}
	g.GET("/request-logs", projectLogs)
	g.GET("/logs", projectLogs)
	g.GET("/models", func(c *gin.Context) {
		projectID := strings.TrimSpace(c.Query("project_id"))
		// Console users only need stable RelayDock aliases. Provider base URLs,
		// credential-group identifiers, and upstream routing details remain in
		// the administrator control plane.
		models := make([]gin.H, 0)
		if projectID != "" {
			if _, ok := requireProjectIDAccess(c, d, false, projectID, "VIEWER"); !ok {
				return
			}
			routes, err := d.Store.ListProjectRoutes(c.Request.Context(), projectID)
			if err != nil {
				respond(c, nil, err)
				return
			}
			for _, route := range routes {
				if route.Enabled {
					models = append(models, gin.H{"id": route.Alias, "id_alias": route.Alias, "alias": route.Alias, "display_name": route.Alias, "enabled": true, "capabilities": []string{}, "context_window": nil})
				}
			}
		} else {
			routes, err := d.Store.ListRoutes(c.Request.Context())
			if err != nil {
				respond(c, nil, err)
				return
			}
			for _, route := range routes {
				if !route.Enabled {
					continue
				}
				models = append(models, gin.H{
					"id": route.Alias, "id_alias": route.Alias, "alias": route.Alias, "display_name": route.Alias,
					"enabled": true, "capabilities": []string{}, "context_window": nil,
				})
			}
		}
		respondList(c, models, nil)
	})
}

func createCredential(c *gin.Context, d Dependencies) {
	var in struct {
		ProviderID        string  `json:"provider_id"`
		Name              string  `json:"name"`
		CredentialType    string  `json:"credential_type"`
		Secret            string  `json:"secret"`
		APIKey            string  `json:"api_key"`
		OrganizationID    *string `json:"organization_id"`
		ProjectID         *string `json:"project_id"`
		GroupID           *string `json:"group_id"`
		CredentialGroupID *string `json:"credential_group_id"`
		Priority          int     `json:"priority"`
		Weight            int     `json:"weight"`
		MaxConcurrency    int     `json:"max_concurrency"`
		Validate          *bool   `json:"validate"`
		SaveDisabled      bool    `json:"save_disabled"`
		Status            string  `json:"status"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, 400, "invalid_request", "Invalid credential body.")
		return
	}
	if in.Secret == "" {
		in.Secret = in.APIKey
	}
	if in.GroupID == nil {
		in.GroupID = in.CredentialGroupID
	}
	if in.ProviderID == "" {
		p, err := d.Store.ProviderBySlug(c.Request.Context(), "openai")
		if err != nil {
			respond(c, nil, err)
			return
		}
		in.ProviderID = p.ID
	}
	if in.Name == "" || strings.TrimSpace(in.Secret) == "" {
		openAIError(c, 400, "invalid_request", "name and an official provider API key are required.")
		return
	}
	if in.CredentialType == "" {
		in.CredentialType = "api_key"
	}
	if in.Weight <= 0 {
		in.Weight = 100
	}
	if in.MaxConcurrency <= 0 {
		in.MaxConcurrency = 10
	}
	credentialID := id.UUID()
	encrypted, err := d.Vault.Encrypt(strings.TrimSpace(in.Secret), credentialID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	status := "ACTIVE"
	if in.SaveDisabled || strings.EqualFold(in.Status, "DISABLED") {
		status = "DISABLED"
	}
	shouldValidate := in.Validate == nil || *in.Validate
	if shouldValidate {
		p, err := d.Store.ProviderByID(c.Request.Context(), in.ProviderID)
		if err == nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
			err = d.OpenAI.HealthCheck(ctx, p.BaseURL, providers.Credential{Secret: strings.TrimSpace(in.Secret), OrganizationID: deref(in.OrganizationID), ProjectID: deref(in.ProjectID)})
			cancel()
		}
		if err != nil && status != "DISABLED" {
			openAIError(c, 422, "credential_validation_failed", safeProviderError(err))
			return
		}
		if err != nil {
			status = "DISABLED"
		}
	}
	v := domain.Credential{ID: credentialID, ProviderID: in.ProviderID, Name: in.Name, CredentialType: in.CredentialType, EncryptedSecret: encrypted, SecretLast4: last4(in.Secret), OrganizationID: in.OrganizationID, ProjectID: in.ProjectID, Status: status, Priority: in.Priority, Weight: in.Weight, MaxConcurrency: in.MaxConcurrency}
	out, err := d.Store.CreateCredential(c.Request.Context(), v, in.GroupID)
	audit(c, d, "credential.create", "provider_credential", out.ID, out)
	respondCreated(c, out, err)
}

// importCredentials accepts only already-issued official provider API
// credentials. It is deliberately JSON-only and never accepts account
// passwords, browser cookies, consumer sessions, or registration inputs.
func importCredentials(c *gin.Context, d Dependencies) {
	type item struct {
		ProviderID     string  `json:"provider_id"`
		Name           string  `json:"name"`
		APIKey         string  `json:"api_key"`
		OrganizationID *string `json:"organization_id"`
		ProjectID      *string `json:"project_id"`
		GroupID        *string `json:"group_id"`
		Priority       int     `json:"priority"`
		Weight         int     `json:"weight"`
		MaxConcurrency int     `json:"max_concurrency"`
	}
	var in struct {
		Credentials []item `json:"credentials"`
		Validate    *bool  `json:"validate"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.Credentials) == 0 || len(in.Credentials) > 25 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "credentials must contain between 1 and 25 authorized API credentials.")
		return
	}
	validate := in.Validate == nil || *in.Validate
	defaultProvider, defaultErr := d.Store.ProviderBySlug(c.Request.Context(), "openai")
	results := make([]gin.H, 0, len(in.Credentials))
	created := 0
	for index, candidate := range in.Credentials {
		candidate.APIKey = strings.TrimSpace(candidate.APIKey)
		if candidate.ProviderID == "" && defaultErr == nil {
			candidate.ProviderID = defaultProvider.ID
		}
		if candidate.Name == "" || candidate.ProviderID == "" || candidate.APIKey == "" {
			results = append(results, gin.H{"index": index, "name": candidate.Name, "created": false, "error": "name, provider_id, and api_key are required"})
			continue
		}
		if candidate.Weight <= 0 {
			candidate.Weight = 100
		}
		if candidate.MaxConcurrency <= 0 {
			candidate.MaxConcurrency = 10
		}
		status := "ACTIVE"
		validationError := ""
		if validate {
			provider, err := d.Store.ProviderByID(c.Request.Context(), candidate.ProviderID)
			if err == nil {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
				err = d.OpenAI.HealthCheck(ctx, provider.BaseURL, providers.Credential{Secret: candidate.APIKey, OrganizationID: deref(candidate.OrganizationID), ProjectID: deref(candidate.ProjectID)})
				cancel()
			}
			if err != nil {
				status = "DISABLED"
				validationError = safeProviderError(err)
			}
		}
		credentialID := id.UUID()
		encrypted, err := d.Vault.Encrypt(candidate.APIKey, credentialID)
		if err != nil {
			results = append(results, gin.H{"index": index, "name": candidate.Name, "created": false, "error": "credential encryption failed"})
			continue
		}
		credential := domain.Credential{ID: credentialID, ProviderID: candidate.ProviderID, Name: candidate.Name, CredentialType: "api_key", EncryptedSecret: encrypted, SecretLast4: last4(candidate.APIKey), OrganizationID: candidate.OrganizationID, ProjectID: candidate.ProjectID, Status: status, Priority: candidate.Priority, Weight: candidate.Weight, MaxConcurrency: candidate.MaxConcurrency}
		stored, err := d.Store.CreateCredential(c.Request.Context(), credential, candidate.GroupID)
		if err != nil {
			message := "credential could not be stored"
			switch {
			case errors.Is(err, store.ErrCredentialGroupProviderMismatch):
				message = "credential and credential group must belong to the same provider"
			case errors.Is(err, store.ErrNotFound):
				message = "credential group was not found"
			}
			results = append(results, gin.H{"index": index, "name": candidate.Name, "created": false, "error": message})
			continue
		}
		created++
		audit(c, d, "credential.import", "provider_credential", stored.ID, stored)
		result := gin.H{"index": index, "id": stored.ID, "name": stored.Name, "status": stored.Status, "created": true}
		if validationError != "" {
			result["validation_error"] = validationError
		}
		results = append(results, result)
	}
	c.JSON(http.StatusOK, gin.H{"created": created, "total": len(in.Credentials), "results": results})
}

func updateCredential(c *gin.Context, d Dependencies) {
	current, err := d.Store.CredentialByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	var in struct {
		Name           string  `json:"name"`
		Secret         string  `json:"secret"`
		OrganizationID *string `json:"organization_id"`
		ProjectID      *string `json:"project_id"`
		Status         string  `json:"status"`
		Priority       int     `json:"priority"`
		Weight         int     `json:"weight"`
		MaxConcurrency int     `json:"max_concurrency"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, 400, "invalid_request", "Invalid credential body.")
		return
	}
	current.Name = in.Name
	current.OrganizationID = in.OrganizationID
	current.ProjectID = in.ProjectID
	current.Status = in.Status
	current.Priority = in.Priority
	current.Weight = in.Weight
	current.MaxConcurrency = in.MaxConcurrency
	if current.Name == "" || current.Weight <= 0 || current.MaxConcurrency <= 0 {
		openAIError(c, 400, "invalid_request", "name, positive weight, and positive max_concurrency are required.")
		return
	}
	replace := strings.TrimSpace(in.Secret) != ""
	if replace {
		current.EncryptedSecret, err = d.Vault.Encrypt(strings.TrimSpace(in.Secret), current.ID)
		current.SecretLast4 = last4(in.Secret)
		if err != nil {
			respond(c, nil, err)
			return
		}
	}
	out, err := d.Store.UpdateCredential(c.Request.Context(), current, replace)
	audit(c, d, "credential.update", "provider_credential", out.ID, out)
	respond(c, out, err)
}

func testCredential(c *gin.Context, d Dependencies, credentialID string) {
	cred, err := d.Store.CredentialByID(c.Request.Context(), credentialID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	provider, err := d.Store.ProviderByID(c.Request.Context(), cred.ProviderID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	secret, err := d.Vault.Decrypt(cred.EncryptedSecret, cred.ID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	err = d.OpenAI.HealthCheck(ctx, provider.BaseURL, providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)})
	if err != nil {
		var upstreamHTTP *provideropenai.HTTPError
		if errors.As(err, &upstreamHTTP) && upstreamHTTP.StatusCode == http.StatusUnauthorized {
			_ = d.Store.SetCredentialStatus(c.Request.Context(), cred.ID, "AUTH_FAILED", nil)
		} else {
			d.Store.MarkCredentialFailure(c.Request.Context(), cred.ID)
		}
		openAIError(c, 422, "credential_validation_failed", safeProviderError(err))
		return
	}
	d.Store.MarkCredentialSuccess(c.Request.Context(), cred.ID)
	c.JSON(200, gin.H{"ok": true, "status": "HEALTHY"})
}

func syncModels(c *gin.Context, d Dependencies) {
	var in struct {
		CredentialID string `json:"credential_id"`
	}
	if c.ShouldBindJSON(&in) != nil || in.CredentialID == "" {
		openAIError(c, 400, "invalid_request", "credential_id is required.")
		return
	}
	cred, err := d.Store.CredentialByID(c.Request.Context(), in.CredentialID)
	if err != nil || cred.ProviderID != c.Param("id") {
		openAIError(c, 404, "not_found", "Credential was not found for this provider.")
		return
	}
	p, err := d.Store.ProviderByID(c.Request.Context(), cred.ProviderID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	secret, err := d.Vault.Decrypt(cred.EncryptedSecret, cred.ID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	models, err := d.OpenAI.ListModels(ctx, p.BaseURL, providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)})
	cancel()
	if err != nil {
		openAIError(c, 502, "provider_error", safeProviderError(err))
		return
	}
	items := make([]domain.Model, 0, len(models))
	for _, m := range models {
		items = append(items, domain.Model{ProviderModelID: m.ID, DisplayName: m.ID, ModelType: "text", Enabled: true, Capabilities: []string{}, CapabilitySource: "provider", Metadata: map[string]any{"owned_by": m.OwnedBy}})
	}
	if err := d.Store.UpsertModels(c.Request.Context(), p.ID, items); err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(200, gin.H{"synced": len(items)})
}

func createAPIKey(c *gin.Context, d Dependencies, admin bool) {
	var in struct {
		UserID            string   `json:"user_id"`
		OrganizationID    string   `json:"organization_id"`
		ProjectID         string   `json:"project_id"`
		Name              string   `json:"name"`
		Environment       string   `json:"environment"`
		ExpiresAt         string   `json:"expires_at"`
		RateLimitRPM      int      `json:"rate_limit_rpm"`
		RateLimitTPM      int      `json:"rate_limit_tpm"`
		MonthlyTokenLimit *int64   `json:"monthly_token_limit"`
		MonthlyCostLimit  *float64 `json:"monthly_cost_limit"`
		AllowedModels     []string `json:"allowed_models"`
	}
	if c.ShouldBindJSON(&in) != nil || in.Name == "" {
		openAIError(c, 400, "invalid_request", "name is required.")
		return
	}
	if !admin {
		in.UserID = claimsFrom(c).Subject
	} else if in.UserID == "" {
		openAIError(c, 400, "invalid_request", "user_id is required for an admin-created key.")
		return
	}
	if in.Environment == "" {
		in.Environment = "live"
	}
	if in.ProjectID == "" {
		in.ProjectID = domain.LegacyProjectID
	}
	project, err := d.Store.ProjectByID(c.Request.Context(), in.ProjectID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	if !admin {
		if _, err = d.Store.CheckProjectAccess(c.Request.Context(), in.UserID, project.ID, "DEVELOPER"); err != nil {
			respond(c, nil, err)
			return
		}
	}
	in.OrganizationID = project.OrganizationID
	expiresAt, err := parseOptionalTime(in.ExpiresAt)
	if err != nil {
		openAIError(c, 400, "invalid_request", "expires_at must be an RFC3339 timestamp or YYYY-MM-DD date.")
		return
	}
	if in.RateLimitRPM <= 0 {
		in.RateLimitRPM = 60
	}
	if in.RateLimitTPM <= 0 {
		in.RateLimitTPM = 100000
	}
	full, prefix, hash, err := d.APIKeys.Generate(in.Environment)
	if err != nil {
		respond(c, nil, err)
		return
	}
	k := domain.APIKey{UserID: in.UserID, OrganizationID: in.OrganizationID, ProjectID: in.ProjectID, Name: in.Name, Environment: in.Environment, KeyPrefix: prefix, KeyHash: hash, ExpiresAt: expiresAt, RateLimitRPM: in.RateLimitRPM, RateLimitTPM: in.RateLimitTPM, MonthlyTokenLimit: in.MonthlyTokenLimit, MonthlyCostLimit: in.MonthlyCostLimit, AllowedModels: in.AllowedModels}
	out, err := d.Store.CreateProjectAPIKey(c.Request.Context(), k)
	if err != nil {
		respond(c, nil, err)
		return
	}
	audit(c, d, "api_key.create", "api_key", out.ID, out)
	c.JSON(201, gin.H{"api_key": out, "key": full, "secret": full, "warning": "This key is shown once. Store it securely."})
}

func respond(c *gin.Context, v any, err error) {
	if err == nil {
		c.JSON(200, v)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		openAIError(c, 404, "not_found", "The requested resource was not found.")
		return
	}
	if errors.Is(err, store.ErrCredentialGroupProviderMismatch) {
		openAIError(c, http.StatusUnprocessableEntity, "provider_mismatch", "Provider credentials can only be assigned to credential groups owned by the same provider.")
		return
	}
	if errors.Is(err, store.ErrModelRouteProviderMismatch) {
		openAIError(c, http.StatusUnprocessableEntity, "provider_mismatch", "A model route's provider must match its primary and fallback credential groups.")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			openAIError(c, http.StatusConflict, "conflict", "A resource with the same unique identifier already exists.")
			return
		case "23503", "23514", "23502":
			openAIError(c, http.StatusUnprocessableEntity, "invalid_reference", "The request violates a resource relationship or constraint.")
			return
		}
	}
	openAIError(c, 500, "internal_error", "The operation could not be completed.")
}
func respondCreated(c *gin.Context, v any, err error) {
	if err != nil {
		respond(c, v, err)
		return
	}
	c.JSON(201, v)
}
func respondList(c *gin.Context, v any, err error) {
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(200, gin.H{"data": v})
}
func respondNoContent(c *gin.Context, err error) {
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.Status(204)
}
func page(c *gin.Context) (int, int) {
	limitRaw := c.Query("limit")
	if limitRaw == "" {
		limitRaw = c.DefaultQuery("page_size", "50")
	}
	limit, _ := strconv.Atoi(limitRaw)
	offsetRaw := c.Query("offset")
	offset, _ := strconv.Atoi(offsetRaw)
	if offsetRaw == "" {
		pageNo, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if pageNo > 1 {
			offset = (pageNo - 1) * limit
		}
	}
	return limit, offset
}
func days(c *gin.Context) int { v, _ := strconv.Atoi(c.DefaultQuery("days", "30")); return v }
func periodDays(c *gin.Context) int {
	switch c.Query("period") {
	case "today":
		return 1
	case "7d":
		return 7
	case "30d", "":
		return 30
	default:
		return days(c)
	}
}
func parseOptionalTime(v string) (*time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return &parsed, nil
		}
	}
	return nil, errors.New("invalid time")
}
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func last4(v string) string {
	v = strings.TrimSpace(v)
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
}
func validBaseURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
func validRoutingPolicy(value string) bool {
	return value == "priority_weighted" || value == "least_loaded" || value == "weighted_round_robin"
}
func safeProviderError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
func audit(c *gin.Context, d Dependencies, action, typ, id string, after any) {
	claims := claimsFrom(c)
	if claims == nil {
		return
	}
	d.Store.Audit(c.Request.Context(), claims.Subject, action, typ, id, c.ClientIP(), after)
}
func authVerify(hash, password string) bool { return auth.VerifyPassword(hash, password) }
func newCSRF() (string, error)              { return auth.CSRFToken() }
