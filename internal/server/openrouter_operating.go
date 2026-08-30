package server

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/store"
)

type publicProviderQuality struct {
	ProviderID        string          `json:"provider_id"`
	ProviderName      string          `json:"provider_name"`
	ProviderSlug      string          `json:"provider_slug"`
	Grade             string          `json:"grade"`
	QualityScore      domain.Decimal  `json:"quality_score"`
	AvailabilityPct   domain.Decimal  `json:"availability_pct"`
	ErrorRatePct      domain.Decimal  `json:"error_rate_pct"`
	RateLimitedPct    domain.Decimal  `json:"rate_limited_pct"`
	P95TTFTMS         *int64          `json:"p95_ttft_ms,omitempty"`
	P95FullLatencyMS  *int64          `json:"p95_full_latency_ms,omitempty"`
	ThroughputTPS     *domain.Decimal `json:"throughput_tps,omitempty"`
	MeasurementCount  int64           `json:"measurement_count"`
	RegionCoveragePct domain.Decimal  `json:"region_coverage_pct"`
	LastEvaluatedAt   *time.Time      `json:"last_evaluated_at,omitempty"`
	MeasurementSource string          `json:"measurement_source"`
}

func registerPublicOperatingRoutes(r *gin.Engine, d Dependencies) {
	public := r.Group("/api/public")
	public.GET("/provider-quality", func(c *gin.Context) {
		region, ok := publicRegion(c)
		if !ok {
			return
		}
		providers, err := d.Store.ListPublicProviders(c.Request.Context(), region)
		if err != nil {
			respond(c, nil, err)
			return
		}
		allowed := make(map[string]struct{}, len(providers))
		for _, provider := range providers {
			if provider.Availability.Available {
				allowed[provider.ID] = struct{}{}
			}
		}
		summaries, err := d.Store.ListProviderQualitySummaries(c.Request.Context())
		if err != nil {
			respond(c, nil, err)
			return
		}
		items := make([]publicProviderQuality, 0, len(summaries))
		for _, summary := range summaries {
			if _, visible := allowed[summary.ProviderID]; !visible || !summary.Policy.Enabled || summary.State.MeasurementCount < int64(summary.Policy.MinimumSamples) {
				continue
			}
			items = append(items, publicProviderQuality{ProviderID: summary.ProviderID, ProviderName: summary.ProviderName,
				ProviderSlug: summary.ProviderSlug, Grade: summary.State.Grade, QualityScore: summary.State.QualityScore,
				AvailabilityPct: summary.State.AvailabilityPct, ErrorRatePct: summary.State.ErrorRatePct,
				RateLimitedPct: summary.State.RateLimitedPct, P95TTFTMS: summary.State.P95TTFTMS,
				P95FullLatencyMS: summary.State.P95FullLatencyMS, ThroughputTPS: summary.State.ThroughputTPS,
				MeasurementCount: summary.State.MeasurementCount, RegionCoveragePct: summary.State.RegionCoveragePct,
				LastEvaluatedAt: summary.State.LastEvaluatedAt, MeasurementSource: "PLATFORM_MEASURED"})
		}
		c.Header("Cache-Control", "public, max-age=30")
		c.JSON(http.StatusOK, gin.H{"items": items, "region": region, "updated_at": time.Now().UTC()})
	})
	public.GET("/catalog/provider-capabilities", func(c *gin.Context) {
		region, ok := publicRegion(c)
		if !ok {
			return
		}
		providers, err := d.Store.ListPublicProviders(c.Request.Context(), region)
		if err != nil {
			respond(c, nil, err)
			return
		}
		allowed := make(map[string]struct{}, len(providers))
		for _, provider := range providers {
			if provider.Availability.Available {
				allowed[provider.ID] = struct{}{}
			}
		}
		items, err := d.Store.ListProviderCapabilityDocuments(c.Request.Context(), strings.TrimSpace(c.Query("provider_id")), true)
		if err != nil {
			respond(c, nil, err)
			return
		}
		visible := items[:0]
		for _, item := range items {
			if _, ok = allowed[item.ProviderID]; ok {
				visible = append(visible, item)
			}
		}
		c.Header("Cache-Control", "public, max-age=60")
		c.JSON(http.StatusOK, gin.H{"items": visible, "region": region, "updated_at": time.Now().UTC()})
	})
}

