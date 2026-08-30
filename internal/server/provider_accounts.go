package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/provisioning"
	"github.com/relayedock/relayedock/internal/store"
)

type providerProvisioningCapabilityView struct {
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	ProviderSlug string `json:"provider_slug"`
	provisioning.Capability
}

func registerAdminProviderAccountRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/provider-provisioning/capabilities", func(c *gin.Context) { listProviderProvisioningCapabilities(c, d) })
	g.GET("/provider-accounts", func(c *gin.Context) {
		limit, offset := page(c)
		values, err := d.Store.ListProviderAccountBindings(c.Request.Context(), c.Query("organization_id"), c.Query("user_id"),
			c.Query("provider_id"), limit, offset)
		respondList(c, values, err)
	})
	g.POST("/provider-accounts", func(c *gin.Context) { createProviderAccountBinding(c, d, true, "") })
	g.GET("/provider-provisioning/jobs", func(c *gin.Context) {
		limit, offset := page(c)
		values, err := d.Store.ListProviderProvisioningJobs(c.Request.Context(), c.Query("organization_id"), c.Query("user_id"),
			c.Query("status"), limit, offset)
		respondList(c, values, err)
	})
	g.POST("/provider-provisioning/jobs/:jobID/retry", func(c *gin.Context) {
		respondNoContent(c, d.Store.RetryProviderProvisioningJob(c.Request.Context(), c.Param("jobID"), stringPtr(claimsFrom(c).Subject)))
	})
}

func registerConsoleProviderAccountRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/provider-provisioning/capabilities", func(c *gin.Context) { listProviderProvisioningCapabilities(c, d) })
	g.GET("/organizations/:organizationID/provider-accounts", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		values, err := d.Store.ListProviderAccountBindings(c.Request.Context(), organization.ID, claimsFrom(c).Subject,
			c.Query("provider_id"), limit, offset)
		respondList(c, values, err)
	})
	g.POST("/organizations/:organizationID/provider-accounts", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		createProviderAccountBinding(c, d, false, organization.ID)
	})
	g.GET("/organizations/:organizationID/provider-provisioning/jobs", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		values, err := d.Store.ListProviderProvisioningJobs(c.Request.Context(), organization.ID, claimsFrom(c).Subject,
			c.Query("status"), limit, offset)
		respondList(c, values, err)
	})
}

func listProviderProvisioningCapabilities(c *gin.Context, d Dependencies) {
	providersList, err := d.Store.ListProviders(c.Request.Context())
	if err != nil {
		respond(c, nil, err)
		return
	}
	out := make([]providerProvisioningCapabilityView, 0, len(providersList))
	for _, provider := range providersList {
		capability := capabilityFor(d, provider.ProviderType)
		if !provider.Enabled {
			capability.Enabled = false
			capability.Reason = strings.TrimSpace("RelayDock provider is disabled. " + capability.Reason)
		}
		out = append(out, providerProvisioningCapabilityView{ProviderID: provider.ID, ProviderName: provider.Name,
			ProviderSlug: provider.Slug, Capability: capability})
	}
	respondList(c, out, nil)
}

func createProviderAccountBinding(c *gin.Context, d Dependencies, admin bool, organizationID string) {
	var input struct {
		OrganizationID    string  `json:"organization_id"`
		UserID            string  `json:"user_id"`
		ProviderID        string  `json:"provider_id"`
		ProvisioningMode  string  `json:"provisioning_mode"`
		ExternalAccountID string  `json:"external_account_id"`
		ExternalProjectID string  `json:"external_project_id"`
		CredentialID      *string `json:"credential_id"`
		Automatic         bool    `json:"automatic"`
		IdempotencyKey    string  `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.ProviderID) == "" {
		openAIError(c, http.StatusBadRequest, "invalid_request", "provider_id is required.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	if !admin {
		input.OrganizationID = organizationID
		input.UserID = claimsFrom(c).Subject
	}
	if input.OrganizationID == "" || input.UserID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "organization_id, user_id, and an Idempotency-Key are required.")
		return
	}
	provider, err := d.Store.ProviderByID(c.Request.Context(), input.ProviderID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	capability := capabilityFor(d, provider.ProviderType)
	mode := strings.ToUpper(strings.TrimSpace(input.ProvisioningMode))
	if mode == "" {
		mode = capability.Mode
	}
	if input.Automatic && (!capability.Enabled || !capability.SupportsAutomaticBinding || mode != capability.Mode) {
		openAIError(c, http.StatusConflict, "automatic_provisioning_unavailable", capability.Reason)
		return
	}
	if !admin && !providerBindingSelfServiceAllowed(input.Automatic, mode, input.CredentialID) {
		openAIError(c, http.StatusForbidden, "reviewed_binding_required", "Console self-service supports organization-owned BYOK credentials only; reviewed manual and platform credential bindings require an administrator.")
		return
	}
	binding, job, replayed, err := d.Store.CreateProviderAccountBinding(c.Request.Context(), store.CreateProviderAccountBindingRequest{
		OrganizationID: input.OrganizationID, UserID: input.UserID, ProviderID: input.ProviderID, ProvisioningMode: mode,
		ExternalAccountID: input.ExternalAccountID, ExternalProjectID: input.ExternalProjectID, CredentialID: input.CredentialID,
		IdempotencyKey: input.IdempotencyKey, EnqueueAutomatic: input.Automatic, CreatedBy: stringPtr(claimsFrom(c).Subject),
	})
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{"binding": binding, "job": job, "replayed": replayed}})
}

func providerBindingSelfServiceAllowed(automatic bool, mode string, credentialID *string) bool {
	if automatic {
		return true
	}
	return mode == "BYOK" && credentialID != nil && strings.TrimSpace(*credentialID) != ""
}

func capabilityFor(d Dependencies, providerType string) provisioning.Capability {
	if d.Provisioners == nil {
		return provisioning.Capability{ProviderType: providerType, Mode: "MANUAL", Enabled: true,
			Reason: "No automatic Provisioner is configured; use a reviewed manual binding."}
	}
	return d.Provisioners.Capability(providerType)
}
