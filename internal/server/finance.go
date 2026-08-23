package server

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

const (
	financeMaximumQueryWindow = 10 * 365 * 24 * time.Hour
	financeMaximumCSVRows     = 100000
	financeReasonMaximum      = 1000
	financeReferenceMaximum   = 200
	financeStatementLineLimit = 100000
)

var (
	financeUUIDPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	financeSHA256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	financeCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	financeRegionPattern   = regexp.MustCompile(`^[A-Z]{2}$`)
	// PostgreSQL NUMERIC(30,12) has 30 total digits, of which 12 are fractional.
	financeDecimalPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,17})(?:\.[0-9]{1,12})?$`)
)

// registerConsoleFinanceRoutes exposes organization-scoped financial evidence.
// Cost and margin fields are deliberately removed from customer responses and
// exports because Provider commercial terms are administrator-only data.
func registerConsoleFinanceRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/organizations/:organizationID/finance/balance", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		value, err := d.Store.WalletBalanceComposition(c.Request.Context(), organization.ID)
		respond(c, value, err)
	})

	g.GET("/organizations/:organizationID/finance/usage", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		filter, ok := financeFilterFromQuery(c, organization.ID, 366*24*time.Hour)
		if !ok {
			return
		}
		rows, err := d.Store.ListFinanceUsage(c.Request.Context(), filter)
		if err != nil {
			respond(c, nil, err)
			return
		}
		respondList(c, sanitizeConsoleFinanceUsage(rows), nil)
	})

	g.GET("/organizations/:organizationID/finance/monthly-statements", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		month := strings.TrimSpace(c.Query("month"))
		if month != "" {
			if _, err := time.Parse("2006-01", month); err != nil {
				financeBadRequest(c, "month must use YYYY-MM.")
				return
			}
		}
		limit, _ := page(c)
		rows, err := d.Store.MonthlyStatements(c.Request.Context(), organization.ID, month, boundedFinanceListLimit(limit))
		respondList(c, sanitizeConsoleMonthlyStatements(rows), err)
	})

	g.GET("/organizations/:organizationID/refund-applications", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		filter := basicFinanceFilter(c, organization.ID)
		items, err := d.Store.ListRefundApplications(c.Request.Context(), filter)
		respondList(c, items, err)
	})
	g.POST("/organizations/:organizationID/refund-applications", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		var input struct {
			SourceType            string `json:"source_type"`
			RechargeOrderID       string `json:"recharge_order_id"`
			SubscriptionInvoiceID string `json:"subscription_invoice_id"`
			Amount                string `json:"amount"`
			Reason                string `json:"reason"`
			IdempotencyKey        string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil {
			financeBadRequest(c, "A valid refund application body is required.")
			return
		}
		input.SourceType = strings.ToUpper(strings.TrimSpace(input.SourceType))
		input.RechargeOrderID = strings.TrimSpace(input.RechargeOrderID)
		input.SubscriptionInvoiceID = strings.TrimSpace(input.SubscriptionInvoiceID)
		input.Amount = strings.TrimSpace(input.Amount)
		input.Reason = strings.TrimSpace(input.Reason)
		input.IdempotencyKey = financeIdempotencyKey(c, input.IdempotencyKey)
		validSource := input.SourceType == "RECHARGE" && validFinanceUUID(input.RechargeOrderID) && input.SubscriptionInvoiceID == "" ||
			input.SourceType == "SUBSCRIPTION" && validFinanceUUID(input.SubscriptionInvoiceID) && input.RechargeOrderID == ""
		if !validSource || !validPositiveFinanceDecimal(input.Amount) || !validFinanceReason(input.Reason) || !validFinanceIdempotency(input.IdempotencyKey) {
			financeBadRequest(c, "source_type, exactly one valid source ID, a positive exact amount, reason, and Idempotency-Key are required.")
			return
		}
		item, replayed, err := d.Store.CreateRefundApplication(c.Request.Context(), store.CreateRefundApplicationRequest{
			OrganizationID: organization.ID, SourceType: input.SourceType, RechargeOrderID: input.RechargeOrderID,
			SubscriptionInvoiceID: input.SubscriptionInvoiceID, Amount: input.Amount, Reason: input.Reason,
			IdempotencyKey: input.IdempotencyKey, RequestedBy: claimsFrom(c).Subject,
		})
		financeCreatedOrReplay(c, item, replayed, err)
	})

	g.GET("/organizations/:organizationID/invoice-applications", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
		if !ok {
			return
		}
		items, err := d.Store.ListInvoiceApplications(c.Request.Context(), basicFinanceFilter(c, organization.ID))
		respondList(c, items, err)
	})
	g.POST("/organizations/:organizationID/invoice-applications", func(c *gin.Context) {
		organization, ok := requireOrganizationAccess(c, d, false, "MEMBER")
		if !ok {
			return
		}
		var input struct {
			InvoiceTitle   string `json:"invoice_title"`
			TaxIdentifier  string `json:"tax_identifier"`
			Amount         string `json:"amount"`
			Currency       string `json:"currency"`
			PeriodStart    string `json:"period_start"`
			PeriodEnd      string `json:"period_end"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil {
			financeBadRequest(c, "A valid invoice application body is required.")
			return
		}
		input.InvoiceTitle = strings.TrimSpace(input.InvoiceTitle)
		input.TaxIdentifier = strings.TrimSpace(input.TaxIdentifier)
		input.Amount = strings.TrimSpace(input.Amount)
		input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
		input.IdempotencyKey = financeIdempotencyKey(c, input.IdempotencyKey)
		periodStart, startErr := parseFinanceDate(input.PeriodStart)
		periodEnd, endErr := parseFinanceDate(input.PeriodEnd)
		if len(input.InvoiceTitle) == 0 || len(input.InvoiceTitle) > 200 || len(input.TaxIdentifier) > 100 ||
			!validPositiveFinanceDecimal(input.Amount) || !financeCurrencyPattern.MatchString(input.Currency) ||
			startErr != nil || endErr != nil || periodEnd.Before(periodStart) || !validFinanceIdempotency(input.IdempotencyKey) {
			financeBadRequest(c, "title, positive exact amount, ISO currency, valid period, and Idempotency-Key are required.")
			return
		}
		item, replayed, err := d.Store.CreateInvoiceApplication(c.Request.Context(), store.CreateInvoiceApplicationRequest{
			OrganizationID: organization.ID, InvoiceTitle: input.InvoiceTitle, TaxIdentifier: input.TaxIdentifier,
			Amount: input.Amount, Currency: input.Currency, PeriodStart: periodStart, PeriodEnd: periodEnd,
			IdempotencyKey: input.IdempotencyKey, RequestedBy: claimsFrom(c).Subject,
		})
		financeCreatedOrReplay(c, item, replayed, err)
	})

	g.GET("/organizations/:organizationID/finance/export", func(c *gin.Context) {
		exportConsoleFinance(c, d)
	})
}

func registerAdminFinanceRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/finance/payment-orders", func(c *gin.Context) {
		items, err := d.Store.ListRechargeOrdersFinance(c.Request.Context(), basicFinanceFilter(c, strings.TrimSpace(c.Query("organization_id"))), false)
		respondList(c, items, err)
	})
	g.GET("/finance/anomalous-orders", func(c *gin.Context) {
		items, err := d.Store.ListRechargeOrdersFinance(c.Request.Context(), basicFinanceFilter(c, strings.TrimSpace(c.Query("organization_id"))), true)
		respondList(c, items, err)
	})
	g.GET("/finance/ledger-entries", func(c *gin.Context) {
		filter, ok := financeFilterFromQuery(c, strings.TrimSpace(c.Query("organization_id")), financeMaximumQueryWindow)
		if !ok {
			return
		}
		items, err := d.Store.ListAccountingRows(c.Request.Context(), filter)
		respondList(c, items, err)
	})
	g.GET("/finance/refund-applications", func(c *gin.Context) {
		items, err := d.Store.ListRefundApplications(c.Request.Context(), basicFinanceFilter(c, strings.TrimSpace(c.Query("organization_id"))))
		respondList(c, items, err)
	})
	g.POST("/finance/refund-applications/:applicationID/decision", func(c *gin.Context) {
		decideFinanceApplication(c, d, "refund")
	})
	g.POST("/finance/refund-applications/:applicationID/process", func(c *gin.Context) {
		processApprovedRefundApplication(c, d)
	})
	g.GET("/finance/invoice-applications", func(c *gin.Context) {
		items, err := d.Store.ListInvoiceApplications(c.Request.Context(), basicFinanceFilter(c, strings.TrimSpace(c.Query("organization_id"))))
		respondList(c, items, err)
	})
	g.POST("/finance/invoice-applications/:applicationID/decision", func(c *gin.Context) {
		decideFinanceApplication(c, d, "invoice")
	})
	g.GET("/finance/invoice-applications/export", func(c *gin.Context) {
		exportInvoiceApplications(c, d)
	})
	g.GET("/finance/reports", func(c *gin.Context) {
		report := strings.ToLower(strings.TrimSpace(c.Query("report")))
		if report != "provider_cost" && report != "user_revenue" && report != "gross_margin" {
			financeBadRequest(c, "report must be provider_cost, user_revenue, or gross_margin.")
			return
		}
		from, to, ok := financeRangeFromQuery(c, financeMaximumQueryWindow, false)
		if !ok {
			return
		}
		items, err := d.Store.FinanceReport(c.Request.Context(), report, from, to)
		respondList(c, items, err)
	})
	g.GET("/finance/accounting-export", func(c *gin.Context) {
		exportAccounting(c, d)
	})
	g.POST("/finance/provider-statements", func(c *gin.Context) {
		importProviderStatement(c, d)
	})
	listReconciliationRuns := func(c *gin.Context) {
		limit, offset := page(c)
		items, err := d.Store.ListReconciliationRuns(c.Request.Context(), boundedFinanceListLimit(limit), maxFinanceOffset(offset))
		respondList(c, items, err)
	}
	runReconciliation := func(c *gin.Context) {
		var input struct {
			BusinessDate string `json:"business_date"`
		}
		if c.ShouldBindJSON(&input) != nil {
			financeBadRequest(c, "business_date is required.")
			return
		}
		businessDate, err := parseFinanceDate(input.BusinessDate)
		if err != nil || businessDate.After(time.Now().UTC()) {
			financeBadRequest(c, "business_date must be a valid date that is not in the future.")
			return
		}
		actor := claimsFrom(c).Subject
		run, replayed, err := d.Store.RunFinancialReconciliation(c.Request.Context(), businessDate, "MANUAL", &actor)
		if err != nil {
			d.Logger.Error("financial_reconciliation_failed", "business_date", businessDate.Format("2006-01-02"), "error", err)
		}
		financeCreatedOrReplay(c, run, replayed, err)
	}
	listReconciliationCases := func(c *gin.Context) {
		limit, offset := page(c)
		items, err := d.Store.ListReconciliationCases(c.Request.Context(), strings.TrimSpace(c.Query("status")), strings.TrimSpace(c.Query("check_type")), boundedFinanceListLimit(limit), maxFinanceOffset(offset))
		if err == nil {
			items = filterReconciliationSeverity(items, strings.TrimSpace(c.Query("severity")))
		}
		respondList(c, items, err)
	}
	resolveReconciliationCase := func(c *gin.Context) {
		var input struct {
			Action          string `json:"action"`
			SourceJournalID string `json:"source_journal_id"`
			Reason          string `json:"reason"`
			IdempotencyKey  string `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil {
			financeBadRequest(c, "A valid case resolution body is required.")
			return
		}
		input.Action = strings.ToUpper(strings.TrimSpace(input.Action))
		input.SourceJournalID = strings.TrimSpace(input.SourceJournalID)
		input.Reason = strings.TrimSpace(input.Reason)
		input.IdempotencyKey = financeIdempotencyKey(c, input.IdempotencyKey)
		validAction := input.Action == "ACCEPT_EXCEPTION" && input.SourceJournalID == "" ||
			input.Action == "REVERSE_JOURNAL" && validFinanceUUID(input.SourceJournalID)
		if !validFinanceUUID(c.Param("caseID")) || !validAction || !validFinanceReason(input.Reason) || !validFinanceIdempotency(input.IdempotencyKey) {
			financeBadRequest(c, "action, reason, Idempotency-Key, and source_journal_id for reversals are required.")
			return
		}
		item, replayed, err := d.Store.ResolveReconciliationCase(c.Request.Context(), c.Param("caseID"), input.Action,
			input.SourceJournalID, input.Reason, input.IdempotencyKey, claimsFrom(c).Subject)
		financeReplayResponse(c, item, replayed, err)
	}
	// Keep the originally shipped flat paths and expose the documented nested
	// aliases. Both route shapes execute the same handlers and persistence.
	g.GET("/finance/reconciliation-runs", listReconciliationRuns)
	g.POST("/finance/reconciliation-runs", runReconciliation)
	g.GET("/finance/reconciliation-cases", listReconciliationCases)
	g.POST("/finance/reconciliation-cases/:caseID/resolve", resolveReconciliationCase)
	g.GET("/finance/reconciliation/runs", listReconciliationRuns)
	g.POST("/finance/reconciliation/runs", runReconciliation)
	g.GET("/finance/reconciliation/cases", listReconciliationCases)
	g.POST("/finance/reconciliation/cases/:caseID/resolve", resolveReconciliationCase)
}

func processApprovedRefundApplication(c *gin.Context, d Dependencies) {
	applicationID := strings.TrimSpace(c.Param("applicationID"))
	var input struct {
		EvidenceReference string `json:"evidence_reference"`
		IdempotencyKey    string `json:"idempotency_key"`
	}
	if !validFinanceUUID(applicationID) || c.ShouldBindJSON(&input) != nil {
		financeBadRequest(c, "A valid approved refund application and evidence body are required.")
		return
	}
	input.EvidenceReference = strings.TrimSpace(input.EvidenceReference)
	input.IdempotencyKey = financeIdempotencyKey(c, input.IdempotencyKey)
	if len(input.EvidenceReference) < 3 || len(input.EvidenceReference) > financeReferenceMaximum || !validFinanceIdempotency(input.IdempotencyKey) {
		financeBadRequest(c, "evidence_reference and Idempotency-Key are required.")
		return
	}
	application, err := d.Store.RefundApplicationByID(c.Request.Context(), applicationID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	if application.SourceType != "RECHARGE" || application.RechargeOrderID == nil {
		openAIError(c, http.StatusConflict, "finance_state_conflict", "Subscription refunds require a separately evidenced payment integration and cannot be marked complete here.")
		return
	}
	order, err := d.Store.RechargeOrderByID(c.Request.Context(), *application.RechargeOrderID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	if order.PaymentProvider != "manual_transfer" {
		openAIError(c, http.StatusConflict, "payment_provider_evidence_required", "This payment provider requires its own verified refund result before wallet completion.")
		return
	}
	actor := stringPtr(claimsFrom(c).Subject)
	refund, replayed, err := d.Store.CreateRefundOrder(c.Request.Context(), store.CreateRefundOrderRequest{
		PlatformRefundNo: newPaymentNumber("RF"), RechargeOrderID: order.ID, RefundApplicationID: application.ID,
		Amount: domain.Decimal(application.RequestedAmount), Reason: application.Reason, IdempotencyKey: input.IdempotencyKey, CreatedBy: actor,
	})
	if err != nil {
		respond(c, nil, err)
		return
	}
	if refund.Status != "SUCCEEDED" {
		refund, _, err = d.Store.CompleteRefund(c.Request.Context(), refund.ID, "manual:"+input.EvidenceReference)
	}
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": refund, "replayed": replayed})
}

func decideFinanceApplication(c *gin.Context, d Dependencies, kind string) {
	if !validFinanceUUID(c.Param("applicationID")) {
		financeBadRequest(c, "applicationID must be a valid UUID.")
		return
	}
	var input struct {
		Decision       string `json:"decision"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&input) != nil {
		financeBadRequest(c, "A valid decision body is required.")
		return
	}
	input.Decision = strings.ToUpper(strings.TrimSpace(input.Decision))
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = financeIdempotencyKey(c, input.IdempotencyKey)
	if input.Decision != "APPROVE" && input.Decision != "REJECT" || !validFinanceReason(input.Reason) || !validFinanceIdempotency(input.IdempotencyKey) {
		financeBadRequest(c, "decision, reason, and Idempotency-Key are required.")
		return
	}
	actor := claimsFrom(c).Subject
	if kind == "invoice" {
		item, replayed, err := d.Store.DecideInvoiceApplication(c.Request.Context(), c.Param("applicationID"), input.Decision, input.Reason, input.IdempotencyKey, actor)
		financeReplayResponse(c, item, replayed, err)
		return
	}
	item, replayed, err := d.Store.DecideRefundApplication(c.Request.Context(), c.Param("applicationID"), input.Decision, input.Reason, input.IdempotencyKey, actor)
	financeReplayResponse(c, item, replayed, err)
}

