package server

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/pricing"
	"github.com/relayedock/relayedock/internal/store"
)

func registerProviderQualityAdminRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/provider-quality", func(c *gin.Context) {
		items, err := d.Store.ListProviderQualitySummaries(c.Request.Context())
		respondList(c, items, err)
	})
	g.GET("/provider-quality/sla-events", func(c *gin.Context) {
		items, err := d.Store.ListProviderSLAEvents(c.Request.Context(), strings.TrimSpace(c.Query("provider_id")),
			strings.TrimSpace(c.Query("status")), queryLimit(c, 100))
		respondList(c, items, err)
	})
	g.GET("/provider-quality/price-verifications", func(c *gin.Context) {
		items, err := d.Store.ListProviderPriceVerifications(c.Request.Context(), strings.TrimSpace(c.Query("provider_id")), queryLimit(c, 100))
		respondList(c, items, err)
	})
	g.POST("/provider-quality/price-verifications", func(c *gin.Context) {
		var input domain.ProviderPriceVerification
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid exact-decimal price verification is required.")
			return
		}
		input.SourceType = strings.ToUpper(strings.TrimSpace(input.SourceType))
		input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
		input.EvidenceSHA256 = strings.ToLower(strings.TrimSpace(input.EvidenceSHA256))
		input.SourceReference = strings.TrimSpace(input.SourceReference)
		input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		if len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > 190 || !validPriceVerification(input) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Valid Provider/model IDs, evidence hash, source, exact prices, unit, observed_at, and Idempotency-Key are required.")
			return
		}
		out, created, err := d.Store.CreateProviderPriceVerification(c.Request.Context(), input, claimsFrom(c).Subject)
		if errors.Is(err, store.ErrIdempotencyConflict) {
			openAIError(c, http.StatusConflict, "idempotency_conflict", "The idempotency key was already used with different verification data.")
			return
		}
		if err != nil {
			respond(c, nil, err)
			return
		}
		if created {
			c.JSON(http.StatusCreated, out)
		} else {
			c.JSON(http.StatusOK, out)
		}
	})
	g.PUT("/providers/:id/quality-policy", func(c *gin.Context) {
		var policy domain.ProviderQualityPolicy
		if c.ShouldBindJSON(&policy) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A complete Provider quality policy is required.")
			return
		}
		policy.ProviderID = c.Param("id")
		out, err := d.Store.UpsertProviderQualityPolicy(c.Request.Context(), policy, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.POST("/providers/:id/quality/evaluate", func(c *gin.Context) {
		out, err := d.Store.EvaluateProviderQuality(c.Request.Context(), c.Param("id"), time.Now().UTC())
		respond(c, out, err)
	})
	g.POST("/providers/:id/quality/circuit-reset", func(c *gin.Context) {
		var input struct {
			Reason string `json:"reason"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 500 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A circuit reset reason of at most 500 characters is required.")
			return
		}
		out, err := d.Store.ResetProviderQualityCircuit(c.Request.Context(), c.Param("id"), input.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.POST("/providers/:id/supplier-link", func(c *gin.Context) {
		var input struct {
			SupplierID string `json:"supplier_id"`
			Reason     string `json:"reason"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.SupplierID) == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 500 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "An approved supplier_id and reason of at most 500 characters are required.")
			return
		}
		out, err := d.Store.LinkSupplierProvider(c.Request.Context(), c.Param("id"), input.SupplierID, input.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
}

func queryLimit(c *gin.Context, fallback int) int {
	limit, _ := page(c)
	if limit <= 0 {
		return fallback
	}
	return limit
}

func validPriceVerification(input domain.ProviderPriceVerification) bool {
	if input.ProviderID == "" || input.ModelID == "" || input.Unit <= 0 || input.ObservedAt.IsZero() {
		return false
	}
	if input.SourceType != "OFFICIAL_API" && input.SourceType != "OFFICIAL_DOCUMENT" && input.SourceType != "CONTRACT_INVOICE" {
		return false
	}
	if strings.TrimSpace(input.SourceReference) == "" || len(input.SourceReference) > 1000 {
		return false
	}
	if input.SourceType != "CONTRACT_INVOICE" {
		parsed, err := url.Parse(input.SourceReference)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return false
		}
	}
	hash, err := hex.DecodeString(input.EvidenceSHA256)
	if err != nil || len(hash) != evidenceSHA256Size {
		return false
	}
	if len(input.Currency) != 3 {
		return false
	}
	for _, value := range []string{input.ObservedInputTokenCost, input.ObservedCachedInputTokenCost, input.ObservedOutputTokenCost, input.ObservedRequestFixedCost} {
		if pricing.ValidateStoredDecimal(value) != nil {
			return false
		}
	}
	return true
}

const evidenceSHA256Size = 32
