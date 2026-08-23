package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/cockpit"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/pricesync"
	"github.com/relayedock/relayedock/internal/providers"
	provideropenai "github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/store"
	"github.com/relayedock/relayedock/internal/version"
)

func ControlEngine(d Dependencies) *gin.Engine {
	r := gin.New()
	configureTrustedProxies(r, d)
	r.Use(recovery(d.Logger), requestMiddleware(d.Logger, d.Metrics, "control_plane"), cors(d.Config.AllowedOrigins), requestBodyLimit(d.Config.MaxBodyBytes))
	registerHealth(r, d)
	r.POST("/api/auth/login", func(c *gin.Context) { loginHandler(c, d, "shared") })
	r.POST("/api/admin/auth/login", func(c *gin.Context) { loginHandler(c, d, "admin") })
	r.POST("/api/console/auth/login", func(c *gin.Context) { loginHandler(c, d, "console") })
	r.POST("/api/auth/refresh", func(c *gin.Context) { refreshHandler(c, d, "shared") })
	r.POST("/api/admin/auth/refresh", func(c *gin.Context) { refreshHandler(c, d, "admin") })
	r.POST("/api/console/auth/refresh", func(c *gin.Context) { refreshHandler(c, d, "console") })
	registerPublicAccountRoutes(r, d)
	registerPublicCommercialRoutes(r, d)
	registerPaymentWebhookRoutes(r, d)
	r.GET("/status", func(c *gin.Context) { publicStatusHandler(c, d) })
	r.GET("/api/status", func(c *gin.Context) { publicStatusHandler(c, d) })
	authenticated := r.Group("/api")
	authenticated.Use(controlAuth(d))
	authenticated.GET("/auth/me", func(c *gin.Context) { meHandler(c, d) })
	authenticated.POST("/auth/logout", func(c *gin.Context) { logoutHandler(c, d, "shared") })
	authenticated.POST("/pricing/quote", func(c *gin.Context) { pricingQuoteHandler(c, d) })
	registerAuthenticatedAccountRoutes(authenticated, d, "shared")
	admin := authenticated.Group("/admin")
	admin.Use(requireAdmin(d))
	admin.GET("/auth/me", func(c *gin.Context) { meHandler(c, d) })
	admin.POST("/auth/logout", func(c *gin.Context) { logoutHandler(c, d, "admin") })
	registerAuthenticatedAccountRoutes(admin, d, "admin")
	registerAdmin(admin, d)
	registerAdminCommercialRoutes(admin, d)
	registerObservabilityRoutes(admin, d, true)
	registerSupportRoutes(admin, d, true)
	registerGovernanceRoutes(admin, d, true)
	registerSupplierAdminRoutes(admin, d)
	registerProviderQualityAdminRoutes(admin, d)
	registerMarketplaceLaunchAdminRoutes(admin, d)
	admin.POST("/api-keys/leak-check", func(c *gin.Context) {
		var in struct {
			APIKey string `json:"api_key"`
		}
		if c.ShouldBindJSON(&in) != nil || !apikey.LooksValid(strings.TrimSpace(in.APIKey)) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid RelayDock API key is required.")
			return
		}
		err := d.Store.FreezeAPIKeyByHash(c.Request.Context(), d.APIKeys.Hash(strings.TrimSpace(in.APIKey)), "suspected_leak", claimsFrom(c).Subject, c.ClientIP())
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"detected": false})
			return
		}
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"detected": true, "frozen": true})
	})
	console := authenticated.Group("/console")
	console.GET("/auth/me", func(c *gin.Context) { meHandler(c, d) })
	console.POST("/auth/logout", func(c *gin.Context) { logoutHandler(c, d, "console") })
	registerAuthenticatedAccountRoutes(console, d, "console")
	registerConsole(console, d)
	registerConsoleOnboardingRoutes(console, d)
	registerSupplierConsoleRoutes(console, d)
	registerObservabilityRoutes(console, d, false)
	registerSupportRoutes(console, d, false)
	registerGovernanceRoutes(console, d, false)
	return r
}

func registerHealth(r *gin.Engine, d Dependencies) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "relayedock", "version": version.Current, "commit": version.Commit})
	})
	r.GET("/startupz", func(c *gin.Context) {
		if d.StartupComplete != nil && !d.StartupComplete.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "starting"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "started"})
	})
	r.GET("/api/version", func(c *gin.Context) {
		metadata := version.Metadata()
		c.JSON(http.StatusOK, gin.H{
			// Keep name=RelayDock for clients that compare the legacy field.
			"name": metadata.CompatibilityName, "product": metadata.Product,
			"compatibility_name": metadata.CompatibilityName, "version": metadata.Version,
			"commit": metadata.Commit, "build_time": metadata.BuildTime,
		})
	})
	r.GET("/readyz", func(c *gin.Context) {
		if d.Draining != nil && d.Draining.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "draining"})
			return
		}
		if d.StartupComplete != nil && !d.StartupComplete.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "starting"})
			return
		}
		if d.Store == nil || d.Redis == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "postgres": d.Store != nil, "redis": d.Redis != nil})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 1500*time.Millisecond)
		defer cancel()
		dbErr := d.Store.Ping(ctx)
		redisErr := d.Redis.Ping(ctx).Err()
		if dbErr != nil || redisErr != nil {
			if dbErr != nil {
				_ = d.Store.RecordOperationalAlert(ctx, "POSTGRES_UNAVAILABLE", "CRITICAL", "PostgreSQL readiness checks are failing.", "dependency:postgres", map[string]any{"component": "postgres"})
			}
			if redisErr != nil {
				_ = d.Store.RecordOperationalAlert(ctx, "REDIS_UNAVAILABLE", "CRITICAL", "Redis readiness checks are failing.", "dependency:redis", map[string]any{"component": "redis"})
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "postgres": dbErr == nil, "redis": redisErr == nil})
			return
		}
		_ = d.Store.ResolveOperationalAlert(ctx, "dependency:postgres")
		_ = d.Store.ResolveOperationalAlert(ctx, "dependency:redis")
		c.JSON(http.StatusOK, gin.H{"status": "ready", "postgres": true, "redis": true})
	})
	r.GET("/metrics", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; version=0.0.4")
		if d.Metrics == nil {
			c.String(http.StatusServiceUnavailable, "# ModelDock metrics are not initialized.\n")
			return
		}
		if d.Store != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
			defer cancel()
			if gauges, err := d.Store.ObservabilityGauges(ctx); err == nil {
				d.Metrics.SetGauge("relaydock_dependency_postgres_up", 1)
				for name, value := range gauges {
					d.Metrics.SetGauge(name, value)
				}
			} else {
				d.Metrics.SetGauge("relaydock_dependency_postgres_up", 0)
			}
			if summaries, err := d.Store.ListProviderQualitySummaries(ctx); err == nil {
				for _, summary := range summaries {
					throughput := ""
					if summary.State.ThroughputTPS != nil {
						throughput = summary.State.ThroughputTPS.String()
					}
					d.Metrics.SetProviderQuality(summary.ProviderSlug, summary.State.Grade, summary.State.QualityScore.String(),
						summary.State.AvailabilityPct.String(), summary.State.ErrorRatePct.String(), summary.State.RateLimitedPct.String(),
						throughput, summary.State.RoutingMultiplier.String(), summary.State.TrafficCapBPS, summary.State.CircuitState == "OPEN")
				}
			}
		}
		if d.Redis != nil {
			pool := d.Redis.PoolStats()
			redisUp := int64(1)
			if err := d.Redis.Ping(c.Request.Context()).Err(); err != nil {
				redisUp = 0
			}
			d.Metrics.SetGauge("relaydock_dependency_redis_up", redisUp)
			d.Metrics.SetGauge("relaydock_redis_pool_total_connections", int64(pool.TotalConns))
			d.Metrics.SetGauge("relaydock_redis_pool_idle_connections", int64(pool.IdleConns))
			d.Metrics.SetGauge("relaydock_redis_pool_max_connections", int64(d.Config.RedisPoolSize))
		}
		d.Metrics.Write(c.Writer)
	})
}

