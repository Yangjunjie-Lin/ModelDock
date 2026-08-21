package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/store"
)

const paymentWebhookBodyLimit = 1 << 20

func registerPaymentWebhookRoutes(r *gin.Engine, d Dependencies) {
	r.POST("/api/payments/webhooks/:provider", func(c *gin.Context) {
		providerName := strings.ToLower(strings.TrimSpace(c.Param("provider")))
		adapter, err := d.Payments.ResolveWebhook(providerName)
		if err != nil {
			respond(c, nil, err)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, paymentWebhookBodyLimit))
		if err != nil {
			openAIError(c, http.StatusRequestEntityTooLarge, "webhook_too_large", "The payment webhook body is too large.")
			return
		}
		verified, err := adapter.VerifyWebhook(c.Request.Context(), payment.WebhookRequest{Body: body,
			Signature: c.GetHeader("X-Payment-Signature"), Timestamp: c.GetHeader("X-Payment-Timestamp"),
			EventID: c.GetHeader("X-Payment-Event-Id"), Now: time.Now().UTC()})
		if errors.Is(err, payment.ErrInvalidSignature) || errors.Is(err, payment.ErrTimestampInvalid) {
			d.Metrics.IncPayment("webhook_rejected")
			_ = d.Store.RecordOperationalAlert(c.Request.Context(), "PAYMENT_WEBHOOK_INVALID", "WARNING", "Payment webhook signature or timestamp validation failed.", "payment-webhook-invalid:"+providerName, map[string]any{"payment_provider": providerName})
			openAIError(c, http.StatusUnauthorized, "invalid_webhook", "The payment webhook signature or timestamp is invalid.")
			return
		}
		if err != nil {
			d.Metrics.IncPayment("webhook_invalid")
			openAIError(c, http.StatusBadRequest, "invalid_webhook", "The payment webhook payload is invalid.")
			return
		}
		order, err := d.Store.RechargeOrderByPlatformNo(c.Request.Context(), verified.PlatformOrderNo)
		if err != nil {
			respond(c, nil, err)
			return
		}
		if _, err = d.Payments.Resolve(providerName, order.Region); err != nil {
			respond(c, nil, err)
			return
		}
		sum := sha256.Sum256(body)
		order, replayed, err := d.Store.RecordVerifiedPaymentWebhook(c.Request.Context(), providerName, verified, hex.EncodeToString(sum[:]))
		if err != nil {
			d.Metrics.IncPayment("webhook_failed")
			_ = d.Store.RecordOperationalAlert(c.Request.Context(), "PAYMENT_WEBHOOK_FAILED", "CRITICAL", "A verified payment webhook could not be durably processed.", "payment-webhook-failed:"+providerName, map[string]any{"payment_provider": providerName})
			respond(c, nil, err)
			return
		}
		if sandbox, ok := adapter.(*payment.SandboxAdapter); ok {
			sandbox.RememberVerifiedWebhook(verified)
		}
		if order.Status == "PAID" {
			order, _, err = d.Store.CreditPaidRecharge(c.Request.Context(), order.ID)
			if err != nil {
				d.Metrics.IncPayment("credit_pending")
				_ = d.Store.RecordOperationalAlert(c.Request.Context(), "PAYMENT_CREDIT_PENDING", "CRITICAL", "A verified payment is waiting for wallet credit recovery.", "payment-credit:"+order.ID, map[string]any{"order_id": order.ID})
				openAIError(c, http.StatusInternalServerError, "payment_credit_pending", "The verified payment is durable and wallet credit will be retried.")
				return
			}
		}
		d.Metrics.IncPayment("webhook_succeeded")
		_ = d.Store.ResolveOperationalAlert(c.Request.Context(), "payment-webhook-failed:"+providerName)
		c.JSON(http.StatusOK, gin.H{"received": true, "replayed": replayed, "order_status": order.Status})
	})
}

func registerConsolePaymentRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/payment-providers", func(c *gin.Context) {
		providers := d.Payments.List()
		available := make([]payment.Capabilities, 0, len(providers))
		for _, provider := range providers {
			if provider.Enabled {
				available = append(available, provider)
			}
		}
		respondList(c, available, nil)
	})
	g.GET("/organizations/:organizationID/recharge-orders", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		limit, offset := page(c)
		orders, err := d.Store.ListRechargeOrders(c.Request.Context(), organization.ID, limit, offset)
		respondList(c, orders, err)
	})
	g.POST("/organizations/:organizationID/recharge-orders", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		createRechargeOrder(c, d, organization.ID)
	})
	g.GET("/organizations/:organizationID/recharge-orders/:orderID", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		order, err := d.Store.RechargeOrderByID(c.Request.Context(), c.Param("orderID"))
		if err == nil && order.OrganizationID != organization.ID {
			err = store.ErrNotFound
		}
		respond(c, order, err)
	})
	// A success page may call this endpoint. It performs an authenticated
	// provider query only; any credit still occurs inside the server ledger.
	g.POST("/organizations/:organizationID/recharge-orders/:orderID/query", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		queryRechargeOrder(c, d, organization.ID, false)
	})
}

func registerAdminPaymentRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/payment-providers", func(c *gin.Context) { respondList(c, d.Payments.List(), nil) })
	g.GET("/recharge-orders", func(c *gin.Context) {
		limit, offset := page(c)
		orders, err := d.Store.ListRechargeOrders(c.Request.Context(), strings.TrimSpace(c.Query("organization_id")), limit, offset)
		respondList(c, orders, err)
	})
	g.POST("/recharge-orders/:orderID/query", func(c *gin.Context) { queryRechargeOrder(c, d, "", true) })
	g.POST("/recharge-orders/:orderID/manual-approve", func(c *gin.Context) {
		var input struct {
			EvidenceReference string `json:"evidence_reference"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.EvidenceReference) == "" || len(input.EvidenceReference) > 200 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A non-secret evidence_reference is required.")
			return
		}
		actor := stringPtr(claimsFrom(c).Subject)
		attempt, attemptErr := d.Store.StartPaymentAttempt(c.Request.Context(), c.Param("orderID"), "MANUAL_REVIEW", "", map[string]any{"decision": "approved"})
		if attemptErr != nil {
			respond(c, nil, attemptErr)
			return
		}
		order, err := d.Store.MarkRechargePaid(c.Request.Context(), c.Param("orderID"), input.EvidenceReference, actor)
		if err == nil {
			order, _, err = d.Store.CreditPaidRecharge(c.Request.Context(), order.ID)
		}
		if err != nil {
			_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "FAILED", order.ProviderOrderNo, "", "manual_review_failed", nil)
		} else {
			_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "SUCCEEDED", order.ProviderOrderNo, order.Status, "", nil)
		}
		respond(c, order, err)
	})
	g.POST("/recharge-orders/:orderID/refunds", func(c *gin.Context) {
		var input struct {
			Amount         domain.Decimal `json:"amount"`
			Reason         string         `json:"reason"`
			IdempotencyKey string         `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil || !input.Amount.IsPositive() || strings.TrimSpace(input.Reason) == "" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A positive exact amount and reason are required.")
			return
		}
		if input.IdempotencyKey == "" {
			input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 || len(input.Reason) > 500 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "An Idempotency-Key and a reason of at most 500 characters are required.")
			return
		}
		order, err := d.Store.RechargeOrderByID(c.Request.Context(), c.Param("orderID"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		actor := stringPtr(claimsFrom(c).Subject)
		refund, replayed, err := d.Store.CreateRefundOrder(c.Request.Context(), store.CreateRefundOrderRequest{
			PlatformRefundNo: newPaymentNumber("RF"), RechargeOrderID: order.ID, Amount: input.Amount,
			Reason: input.Reason, IdempotencyKey: input.IdempotencyKey, CreatedBy: actor,
		})
		if err != nil {
			respond(c, refund, err)
			return
		}
		if replayed {
			if refund.Status == "CREATED" {
				// The previous process may have stopped around the provider call.
				// Reuse the same platform refund number as the provider idempotency key.
			} else {
				respond(c, refund, nil)
				return
			}
		}
		adapter, err := d.Payments.Resolve(order.PaymentProvider, order.Region)
		if err != nil {
			respond(c, nil, err)
			return
		}
		attempt, err := d.Store.StartPaymentAttempt(c.Request.Context(), order.ID, "REFUND", "", map[string]any{"refund_order_id": refund.ID})
		if err != nil {
			respond(c, nil, err)
			return
		}
		result, err := adapter.RefundPayment(c.Request.Context(), payment.RefundRequest{PlatformOrderNo: order.PlatformOrderNo,
			ProviderOrderNo: order.ProviderOrderNo, PlatformRefundNo: refund.PlatformRefundNo, Amount: refund.Amount,
			Currency: refund.Currency, Reason: refund.Reason})
		if err != nil {
			_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "FAILED", order.ProviderOrderNo, "", "provider_refund_failed", nil)
			respond(c, nil, err)
			return
		}
		attemptStatus := "PENDING"
		if result.Status == "SUCCEEDED" {
			attemptStatus = "SUCCEEDED"
		}
		_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, attemptStatus, order.ProviderOrderNo, result.Status, "", map[string]any{"provider_refund_no": result.ProviderRefundNo})
		if err = d.Store.MarkRefundPending(c.Request.Context(), refund.ID, result.ProviderRefundNo); err != nil {
			respond(c, nil, err)
			return
		}
		if result.Status == "SUCCEEDED" {
			refund, _, err = d.Store.CompleteRefund(c.Request.Context(), refund.ID, result.ProviderRefundNo)
		} else {
			refund, err = d.Store.RefundOrderByID(c.Request.Context(), refund.ID)
		}
		respondCreated(c, refund, err)
	})
	g.POST("/refund-orders/:refundID/manual-approve", func(c *gin.Context) {
		var input struct {
			EvidenceReference string `json:"evidence_reference"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.EvidenceReference) == "" || len(input.EvidenceReference) > 200 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A non-secret evidence_reference is required.")
			return
		}
		refund, err := d.Store.RefundOrderByID(c.Request.Context(), c.Param("refundID"))
		if err == nil && refund.PaymentProvider != "manual_transfer" {
			err = store.ErrPaymentState
		}
		if err == nil {
			attempt, startErr := d.Store.StartPaymentAttempt(c.Request.Context(), refund.RechargeOrderID, "MANUAL_REVIEW", "", map[string]any{"refund_order_id": refund.ID})
			if startErr != nil {
				respond(c, nil, startErr)
				return
			}
			refund, _, err = d.Store.CompleteRefund(c.Request.Context(), refund.ID, "manual:"+strings.TrimSpace(input.EvidenceReference))
			if err != nil {
				_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "FAILED", "", "", "manual_refund_review_failed", nil)
			} else {
				_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "SUCCEEDED", "", refund.Status, "", nil)
			}
		}
		respond(c, refund, err)
	})
	g.POST("/recharge-orders/:orderID/reconcile", func(c *gin.Context) {
		order, err := d.Store.RechargeOrderByID(c.Request.Context(), c.Param("orderID"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		adapter, err := d.Payments.Resolve(order.PaymentProvider, order.Region)
		if err != nil {
			respond(c, nil, err)
			return
		}
		if !adapter.Capabilities().SupportsReconcile {
			respond(c, nil, payment.ErrReconcileUnsupported)
			return
		}
		attempt, err := d.Store.StartPaymentAttempt(c.Request.Context(), order.ID, "RECONCILE", "", nil)
		if err != nil {
			respond(c, nil, err)
			return
		}
		result, err := adapter.ReconcilePayment(c.Request.Context(), payment.ReconcileRequest{PlatformOrderNo: order.PlatformOrderNo,
			ProviderOrderNo: order.ProviderOrderNo, LocalStatus: order.Status, Amount: order.Amount, Currency: order.Currency})
		if err != nil {
			_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "FAILED", order.ProviderOrderNo, "", "reconcile_failed", nil)
			respond(c, nil, err)
			return
		}
		_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "SUCCEEDED", order.ProviderOrderNo, result.ProviderStatus, "", nil)
		d.Metrics.ObserveReconciliation(order.Amount.String(), result.Amount.String())
		record, err := d.Store.RecordReconciliation(c.Request.Context(), order, newPaymentNumber("RC"), result, stringPtr(claimsFrom(c).Subject))
		respondCreated(c, record, err)
	})
}

func createRechargeOrder(c *gin.Context, d Dependencies, organizationID string) {
	var input struct {
		Amount          domain.Decimal `json:"amount"`
		Currency        string         `json:"currency"`
		Region          string         `json:"region"`
		PaymentProvider string         `json:"payment_provider"`
		IdempotencyKey  string         `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&input) != nil || !input.Amount.IsPositive() {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A positive exact amount is required.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.Region = strings.ToUpper(strings.TrimSpace(input.Region))
	input.PaymentProvider = strings.ToLower(strings.TrimSpace(input.PaymentProvider))
	if len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > 200 || len(input.Currency) != 3 || len(input.Region) != 2 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "payment_provider, currency, region, and an Idempotency-Key are required.")
		return
	}
	adapter, err := d.Payments.Resolve(input.PaymentProvider, input.Region)
	if err != nil {
		respond(c, nil, err)
		return
	}
	actor := stringPtr(claimsFrom(c).Subject)
	order, replayed, err := d.Store.CreateRechargeOrder(c.Request.Context(), store.CreateRechargeOrderRequest{
		PlatformOrderNo: newPaymentNumber("RO"), OrganizationID: organizationID, PaymentProvider: input.PaymentProvider,
		Amount: input.Amount, Currency: input.Currency, Region: input.Region, IdempotencyKey: input.IdempotencyKey,
		ExpiresAt: time.Now().UTC().Add(d.Config.PaymentOrderTTL), CreatedBy: actor,
	})
	if err != nil {
		respond(c, nil, err)
		return
	}
	if order.Status != "CREATED" {
		c.JSON(http.StatusOK, gin.H{"order": order, "replayed": true})
		return
	}
	attempt, err := d.Store.StartPaymentAttempt(c.Request.Context(), order.ID, "CREATE", "", map[string]any{"replayed_request": replayed})
	if err != nil {
		respond(c, nil, err)
		return
	}
	result, err := adapter.CreatePayment(c.Request.Context(), payment.CreateRequest{PlatformOrderNo: order.PlatformOrderNo,
		Amount: order.Amount, Currency: order.Currency, Region: order.Region, ExpiresAt: order.ExpiresAt})
	if err != nil {
		_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "FAILED", "", "", "create_failed", nil)
		respond(c, nil, err)
		return
	}
	_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "SUCCEEDED", result.ProviderOrderNo, result.Status, "", nil)
	order, err = d.Store.MarkRechargePending(c.Request.Context(), order.ID, result.ProviderOrderNo, result.Instructions)
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"order": order, "payment": result, "replayed": replayed})
}

