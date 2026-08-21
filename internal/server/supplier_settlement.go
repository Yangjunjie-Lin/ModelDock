package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

func registerSupplierSettlementConsoleRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/suppliers/:id/payables/summary", func(c *gin.Context) {
		if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
			respond(c, nil, err)
			return
		}
		out, err := d.Store.SupplierPayableSummary(c.Request.Context(), c.Param("id"))
		respondList(c, out, err)
	})
	g.GET("/suppliers/:id/payables", func(c *gin.Context) {
		if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset := page(c)
		out, err := d.Store.ListSupplierPayableAccruals(c.Request.Context(), c.Param("id"), limit, offset)
		respondList(c, out, err)
	})
	g.GET("/suppliers/:id/settlements", func(c *gin.Context) {
		if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset := page(c)
		out, err := d.Store.ListSupplierSettlementBatches(c.Request.Context(), c.Param("id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.GET("/suppliers/:id/settlements/:batchID", func(c *gin.Context) {
		if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
			respond(c, nil, err)
			return
		}
		out, err := d.Store.SupplierSettlementBatchByID(c.Request.Context(), c.Param("batchID"), true)
		if err == nil && out.SupplierID != c.Param("id") {
			err = store.ErrNotFound
		}
		respond(c, out, err)
	})
	g.GET("/suppliers/:id/bills", func(c *gin.Context) {
		if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset := page(c)
		out, err := d.Store.ListSupplierBills(c.Request.Context(), c.Param("id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.POST("/suppliers/:id/bills", func(c *gin.Context) { importSupplierBillHandler(c, d, false) })
	g.GET("/suppliers/:id/appeals", func(c *gin.Context) {
		if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
			respond(c, nil, err)
			return
		}
		limit, offset := page(c)
		out, err := d.Store.ListSupplierAppeals(c.Request.Context(), c.Param("id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.POST("/suppliers/:id/appeals", func(c *gin.Context) { createSupplierAppealHandler(c, d) })
}

func registerSupplierSettlementAdminRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/payout-adapters", func(c *gin.Context) {
		if d.Payouts == nil {
			c.JSON(http.StatusOK, gin.H{"items": []any{}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": d.Payouts.List()})
	})
	g.GET("/supplier-payables", func(c *gin.Context) {
		limit, offset := page(c)
		out, err := d.Store.ListSupplierPayableAccruals(c.Request.Context(), c.Query("supplier_id"), limit, offset)
		respondList(c, out, err)
	})
	g.GET("/supplier-settlement-policies/:supplierID", func(c *gin.Context) {
		out, err := d.Store.SupplierSettlementPolicy(c.Request.Context(), c.Param("supplierID"))
		respond(c, out, err)
	})
	g.PUT("/supplier-settlement-policies/:supplierID", func(c *gin.Context) {
		var in domain.SupplierSettlementPolicy
		if c.ShouldBindJSON(&in) != nil {
			financeBadRequest(c, "A valid exact-decimal settlement policy is required.")
			return
		}
		if in.Enabled {
			if d.Payouts == nil {
				openAIError(c, http.StatusUnprocessableEntity, "payout_adapter_unavailable", "No payout adapter registry is configured.")
				return
			}
			if _, err := d.Payouts.Resolve(in.PayoutAdapter, in.PayoutRegion); err != nil {
				openAIError(c, http.StatusForbidden, "payout_adapter_unavailable", "The payout adapter switch, contract, production status, or region does not permit settlement.")
				return
			}
		}
		in.SupplierID = c.Param("supplierID")
		out, err := d.Store.UpdateSupplierSettlementPolicy(c.Request.Context(), in, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.GET("/supplier-settlements", func(c *gin.Context) {
		limit, offset := page(c)
		out, err := d.Store.ListSupplierSettlementBatches(c.Request.Context(), c.Query("supplier_id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.GET("/supplier-settlements/:id", func(c *gin.Context) {
		out, err := d.Store.SupplierSettlementBatchByID(c.Request.Context(), c.Param("id"), true)
		respond(c, out, err)
	})
	g.POST("/supplier-settlements/run", func(c *gin.Context) {
		var in struct {
			SupplierID     string `json:"supplier_id"`
			ProviderID     string `json:"provider_id"`
			PeriodStart    string `json:"period_start"`
			PeriodEnd      string `json:"period_end"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&in) != nil {
			financeBadRequest(c, "supplier_id, provider_id, period_start, period_end, and idempotency_key are required.")
			return
		}
		start, err := time.Parse("2006-01-02", in.PeriodStart)
		if err != nil {
			financeBadRequest(c, "period_start must be YYYY-MM-DD.")
			return
		}
		end, err := time.Parse("2006-01-02", in.PeriodEnd)
		if err != nil {
			financeBadRequest(c, "period_end must be YYYY-MM-DD.")
			return
		}
		_, _ = d.Store.AccrueEligibleSupplierUsage(c.Request.Context(), 500)
		actor := claimsFrom(c).Subject
		out, replayed, err := d.Store.CreateSupplierSettlementBatch(c.Request.Context(), in.SupplierID, in.ProviderID, start, end, in.IdempotencyKey, &actor)
		financeCreatedOrReplay(c, out, replayed, err)
	})
	g.POST("/supplier-settlements/:id/approve", func(c *gin.Context) {
		var in struct {
			ProviderStatementID string `json:"provider_statement_id"`
			Reason              string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Reason) == "" {
			financeBadRequest(c, "provider_statement_id and an approval reason are required for unmatched usage.")
			return
		}
		out, err := d.Store.ApproveSupplierSettlement(c.Request.Context(), c.Param("id"), in.ProviderStatementID, in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.POST("/supplier-settlements/:id/retry", func(c *gin.Context) {
		var in struct {
			Reason string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil {
			financeBadRequest(c, "reason is required.")
			return
		}
		out, err := d.Store.RetrySupplierSettlement(c.Request.Context(), c.Param("id"), in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.PATCH("/supplier-settlements/:id/compliance", func(c *gin.Context) {
		var in struct {
			TaxStatus     string `json:"tax_status"`
			InvoiceStatus string `json:"invoice_status"`
			Reason        string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil {
			financeBadRequest(c, "tax_status, invoice_status, and reason are required.")
			return
		}
		out, err := d.Store.UpdateSupplierSettlementCompliance(c.Request.Context(), c.Param("id"), in.TaxStatus, in.InvoiceStatus, in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.POST("/supplier-payables/:id/refund-share", func(c *gin.Context) {
		var in struct {
			Amount         string `json:"amount"`
			Reference      string `json:"reference"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&in) != nil || !validPositiveFinanceDecimal(in.Amount) {
			financeBadRequest(c, "A positive exact-decimal amount, reference, and idempotency_key are required.")
			return
		}
		out, replayed, err := d.Store.CreateSupplierRefundShare(c.Request.Context(), c.Param("id"), in.Amount, in.Reference, in.IdempotencyKey, claimsFrom(c).Subject)
		financeCreatedOrReplay(c, out, replayed, err)
	})
	g.GET("/supplier-bills", func(c *gin.Context) {
		limit, offset := page(c)
		out, err := d.Store.ListSupplierBills(c.Request.Context(), c.Query("supplier_id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.POST("/supplier-bills", func(c *gin.Context) { importSupplierBillHandler(c, d, true) })
	g.POST("/supplier-bills/:id/reconcile", func(c *gin.Context) {
		out, err := d.Store.ReconcileSupplierBill(c.Request.Context(), c.Param("id"), claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.GET("/supplier-appeals", func(c *gin.Context) {
		limit, offset := page(c)
		out, err := d.Store.ListSupplierAppeals(c.Request.Context(), c.Query("supplier_id"), c.Query("status"), limit, offset)
		respondList(c, out, err)
	})
	g.POST("/supplier-appeals/:id/resolve", func(c *gin.Context) {
		var in struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil {
			financeBadRequest(c, "decision and reason are required.")
			return
		}
		out, err := d.Store.ResolveSupplierAppeal(c.Request.Context(), c.Param("id"), in.Decision, in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
}

func importSupplierBillHandler(c *gin.Context, d Dependencies, admin bool) {
	var in struct {
		SupplierID    string                        `json:"supplier_id"`
		ProviderID    string                        `json:"provider_id"`
		BillReference string                        `json:"bill_reference"`
		PeriodStart   string                        `json:"period_start"`
		PeriodEnd     string                        `json:"period_end"`
		Currency      string                        `json:"currency"`
		TotalAmount   string                        `json:"total_amount"`
		SourceSHA256  string                        `json:"source_sha256"`
		Lines         []store.SupplierBillLineInput `json:"lines"`
	}
	if c.ShouldBindJSON(&in) != nil || !validNonNegativeFinanceDecimal(in.TotalAmount) {
		financeBadRequest(c, "A valid bill and exact-decimal line amounts are required.")
		return
	}
	if !admin {
		in.SupplierID = c.Param("id")
		if _, err := supplierOwnedBy(c, d, in.SupplierID); err != nil {
			respond(c, nil, err)
			return
		}
	}
	start, err := time.Parse("2006-01-02", in.PeriodStart)
	if err != nil {
		financeBadRequest(c, "period_start must be YYYY-MM-DD.")
		return
	}
	end, err := time.Parse("2006-01-02", in.PeriodEnd)
	if err != nil {
		financeBadRequest(c, "period_end must be YYYY-MM-DD.")
		return
	}
	for i := range in.Lines {
		if !validNonNegativeFinanceDecimal(in.Lines[i].Amount) {
			financeBadRequest(c, "Bill line amounts must be exact non-negative decimals.")
			return
		}
		in.Lines[i].Currency = strings.ToUpper(in.Lines[i].Currency)
	}
	out, replayed, err := d.Store.ImportSupplierBill(c.Request.Context(), store.SupplierBillInput{SupplierID: in.SupplierID, ProviderID: in.ProviderID, BillReference: in.BillReference, PeriodStart: start, PeriodEnd: end, Currency: in.Currency, TotalAmount: in.TotalAmount, SourceSHA256: in.SourceSHA256, DeclaredBy: claimsFrom(c).Subject, Lines: in.Lines})
	financeCreatedOrReplay(c, out, replayed, err)
}

func createSupplierAppealHandler(c *gin.Context, d Dependencies) {
	if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
		respond(c, nil, err)
		return
	}
	var in domain.SupplierAppeal
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Reason) == "" {
		financeBadRequest(c, "appeal_type, one target, reason, and idempotency_key are required.")
		return
	}
	key := financeIdempotencyKey(c, in.ID)
	if !validFinanceIdempotency(key) {
		financeBadRequest(c, "A bounded Idempotency-Key is required.")
		return
	}
	in.ID, in.AppealNumber, in.SupplierID = key, "pending", c.Param("id")
	out, replayed, err := d.Store.CreateSupplierAppeal(c.Request.Context(), in, claimsFrom(c).Subject)
	financeCreatedOrReplay(c, out, replayed, err)
}