func registerAdminOperatingRoutes(g *gin.RouterGroup, d Dependencies) {
	registerWorkspaceOperatingRoutes(g, d, true)
	g.GET("/provider-capabilities", func(c *gin.Context) {
		items, err := d.Store.ListProviderCapabilityDocuments(c.Request.Context(), strings.TrimSpace(c.Query("provider_id")), false)
		respondList(c, items, err)
	})
	g.POST("/provider-capabilities", func(c *gin.Context) {
		var input domain.ProviderCapabilityDocument
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.ProviderID) == "" ||
			strings.TrimSpace(input.SchemaVersion) == "" || len(input.Document) == 0 || !validCapabilitySourceURL(input.SourceURL) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "provider_id, schema_version, a capability document, and an optional HTTPS source_url are required.")
			return
		}
		input.CreatedBy = stringPtr(claimsFrom(c).Subject)
		out, err := d.Store.PublishProviderCapabilityDocument(c.Request.Context(), input)
		respondCreated(c, out, err)
	})
	registerEnterpriseIdentityRoutes(g, d, true)
}

func registerConsoleOperatingRoutes(g *gin.RouterGroup, d Dependencies) {
	registerWorkspaceOperatingRoutes(g, d, false)
	g.GET("/provider-capabilities", func(c *gin.Context) {
		items, err := d.Store.ListProviderCapabilityDocuments(c.Request.Context(), strings.TrimSpace(c.Query("provider_id")), true)
		respondList(c, items, err)
	})
	registerEnterpriseIdentityRoutes(g, d, false)
}

func registerWorkspaceOperatingRoutes(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/projects/:projectID/workspace-settings", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		out, err := d.Store.WorkspaceSettings(c.Request.Context(), project.ID)
		respond(c, out, err)
	})
	g.PUT("/projects/:projectID/workspace-settings", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input domain.WorkspaceSettings
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid workspace settings document is required.")
			return
		}
		input.ProjectID = project.ID
		input.AllowedProcessingRegions = normalizedStringList(input.AllowedProcessingRegions)
		input.DefaultProviderPolicy.Order = normalizedStringList(input.DefaultProviderPolicy.Order)
		input.DefaultProviderPolicy.Only = normalizedStringList(input.DefaultProviderPolicy.Only)
		input.DefaultProviderPolicy.Ignore = normalizedStringList(input.DefaultProviderPolicy.Ignore)
		input.DefaultProviderPolicy.Quantizations = normalizedStringList(input.DefaultProviderPolicy.Quantizations)
		input.DefaultProviderPolicy.RequiredCapabilities = normalizedStringList(input.DefaultProviderPolicy.RequiredCapabilities)
		input.DefaultProviderPolicy.ProcessingRegions = normalizedStringList(input.DefaultProviderPolicy.ProcessingRegions)
		if !validWorkspaceSettings(input) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Workspace limits or Provider routing policy values are invalid.")
			return
		}
		out, err := d.Store.UpsertWorkspaceSettings(c.Request.Context(), input, stringPtr(claimsFrom(c).Subject))
		respond(c, out, err)
	})
}

