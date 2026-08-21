package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/store"
)

const webhookSecretAADPrefix = "webhook:"

var supportedWebhookEvents = map[string]struct{}{
	"webhook.test":    {},
	"budget.warning":  {},
	"budget.exceeded": {},
	"api_key.rotated": {},
}

func registerAdminV2(g *gin.RouterGroup, d Dependencies) {
	registerTenantV2(g, d, true)
	registerCredentialTagsV2(g, d)
	g.POST("/alerts/:alertID/acknowledge", func(c *gin.Context) {
		actorID := claimsFrom(c).Subject
		acknowledgedAt, err := d.Store.AcknowledgeAlert(c.Request.Context(), c.Param("alertID"), actorID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		result := gin.H{"id": c.Param("alertID"), "status": "ACKNOWLEDGED", "acknowledged_at": acknowledgedAt, "acknowledged_by": actorID}
		audit(c, d, "alert.acknowledge", "alert", c.Param("alertID"), result)
		c.JSON(http.StatusOK, result)
	})
}

func registerConsoleV2(g *gin.RouterGroup, d Dependencies) {
	registerTenantV2(g, d, false)
	g.GET("/projects", func(c *gin.Context) {
		userID := claimsFrom(c).Subject
		organizations, err := d.Store.ListOrganizations(c.Request.Context(), &userID, 200, 0)
		if err != nil {
			if d.Logger != nil {
				d.Logger.Error("console_projects_organizations_failed", "error", err, "user_id", userID)
			}
			respond(c, nil, err)
			return
		}
		projects := make([]gin.H, 0)
		for _, organization := range organizations {
			items, listErr := d.Store.ListProjects(c.Request.Context(), organization.ID, &userID, 200, 0)
			if listErr != nil {
				if d.Logger != nil {
					d.Logger.Error("console_projects_listing_failed", "error", listErr, "user_id", userID, "organization_id", organization.ID)
				}
				respond(c, nil, listErr)
				return
			}
			for _, project := range items {
				projects = append(projects, gin.H{
					"id": project.ID, "organization_id": project.OrganizationID,
					"organization_name": organization.Name, "organization_slug": organization.Slug,
					"name": project.Name, "slug": project.Slug, "description": project.Description,
					"status": project.Status, "metadata": project.Metadata,
					"created_at": project.CreatedAt, "updated_at": project.UpdatedAt,
				})
			}
		}
		respondList(c, projects, nil)
	})
}

func registerCredentialTagsV2(g *gin.RouterGroup, d Dependencies) {
	g.GET("/credentials/:id/tags", func(c *gin.Context) {
		tags, err := d.Store.CredentialTags(c.Request.Context(), c.Param("id"))
		respond(c, gin.H{"tags": tags}, err)
	})
	g.PUT("/credentials/:id/tags", func(c *gin.Context) {
		var input struct {
			Tags []string `json:"tags"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Tags == nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "tags must be a JSON array.")
			return
		}
		credentialID := c.Param("id")
		if err := d.Store.SetCredentialTags(c.Request.Context(), credentialID, input.Tags); err != nil {
			respond(c, nil, err)
			return
		}
		tags, err := d.Store.CredentialTags(c.Request.Context(), credentialID)
		if err == nil {
			audit(c, d, "credential.tags_set", "provider_credential", credentialID, gin.H{"tags": tags})
		}
		respond(c, gin.H{"tags": tags}, err)
	})
}

func registerTenantV2(g *gin.RouterGroup, d Dependencies, admin bool) {
	registerOrganizationV2(g, d, admin)
	registerProjectV2(g, d, admin)
	registerAPIKeyLifecycleV2(g, d, admin)
}

func registerOrganizationV2(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/organizations", func(c *gin.Context) {
		limit, offset := page(c)
		var userID *string
		if !admin {
			id := claimsFrom(c).Subject
			userID = &id
		}
		organizations, err := d.Store.ListOrganizations(c.Request.Context(), userID, limit, offset)
		respondList(c, organizations, err)
	})
	g.POST("/organizations", func(c *gin.Context) {
		var organization domain.Organization
		if c.ShouldBindJSON(&organization) != nil || strings.TrimSpace(organization.Name) == "" || !validTenantSlug(organization.Slug) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "name and a lowercase URL-safe slug are required.")
			return
		}
		if organization.Status == "" {
			organization.Status = "ACTIVE"
		}
		if !validTenantStatus(organization.Status) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "status must be ACTIVE, DISABLED, or ARCHIVED.")
			return
		}
		out, err := d.Store.CreateOrganization(c.Request.Context(), organization, claimsFrom(c).Subject)
		if err == nil {
			audit(c, d, "organization.create", "organization", out.ID, out)
		}
		respondCreated(c, out, err)
	})
	g.GET("/organizations/:organizationID", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "VIEWER")
		if ok {
			c.JSON(http.StatusOK, organization)
		}
	})
	g.PUT("/organizations/:organizationID", func(c *gin.Context) {
		current, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input domain.Organization
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid organization body is required.")
			return
		}
		input = mergeOrganizationUpdate(current, input)
		if strings.TrimSpace(input.Name) == "" || !validTenantSlug(input.Slug) || !validTenantStatus(input.Status) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "name, slug, or status is invalid.")
			return
		}
		out, err := d.Store.UpdateOrganization(c.Request.Context(), input)
		if err == nil {
			audit(c, d, "organization.update", "organization", out.ID, out)
		}
		respond(c, out, err)
	})
	g.DELETE("/organizations/:organizationID", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "OWNER")
		if !ok {
			return
		}
		err := d.Store.DeleteOrganization(c.Request.Context(), organization.ID)
		if err == nil {
			audit(c, d, "organization.archive", "organization", organization.ID, nil)
		}
		respondNoContent(c, err)
	})

	g.GET("/organizations/:organizationID/members", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		members, err := d.Store.ListOrganizationMembers(c.Request.Context(), organization.ID)
		respondList(c, members, err)
	})
	setOrganizationMember := func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var member domain.OrganizationMembership
		if c.ShouldBindJSON(&member) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid membership body is required.")
			return
		}
		if pathID := c.Param("userID"); pathID != "" {
			member.UserID = pathID
		}
		member.OrganizationID = organization.ID
		member.Role = strings.ToUpper(member.Role)
		member.Status = strings.ToUpper(member.Status)
		if member.Status == "" {
			member.Status = "ACTIVE"
		}
		if member.UserID == "" || !validOrganizationRole(member.Role) || !validMembershipStatus(member.Status) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "user_id, a valid role, and a valid status are required.")
			return
		}
		err := d.Store.SetOrganizationMember(c.Request.Context(), member)
		if err == nil {
			audit(c, d, "organization.member_set", "organization", organization.ID, member)
		}
		respond(c, member, err)
	}
	g.POST("/organizations/:organizationID/members", setOrganizationMember)
	g.PUT("/organizations/:organizationID/members/:userID", setOrganizationMember)
	g.DELETE("/organizations/:organizationID/members/:userID", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		err := d.Store.RemoveOrganizationMember(c.Request.Context(), organization.ID, c.Param("userID"))
		if err == nil {
			audit(c, d, "organization.member_remove", "organization", organization.ID, gin.H{"user_id": c.Param("userID")})
		}
		respondNoContent(c, err)
	})

	g.GET("/organizations/:organizationID/projects", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		var userID *string
		if !admin {
			id := claimsFrom(c).Subject
			userID = &id
		}
		projects, err := d.Store.ListProjects(c.Request.Context(), organization.ID, userID, limit, offset)
		respondList(c, projects, err)
	})
	g.POST("/organizations/:organizationID/projects", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var project domain.Project
		if c.ShouldBindJSON(&project) != nil || strings.TrimSpace(project.Name) == "" || !validTenantSlug(project.Slug) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "name and a lowercase URL-safe slug are required.")
			return
		}
		project.OrganizationID = organization.ID
		if project.Status == "" {
			project.Status = "ACTIVE"
		}
		if !validTenantStatus(project.Status) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "status must be ACTIVE, DISABLED, or ARCHIVED.")
			return
		}
		out, err := d.Store.CreateProject(c.Request.Context(), project)
		if err == nil {
			actor := claimsFrom(c).Subject
			_ = d.Store.SetProjectMember(c.Request.Context(), domain.ProjectMembership{ProjectID: out.ID, UserID: actor, Role: "ADMIN", Status: "ACTIVE"})
			audit(c, d, "project.create", "project", out.ID, out)
		}
		respondCreated(c, out, err)
	})
}

// mergeOrganizationUpdate preserves fields omitted by the partial-update
// behavior of the organization PUT endpoint. An explicit empty JSON array is
// still retained so administrators can intentionally clear a provider policy.
func mergeOrganizationUpdate(current, input domain.Organization) domain.Organization {
	input.ID = current.ID
	if input.Name == "" {
		input.Name = current.Name
	}
	if input.Slug == "" {
		input.Slug = current.Slug
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	if strings.TrimSpace(input.BillingRegion) == "" {
		input.BillingRegion = current.BillingRegion
	}
	if input.Metadata == nil {
		input.Metadata = current.Metadata
	}
	if input.AllowedProviderIDs == nil {
		input.AllowedProviderIDs = current.AllowedProviderIDs
	}
	if input.ProhibitedProviderIDs == nil {
		input.ProhibitedProviderIDs = current.ProhibitedProviderIDs
	}
	if input.RequiredDataRegions == nil {
		input.RequiredDataRegions = current.RequiredDataRegions
	}
	if strings.TrimSpace(input.MinimumGrossMargin) == "" {
		input.MinimumGrossMargin = current.MinimumGrossMargin
	}
	return input
}

func registerProjectV2(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/projects/:projectID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if ok {
			c.JSON(http.StatusOK, project)
		}
	})
	g.PUT("/projects/:projectID", func(c *gin.Context) {
		current, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input domain.Project
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid project body is required.")
			return
		}
		input.ID = current.ID
		input.OrganizationID = current.OrganizationID
		if input.Name == "" {
			input.Name = current.Name
		}
		if input.Slug == "" {
			input.Slug = current.Slug
		}
		if input.Description == "" {
			input.Description = current.Description
		}
		if input.Status == "" {
			input.Status = current.Status
		}
		if input.Metadata == nil {
			input.Metadata = current.Metadata
		}
		if !validTenantSlug(input.Slug) || !validTenantStatus(input.Status) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "slug or status is invalid.")
			return
		}
		out, err := d.Store.UpdateProject(c.Request.Context(), input)
		if err == nil {
			audit(c, d, "project.update", "project", out.ID, out)
		}
		respond(c, out, err)
	})
	g.DELETE("/projects/:projectID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		err := d.Store.DeleteProject(c.Request.Context(), project.ID)
		if err == nil {
			audit(c, d, "project.archive", "project", project.ID, nil)
		}
		respondNoContent(c, err)
	})

	g.GET("/projects/:projectID/members", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		members, err := d.Store.ListProjectMembers(c.Request.Context(), project.ID)
		respondList(c, members, err)
	})
	setProjectMember := func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var member domain.ProjectMembership
		if c.ShouldBindJSON(&member) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid membership body is required.")
			return
		}
		if pathID := c.Param("userID"); pathID != "" {
			member.UserID = pathID
		}
		member.ProjectID = project.ID
		member.OrganizationID = project.OrganizationID
		member.Role = strings.ToUpper(member.Role)
		member.Status = strings.ToUpper(member.Status)
		if member.Status == "" {
			member.Status = "ACTIVE"
		}
		if member.UserID == "" || !validProjectRole(member.Role) || !validMembershipStatus(member.Status) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "user_id, a valid role, and a valid status are required.")
			return
		}
		err := d.Store.SetProjectMember(c.Request.Context(), member)
		if err == nil {
			audit(c, d, "project.member_set", "project", project.ID, member)
		}
		respond(c, member, err)
	}
	g.POST("/projects/:projectID/members", setProjectMember)
	g.PUT("/projects/:projectID/members/:userID", setProjectMember)
	g.DELETE("/projects/:projectID/members/:userID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		err := d.Store.RemoveProjectMember(c.Request.Context(), project.ID, c.Param("userID"))
		if err == nil {
			audit(c, d, "project.member_remove", "project", project.ID, gin.H{"user_id": c.Param("userID")})
		}
		respondNoContent(c, err)
	})

	g.GET("/projects/:projectID/routes", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		routes, err := d.Store.ListProjectRoutes(c.Request.Context(), project.ID)
		respondList(c, routes, err)
	})
	setProjectRoute := func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input struct {
			domain.ProjectModelRoute
			Enabled *bool `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid project route body is required.")
			return
		}
		route := input.ProjectModelRoute
		route.ProjectID = project.ID
		if c.Param("routeID") != "" {
			route.ModelRouteID = c.Param("routeID")
		}
		if route.ModelRouteID == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "model_route_id is required.")
			return
		}
		route.Enabled = true
		if input.Enabled != nil {
			route.Enabled = *input.Enabled
		}
		if existing, found, err := findProjectRoute(c, d, project.ID, route.ModelRouteID); err != nil {
			respond(c, nil, err)
			return
		} else if found {
			route.ID = existing.ID
			if route.Alias == "" {
				route.Alias = existing.Alias
			}
		}
		out, err := d.Store.UpsertProjectRoute(c.Request.Context(), route)
		if err == nil {
			audit(c, d, "project.route_set", "project", project.ID, out)
		}
		respond(c, out, err)
	}
	g.POST("/projects/:projectID/routes", setProjectRoute)
	g.PUT("/projects/:projectID/routes/:routeID", setProjectRoute)
	g.DELETE("/projects/:projectID/routes/:routeID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		route, found, err := findProjectRoute(c, d, project.ID, c.Param("routeID"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		if !found {
			respond(c, nil, store.ErrNotFound)
			return
		}
		err = d.Store.RemoveProjectRoute(c.Request.Context(), project.ID, route.ID)
		if err == nil {
			audit(c, d, "project.route_remove", "project", project.ID, route)
		}
		respondNoContent(c, err)
	})

	registerProjectBudgetV2(g, d, admin)
	registerProjectWebhookV2(g, d, admin)
	g.GET("/projects/:projectID/usage/export", func(c *gin.Context) { exportProjectUsage(c, d, admin) })
}