func importProviderStatement(c *gin.Context, d Dependencies) {
	var input struct {
		ProviderID         string                             `json:"provider_id"`
		StatementReference string                             `json:"statement_reference"`
		PeriodStart        string                             `json:"period_start"`
		PeriodEnd          string                             `json:"period_end"`
		Region             string                             `json:"region"`
		Currency           string                             `json:"currency"`
		TotalAmount        string                             `json:"total_amount"`
		SourceSHA256       string                             `json:"source_sha256"`
		Lines              []store.ProviderStatementLineInput `json:"lines"`
	}
	if c.ShouldBindJSON(&input) != nil {
		financeBadRequest(c, "A valid Provider statement is required.")
		return
	}
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.StatementReference = strings.TrimSpace(input.StatementReference)
	input.Region = strings.ToUpper(strings.TrimSpace(input.Region))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.TotalAmount = strings.TrimSpace(input.TotalAmount)
	input.SourceSHA256 = strings.ToLower(strings.TrimSpace(input.SourceSHA256))
	periodStart, startErr := parseFinanceDate(input.PeriodStart)
	periodEnd, endErr := parseFinanceDate(input.PeriodEnd)
	if !validFinanceUUID(input.ProviderID) || len(input.StatementReference) == 0 || len(input.StatementReference) > financeReferenceMaximum ||
		startErr != nil || endErr != nil || periodEnd.Before(periodStart) || !financeRegionPattern.MatchString(input.Region) ||
		!financeCurrencyPattern.MatchString(input.Currency) || !validNonNegativeFinanceDecimal(input.TotalAmount) ||
		!financeSHA256Pattern.MatchString(input.SourceSHA256) || len(input.Lines) == 0 || len(input.Lines) > financeStatementLineLimit {
		financeBadRequest(c, "Provider, statement reference, period, region, currency, exact total, SHA-256, and bounded lines are required.")
		return
	}
	seen := make(map[string]struct{}, len(input.Lines))
	for index := range input.Lines {
		line := &input.Lines[index]
		line.ExternalLineID = strings.TrimSpace(line.ExternalLineID)
		line.RequestID = strings.TrimSpace(line.RequestID)
		line.UpstreamRequestID = strings.TrimSpace(line.UpstreamRequestID)
		line.Currency = strings.ToUpper(strings.TrimSpace(line.Currency))
		line.Amount = strings.TrimSpace(line.Amount)
		_, duplicate := seen[line.ExternalLineID]
		seen[line.ExternalLineID] = struct{}{}
		if duplicate || len(line.ExternalLineID) == 0 || len(line.ExternalLineID) > financeReferenceMaximum ||
			line.RequestID == "" && line.UpstreamRequestID == "" || len(line.RequestID) > 255 || len(line.UpstreamRequestID) > 255 ||
			line.UsageDate.IsZero() || line.UsageDate.Before(periodStart) || line.UsageDate.After(periodEnd.Add(24*time.Hour-time.Nanosecond)) ||
			line.Currency != input.Currency || !validNonNegativeFinanceDecimal(line.Amount) || negativeOptionalInt64(line.InputTokens) ||
			negativeOptionalInt64(line.CachedInputTokens) || negativeOptionalInt64(line.OutputTokens) || line.Metadata == nil {
			financeBadRequest(c, fmt.Sprintf("Provider statement line %d is invalid.", index+1))
			return
		}
	}
	statementID, replayed, err := d.Store.ImportProviderStatement(c.Request.Context(), store.ProviderStatementInput{
		ProviderID: input.ProviderID, StatementReference: input.StatementReference, PeriodStart: periodStart, PeriodEnd: periodEnd,
		Region: input.Region, Currency: input.Currency, TotalAmount: input.TotalAmount, SourceSHA256: input.SourceSHA256,
		ImportedBy: claimsFrom(c).Subject, Lines: input.Lines,
	})
	financeCreatedOrReplay(c, gin.H{"id": statementID, "replayed": replayed}, replayed, err)
}