func loginHandler(c *gin.Context, d Dependencies, realm string) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		openAIError(c, 400, "invalid_request", "A valid email and password are required.")
		return
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(in.Email))
	if !allowIdentity(c, d, "login", c.ClientIP()+"|"+normalizedEmail, d.Config.LoginRateLimit) {
		return
	}
	u, err := d.Store.UserByEmail(c.Request.Context(), normalizedEmail)
	passwordHash := dummyPasswordHash
	if err != nil {
		u = domain.User{}
	} else {
		passwordHash = u.PasswordHash
	}
	validPassword := authVerify(passwordHash, in.Password)
	validPassword = validPassword && err == nil && u.Status == "ACTIVE"
	if !validPassword {
		d.Store.Audit(c.Request.Context(), "", "security.login_failed", "user", "", c.ClientIP(), map[string]any{"result": "invalid_credentials"})
		openAIError(c, 401, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if u.AbuseStatus == "FROZEN" || u.PaymentRisk == "BLOCKED" {
		d.Store.Audit(c.Request.Context(), u.ID, "security.login_risk_blocked", "user", u.ID, c.ClientIP(), map[string]any{"abuse_status": u.AbuseStatus, "payment_risk": u.PaymentRisk})
		openAIError(c, http.StatusForbidden, "risk_frozen", "This account is frozen pending review.")
		return
	}
	mfaVerified := false
	if u.Role == "ADMIN" || u.Role == "SUPER_ADMIN" {
		if u.MFAEnabled {
			if strings.TrimSpace(in.MFACode) == "" {
				d.Store.Audit(c.Request.Context(), u.ID, "security.login_mfa_required", "user", u.ID, c.ClientIP(), nil)
				openAIError(c, 401, "mfa_required", "Email or password is incorrect.")
				return
			}
			envelope, secretErr := d.Store.TOTPSecret(c.Request.Context(), u.ID)
			secret, decryptErr := d.Vault.Decrypt(envelope, "mfa:"+u.ID)
			step, codeErr := auth.ValidateTOTP(secret, in.MFACode, time.Now().UTC())
			if secretErr != nil || decryptErr != nil || codeErr != nil || d.Store.ConsumeTOTPStep(c.Request.Context(), u.ID, step) != nil {
				d.Store.Audit(c.Request.Context(), u.ID, "security.login_mfa_failed", "user", u.ID, c.ClientIP(), nil)
				openAIError(c, 401, "mfa_required", "Email or password is incorrect.")
				return
			}
			mfaVerified = true
		}
	}
	if strings.HasPrefix(u.PasswordHash, "$2") {
		if upgraded, hashErr := auth.HashPassword(in.Password); hashErr == nil {
			d.Store.UpgradePasswordHash(c.Request.Context(), u.ID, upgraded)
		}
	}
	signed, expires, err := d.Auth.IssueVersioned(u.ID, u.Email, u.Role, u.SessionVersion, mfaVerified)
	if err != nil {
		openAIError(c, 500, "internal_error", "Could not create the session.")
		return
	}
	refreshToken, refreshExpires, err := d.Auth.IssueRefreshVersioned(u.ID, u.Email, u.Role, u.SessionVersion, mfaVerified)
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
	d.Store.Audit(c.Request.Context(), u.ID, "security.login_succeeded", "user", u.ID, c.ClientIP(), map[string]any{"mfa": mfaVerified})
	u.PasswordHash = ""
	c.JSON(200, gin.H{"user": u, "expires_at": expires, "csrf_token": csrf,
		"mfa_enrollment_required": d.Config.AdminMFARequired && (u.Role == "ADMIN" || u.Role == "SUPER_ADMIN") && !u.MFAEnabled})
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
	if err != nil || u.Status != "ACTIVE" || u.SessionVersion != claims.SessionVersion {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "The session user is unavailable.")
		return
	}
	accessToken, accessExpires, err := d.Auth.IssueVersioned(u.ID, u.Email, u.Role, u.SessionVersion, claims.MFA)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not refresh the session.")
		return
	}
	refreshToken, refreshExpires, err := d.Auth.IssueRefreshVersioned(u.ID, u.Email, u.Role, u.SessionVersion, claims.MFA)
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
	registerAdminPaymentRoutes(g, d)
	registerAdminSubscriptionRoutes(g, d)
	registerAdminFinanceRoutes(g, d)
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
		p.ProviderType, _ = normalizeProviderType(p.ProviderType)
		if p.ProviderType == "" {
			openAIError(c, 400, "invalid_request", "provider_type is not supported by the ModelDock provider registry.")
			return
		}
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
		p.ProviderType, _ = normalizeProviderType(p.ProviderType)
		if p.ProviderType == "" {
			openAIError(c, 400, "invalid_request", "provider_type is not supported by the ModelDock provider registry.")
			return
		}
		v, err := d.Store.UpdateProvider(c.Request.Context(), p)
		audit(c, d, "provider.update", "provider", p.ID, v)
		respond(c, v, err)
	})
	g.POST("/providers/:id/kill-switch", func(c *gin.Context) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "enabled is required.")
			return
		}
		v, err := d.Store.SetProviderKillSwitch(c.Request.Context(), c.Param("id"), in.Enabled, stringPtr(claimsFrom(c).Subject))
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
				err = healthCheckProvider(ctx, d, provider, providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)})
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
	g.GET("/marketplace/providers", func(c *gin.Context) {
		v, err := d.Store.ListMarketplaceListings(c.Request.Context())
		respondList(c, v, err)
	})
	g.POST("/marketplace/providers", func(c *gin.Context) {
		var listing domain.MarketplaceListing
		if c.ShouldBindJSON(&listing) != nil || listing.ProviderID == "" || !validBaseURL(listing.Endpoint) ||
			!validMarketplaceStatus(listing.Status) || listing.Uptime < 0 || listing.Uptime > 100 {
			openAIError(c, 400, "invalid_request", "provider_id, endpoint, valid status, and uptime between 0 and 100 are required.")
			return
		}
		listing.ID = ""
		out, err := d.Store.UpsertMarketplaceListing(c.Request.Context(), listing)
		audit(c, d, "marketplace_listing.create", "marketplace_listing", out.ID, out)
		respondCreated(c, out, err)
	})
	g.PUT("/marketplace/providers/:id", func(c *gin.Context) {
		var listing domain.MarketplaceListing
		if c.ShouldBindJSON(&listing) != nil || listing.ProviderID == "" || !validBaseURL(listing.Endpoint) ||
			!validMarketplaceStatus(listing.Status) || listing.Uptime < 0 || listing.Uptime > 100 {
			openAIError(c, 400, "invalid_request", "provider_id, endpoint, valid status, and uptime between 0 and 100 are required.")
			return
		}
		listing.ID = c.Param("id")
		out, err := d.Store.UpsertMarketplaceListing(c.Request.Context(), listing)
		audit(c, d, "marketplace_listing.update", "marketplace_listing", out.ID, out)
		respond(c, out, err)
	})
	g.DELETE("/marketplace/providers/:id", func(c *gin.Context) {
		err := d.Store.DeleteMarketplaceListing(c.Request.Context(), c.Param("id"))
		audit(c, d, "marketplace_listing.delete", "marketplace_listing", c.Param("id"), nil)
		respondNoContent(c, err)
	})

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
	g.POST("/models", func(c *gin.Context) {
		var model domain.Model
		if c.ShouldBindJSON(&model) != nil || model.ProviderID == "" || strings.TrimSpace(model.ProviderModelID) == "" ||
			!validModelScore(model.LatencyScore) || !validModelScore(model.QualityScore) {
			openAIError(c, 400, "invalid_request", "provider_id, provider_model_id, and scores between 0 and 100 are required.")
			return
		}
		// Model prices are optional at creation; omission is the explicit
		// business rule that maps them to zero before money validation.
		if strings.TrimSpace(model.InputPrice.String()) == "" {
			model.InputPrice = domain.MustDecimal("0")
		}
		if strings.TrimSpace(model.OutputPrice.String()) == "" {
			model.OutputPrice = domain.MustDecimal("0")
		}
		model.Enabled = true
		out, err := d.Store.CreateModel(c.Request.Context(), model)
		if err == nil && (validPositiveDecimal(model.InputPrice) || validPositiveDecimal(model.OutputPrice)) {
			_, err = d.Store.CreateModelPrice(c.Request.Context(), domain.ModelPrice{ModelID: out.ID, InputPrice: model.InputPrice,
				OutputPrice: model.OutputPrice, Currency: firstNonEmpty(model.PriceCurrency, "USD"), Source: "manual"})
			if err == nil {
				out, err = d.Store.ModelByID(c.Request.Context(), out.ID)
			}
		}
		audit(c, d, "model.create", "model", out.ID, out)
		respondCreated(c, out, err)
	})
	g.PUT("/models/:id", func(c *gin.Context) {
		var model domain.Model
		if c.ShouldBindJSON(&model) != nil || model.ProviderID == "" || strings.TrimSpace(model.ProviderModelID) == "" ||
			!validModelScore(model.LatencyScore) || !validModelScore(model.QualityScore) {
			openAIError(c, 400, "invalid_request", "provider_id, provider_model_id, and scores between 0 and 100 are required.")
			return
		}
		model.ID = c.Param("id")
		out, err := d.Store.UpdateModel(c.Request.Context(), model)
		audit(c, d, "model.update", "model", model.ID, out)
		respond(c, out, err)
	})
	g.DELETE("/models/:id", func(c *gin.Context) {
		err := d.Store.DisableModel(c.Request.Context(), c.Param("id"))
		audit(c, d, "model.disable", "model", c.Param("id"), nil)
		respondNoContent(c, err)
	})
	g.GET("/models/:id/prices", func(c *gin.Context) {
		v, err := d.Store.ListModelPrices(c.Request.Context(), c.Param("id"))
		respondList(c, v, err)
	})
	g.GET("/pricing/provider-cost-price-books", func(c *gin.Context) {
		v, err := d.Store.ListProviderCostPriceBooks(c.Request.Context(), c.Query("provider_id"), c.Query("model_id"))
		respondList(c, v, err)
	})
	g.POST("/pricing/provider-cost-price-books", func(c *gin.Context) {
		var v domain.ProviderCostChangeRequest
		if c.ShouldBindJSON(&v) != nil {
			openAIError(c, 400, "invalid_request", "A valid provider cost price book is required.")
			return
		}
		if v.SourceType == "" {
			v.SourceType = "MANUAL"
		}
		if v.IdempotencyKey == "" {
			v.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		out, replayed, err := d.Store.CreateProviderCostChange(c.Request.Context(), v, stringPtr(claimsFrom(c).Subject))
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"change_request": out, "replayed": replayed})
	})
	g.POST("/pricing/provider-cost-changes/manual", func(c *gin.Context) {
		var change domain.ProviderCostChangeRequest
		if c.ShouldBindJSON(&change) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid manual Provider cost change is required.")
			return
		}
		change.SourceType = "MANUAL"
		if change.IdempotencyKey == "" {
			change.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		out, replayed, err := d.Store.CreateProviderCostChange(c.Request.Context(), change, stringPtr(claimsFrom(c).Subject))
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"change_request": out, "replayed": replayed})
	})
	g.POST("/pricing/provider-cost-changes/fetch", func(c *gin.Context) {
		var in struct {
			ProviderID string `json:"provider_id"`
			SourceURL  string `json:"source_url"`
			BatchKey   string `json:"batch_idempotency_key"`
		}
		if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.ProviderID) == "" || strings.TrimSpace(in.SourceURL) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "provider_id and source_url are required.")
			return
		}
		if in.BatchKey == "" {
			in.BatchKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		if in.BatchKey == "" || len(in.BatchKey) > 190 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A batch idempotency key of at most 190 characters is required.")
			return
		}
		provider, err := d.Store.ProviderByID(c.Request.Context(), in.ProviderID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		result, err := pricesync.FetchAPI(c.Request.Context(), in.SourceURL, in.ProviderID, in.BatchKey, providerPricingHosts(provider))
		if err != nil {
			openAIError(c, http.StatusUnprocessableEntity, "provider_pricing_fetch_failed", "The approved Provider pricing source could not be fetched or validated.")
			return
		}
		for index := range result.Changes {
			result.Changes[index].SourceReference = result.SourceReference
		}
		changes, replayed, err := d.Store.CreateProviderCostChanges(c.Request.Context(), result.Changes, stringPtr(claimsFrom(c).Subject))
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"change_requests": changes, "replayed_count": replayed, "source_reference": result.SourceReference})
	})
	g.POST("/pricing/provider-cost-changes/import-csv", func(c *gin.Context) {
		providerID := strings.TrimSpace(c.Query("provider_id"))
		batchKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		if providerID == "" || batchKey == "" || len(batchKey) > 190 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "provider_id and an Idempotency-Key of at most 190 characters are required.")
			return
		}
		if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(c.GetHeader("Content-Type"), ";")[0])); mediaType != "text/csv" && mediaType != "application/csv" {
			openAIError(c, http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be text/csv.")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, pricesync.MaxFeedBytes))
		if err != nil {
			openAIError(c, http.StatusRequestEntityTooLarge, "request_too_large", "The CSV import exceeds the 1 MiB limit.")
			return
		}
		hash := sha256.Sum256(body)
		reference := "csv:sha256:" + hex.EncodeToString(hash[:])
		changes, err := pricesync.ParseCSV(body, providerID, batchKey, reference)
		if err != nil {
			openAIError(c, http.StatusBadRequest, "invalid_csv", err.Error())
			return
		}
		created, replayed, err := d.Store.CreateProviderCostChanges(c.Request.Context(), changes, stringPtr(claimsFrom(c).Subject))
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"change_requests": created, "replayed_count": replayed, "source_reference": reference})
	})
	g.GET("/pricing/provider-cost-changes", func(c *gin.Context) {
		v, err := d.Store.ListProviderCostChanges(c.Request.Context(), c.Query("status"))
		respondList(c, v, err)
	})
	g.POST("/pricing/provider-cost-changes/:id/review", func(c *gin.Context) {
		var in struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "decision and reason are required.")
			return
		}
		v, err := d.Store.ReviewProviderCostChange(c.Request.Context(), c.Param("id"), in.Decision, in.Reason, stringPtr(claimsFrom(c).Subject))
		respond(c, v, err)
	})
	g.GET("/pricing/byok-service-fee-policies", func(c *gin.Context) {
		policies, err := d.Store.ListBYOKServiceFeePolicies(c.Request.Context())
		respondList(c, policies, err)
	})
	g.POST("/pricing/byok-service-fee-policies", func(c *gin.Context) {
		var policy domain.BYOKServiceFeePolicy
		if c.ShouldBindJSON(&policy) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid BYOK service fee policy is required.")
			return
		}
		created, err := d.Store.CreateBYOKServiceFeePolicy(c.Request.Context(), policy, stringPtr(claimsFrom(c).Subject))
		respondCreated(c, created, err)
	})
	g.DELETE("/pricing/byok-service-fee-policies/:id", func(c *gin.Context) {
		respondNoContent(c, d.Store.DisableBYOKServiceFeePolicy(c.Request.Context(), c.Param("id"), stringPtr(claimsFrom(c).Subject)))
	})
	g.GET("/pricing/customer-retail-price-books", func(c *gin.Context) {
		v, err := d.Store.ListCustomerRetailPriceBooks(c.Request.Context(), c.Query("organization_id"), c.Query("provider_id"), c.Query("model_id"))
		respondList(c, v, err)
	})
	g.POST("/pricing/customer-retail-price-books", func(c *gin.Context) {
		var in struct {
			domain.CustomerRetailPriceBook
			ForceOverride bool   `json:"force_override"`
			Confirmation  string `json:"confirmation"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "A valid customer retail price book is required.")
			return
		}
		out, err := d.Store.CreateCustomerRetailPriceBook(c.Request.Context(), in.CustomerRetailPriceBook, stringPtr(claimsFrom(c).Subject), in.ForceOverride, in.Confirmation)
		if err != nil {
			respond(c, nil, err)
			return
		}
		audit(c, d, "pricing.customer_retail_price_book.create", "customer_retail_price_book", out.ID, gin.H{"price": out, "force_override": in.ForceOverride})
		respondCreated(c, out, nil)
	})
	g.GET("/pricing/organization-price-plans", func(c *gin.Context) {
		v, err := d.Store.ListOrganizationPricePlans(c.Request.Context(), c.Query("organization_id"))
		respondList(c, v, err)
	})
	g.POST("/pricing/organization-price-plans", func(c *gin.Context) {
		var in struct {
			domain.OrganizationPricePlan
			ForceOverride bool   `json:"force_override"`
			Confirmation  string `json:"confirmation"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "A valid organization price plan is required.")
			return
		}
		out, err := d.Store.CreateOrganizationPricePlan(c.Request.Context(), in.OrganizationPricePlan, stringPtr(claimsFrom(c).Subject), in.ForceOverride, in.Confirmation)
		if err != nil {
			respond(c, nil, err)
			return
		}
		audit(c, d, "pricing.organization_price_plan.create", "organization_price_plan", out.ID, gin.H{"price": out, "force_override": in.ForceOverride})
		respondCreated(c, out, nil)
	})
	g.GET("/pricing/margin-policies", func(c *gin.Context) {
		v, err := d.Store.ListPricingMarginPolicies(c.Request.Context())
		respondList(c, v, err)
	})
	g.POST("/pricing/margin-policies", func(c *gin.Context) {
		var v domain.PricingMarginPolicy
		if c.ShouldBindJSON(&v) != nil {
			openAIError(c, 400, "invalid_request", "A valid pricing margin policy is required.")
			return
		}
		out, err := d.Store.CreatePricingMarginPolicy(c.Request.Context(), v, stringPtr(claimsFrom(c).Subject))
		if err != nil {
			respond(c, nil, err)
			return
		}
		audit(c, d, "pricing.margin_policy.create", "pricing_margin_policy", out.ID, out)
		respondCreated(c, out, nil)
	})
	g.GET("/pricing/promotion-credits", func(c *gin.Context) {
		v, err := d.Store.ListPromotionCredits(c.Request.Context(), c.Query("organization_id"))
		respondList(c, v, err)
	})
	g.POST("/pricing/promotion-credits", func(c *gin.Context) {
		var v domain.PromotionCredit
		if c.ShouldBindJSON(&v) != nil {
			openAIError(c, 400, "invalid_request", "A valid non-refundable promotion credit is required.")
			return
		}
		if v.IdempotencyKey == "" {
			v.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		out, err := d.Store.CreatePromotionCredit(c.Request.Context(), v, stringPtr(claimsFrom(c).Subject))
		if err != nil {
			respond(c, nil, err)
			return
		}
		audit(c, d, "pricing.promotion_credit.create", "promotion_credit", out.ID, out)
		respondCreated(c, out, nil)
	})
	g.POST("/pricing/quotes", func(c *gin.Context) { pricingQuoteHandler(c, d) })
	g.POST("/pricing/quote", func(c *gin.Context) { pricingQuoteHandler(c, d) })
	g.POST("/models/:id/prices", func(c *gin.Context) {
		var v domain.ModelPrice
		if c.ShouldBindJSON(&v) != nil || invalidOrNegativeDecimal(v.InputPrice) || invalidOrNegativeDecimal(v.CachedInputPrice) || invalidOrNegativeDecimal(v.OutputPrice) {
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
	g.GET("/routing-rules", func(c *gin.Context) {
		v, err := d.Store.ListRoutingRules(c.Request.Context(), c.Query("project_id"))
		respondList(c, v, err)
	})
	g.POST("/routing-rules", func(c *gin.Context) { upsertRoutingRule(c, d, true) })
	g.PUT("/routing-rules/:id", func(c *gin.Context) { upsertRoutingRule(c, d, false) })
	g.DELETE("/routing-rules/:id", func(c *gin.Context) {
		err := d.Store.DeleteRoutingRule(c.Request.Context(), c.Param("id"))
		audit(c, d, "routing_rule.delete", "routing_rule", c.Param("id"), nil)
		respondNoContent(c, err)
	})

	g.GET("/teams", func(c *gin.Context) {
		v, err := d.Store.ListTeams(c.Request.Context(), c.Query("organization_id"))
		respondList(c, v, err)
	})
	g.POST("/teams", func(c *gin.Context) { upsertTeam(c, d, true) })
	g.PUT("/teams/:id", func(c *gin.Context) { upsertTeam(c, d, false) })
	g.DELETE("/teams/:id", func(c *gin.Context) {
		err := d.Store.DeleteTeam(c.Request.Context(), c.Param("id"))
		audit(c, d, "team.archive", "team", c.Param("id"), nil)
		respondNoContent(c, err)
	})
	g.GET("/teams/:id/members", func(c *gin.Context) {
		v, err := d.Store.ListTeamMembers(c.Request.Context(), c.Param("id"))
		respondList(c, v, err)
	})
	g.PUT("/teams/:id/members/:userID", func(c *gin.Context) {
		var member domain.TeamMembership
		if c.ShouldBindJSON(&member) != nil {
			openAIError(c, 400, "invalid_request", "A valid team membership body is required.")
			return
		}
		member.TeamID, member.UserID = c.Param("id"), c.Param("userID")
		out, err := d.Store.UpsertTeamMember(c.Request.Context(), member)
		audit(c, d, "team.member_set", "team", member.TeamID, out)
		respond(c, out, err)
	})
	g.DELETE("/teams/:id/members/:userID", func(c *gin.Context) {
		err := d.Store.DeleteTeamMember(c.Request.Context(), c.Param("id"), c.Param("userID"))
		audit(c, d, "team.member_delete", "team", c.Param("id"), gin.H{"user_id": c.Param("userID")})
		respondNoContent(c, err)
	})

	g.GET("/wallets", func(c *gin.Context) { v, err := d.Store.ListWallets(c.Request.Context()); respondList(c, v, err) })
	g.PUT("/wallets/:id", func(c *gin.Context) {
		var in struct {
			BillingMode    string          `json:"billing_mode"`
			CreditLimit    domain.Decimal  `json:"credit_limit"`
			RiskLimit      *domain.Decimal `json:"risk_limit"`
			CreditEnforced *bool           `json:"credit_enforced"`
			Status         string          `json:"status"`
		}
		if c.ShouldBindJSON(&in) != nil || !validBillingMode(in.BillingMode) || !validWalletStatus(in.Status) || invalidOrNegativeDecimal(in.CreditLimit) || (in.RiskLimit != nil && invalidOrNegativeDecimal(*in.RiskLimit)) {
			openAIError(c, 400, "invalid_request", "billing_mode, status, and non-negative credit/risk limits are required.")
			return
		}
		wallet, err := d.Store.WalletByID(c.Request.Context(), c.Param("id"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		wallet.BillingMode, wallet.CreditLimit, wallet.Status = in.BillingMode, in.CreditLimit, in.Status
		if in.RiskLimit != nil {
			wallet.RiskLimit = *in.RiskLimit
		}
		if in.CreditEnforced != nil {
			wallet.CreditEnforced = *in.CreditEnforced
		}
		out, err := d.Store.UpdateWallet(c.Request.Context(), wallet)
		audit(c, d, "wallet.update", "wallet", wallet.ID, out)
		respond(c, out, err)
	})
	g.GET("/wallets/:id/transactions", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListWalletTransactions(c.Request.Context(), c.Param("id"), limit, offset)
		respondList(c, v, err)
	})
	g.GET("/wallets/:id/funding-operations", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListFundingOperations(c.Request.Context(), c.Param("id"), limit, offset)
		respondList(c, v, err)
	})
	g.GET("/wallets/:id/journals", func(c *gin.Context) {
		limit, offset := page(c)
		v, err := d.Store.ListJournals(c.Request.Context(), c.Param("id"), limit, offset)
		respondList(c, v, err)
	})
	g.POST("/funding-operations/:id/late-usage", func(c *gin.Context) {
		var in struct {
			InputTokens       int64  `json:"input_tokens"`
			CachedInputTokens int64  `json:"cached_input_tokens"`
			OutputTokens      int64  `json:"output_tokens"`
			UsageSource       string `json:"usage_source"`
			IdempotencyKey    string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&in) != nil || in.InputTokens < 0 || in.CachedInputTokens < 0 || in.OutputTokens < 0 || in.CachedInputTokens > in.InputTokens {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Non-negative, internally consistent usage is required.")
			return
		}
		if in.IdempotencyKey == "" {
			in.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		if in.IdempotencyKey == "" || len(in.IdempotencyKey) > 200 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "An Idempotency-Key of at most 200 characters is required.")
			return
		}
		claims := claimsFrom(c)
		out, err := d.Store.AdjustLateFundingUsage(c.Request.Context(), store.LateUsageRequest{OperationID: c.Param("id"),
			IdempotencyKey: in.IdempotencyKey, InputTokens: in.InputTokens, CachedInput: in.CachedInputTokens,
			OutputTokens: in.OutputTokens, UsageSource: firstNonEmpty(in.UsageSource, "PROVIDER_LATE"), CreatedBy: stringPtr(claims.Subject)})
		respond(c, out, err)
	})
	g.POST("/funding-operations/:id/reversals", func(c *gin.Context) {
		var in struct {
			Reason         string `json:"reason"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A reversal reason is required.")
			return
		}
		if in.IdempotencyKey == "" {
			in.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		if in.IdempotencyKey == "" || len(in.IdempotencyKey) > 200 || strings.TrimSpace(in.Reason) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A reason and Idempotency-Key of at most 200 characters are required.")
			return
		}
		claims := claimsFrom(c)
		out, err := d.Store.ReverseFunding(c.Request.Context(), c.Param("id"), in.IdempotencyKey, strings.TrimSpace(in.Reason), stringPtr(claims.Subject))
		respond(c, out, err)
	})
	g.POST("/wallets/:id/topups", func(c *gin.Context) { createWalletAdjustment(c, d, "TOPUP") })
	g.POST("/wallets/:id/adjustments", func(c *gin.Context) { createWalletAdjustment(c, d, "ADJUSTMENT") })
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
		err := d.Store.UpdateUserStatusAudited(c.Request.Context(), c.Param("id"), in.Status, claimsFrom(c).Subject, c.ClientIP())
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
	registerConsolePaymentRoutes(g, d)
	registerConsoleSubscriptionRoutes(g, d)
	registerConsoleFinanceRoutes(g, d)
	g.GET("/byok/credentials", func(c *gin.Context) {
		organizationID := strings.TrimSpace(c.Query("organization_id"))
		if organizationID == "" {
			openAIError(c, 400, "invalid_request", "organization_id is required.")
			return
		}
		if err := d.Store.CheckOrganizationPricingAccess(c.Request.Context(), claimsFrom(c).Subject, organizationID); err != nil {
			respond(c, nil, err)
			return
		}
		v, err := d.Store.ListBYOKCredentials(c.Request.Context(), organizationID)
		respondList(c, v, err)
	})
	g.POST("/byok/credentials", func(c *gin.Context) {
		var in struct {
			ProviderID         string `json:"provider_id"`
			ProjectID          string `json:"project_id"`
			Name               string `json:"name"`
			Secret             string `json:"secret"`
			TermsVersion       string `json:"terms_version"`
			OwnershipConfirmed bool   `json:"ownership_confirmed"`
		}
		if c.ShouldBindJSON(&in) != nil || in.ProviderID == "" || in.ProjectID == "" || in.Name == "" || strings.TrimSpace(in.Secret) == "" || !in.OwnershipConfirmed || in.TermsVersion == "" {
			openAIError(c, 400, "invalid_request", "provider_id, project_id, name, secret, terms_version, and ownership confirmation are required.")
			return
		}
		project, ok := requireProjectIDAccess(c, d, false, in.ProjectID, "DEVELOPER")
		if !ok {
			return
		}
		credentialID := id.UUID()
		encrypted, err := d.Vault.Encrypt(strings.TrimSpace(in.Secret), credentialID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		now := time.Now().UTC()
		actor := claimsFrom(c).Subject
		owner := project.OrganizationID
		credential := domain.Credential{ID: credentialID, ProviderID: in.ProviderID, Name: in.Name, CredentialType: "api_key", EncryptedSecret: encrypted, SecretLast4: secretcrypto.Last4(strings.TrimSpace(in.Secret)), Status: "ACTIVE", Weight: 100, MaxConcurrency: 10, CredentialOwner: domain.CredentialOwnerCustomer, OwnerOrganizationID: &owner, OwnershipConfirmedAt: &now, OwnershipConfirmedBy: &actor, OwnershipTermsVersion: in.TermsVersion}
		out, err := d.Store.CreateBYOKCredential(c.Request.Context(), credential, in.ProjectID)
		respondCreated(c, out, err)
	})
	g.DELETE("/byok/credentials/:id", func(c *gin.Context) {
		organizationID := strings.TrimSpace(c.Query("organization_id"))
		if organizationID == "" {
			openAIError(c, 400, "invalid_request", "organization_id is required.")
			return
		}
		if err := d.Store.CheckOrganizationPricingAccess(c.Request.Context(), claimsFrom(c).Subject, organizationID); err != nil {
			respond(c, nil, err)
			return
		}
		respondNoContent(c, d.Store.DisableBYOKCredential(c.Request.Context(), c.Param("id"), organizationID, stringPtr(claimsFrom(c).Subject)))
	})
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
			project, ok := requireProjectIDAccess(c, d, false, projectID, "VIEWER")
			if !ok {
				return
			}
			if err := d.Store.RequireBooleanEntitlement(c.Request.Context(), project.OrganizationID, "cost_analysis"); err != nil {
				respond(c, nil, err)
				return
			}
			v, err := consoleProjectUsage(c.Request.Context(), d, projectID, days(c))
			respond(c, v, err)
			return
		}
		if err := requireUserOrganizationCapability(c, d, uid, "cost_analysis"); err != nil {
			respond(c, nil, err)
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
			project, ok := requireProjectIDAccess(c, d, false, projectID, "VIEWER")
			if !ok {
				return
			}
			v, err = d.Store.ListProjectRequestLogs(c.Request.Context(), projectID, &uid, limit, offset)
			if err == nil {
				v, err = enforceLogRetention(c, d, project.OrganizationID, v)
			}
		} else {
			v, err = d.Store.ListRequestLogs(c.Request.Context(), &uid, limit, offset)
			if err == nil {
				v, err = enforceUserLogRetention(c, d, uid, v)
			}
		}
		sanitizeConsoleRequestLogs(v)
		respondList(c, v, err)
	}
	g.GET("/request-logs", projectLogs)
	g.GET("/logs", projectLogs)
	g.GET("/models", func(c *gin.Context) {
		projectID := strings.TrimSpace(c.Query("project_id"))
		// Project-scoped responses include only the provider/model correlation
		// keys required to join an alias to the public catalog. Provider base
		// URLs, credential groups, and fallback configuration remain confined to
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
					models = append(models, consoleProjectModel(route))
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

func consoleProjectModel(route domain.ProjectModelRoute) gin.H {
	return gin.H{
		"id": route.Alias, "id_alias": route.Alias, "alias": route.Alias,
		"display_name": route.Alias, "enabled": true, "capabilities": []string{}, "context_window": nil,
		"provider_id": route.ProviderID, "upstream_model": route.UpstreamModel,
	}
}

func requireUserOrganizationCapability(c *gin.Context, d Dependencies, userID, capability string) error {
	organizations, err := d.Store.ListOrganizations(c.Request.Context(), &userID, 200, 0)
	if err != nil {
		return err
	}
	for _, organization := range organizations {
		if err = d.Store.RequireBooleanEntitlement(c.Request.Context(), organization.ID, capability); err == nil {
			return nil
		}
	}
	return store.ErrEntitlementRequired
}

func enforceUserLogRetention(c *gin.Context, d Dependencies, userID string, logs []domain.RequestLog) ([]domain.RequestLog, error) {
	organizations, err := d.Store.ListOrganizations(c.Request.Context(), &userID, 200, 0)
	if err != nil {
		return nil, err
	}
	retention := make(map[string]time.Time, len(organizations))
	for _, organization := range organizations {
		entitlements, entitlementErr := d.Store.EffectiveEntitlements(c.Request.Context(), organization.ID)
		if entitlementErr != nil {
			return nil, entitlementErr
		}
		retention[organization.ID] = time.Now().UTC().AddDate(0, 0, -int(entitlements.LogRetentionDays))
	}
	out := logs[:0]
	for _, item := range logs {
		if cutoff, ok := retention[item.OrganizationID]; ok && !item.CreatedAt.Before(cutoff) {
			out = append(out, item)
		}
	}
	return out, nil
}

func enforceLogRetention(c *gin.Context, d Dependencies, organizationID string, logs []domain.RequestLog) ([]domain.RequestLog, error) {
	entitlements, err := d.Store.EffectiveEntitlements(c.Request.Context(), organizationID)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -int(entitlements.LogRetentionDays))
	out := logs[:0]
	for _, item := range logs {
		if !item.CreatedAt.Before(cutoff) {
			out = append(out, item)
		}
	}
	return out, nil
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
			err = healthCheckProvider(ctx, d, p, providers.Credential{Secret: strings.TrimSpace(in.Secret), OrganizationID: deref(in.OrganizationID), ProjectID: deref(in.ProjectID)})
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
				err = healthCheckProvider(ctx, d, provider, providers.Credential{Secret: candidate.APIKey, OrganizationID: deref(candidate.OrganizationID), ProjectID: deref(candidate.ProjectID)})
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
	err = healthCheckProvider(ctx, d, provider, providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)})
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
	adapter, adapterErr := providerAdapter(d, p.ProviderType)
	if adapterErr != nil {
		cancel()
		openAIError(c, 422, "provider_adapter_unavailable", "The configured provider adapter is unavailable.")
		return
	}
	models, err := adapter.ListModels(ctx, p.BaseURL, providers.Credential{Secret: secret, OrganizationID: deref(cred.OrganizationID), ProjectID: deref(cred.ProjectID)})
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
		UserID            string          `json:"user_id"`
		OrganizationID    string          `json:"organization_id"`
		ProjectID         string          `json:"project_id"`
		TeamID            string          `json:"team_id"`
		Name              string          `json:"name"`
		Environment       string          `json:"environment"`
		ExpiresAt         string          `json:"expires_at"`
		RateLimitRPM      int             `json:"rate_limit_rpm"`
		RateLimitTPM      int             `json:"rate_limit_tpm"`
		MonthlyTokenLimit *int64          `json:"monthly_token_limit"`
		MonthlyCostLimit  *domain.Decimal `json:"monthly_cost_limit"`
		AllowedModels     []string        `json:"allowed_models"`
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
	k := domain.APIKey{UserID: in.UserID, OrganizationID: in.OrganizationID, ProjectID: in.ProjectID, TeamID: in.TeamID, Name: in.Name, Environment: in.Environment, KeyPrefix: prefix, KeyHash: hash, ExpiresAt: expiresAt, RateLimitRPM: in.RateLimitRPM, RateLimitTPM: in.RateLimitTPM, MonthlyTokenLimit: in.MonthlyTokenLimit, MonthlyCostLimit: in.MonthlyCostLimit, AllowedModels: in.AllowedModels}
	out, err := d.Store.CreateProjectAPIKey(c.Request.Context(), k)
	if err != nil {
		if d.Logger != nil {
			d.Logger.Error("api_key_creation_failed", "error", err, "user_id", in.UserID, "organization_id", in.OrganizationID, "project_id", in.ProjectID)
		}
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
	if errors.Is(err, store.ErrRiskFrozen) {
		openAIError(c, http.StatusForbidden, "risk_frozen", "This account, organization, or API key is frozen pending review.")
		return
	}
	if errors.Is(err, store.ErrRiskRestricted) {
		openAIError(c, http.StatusForbidden, "risk_restricted", "This operation is restricted pending risk review.")
		return
	}
	if errors.Is(err, store.ErrKeyCreationRate) {
		openAIError(c, http.StatusTooManyRequests, "api_key_creation_rate_limited", "Too many API keys were created recently.")
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
	if errors.Is(err, store.ErrPricingUnavailable) {
		openAIError(c, http.StatusUnprocessableEntity, "pricing_unavailable", "No approved pricing is available for this provider and model.")
		return
	}
	if errors.Is(err, store.ErrProviderNotContracted) {
		openAIError(c, http.StatusForbidden, "provider_pricing_disabled", "The provider contract, region, or pricing switch does not allow this operation.")
		return
	}
	if errors.Is(err, store.ErrNegativeMargin) {
		openAIError(c, http.StatusUnprocessableEntity, "negative_margin", "The retail price is below the configured minimum gross margin.")
		return
	}
	if errors.Is(err, store.ErrForceOverrideConfirmation) {
		openAIError(c, http.StatusConflict, "margin_override_confirmation_required", "A second confirmation is required before forcing a margin override.")
		return
	}
	if errors.Is(err, store.ErrIdempotencyConflict) {
		openAIError(c, http.StatusConflict, "idempotency_conflict", "The idempotency key was already used with different operation data.")
		return
	}
	if errors.Is(err, store.ErrPriceChangeSelfReview) {
		openAIError(c, http.StatusConflict, "price_change_self_review_forbidden", "Provider cost changes require review by a different administrator.")
		return
	}
	if errors.Is(err, store.ErrEntitlementExceeded) {
		openAIError(c, http.StatusForbidden, "subscription_entitlement_exceeded", "The current subscription limit has been reached.")
		return
	}
	if errors.Is(err, store.ErrEntitlementRequired) {
		openAIError(c, http.StatusForbidden, "subscription_entitlement_required", "The current subscription does not include this capability.")
		return
	}
	if errors.Is(err, store.ErrSubscriptionState) {
		openAIError(c, http.StatusConflict, "subscription_state_conflict", "The subscription does not allow this state transition.")
		return
	}
	if errors.Is(err, store.ErrPaymentState) {
		openAIError(c, http.StatusConflict, "payment_state_conflict", "The payment order does not allow this state transition.")
		return
	}
	if errors.Is(err, store.ErrPaymentMismatch) {
		openAIError(c, http.StatusUnprocessableEntity, "payment_mismatch", "The verified provider payment does not match the local order.")
		return
	}
	if errors.Is(err, payment.ErrProviderDisabled) || errors.Is(err, payment.ErrContractUnavailable) || errors.Is(err, payment.ErrRegionNotAllowed) {
		openAIError(c, http.StatusForbidden, "payment_provider_unavailable", "The payment provider switch, contract, or allowed region does not permit this operation.")
		return
	}
	if errors.Is(err, payment.ErrProviderNotRegistered) || errors.Is(err, payment.ErrWebhookUnsupported) || errors.Is(err, payment.ErrReconcileUnsupported) {
		openAIError(c, http.StatusUnprocessableEntity, "payment_provider_unsupported", "The requested payment provider capability is unavailable.")
		return
	}
	if errors.Is(err, store.ErrFundingInProgress) {
		openAIError(c, http.StatusConflict, "funding_in_progress", "The funding operation has not reached a terminal settlement state.")
		return
	}
	if errors.Is(err, store.ErrFundingTerminal) {
		openAIError(c, http.StatusConflict, "funding_terminal", "The funding operation cannot accept this transition.")
		return
	}
	if errors.Is(err, store.ErrPricingCurrencyMismatch) {
		openAIError(c, http.StatusUnprocessableEntity, "pricing_currency_mismatch", "Provider cost and retail price currencies must match until an approved FX pricing policy is configured.")
		return
	}
	if errors.Is(err, store.ErrFinanceState) {
		openAIError(c, http.StatusConflict, "finance_state_conflict", "The financial record does not allow this operation.")
		return
	}
	if errors.Is(err, store.ErrRefundNotEligible) {
		openAIError(c, http.StatusUnprocessableEntity, "refund_not_eligible", "The requested amount is not eligible for a refund.")
		return
	}
	if errors.Is(err, store.ErrInvoiceAmount) {
		openAIError(c, http.StatusUnprocessableEntity, "invoice_amount_not_eligible", "The requested invoice amount exceeds eligible settled revenue.")
		return
	}
	if errors.Is(err, store.ErrStatementMismatch) {
		openAIError(c, http.StatusUnprocessableEntity, "provider_statement_mismatch", "The Provider statement total does not equal its line totals.")
		return
	}
	if errors.Is(err, store.ErrSupplierBillMismatch) {
		openAIError(c, http.StatusUnprocessableEntity, "supplier_bill_mismatch", "The supplier-declared bill total does not equal its line totals.")
		return
	}
	if errors.Is(err, store.ErrMinimumSettlement) {
		openAIError(c, http.StatusUnprocessableEntity, "minimum_settlement_not_met", "Eligible platform-measured payable is below the configured minimum settlement amount.")
		return
	}
	if errors.Is(err, store.ErrSupplierPayoutBlocked) {
		openAIError(c, http.StatusConflict, "supplier_payout_blocked", "Payout requires settled platform usage, verified bill matches, approved tax/invoice status, and no open dispute.")
		return
	}
	if errors.Is(err, store.ErrSupplierSettlementState) {
		openAIError(c, http.StatusConflict, "supplier_settlement_state_conflict", "The supplier settlement does not allow this state transition.")
		return
	}
	if errors.Is(err, store.ErrMarketplaceLaunchState) {
		openAIError(c, http.StatusConflict, "marketplace_launch_state_conflict", "Marketplace launch gates or lifecycle state do not allow this operation.")
		return
	}
	if errors.Is(err, store.ErrMarketplaceGateEvidence) {
		openAIError(c, http.StatusUnprocessableEntity, "marketplace_gate_evidence_invalid", "The gate requires bounded administrator evidence and a valid decision.")
		return
	}
	if errors.Is(err, store.ErrMarketplacePayoutReadiness) {
		openAIError(c, http.StatusConflict, "marketplace_payout_readiness_incomplete", "Contract, tax, payment, and security evidence must be independently approved before production payout.")
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
		case "42501":
			openAIError(c, http.StatusForbidden, "policy_forbidden", "A protected administrator or release policy forbids this transition.")
			return
		}
	}
	openAIError(c, 500, "internal_error", "The operation could not be completed.")
}

func pricingQuoteHandler(c *gin.Context, d Dependencies) {
	var in struct {
		OrganizationID             string `json:"organization_id"`
		ProviderID                 string `json:"provider_id"`
		Model                      string `json:"model"`
		InputTokens                int64  `json:"input_tokens"`
		CachedInputTokens          int64  `json:"cached_input_tokens"`
		OutputTokens               int64  `json:"output_tokens"`
		EstimatedInputTokens       int64  `json:"estimated_input_tokens"`
		EstimatedCachedInputTokens int64  `json:"estimated_cached_input_tokens"`
		EstimatedOutputTokens      int64  `json:"estimated_output_tokens"`
		PromotionAmount            string `json:"promotion_amount"`
		TaxRate                    string `json:"tax_rate"`
		ExchangeRate               string `json:"exchange_rate"`
		IdempotencyKey             string `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.OrganizationID) == "" || strings.TrimSpace(in.Model) == "" {
		openAIError(c, http.StatusBadRequest, "invalid_request", "organization_id and model are required.")
		return
	}
	if len(in.IdempotencyKey) > 200 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "idempotency_key must not exceed 200 characters.")
		return
	}
	if in.InputTokens == 0 {
		in.InputTokens = in.EstimatedInputTokens
	}
	if in.CachedInputTokens == 0 {
		in.CachedInputTokens = in.EstimatedCachedInputTokens
	}
	if in.OutputTokens == 0 {
		in.OutputTokens = in.EstimatedOutputTokens
	}
	claims := claimsFrom(c)
	if claims.Role != "ADMIN" && claims.Role != "SUPER_ADMIN" {
		if err := d.Store.CheckOrganizationPricingAccess(c.Request.Context(), claims.Subject, in.OrganizationID); err != nil {
			openAIError(c, http.StatusForbidden, "forbidden", "The organization is outside this session's scope.")
			return
		}
	}
	actor := stringPtr(claims.Subject)
	out, err := d.Store.QuotePricing(c.Request.Context(), store.PriceQuoteRequest{OrganizationID: in.OrganizationID, ProviderID: in.ProviderID, Model: strings.TrimSpace(in.Model), InputTokens: in.InputTokens, CachedInputTokens: in.CachedInputTokens, OutputTokens: in.OutputTokens, PromotionAmount: in.PromotionAmount, TaxRate: in.TaxRate, ExchangeRate: in.ExchangeRate, IdempotencyKey: in.IdempotencyKey, CreatedBy: actor})
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"quote": out, "data": out})
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

func stringPtr(value string) *string { return &value }
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

func upsertRoutingRule(c *gin.Context, d Dependencies, create bool) {
	var rule domain.RoutingRule
	if c.ShouldBindJSON(&rule) != nil || rule.ProjectID == "" || strings.TrimSpace(rule.Name) == "" ||
		strings.TrimSpace(rule.Alias) == "" || !validIntelligentStrategy(rule.Strategy) || rule.QualityWeight < 0 ||
		rule.PriceWeight < 0 || rule.LatencyWeight < 0 {
		openAIError(c, 400, "invalid_request", "project_id, name, alias, strategy, and non-negative weights are required.")
		return
	}
	if !create {
		rule.ID = c.Param("id")
	} else {
		rule.ID = ""
		rule.Enabled = true
	}
	project, err := d.Store.ProjectByID(c.Request.Context(), rule.ProjectID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	if err = d.Store.RequireBooleanEntitlement(c.Request.Context(), project.OrganizationID, "advanced_routing"); err != nil {
		respond(c, nil, err)
		return
	}
	out, err := d.Store.UpsertRoutingRule(c.Request.Context(), rule)
	action := "routing_rule.update"
	if create {
		action = "routing_rule.create"
	}
	audit(c, d, action, "routing_rule", out.ID, out)
	if create {
		respondCreated(c, out, err)
	} else {
		respond(c, out, err)
	}
}

func upsertTeam(c *gin.Context, d Dependencies, create bool) {
	var team domain.Team
	if c.ShouldBindJSON(&team) != nil || team.OrganizationID == "" || strings.TrimSpace(team.Name) == "" ||
		(team.Status != "" && team.Status != "ACTIVE" && team.Status != "DISABLED") {
		openAIError(c, 400, "invalid_request", "organization_id, name, and a valid status are required.")
		return
	}
	if !create {
		team.ID = c.Param("id")
	} else {
		team.ID = ""
	}
	out, err := d.Store.UpsertTeam(c.Request.Context(), team)
	action := "team.update"
	if create {
		action = "team.create"
	}
	audit(c, d, action, "team", out.ID, out)
	if create {
		respondCreated(c, out, err)
	} else {
		respond(c, out, err)
	}
}

func createWalletAdjustment(c *gin.Context, d Dependencies, transactionType string) {
	var in struct {
		Amount         domain.Decimal `json:"amount"`
		IdempotencyKey string         `json:"idempotency_key"`
		Reference      string         `json:"reference"`
		Metadata       map[string]any `json:"metadata"`
	}
	if c.ShouldBindJSON(&in) != nil || invalidOrZeroDecimal(in.Amount) || (transactionType == "TOPUP" && !validPositiveDecimal(in.Amount)) {
		openAIError(c, 400, "invalid_request", "A non-zero amount is required; topups must be positive.")
		return
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	if in.IdempotencyKey == "" || len(in.IdempotencyKey) > 200 {
		openAIError(c, 400, "invalid_request", "An Idempotency-Key of at most 200 characters is required.")
		return
	}
	claims := claimsFrom(c)
	var actor *string
	if claims != nil {
		actor = &claims.Subject
	}
	out, err := d.Store.CreateWalletTransaction(c.Request.Context(), domain.WalletTransaction{WalletID: c.Param("id"),
		TransactionType: transactionType, Amount: in.Amount, IdempotencyKey: in.IdempotencyKey, Reference: in.Reference,
		Metadata: in.Metadata, CreatedBy: actor})
	if errors.Is(err, store.ErrWalletUnavailable) {
		openAIError(c, http.StatusPaymentRequired, "insufficient_balance", "The wallet is unavailable or the adjustment exceeds its credit limit.")
		return
	}
	audit(c, d, "wallet."+strings.ToLower(transactionType), "wallet", c.Param("id"), out)
	respondCreated(c, out, err)
}

func validModelScore(value float64) bool { return value >= 0 && value <= 100 }
func validIntelligentStrategy(value string) bool {
	return value == "cost_optimized" || value == "quality_optimized" || value == "balanced"
}
func validMarketplaceStatus(value string) bool {
	return value == "DRAFT" || value == "REVIEW" || value == "CANARY" || value == "ACTIVE" || value == "SUSPENDED" || value == "REJECTED" || value == "EXITED"
}
func validBillingMode(value string) bool { return value == "PREPAID" || value == "POSTPAID" }
func validWalletStatus(value string) bool {
	return value == "ACTIVE" || value == "FROZEN" || value == "CLOSED"
}

func providerPricingHosts(provider domain.Provider) []string {
	configured := make([]string, 0)
	if raw, ok := provider.Config["pricing_api_hosts"]; ok {
		switch values := raw.(type) {
		case []any:
			for _, value := range values {
				if host, ok := value.(string); ok && strings.TrimSpace(host) != "" {
					configured = append(configured, strings.TrimSpace(host))
				}
			}
		case []string:
			configured = append(configured, values...)
		}
	}
	return configured
}

func normalizeProviderType(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "openai", true
	}
	switch value {
	case "openai", "anthropic", "gemini", "deepseek", "qwen", "kimi", "glm", "openrouter":
		return value, true
	default:
		return "", false
	}
}

func healthCheckProvider(ctx context.Context, d Dependencies, provider domain.Provider, credential providers.Credential) error {
	adapter, err := providerAdapter(d, provider.ProviderType)
	if err != nil {
		return err
	}
	return adapter.HealthCheck(ctx, provider.BaseURL, credential)
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