func registerProjectBudgetV2(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/projects/:projectID/budgets", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		policies, err := d.Store.ListProjectBudgetPolicies(c.Request.Context(), project.ID)
		respondList(c, policies, err)
	})
	upsertBudget := func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		if err := d.Store.RequireBooleanEntitlement(c.Request.Context(), project.OrganizationID, "custom_budget"); err != nil {
			respond(c, nil, err)
			return
		}
		var policy domain.ProjectBudgetPolicy
		if c.ShouldBindJSON(&policy) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid budget policy body is required.")
			return
		}
		policy.ProjectID = project.ID
		if pathID := c.Param("policyID"); pathID != "" {
			policy.ID = pathID
		}
		policy.Period = strings.ToUpper(policy.Period)
		policy.Status = strings.ToUpper(policy.Status)
		if policy.Period == "" {
			policy.Period = "MONTHLY"
		}
		if policy.Status == "" {
			policy.Status = "ACTIVE"
		}
		if strings.TrimSpace(policy.Name) == "" || (policy.Period != "DAILY" && policy.Period != "MONTHLY") ||
			(policy.Status != "ACTIVE" && policy.Status != "DISABLED") || policy.AlertThreshold < 0 || policy.AlertThreshold > 1 ||
			(policy.TokenLimit == nil && policy.CostLimit == nil) || (policy.TokenLimit != nil && *policy.TokenLimit < 0) ||
			(policy.CostLimit != nil && *policy.CostLimit < 0) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "name, period, at least one non-negative limit, and a threshold from 0 to 1 are required.")
			return
		}
		out, err := d.Store.UpsertProjectBudgetPolicy(c.Request.Context(), policy)
		if err == nil {
			audit(c, d, "project.budget_set", "project", project.ID, out)
		}
		if c.Request.Method == http.MethodPost {
			respondCreated(c, out, err)
		} else {
			respond(c, out, err)
		}
	}
	g.POST("/projects/:projectID/budgets", upsertBudget)
	g.PUT("/projects/:projectID/budgets/:policyID", upsertBudget)
	g.DELETE("/projects/:projectID/budgets/:policyID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		if err := d.Store.RequireBooleanEntitlement(c.Request.Context(), project.OrganizationID, "custom_budget"); err != nil {
			respond(c, nil, err)
			return
		}
		err := d.Store.DeleteProjectBudgetPolicy(c.Request.Context(), project.ID, c.Param("policyID"))
		if err == nil {
			audit(c, d, "project.budget_delete", "project", project.ID, gin.H{"policy_id": c.Param("policyID")})
		}
		respondNoContent(c, err)
	})
	g.GET("/projects/:projectID/budget-usage", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		period := strings.ToUpper(c.DefaultQuery("period", "MONTHLY"))
		usage, err := d.Store.ProjectBudgetUsage(c.Request.Context(), project.ID, period, time.Now().UTC())
		respond(c, usage, err)
	})
	g.GET("/projects/:projectID/budget-events", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		from, to, err := queryTimeRange(c, 90*24*time.Hour, 366*24*time.Hour)
		if err != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		limit, offset := page(c)
		events, err := d.Store.ListBudgetEvents(c.Request.Context(), project.ID, from, to, limit, offset)
		respondList(c, events, err)
	})
}