func registerEnterpriseIdentityRoutes(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/organizations/:organizationID/enterprise-identity", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		out, err := d.Store.EnterpriseIdentityConnection(c.Request.Context(), organization.ID)
		if errors.Is(err, store.ErrNotFound) {
			out = domain.EnterpriseIdentityConnection{OrganizationID: organization.ID, AllowedDomains: []string{},
				Status: "DISABLED", Metadata: map[string]any{}}
			err = nil
		}
		respond(c, out, err)
	})
	g.PUT("/organizations/:organizationID/enterprise-identity", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input struct {
			domain.EnterpriseIdentityConnection
			ClientSecret    string `json:"client_secret"`
			RotateSCIMToken bool   `json:"rotate_scim_token"`
		}
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid enterprise identity connection is required.")
			return
		}
		value := input.EnterpriseIdentityConnection
		value.OrganizationID = organization.ID
		value.AllowedDomains = normalizedStringList(value.AllowedDomains)
		if value.Metadata == nil {
			value.Metadata = map[string]any{}
		}
		current, currentErr := d.Store.EnterpriseIdentityConnection(c.Request.Context(), organization.ID)
		if currentErr == nil {
			value.ID = current.ID
		} else if errors.Is(currentErr, store.ErrNotFound) {
			value.ID = id.UUID()
		} else {
			respond(c, nil, currentErr)
			return
		}
		if !validEnterpriseIdentity(value, input.ClientSecret != "" || current.HasClientSecret,
			input.RotateSCIMToken || current.HasSCIMToken) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Active SSO requires a public HTTPS issuer, client ID and secret; active SCIM requires a token.")
			return
		}
		var encryptedSecret []byte
		if strings.TrimSpace(input.ClientSecret) != "" {
			if d.Vault == nil {
				openAIError(c, http.StatusServiceUnavailable, "secret_vault_unavailable", "Secret encryption is unavailable.")
				return
			}
			var err error
			encryptedSecret, err = d.Vault.Encrypt(strings.TrimSpace(input.ClientSecret), "enterprise-identity:"+value.ID)
			if err != nil {
				respond(c, nil, err)
				return
			}
		}
		var rawToken string
		var tokenHash []byte
		if input.RotateSCIMToken {
			if d.Auth == nil {
				openAIError(c, http.StatusServiceUnavailable, "identity_token_service_unavailable", "SCIM token generation is unavailable.")
				return
			}
			var err error
			rawToken, err = auth.NewOpaqueToken()
			if err != nil {
				respond(c, nil, err)
				return
			}
			tokenHash = d.Auth.DigestToken(rawToken)
		}
		value.CreatedBy = stringPtr(claimsFrom(c).Subject)
		out, err := d.Store.UpsertEnterpriseIdentityConnection(c.Request.Context(), value, encryptedSecret, tokenHash, value.CreatedBy)
		if err != nil {
			respond(c, nil, err)
			return
		}
		response := gin.H{"connection": out, "scim_base_path": "/scim/v2/" + organization.ID}
		if rawToken != "" {
			response["scim_token"] = rawToken
		}
		c.JSON(http.StatusOK, gin.H{"data": response})
	})
	g.POST("/organizations/:organizationID/enterprise-identity/test", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		connection, err := d.Store.EnterpriseIdentityConnection(c.Request.Context(), organization.ID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		result, err := testOIDCDiscovery(c.Request.Context(), connection.IssuerURL)
		respond(c, result, err)
	})
}

func validWorkspaceSettings(value domain.WorkspaceSettings) bool {
	if value.FreeDailyRequestLimit < 0 || value.FreeDailyRequestLimit > 1_000_000 || value.FreeDailyTokenLimit < 0 || value.FreeDailyTokenLimit > 1_000_000_000_000 {
		return false
	}
	policy := value.DefaultProviderPolicy
	if policy.Sort != "" && policy.Sort != "price" && policy.Sort != "latency" && policy.Sort != "throughput" {
		return false
	}
	if policy.DataCollection != "" && policy.DataCollection != "allow" && policy.DataCollection != "deny" {
		return false
	}
	return !intersects(policy.Only, policy.Ignore)
}

func intersects(left, right []string) bool {
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		values[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := values[value]; ok {
			return true
		}
	}
	return false
}

func normalizedStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func validCapabilitySourceURL(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil
}

func validEnterpriseIdentity(value domain.EnterpriseIdentityConnection, hasSecret, hasSCIMToken bool) bool {
	value.Status = strings.ToUpper(strings.TrimSpace(value.Status))
	if value.Status != "ACTIVE" && value.Status != "DISABLED" {
		return false
	}
	for _, domainName := range value.AllowedDomains {
		if strings.ContainsAny(domainName, " /:@") || !strings.Contains(domainName, ".") {
			return false
		}
	}
	if value.SSOEnabled {
		if value.Status != "ACTIVE" || strings.TrimSpace(value.ClientID) == "" || !hasSecret || validateOIDCIssuer(value.IssuerURL) != nil {
			return false
		}
	}
	if value.SCIMEnabled && (value.Status != "ACTIVE" || !hasSCIMToken) {
		return false
	}
	return !value.EnforceSSO || value.SSOEnabled
}
