package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
)

func registerMarketplaceLaunchAdminRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/marketplace/launch-reviews", func(c *gin.Context) {
		limit, offset := page(c)
		out, err := d.Store.ListMarketplaceLaunchReviews(c.Request.Context(), c.Query("listing_id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.GET("/marketplace/launch-reviews/:id", func(c *gin.Context) {
		out, err := d.Store.MarketplaceLaunchReviewByID(c.Request.Context(), c.Param("id"))
		respond(c, out, err)
	})
	g.POST("/marketplace/providers/:id/launch-reviews", func(c *gin.Context) {
		var in struct {
			PolicyVersion string `json:"policy_version"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid launch-review body is required.")
			return
		}
		key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		if key == "" || len(key) > 200 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A bounded Idempotency-Key is required.")
			return
		}
		out, replayed, err := d.Store.CreateMarketplaceLaunchReview(c.Request.Context(), c.Param("id"), key, in.PolicyVersion, claimsFrom(c).Subject)
		financeCreatedOrReplay(c, out, replayed, err)
	})
	g.POST("/marketplace/launch-reviews/:id/evaluate", func(c *gin.Context) {
		out, err := d.Store.EvaluateMarketplaceLaunchReview(c.Request.Context(), c.Param("id"), claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.PUT("/marketplace/launch-reviews/:id/gates/:gateCode", func(c *gin.Context) {
		var in struct {
			Status            string `json:"status"`
			EvidenceReference string `json:"evidence_reference"`
			Reason            string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "status, evidence_reference, and reason are required.")
			return
		}
		out, err := d.Store.AttestMarketplaceLaunchGate(c.Request.Context(), c.Param("id"), c.Param("gateCode"),
			in.Status, in.EvidenceReference, in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.POST("/marketplace/launch-reviews/:id/approve", func(c *gin.Context) {
		var in struct {
			Reason string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Reason) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A release approval reason is required.")
			return
		}
		out, err := d.Store.ApproveMarketplaceLaunchReview(c.Request.Context(), c.Param("id"), in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.GET("/marketplace/providers/:id/lifecycle-events", func(c *gin.Context) {
		out, err := d.Store.ListMarketplaceLifecycleEvents(c.Request.Context(), c.Param("id"), queryLimit(c, 100))
		respondList(c, out, err)
	})
	g.POST("/marketplace/providers/:id/lifecycle", func(c *gin.Context) {
		var in struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Reason) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A lifecycle action and reason are required.")
			return
		}
		out, err := d.Store.MarketplaceLifecycleAction(c.Request.Context(), c.Param("id"), in.Action, in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.GET("/marketplace/payout-readiness/:supplierID", func(c *gin.Context) {
		out, err := d.Store.SupplierPayoutReadiness(c.Request.Context(), c.Param("supplierID"))
		respond(c, out, err)
	})
	g.PUT("/marketplace/payout-readiness/:supplierID", func(c *gin.Context) {
		var in struct {
			ContractStatus            string `json:"contract_status"`
			ContractEvidenceReference string `json:"contract_evidence_reference"`
			TaxStatus                 string `json:"tax_status"`
			TaxEvidenceReference      string `json:"tax_evidence_reference"`
			PaymentStatus             string `json:"payment_status"`
			PaymentEvidenceReference  string `json:"payment_evidence_reference"`
			SecurityStatus            string `json:"security_status"`
			SecurityEvidenceReference string `json:"security_evidence_reference"`
			ReviewReason              string `json:"review_reason"`
			ExpectedVersion           int64  `json:"expected_version"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid payout-readiness review is required.")
			return
		}
		value := domain.SupplierPayoutReadinessReview{SupplierID: c.Param("supplierID"), ContractStatus: in.ContractStatus,
			ContractEvidenceReference: in.ContractEvidenceReference, TaxStatus: in.TaxStatus,
			TaxEvidenceReference: in.TaxEvidenceReference, PaymentStatus: in.PaymentStatus,
			PaymentEvidenceReference: in.PaymentEvidenceReference, SecurityStatus: in.SecurityStatus,
			SecurityEvidenceReference: in.SecurityEvidenceReference, ReviewReason: in.ReviewReason}
		out, err := d.Store.UpdateSupplierPayoutReadiness(c.Request.Context(), value, in.ExpectedVersion, claimsFrom(c).Subject)
		respond(c, out, err)
	})
}