func registerProjectWebhookV2(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/projects/:projectID/webhooks", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		endpoints, err := d.Store.ListWebhookEndpoints(c.Request.Context(), project.ID)
		respondList(c, endpoints, err)
	})
	g.POST("/projects/:projectID/webhooks", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input struct {
			Name          string   `json:"name"`
			URL           string   `json:"url"`
			SigningSecret string   `json:"signing_secret"`
			Secret        string   `json:"secret"`
			EventTypes    []string `json:"event_types"`
			Enabled       *bool    `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.URL) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "name and url are required.")
			return
		}
		if input.SigningSecret == "" {
			input.SigningSecret = input.Secret
		}
		if input.SigningSecret == "" {
			input.SigningSecret, _ = randomSigningSecret()
		}
		if len(input.SigningSecret) < 16 || !validWebhookEventTypes(input.EventTypes) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A signing secret of at least 16 characters and supported event types are required.")
			return
		}
		if d.Webhooks == nil || d.Webhooks.ValidateTarget(c.Request.Context(), input.URL) != nil {
			openAIError(c, http.StatusUnprocessableEntity, "invalid_webhook_target", "The webhook URL is not an allowed delivery target.")
			return
		}
		encrypted, err := d.Vault.Encrypt(input.SigningSecret, webhookSecretAADPrefix+project.ID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		endpoint, err := d.Store.CreateWebhookEndpoint(c.Request.Context(), domain.WebhookEndpoint{
			OrganizationID: project.OrganizationID, ProjectID: project.ID, Name: strings.TrimSpace(input.Name), URL: strings.TrimSpace(input.URL),
			EncryptedSecret: encrypted, SecretLast4: last4(input.SigningSecret), EventTypes: normalizedWebhookEventTypes(input.EventTypes), Enabled: enabled,
		})
		if err != nil {
			respond(c, nil, err)
			return
		}
		audit(c, d, "webhook.create", "webhook_endpoint", endpoint.ID, endpoint)
		c.JSON(http.StatusCreated, gin.H{"webhook": endpoint, "signing_secret": input.SigningSecret, "warning": "The signing secret is shown once."})
	})
	g.PUT("/projects/:projectID/webhooks/:webhookID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		current, err := d.Store.WebhookEndpointByID(c.Request.Context(), c.Param("webhookID"))
		if err != nil || current.ProjectID != project.ID {
			respond(c, nil, store.ErrNotFound)
			return
		}
		var input struct {
			Name          string   `json:"name"`
			URL           string   `json:"url"`
			SigningSecret string   `json:"signing_secret"`
			Secret        string   `json:"secret"`
			EventTypes    []string `json:"event_types"`
			Enabled       *bool    `json:"enabled"`
		}
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid webhook body is required.")
			return
		}
		if input.Name != "" {
			current.Name = input.Name
		}
		if input.URL != "" {
			current.URL = input.URL
		}
		if input.EventTypes != nil {
			current.EventTypes = normalizedWebhookEventTypes(input.EventTypes)
		}
		if input.Enabled != nil {
			current.Enabled = *input.Enabled
		}
		if input.SigningSecret == "" {
			input.SigningSecret = input.Secret
		}
		if current.Name == "" || !validWebhookEventTypes(current.EventTypes) || d.Webhooks == nil || d.Webhooks.ValidateTarget(c.Request.Context(), current.URL) != nil {
			openAIError(c, http.StatusUnprocessableEntity, "invalid_webhook_target", "The webhook configuration is invalid.")
			return
		}
		replaceSecret := input.SigningSecret != ""
		if replaceSecret {
			if len(input.SigningSecret) < 16 {
				openAIError(c, http.StatusBadRequest, "invalid_request", "The signing secret must contain at least 16 characters.")
				return
			}
			current.EncryptedSecret, err = d.Vault.Encrypt(input.SigningSecret, webhookSecretAADPrefix+project.ID)
			current.SecretLast4 = last4(input.SigningSecret)
			if err != nil {
				respond(c, nil, err)
				return
			}
		}
		out, err := d.Store.UpdateWebhookEndpoint(c.Request.Context(), current, replaceSecret)
		if err == nil {
			audit(c, d, "webhook.update", "webhook_endpoint", out.ID, out)
		}
		respond(c, out, err)
	})
	g.DELETE("/projects/:projectID/webhooks/:webhookID", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		err := d.Store.DeleteWebhookEndpoint(c.Request.Context(), project.ID, c.Param("webhookID"))
		if err == nil {
			audit(c, d, "webhook.disable", "webhook_endpoint", c.Param("webhookID"), nil)
		}
		respondNoContent(c, err)
	})
	g.POST("/projects/:projectID/webhooks/:webhookID/test", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		endpoint, err := d.Store.WebhookEndpointByID(c.Request.Context(), c.Param("webhookID"))
		if err != nil || endpoint.ProjectID != project.ID || !endpoint.Enabled {
			respond(c, nil, store.ErrNotFound)
			return
		}
		eventID := id.UUID()
		payload := map[string]any{"id": eventID, "type": "webhook.test", "created_at": time.Now().UTC(), "project_id": project.ID, "data": map[string]any{"message": "RelayDock webhook test"}}
		outbox, err := d.Store.EnqueueWebhookOutbox(c.Request.Context(), domain.WebhookOutbox{
			EndpointID: endpoint.ID, OrganizationID: project.OrganizationID, ProjectID: project.ID, EventID: eventID,
			EventType: "webhook.test", Payload: payload, MaxAttempts: d.Config.WebhookMaxAttempts,
		})
		if err == nil {
			audit(c, d, "webhook.test", "webhook_endpoint", endpoint.ID, gin.H{"event_id": eventID})
		}
		respondCreated(c, outbox, err)
	})
	g.GET("/projects/:projectID/webhook-deliveries", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		status := strings.ToUpper(strings.TrimSpace(c.Query("status")))
		if status != "" && status != "PENDING" && status != "PROCESSING" && status != "RETRY" && status != "DELIVERED" && status != "DEAD" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Unsupported delivery status.")
			return
		}
		deliveries, err := d.Store.ListWebhookOutbox(c.Request.Context(), project.ID, status, limit, offset)
		respondList(c, deliveries, err)
	})
	g.POST("/projects/:projectID/webhook-deliveries/:deliveryID/retry", func(c *gin.Context) {
		project, ok := requireProjectAccess(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		delivery, err := d.Store.RetryDeadWebhookOutbox(c.Request.Context(), project.ID, c.Param("deliveryID"))
		if err == nil {
			audit(c, d, "webhook.retry", "webhook_delivery", c.Param("deliveryID"), gin.H{"project_id": project.ID})
		}
		respond(c, delivery, err)
	})
}

func registerAPIKeyLifecycleV2(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.POST("/api-keys/:keyID/rotate", func(c *gin.Context) {
		key, ok := requireAPIKeyAccess(c, d, admin)
		if !ok {
			return
		}
		var input struct {
			GraceSeconds       int `json:"grace_seconds"`
			GracePeriodSeconds int `json:"grace_period_seconds"`
		}
		if c.Request.ContentLength > 0 && c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid rotation body is required.")
			return
		}
		graceSeconds := input.GracePeriodSeconds
		if graceSeconds == 0 {
			graceSeconds = input.GraceSeconds
		}
		if graceSeconds == 0 {
			graceSeconds = 300
		}
		if graceSeconds < 30 || graceSeconds > 86400 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "The grace period must be between 30 and 86400 seconds.")
			return
		}
		full, prefix, hash, err := d.APIKeys.Generate(key.Environment)
		if err != nil {
			respond(c, nil, err)
			return
		}
		graceUntil := time.Now().UTC().Add(time.Duration(graceSeconds) * time.Second)
		version, err := d.Store.RotateAPIKeyVersion(c.Request.Context(), key.ID, prefix, hash, graceUntil)
		if err != nil {
			respond(c, nil, err)
			return
		}
		eventID := id.UUID()
		payload := map[string]any{"id": eventID, "type": "api_key.rotated", "created_at": time.Now().UTC(), "project_id": key.ProjectID,
			"data": map[string]any{"api_key_id": key.ID, "version": version.Version, "grace_expires_at": graceUntil}}
		_, _ = d.Store.EnqueueWebhookEvent(c.Request.Context(), key.ProjectID, eventID, "api_key.rotated", payload, d.Config.WebhookMaxAttempts)
		audit(c, d, "api_key.rotate", "api_key", key.ID, version)
		c.JSON(http.StatusCreated, gin.H{"api_key": key, "version": version, "key": full, "secret": full, "grace_expires_at": graceUntil, "warning": "This key is shown once."})
	})
	g.POST("/api-keys/:keyID/finalize", func(c *gin.Context) {
		key, ok := requireAPIKeyAccess(c, d, admin)
		if !ok {
			return
		}
		var input struct {
			Version int `json:"version"`
		}
		if c.Request.ContentLength > 0 && c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid finalization body is required.")
			return
		}
		err := d.Store.FinalizeAPIKeyRotation(c.Request.Context(), key.ID, input.Version)
		if err == nil {
			audit(c, d, "api_key.rotation_finalize", "api_key", key.ID, gin.H{"version": input.Version})
		}
		respond(c, gin.H{"finalized": err == nil, "api_key_id": key.ID}, err)
	})
}

func requireOrganizationAccess(c *gin.Context, d Dependencies, admin bool, minimumRole string) (domain.Organization, bool) {
	organization, err := d.Store.OrganizationByID(c.Request.Context(), c.Param("organizationID"))
	if err != nil {
		respond(c, nil, err)
		return domain.Organization{}, false
	}
	if admin || claimsFrom(c).Role == "SUPER_ADMIN" {
		return organization, true
	}
	members, err := d.Store.ListOrganizationMembers(c.Request.Context(), organization.ID)
	if err != nil {
		respond(c, nil, err)
		return domain.Organization{}, false
	}
	role := ""
	for _, member := range members {
		if member.UserID == claimsFrom(c).Subject && member.Status == "ACTIVE" {
			role = member.Role
			break
		}
	}
	if organizationRoleRank(role) < organizationRoleRank(minimumRole) {
		respond(c, nil, store.ErrNotFound)
		return domain.Organization{}, false
	}
	return organization, true
}

func organizationRoleRank(role string) int {
	return map[string]int{"VIEWER": 1, "MEMBER": 2, "ADMIN": 3, "OWNER": 4}[strings.ToUpper(strings.TrimSpace(role))]
}

func requireProjectAccess(c *gin.Context, d Dependencies, admin bool, minimumRole string) (domain.Project, bool) {
	return requireProjectIDAccess(c, d, admin, c.Param("projectID"), minimumRole)
}

func requireProjectIDAccess(c *gin.Context, d Dependencies, admin bool, projectID, minimumRole string) (domain.Project, bool) {
	project, err := d.Store.ProjectByID(c.Request.Context(), projectID)
	if err != nil {
		respond(c, nil, err)
		return domain.Project{}, false
	}
	if !admin {
		if _, err = d.Store.CheckProjectAccess(c.Request.Context(), claimsFrom(c).Subject, project.ID, minimumRole); err != nil {
			respond(c, nil, err)
			return domain.Project{}, false
		}
	}
	return project, true
}

func requireAPIKeyAccess(c *gin.Context, d Dependencies, admin bool) (domain.APIKey, bool) {
	key, err := d.Store.ProjectAPIKeyByID(c.Request.Context(), c.Param("keyID"))
	if err != nil {
		respond(c, nil, err)
		return domain.APIKey{}, false
	}
	if !admin {
		userID := claimsFrom(c).Subject
		if key.UserID != userID {
			respond(c, nil, store.ErrNotFound)
			return domain.APIKey{}, false
		}
		if _, err = d.Store.CheckProjectAccess(c.Request.Context(), userID, key.ProjectID, "DEVELOPER"); err != nil {
			respond(c, nil, err)
			return domain.APIKey{}, false
		}
	}
	return key, true
}

func findProjectRoute(c *gin.Context, d Dependencies, projectID, routeID string) (domain.ProjectModelRoute, bool, error) {
	routes, err := d.Store.ListProjectRoutes(c.Request.Context(), projectID)
	if err != nil {
		return domain.ProjectModelRoute{}, false, err
	}
	for _, route := range routes {
		if route.ID == routeID || route.ModelRouteID == routeID {
			return route, true, nil
		}
	}
	return domain.ProjectModelRoute{}, false, nil
}

func exportProjectUsage(c *gin.Context, d Dependencies, admin bool) {
	project, ok := requireProjectAccess(c, d, admin, "VIEWER")
	if !ok {
		return
	}
	if !admin {
		if err := d.Store.RequireBooleanEntitlement(c.Request.Context(), project.OrganizationID, "cost_analysis"); err != nil {
			respond(c, nil, err)
			return
		}
	}
	from, to, err := queryTimeRange(c, 30*24*time.Hour, 366*24*time.Hour)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !admin {
		entitlements, entitlementErr := d.Store.EffectiveEntitlements(c.Request.Context(), project.OrganizationID)
		if entitlementErr != nil {
			respond(c, nil, entitlementErr)
			return
		}
		retentionStart := time.Now().UTC().AddDate(0, 0, -int(entitlements.LogRetentionDays))
		if from.Before(retentionStart) {
			from = retentionStart
		}
	}
	filter := domain.UsageExportFilter{OrganizationID: project.OrganizationID, ProjectID: project.ID, From: from, To: to, Limit: 100000}
	if !admin {
		filter.UserID = claimsFrom(c).Subject
	}
	rows, err := d.Store.ExportUsageRows(c.Request.Context(), filter)
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="relayedock-project-usage.csv"`)
	c.Status(http.StatusOK)
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"request_id", "organization_id", "project_id", "user_id", "api_key_id", "route_id", "model", "endpoint", "status_code", "input_tokens", "cached_input_tokens", "output_tokens", "total_tokens", "estimated_cost", "latency_ms", "created_at"})
	for _, row := range rows {
		_ = w.Write([]string{
			store.CSVSafeCell(row.RequestID), store.CSVSafeCell(row.OrganizationID), store.CSVSafeCell(row.ProjectID),
			store.CSVSafeCell(row.UserID), store.CSVSafeCell(row.APIKeyID), store.CSVSafeCell(row.RouteID),
			store.CSVSafeCell(row.Model), store.CSVSafeCell(row.Endpoint), strconv.Itoa(row.StatusCode),
			strconv.FormatInt(row.InputTokens, 10), strconv.FormatInt(row.CachedTokens, 10), strconv.FormatInt(row.OutputTokens, 10),
			strconv.FormatInt(row.TotalTokens, 10), strconv.FormatFloat(row.EstimatedCost, 'f', 8, 64),
			strconv.FormatInt(row.LatencyMS, 10), row.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	w.Flush()
}

func queryTimeRange(c *gin.Context, defaultWindow, maximumWindow time.Duration) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-defaultWindow)
	to := now.Add(time.Second)
	var err error
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		from, _, err = parseQueryTime(raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must be an RFC3339 timestamp or YYYY-MM-DD date")
		}
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		var dateOnly bool
		to, dateOnly, err = parseQueryTime(raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("to must be an RFC3339 timestamp or YYYY-MM-DD date")
		}
		if dateOnly {
			to = to.AddDate(0, 0, 1)
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("to must be later than from")
	}
	if maximumWindow > 0 && to.Sub(from) > maximumWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("the requested range must not exceed %.0f days", maximumWindow.Hours()/24)
	}
	return from, to, nil
}

func parseQueryTime(value string) (time.Time, bool, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), false, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	return parsed.UTC(), true, err
}

func validTenantSlug(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validTenantStatus(value string) bool {
	return value == "ACTIVE" || value == "DISABLED" || value == "ARCHIVED"
}

func validOrganizationRole(value string) bool {
	return value == "OWNER" || value == "ADMIN" || value == "MEMBER" || value == "VIEWER"
}

func validProjectRole(value string) bool {
	return value == "ADMIN" || value == "DEVELOPER" || value == "VIEWER"
}

func validMembershipStatus(value string) bool {
	return value == "ACTIVE" || value == "DISABLED"
}

func validWebhookEventTypes(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if _, ok := supportedWebhookEvents[strings.TrimSpace(value)]; !ok {
			return false
		}
	}
	return true
}

func normalizedWebhookEventTypes(values []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := supportedWebhookEvents[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func randomSigningSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