func queryRechargeOrder(c *gin.Context, d Dependencies, organizationID string, admin bool) {
	order, err := d.Store.RechargeOrderByID(c.Request.Context(), c.Param("orderID"))
	if err == nil && !admin && order.OrganizationID != organizationID {
		err = store.ErrNotFound
	}
	if err != nil {
		respond(c, nil, err)
		return
	}
	adapter, err := d.Payments.Resolve(order.PaymentProvider, order.Region)
	if err != nil {
		respond(c, nil, err)
		return
	}
	attempt, err := d.Store.StartPaymentAttempt(c.Request.Context(), order.ID, "QUERY", "", nil)
	if err != nil {
		respond(c, nil, err)
		return
	}
	result, err := adapter.QueryPayment(c.Request.Context(), payment.QueryRequest{PlatformOrderNo: order.PlatformOrderNo, ProviderOrderNo: order.ProviderOrderNo})
	if err != nil {
		_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "FAILED", order.ProviderOrderNo, "", "query_failed", nil)
		respond(c, nil, err)
		return
	}
	_ = d.Store.FinishPaymentAttempt(c.Request.Context(), attempt.ID, "SUCCEEDED", result.ProviderOrderNo, result.Status, "", nil)
	var actor *string
	if claimsFrom(c) != nil {
		actor = stringPtr(claimsFrom(c).Subject)
	}
	order, err = d.Store.ApplyPaymentQueryResult(c.Request.Context(), order.ID, result, actor)
	if err == nil && order.Status == "PAID" {
		order, _, err = d.Store.CreditPaidRecharge(c.Request.Context(), order.ID)
	}
	respond(c, order, err)
}

func newPaymentNumber(prefix string) string {
	return prefix + time.Now().UTC().Format("20060102T150405") + strings.ReplaceAll(id.UUID(), "-", "")[:16]
}