func exportConsoleFinance(c *gin.Context, d Dependencies) {
	organization, ok := requireOrganizationAccess(c, d, false, "VIEWER")
	if !ok {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	month := strings.TrimSpace(c.Query("month"))
	if _, err := time.Parse("2006-01", month); err != nil {
		financeBadRequest(c, "month must use YYYY-MM.")
		return
	}
	from, err := time.Parse("2006-01", month)
	if err != nil {
		financeBadRequest(c, "month must use YYYY-MM.")
		return
	}
	to := from.AddDate(0, 1, 0)
	filename := fmt.Sprintf("modeldock-%s-%s.csv", safeFinanceFilename(kind), month)
	switch kind {
	case "recharges":
		rows, listErr := d.Store.ListRechargeOrdersExport(c.Request.Context(), organization.ID, from, to, financeMaximumCSVRows)
		if listErr != nil {
			respond(c, nil, listErr)
			return
		}
		financeCSV(c, filename, []string{"platform_order_no", "payment_provider", "provider_order_no", "status", "amount", "currency", "wallet_transaction_id", "ledger_journal_id", "paid_at", "credited_at", "created_at"}, func(write func([]string) error) error {
			for _, row := range rows {
				if err = write([]string{row.PlatformOrderNo, row.PaymentProvider, row.ProviderOrderNo, row.Status, row.Amount.String(), row.Currency, deref(row.WalletTransactionID), deref(row.LedgerJournalID), financeTime(row.PaidAt), financeTime(row.CreditedAt), row.CreatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
					return err
				}
			}
			return nil
		})
	case "usage":
		rows, listErr := d.Store.ListFinanceUsageExport(c.Request.Context(), store.FinanceFilter{OrganizationID: organization.ID, From: from, To: to, Limit: financeMaximumCSVRows})
		if listErr != nil {
			respond(c, nil, listErr)
			return
		}
		rows = sanitizeConsoleFinanceUsage(rows)
		financeCSV(c, filename, []string{"request_id", "project_id", "provider_id", "provider_name", "model", "input_tokens", "cached_input_tokens", "output_tokens", "customer_charge", "promotion_amount", "cash_charge", "currency", "funding_operation_id", "wallet_transaction_id", "ledger_journal_id", "upstream_request_id", "provider_attempt_status", "created_at"}, func(write func([]string) error) error {
			for _, row := range rows {
				if err = write([]string{row.RequestID, row.ProjectID, row.ProviderID, row.ProviderName, row.Model, strconv.FormatInt(row.InputTokens, 10), strconv.FormatInt(row.CachedInputTokens, 10), strconv.FormatInt(row.OutputTokens, 10), row.CustomerCharge, row.PromotionAmount, row.CashCharge, row.Currency, deref(row.FundingOperationID), deref(row.WalletTransactionID), deref(row.LedgerJournalID), row.UpstreamRequestID, row.ProviderAttemptStatus, row.CreatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
					return err
				}
			}
			return nil
		})
	case "subscriptions":
		rows, listErr := d.Store.ListSubscriptionInvoicesExport(c.Request.Context(), organization.ID, from, to, financeMaximumCSVRows)
		if listErr != nil {
			respond(c, nil, listErr)
			return
		}
		financeCSV(c, filename, []string{"invoice_number", "invoice_type", "status", "subtotal", "discount_amount", "tax_amount", "total_amount", "currency", "period_start", "period_end", "payment_provider", "provider_payment_reference", "ledger_journal_id", "paid_at", "created_at"}, func(write func([]string) error) error {
			for _, row := range rows {
				if err = write([]string{row.InvoiceNumber, row.InvoiceType, row.Status, row.Subtotal.String(), row.DiscountAmount.String(), row.TaxAmount.String(), row.TotalAmount.String(), row.Currency, row.PeriodStart.UTC().Format(time.RFC3339), row.PeriodEnd.UTC().Format(time.RFC3339), row.PaymentProvider, row.ProviderPaymentReference, deref(row.LedgerJournalID), financeTime(row.PaidAt), row.CreatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
					return err
				}
			}
			return nil
		})
	case "monthly_statement":
		rows, listErr := d.Store.MonthlyStatements(c.Request.Context(), organization.ID, month, 24)
		if listErr != nil {
			respond(c, nil, listErr)
			return
		}
		rows = sanitizeConsoleMonthlyStatements(rows)
		financeCSV(c, filename, []string{"month", "currency", "opening_balance", "recharge_amount", "usage_charge", "promotion_amount", "subscription_amount", "refund_amount", "closing_balance", "request_count"}, func(write func([]string) error) error {
			for _, row := range rows {
				if err = write([]string{row.Month, row.Currency, row.OpeningBalance, row.RechargeAmount, row.UsageCharge, row.PromotionAmount, row.SubscriptionAmount, row.RefundAmount, row.ClosingBalance, strconv.FormatInt(row.RequestCount, 10)}); err != nil {
					return err
				}
			}
			return nil
		})
	default:
		financeBadRequest(c, "kind must be recharges, usage, subscriptions, or monthly_statement.")
	}
}

func exportAccounting(c *gin.Context, d Dependencies) {
	filter, ok := financeFilterFromQuery(c, strings.TrimSpace(c.Query("organization_id")), financeMaximumQueryWindow)
	if !ok {
		return
	}
	if strings.TrimSpace(c.Query("from")) == "" || strings.TrimSpace(c.Query("to")) == "" {
		financeBadRequest(c, "from and to are required for an accounting export.")
		return
	}
	filter.Limit = boundedFinanceCSVLimit(filter.Limit)
	filter.RejectTruncated = true
	rows, err := d.Store.ListAccountingRows(c.Request.Context(), filter)
	if err != nil {
		if errors.Is(err, store.ErrFinanceExportLimit) {
			openAIError(c, http.StatusUnprocessableEntity, "finance_export_too_large", "Accounting export exceeds 100000 rows; use a narrower half-open UTC date interval.")
			return
		}
		respond(c, nil, err)
		return
	}
	financeCSV(c, "modeldock-accounting.csv", []string{"posted_at", "journal_id", "external_key", "journal_type", "account_key", "account_name", "debit", "credit", "currency", "reference", "reconciliation_case_id"}, func(write func([]string) error) error {
		for _, row := range rows {
			if err = write([]string{row.PostedAt.UTC().Format(time.RFC3339Nano), row.JournalID, row.ExternalKey, row.JournalType, row.AccountKey, row.AccountName, row.Debit, row.Credit, row.Currency, row.Reference, row.CaseID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// exportInvoiceApplications exports only approved applications and marks the
// exact exported set in the same audited database transaction before any CSV
// bytes are sent. A failed state transition therefore cannot yield an
// untracked finance export.
func exportInvoiceApplications(c *gin.Context, d Dependencies) {
	filter := basicFinanceFilter(c, strings.TrimSpace(c.Query("organization_id")))
	filter.Limit = boundedFinanceCSVLimit(filter.Limit)
	batchKey := financeIdempotencyKey(c, strings.TrimSpace(c.Query("batch_key")))
	if !validFinanceIdempotency(batchKey) {
		financeBadRequest(c, "Idempotency-Key or batch_key is required for an invoice export.")
		return
	}
	batch, replayed, err := d.Store.ExportInvoiceApplicationBatch(c.Request.Context(), filter, batchKey, claimsFrom(c).Subject)
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="modeldock-invoice-applications.csv"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-RelayDock-Export-Batch-ID", batch.ID)
	c.Header("X-RelayDock-Artifact-SHA256", batch.ArtifactSHA256)
	c.Header("X-Idempotent-Replay", strconv.FormatBool(replayed))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", batch.Artifact)
}

func financeCSV(c *gin.Context, filename string, header []string, rows func(func([]string) error) error) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+safeFinanceFilename(filename)+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)
	writer := csv.NewWriter(c.Writer)
	write := func(record []string) error {
		for index := range record {
			record[index] = store.CSVSafeCell(record[index])
		}
		return writer.Write(record)
	}
	if err := write(header); err != nil {
		_ = c.Error(err)
		return
	}
	if err := rows(write); err != nil {
		_ = c.Error(err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		c.Error(err)
	}
}

func sanitizeConsoleFinanceUsage(rows []domain.FinanceUsageDetail) []domain.FinanceUsageDetail {
	for index := range rows {
		rows[index].ProviderCost = ""
		rows[index].GrossMargin = ""
		rows[index].ProviderCurrency = ""
	}
	return rows
}

func sanitizeConsoleMonthlyStatements(rows []domain.MonthlyStatement) []domain.MonthlyStatement {
	for index := range rows {
		rows[index].ProviderCost = ""
		rows[index].GrossMargin = ""
	}
	return rows
}

func basicFinanceFilter(c *gin.Context, organizationID string) store.FinanceFilter {
	limit, offset := page(c)
	return store.FinanceFilter{OrganizationID: organizationID, Status: strings.TrimSpace(c.Query("status")),
		Query: strings.TrimSpace(c.Query("query")), Month: strings.TrimSpace(c.Query("month")),
		Limit: boundedFinanceListLimit(limit), Offset: maxFinanceOffset(offset)}
}

func financeFilterFromQuery(c *gin.Context, organizationID string, maximumWindow time.Duration) (store.FinanceFilter, bool) {
	filter := basicFinanceFilter(c, organizationID)
	if strings.TrimSpace(c.Query("from")) == "" && strings.TrimSpace(c.Query("to")) == "" {
		return filter, true
	}
	from, to, ok := financeRangeFromQuery(c, maximumWindow, false)
	if !ok {
		return store.FinanceFilter{}, false
	}
	filter.From, filter.To = from, to
	return filter, true
}

func financeRangeFromQuery(c *gin.Context, maximumWindow time.Duration, requireBoth bool) (time.Time, time.Time, bool) {
	fromRaw, toRaw := strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to"))
	if requireBoth && (fromRaw == "" || toRaw == "") {
		financeBadRequest(c, "from and to are required.")
		return time.Time{}, time.Time{}, false
	}
	var from, to time.Time
	var err error
	if fromRaw != "" {
		from, _, err = parseQueryTime(fromRaw)
		if err != nil {
			financeBadRequest(c, "from must be RFC3339 or YYYY-MM-DD.")
			return time.Time{}, time.Time{}, false
		}
	}
	if toRaw != "" {
		var dateOnly bool
		to, dateOnly, err = parseQueryTime(toRaw)
		if err != nil {
			financeBadRequest(c, "to must be RFC3339 or YYYY-MM-DD.")
			return time.Time{}, time.Time{}, false
		}
		if dateOnly {
			to = to.AddDate(0, 0, 1)
		}
	}
	if !from.IsZero() && !to.IsZero() {
		if !to.After(from) {
			financeBadRequest(c, "to must be later than from.")
			return time.Time{}, time.Time{}, false
		}
		if maximumWindow > 0 && to.Sub(from) > maximumWindow {
			financeBadRequest(c, "The requested financial range is too large.")
			return time.Time{}, time.Time{}, false
		}
	}
	return from, to, true
}

func filterReconciliationSeverity(items []domain.ReconciliationCase, severity string) []domain.ReconciliationCase {
	severity = strings.ToUpper(strings.TrimSpace(severity))
	if severity == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item.Severity == severity {
			out = append(out, item)
		}
	}
	return out
}

func financeCreatedOrReplay(c *gin.Context, value any, replayed bool, err error) {
	if err != nil {
		respond(c, nil, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	c.JSON(status, value)
}

func financeReplayResponse(c *gin.Context, value any, replayed bool, err error) {
	if err != nil {
		respond(c, nil, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": value, "replayed": replayed})
}

func financeIdempotencyKey(c *gin.Context, bodyValue string) string {
	value := strings.TrimSpace(bodyValue)
	if value == "" {
		value = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	return value
}

func financeBadRequest(c *gin.Context, message string) {
	openAIError(c, http.StatusBadRequest, "invalid_request", message)
}

func parseFinanceDate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(value))
}

func validFinanceUUID(value string) bool {
	return financeUUIDPattern.MatchString(strings.TrimSpace(value))
}

func validFinanceReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= financeReasonMaximum
}

func validFinanceIdempotency(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= financeReferenceMaximum
}

func validPositiveFinanceDecimal(value string) bool {
	value = strings.TrimSpace(value)
	decimal := domain.Decimal(value)
	return financeDecimalPattern.MatchString(value) && validPositiveDecimal(decimal)
}

func validNonNegativeFinanceDecimal(value string) bool {
	value = strings.TrimSpace(value)
	decimal := domain.Decimal(value)
	if !financeDecimalPattern.MatchString(value) || invalidOrNegativeDecimal(decimal) {
		return false
	}
	zero, err := decimal.IsZero()
	return err == nil && (zero || validPositiveDecimal(decimal))
}

func negativeOptionalInt64(value *int64) bool {
	return value != nil && *value < 0
}

func boundedFinanceListLimit(value int) int {
	if value <= 0 {
		return 50
	}
	if value > 200 {
		return 200
	}
	return value
}

func boundedFinanceCSVLimit(value int) int {
	if value <= 0 || value > financeMaximumCSVRows {
		return financeMaximumCSVRows
	}
	return value
}

func maxFinanceOffset(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func safeFinanceFilename(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		}
	}
	clean := strings.TrimLeft(builder.String(), ".")
	if clean == "" {
		return "finance.csv"
	}
	return clean
}

func financeTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
