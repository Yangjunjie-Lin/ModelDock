package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

func registerAdminSubscriptionRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/subscription-plans", func(c *gin.Context) {
		plans, err := d.Store.ListSubscriptionPlans(c.Request.Context(), true)
		respondList(c, plans, err)
	})
	g.POST("/subscription-plans", func(c *gin.Context) {
		var input domain.SubscriptionPlan
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Slug) == "" || strings.TrimSpace(input.Name) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "slug and name are required.")
			return
		}
		input.Enabled = true
		input.CreatedBy = stringPtr(claimsFrom(c).Subject)
		out, err := d.Store.CreateSubscriptionPlan(c.Request.Context(), input)
		if err == nil {
			audit(c, d, "subscription.plan_create", "subscription_plan", out.ID, out)
		}
		respondCreated(c, out, err)
	})
	g.GET("/subscription-plans/:planID/versions", func(c *gin.Context) {
		versions, err := d.Store.ListPlanVersions(c.Request.Context(), c.Param("planID"))
		respondList(c, versions, err)
	})
	g.POST("/subscription-plans/:planID/versions", func(c *gin.Context) {
		var input domain.PlanVersion
		if c.ShouldBindJSON(&input) != nil || input.SubscriptionFee.String() == "" || input.Currency == "" || input.BillingInterval == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "subscription_fee, currency, billing_interval, and all entitlements are required.")
			return
		}
		input.SubscriptionPlanID = c.Param("planID")
		input.CreatedBy = stringPtr(claimsFrom(c).Subject)
		if input.EffectiveAt.IsZero() {
			input.EffectiveAt = time.Now().UTC()
		}
		out, err := d.Store.CreatePlanVersion(c.Request.Context(), input)
		if err == nil {
			audit(c, d, "subscription.plan_version_create", "plan_version", out.ID, out)
		}
		respondCreated(c, out, err)
	})
	g.POST("/plan-versions/:versionID/freeze", func(c *gin.Context) {
		actor := stringPtr(claimsFrom(c).Subject)
		out, err := d.Store.FreezePlanVersion(c.Request.Context(), c.Param("versionID"), actor)
		respond(c, out, err)
	})
	g.GET("/coupons", func(c *gin.Context) {
		coupons, err := d.Store.ListCoupons(c.Request.Context())
		respondList(c, coupons, err)
	})
	g.POST("/coupons", func(c *gin.Context) {
		var input domain.Coupon
		if c.ShouldBindJSON(&input) != nil || input.Code == "" || input.Name == "" || input.DiscountType == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "code, name, and a valid discount are required.")
			return
		}
		input.CreatedBy = stringPtr(claimsFrom(c).Subject)
		if input.StartsAt.IsZero() {
			input.StartsAt = time.Now().UTC()
		}
		input.Enabled = true
		out, err := d.Store.CreateCoupon(c.Request.Context(), input)
		if err == nil {
			audit(c, d, "subscription.coupon_create", "coupon", out.ID, out)
		}
		respondCreated(c, out, err)
	})
	registerSubscriptionCommonRoutes(g, d, true)
	g.POST("/subscription-invoices/:invoiceID/pay", func(c *gin.Context) {
		var input struct {
			PaymentProvider          string `json:"payment_provider"`
			ProviderPaymentReference string `json:"provider_payment_reference"`
			IdempotencyKey           string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil || input.PaymentProvider == "" || input.ProviderPaymentReference == "" || input.IdempotencyKey == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "payment_provider, provider_payment_reference, and idempotency_key are required.")
			return
		}
		out, err := d.Store.PaySubscriptionInvoice(c.Request.Context(), store.SubscriptionPaymentRequest{
			InvoiceID: c.Param("invoiceID"), PaymentProvider: input.PaymentProvider,
			ProviderPaymentReference: input.ProviderPaymentReference, IdempotencyKey: input.IdempotencyKey,
			CreatedBy: stringPtr(claimsFrom(c).Subject),
		})
		respond(c, out, err)
	})
	g.POST("/subscription-invoices/:invoiceID/fail", func(c *gin.Context) {
		var input struct {
			IdempotencyKey string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil || input.IdempotencyKey == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "idempotency_key is required.")
			return
		}
		out, err := d.Store.FailSubscriptionInvoice(c.Request.Context(), c.Param("invoiceID"), input.IdempotencyKey,
			stringPtr(claimsFrom(c).Subject))
		respond(c, out, err)
	})
}

func registerConsoleSubscriptionRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/subscription-plans", func(c *gin.Context) {
		plans, err := d.Store.ListSubscriptionPlans(c.Request.Context(), false)
		respondList(c, plans, err)
	})
	g.GET("/subscription-plans/:planID/versions", func(c *gin.Context) {
		versions, err := d.Store.ListPlanVersions(c.Request.Context(), c.Param("planID"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		published := versions[:0]
		for _, version := range versions {
			if version.Status == "FROZEN" && !version.EnterpriseContract {
				published = append(published, version)
			}
		}
		respondList(c, published, nil)
	})
	registerSubscriptionCommonRoutes(g, d, false)
}

func registerSubscriptionCommonRoutes(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/organizations/:organizationID/subscription", func(c *gin.Context) {
		organization, ok := requireSubscriptionOrganization(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		subscription, err := d.Store.CurrentSubscription(c.Request.Context(), organization.ID)
		if err != nil {
			respond(c, nil, err)
			return
		}
		entitlements, err := d.Store.EffectiveEntitlements(c.Request.Context(), organization.ID)
		respond(c, gin.H{"subscription": subscription, "entitlements": entitlements, "token_billing_mode": "METERED_SEPARATE"}, err)
	})
	g.GET("/organizations/:organizationID/subscriptions", func(c *gin.Context) {
		organization, ok := requireSubscriptionOrganization(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		items, err := d.Store.ListOrganizationSubscriptions(c.Request.Context(), organization.ID, limit, offset)
		respondList(c, items, err)
	})
	g.POST("/organizations/:organizationID/subscription/change", func(c *gin.Context) {
		organization, ok := requireSubscriptionOrganization(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input struct {
			PlanVersionID     string         `json:"plan_version_id"`
			Mode              string         `json:"mode"`
			UseTrial          bool           `json:"use_trial"`
			CouponCode        string         `json:"coupon_code"`
			IdempotencyKey    string         `json:"idempotency_key"`
			ContractReference string         `json:"contract_reference"`
			ContractStartsAt  *time.Time     `json:"contract_starts_at"`
			ContractEndsAt    *time.Time     `json:"contract_ends_at"`
			Metadata          map[string]any `json:"metadata"`
		}
		if c.ShouldBindJSON(&input) != nil || input.PlanVersionID == "" || input.IdempotencyKey == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "plan_version_id and idempotency_key are required.")
			return
		}
		if !admin {
			version, versionErr := d.Store.PlanVersionByID(c.Request.Context(), input.PlanVersionID)
			if versionErr != nil {
				respond(c, nil, versionErr)
				return
			}
			if version.EnterpriseContract {
				openAIError(c, http.StatusForbidden, "admin_required", "Enterprise manual contracts must be activated by an administrator.")
				return
			}
		}
		out, invoice, err := d.Store.ChangeSubscription(c.Request.Context(), store.SubscriptionChangeRequest{
			OrganizationID: organization.ID, PlanVersionID: input.PlanVersionID, Mode: input.Mode,
			UseTrial: input.UseTrial, CouponCode: input.CouponCode, IdempotencyKey: input.IdempotencyKey,
			ContractReference: input.ContractReference, ContractStartsAt: input.ContractStartsAt,
			ContractEndsAt: input.ContractEndsAt, Metadata: input.Metadata, CreatedBy: stringPtr(claimsFrom(c).Subject),
		})
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"subscription": out, "invoice": invoice, "token_billing_mode": "METERED_SEPARATE"})
	})
	g.POST("/organizations/:organizationID/subscription/cancel", func(c *gin.Context) {
		organization, ok := requireSubscriptionOrganization(c, d, admin, "ADMIN")
		if !ok {
			return
		}
		var input struct {
			Mode           string `json:"mode"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil || input.IdempotencyKey == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "mode and idempotency_key are required.")
			return
		}
		out, err := d.Store.CancelSubscription(c.Request.Context(), store.SubscriptionCancelRequest{
			OrganizationID: organization.ID, Mode: input.Mode, IdempotencyKey: input.IdempotencyKey,
			CreatedBy: stringPtr(claimsFrom(c).Subject),
		})
		respond(c, out, err)
	})
	g.GET("/organizations/:organizationID/subscription-invoices", func(c *gin.Context) {
		organization, ok := requireSubscriptionOrganization(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		items, err := d.Store.ListSubscriptionInvoices(c.Request.Context(), organization.ID, limit, offset)
		respondList(c, items, err)
	})
	g.GET("/organizations/:organizationID/subscription-events", func(c *gin.Context) {
		organization, ok := requireSubscriptionOrganization(c, d, admin, "VIEWER")
		if !ok {
			return
		}
		limit, offset := page(c)
		items, err := d.Store.ListSubscriptionEvents(c.Request.Context(), organization.ID, limit, offset)
		respondList(c, items, err)
	})
}

func requireSubscriptionOrganization(c *gin.Context, d Dependencies, admin bool, minimumRole string) (domain.Organization, bool) {
	organizationID := c.Param("organizationID")
	organization, err := d.Store.OrganizationByID(c.Request.Context(), organizationID)
	if err != nil {
		respond(c, nil, err)
		return domain.Organization{}, false
	}
	if admin {
		return organization, true
	}
	members, err := d.Store.ListOrganizationMembers(c.Request.Context(), organizationID)
	if err != nil {
		respond(c, nil, err)
		return domain.Organization{}, false
	}
	ranks := map[string]int{"VIEWER": 1, "MEMBER": 1, "ADMIN": 2, "OWNER": 3}
	role := ""
	for _, member := range members {
		if member.UserID == claimsFrom(c).Subject && member.Status == "ACTIVE" {
			role = member.Role
			break
		}
	}
	if ranks[role] < ranks[minimumRole] {
		respond(c, nil, store.ErrNotFound)
		return domain.Organization{}, false
	}
	return organization, true
}
