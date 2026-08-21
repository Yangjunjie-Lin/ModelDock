package domain

import "time"

type SupplierSettlementPolicy struct {
	SupplierID              string     `json:"supplier_id"`
	Enabled                 bool       `json:"enabled"`
	SettlementCycle         string     `json:"settlement_cycle"`
	MinimumPayout           Decimal    `json:"minimum_payout"`
	CommissionBPS           int        `json:"commission_bps"`
	RiskReserveBPS          int        `json:"risk_reserve_bps"`
	ReserveHoldDays         int        `json:"reserve_hold_days"`
	PayoutAdapter           string     `json:"payout_adapter"`
	PayoutRegion            string     `json:"payout_region"`
	TaxVerificationRequired bool       `json:"tax_verification_required"`
	InvoiceRequired         bool       `json:"invoice_required"`
	NextSettlementAt        *time.Time `json:"next_settlement_at,omitempty"`
	LastPeriodEnd           *string    `json:"last_period_end,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type SupplierPayableAccrual struct {
	ID                   string    `json:"id"`
	SupplierID           string    `json:"supplier_id"`
	ProviderID           string    `json:"provider_id"`
	BillingUsageRecordID string    `json:"billing_usage_record_id"`
	UsagePriceSnapshotID string    `json:"usage_price_snapshot_id"`
	FundingOperationID   string    `json:"funding_operation_id"`
	RequestID            string    `json:"request_id"`
	GrossAmount          Decimal   `json:"gross_amount"`
	CommissionBPS        int       `json:"commission_bps"`
	CommissionAmount     Decimal   `json:"commission_amount"`
	ReserveBPS           int       `json:"reserve_bps"`
	ReserveAmount        Decimal   `json:"reserve_amount"`
	InitialPayableAmount Decimal   `json:"initial_payable_amount"`
	Currency             string    `json:"currency"`
	UsageSettledAt       time.Time `json:"usage_settled_at"`
	ReserveReleasableAt  time.Time `json:"reserve_releasable_at"`
	StatementMatched     bool      `json:"statement_matched"`
	OpenAppeal           bool      `json:"open_appeal"`
	CreatedAt            time.Time `json:"created_at"`
}

type SupplierPayableEntry struct {
	ID                string         `json:"id"`
	SupplierID        string         `json:"supplier_id"`
	ProviderID        string         `json:"provider_id"`
	AccrualID         *string        `json:"accrual_id,omitempty"`
	SettlementBatchID *string        `json:"settlement_batch_id,omitempty"`
	EntryType         string         `json:"entry_type"`
	EntrySide         string         `json:"entry_side"`
	Amount            Decimal        `json:"amount"`
	Currency          string         `json:"currency"`
	AvailableAt       time.Time      `json:"available_at"`
	Reference         string         `json:"reference"`
	Metadata          map[string]any `json:"metadata"`
	CreatedAt         time.Time      `json:"created_at"`
}

type SupplierSettlementBatch struct {
	ID                      string                   `json:"id"`
	BatchNumber             string                   `json:"batch_number"`
	SupplierID              string                   `json:"supplier_id"`
	SupplierName            string                   `json:"supplier_name,omitempty"`
	ProviderID              string                   `json:"provider_id"`
	ProviderName            string                   `json:"provider_name,omitempty"`
	PeriodStart             string                   `json:"period_start"`
	PeriodEnd               string                   `json:"period_end"`
	Currency                string                   `json:"currency"`
	GrossUsageAmount        Decimal                  `json:"gross_usage_amount"`
	CommissionAmount        Decimal                  `json:"commission_amount"`
	ReserveHeldAmount       Decimal                  `json:"reserve_held_amount"`
	AdjustmentAmount        Decimal                  `json:"adjustment_amount"`
	PayoutAmount            Decimal                  `json:"payout_amount"`
	Status                  string                   `json:"status"`
	TaxStatus               string                   `json:"tax_status"`
	InvoiceStatus           string                   `json:"invoice_status"`
	ProviderStatementID     *string                  `json:"provider_statement_id,omitempty"`
	PayoutAdapter           string                   `json:"payout_adapter"`
	PayoutRegion            string                   `json:"payout_region"`
	ProviderPayoutReference string                   `json:"provider_payout_reference,omitempty"`
	RetryCount              int                      `json:"retry_count"`
	MaxAttempts             int                      `json:"max_attempts"`
	NextRetryAt             *time.Time               `json:"next_retry_at,omitempty"`
	LastFailureCode         string                   `json:"last_failure_code,omitempty"`
	ApprovedBy              *string                  `json:"approved_by,omitempty"`
	ApprovalReason          string                   `json:"approval_reason,omitempty"`
	ApprovedAt              *time.Time               `json:"approved_at,omitempty"`
	PaidAt                  *time.Time               `json:"paid_at,omitempty"`
	CreatedAt               time.Time                `json:"created_at"`
	UpdatedAt               time.Time                `json:"updated_at"`
	Items                   []SupplierSettlementItem `json:"items,omitempty"`
}

type SupplierSettlementItem struct {
	ID             string    `json:"id"`
	PayableEntryID string    `json:"payable_entry_id"`
	AccrualID      *string   `json:"accrual_id,omitempty"`
	EntrySide      string    `json:"entry_side"`
	Amount         Decimal   `json:"amount"`
	RequestID      string    `json:"request_id,omitempty"`
	EntryType      string    `json:"entry_type,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type SupplierBill struct {
	ID            string             `json:"id"`
	SupplierID    string             `json:"supplier_id"`
	ProviderID    string             `json:"provider_id"`
	BillReference string             `json:"bill_reference"`
	PeriodStart   string             `json:"period_start"`
	PeriodEnd     string             `json:"period_end"`
	Currency      string             `json:"currency"`
	TotalAmount   Decimal            `json:"total_amount"`
	SourceSHA256  string             `json:"source_sha256"`
	Status        string             `json:"status"`
	DeclaredBy    string             `json:"declared_by"`
	DeclaredAt    time.Time          `json:"declared_at"`
	Lines         []SupplierBillLine `json:"lines,omitempty"`
}

type SupplierBillLine struct {
	ID                string         `json:"id"`
	ExternalLineID    string         `json:"external_line_id"`
	RequestID         string         `json:"request_id,omitempty"`
	UpstreamRequestID string         `json:"upstream_request_id,omitempty"`
	UsageDate         string         `json:"usage_date"`
	InputTokens       *int64         `json:"input_tokens,omitempty"`
	CachedInputTokens *int64         `json:"cached_input_tokens,omitempty"`
	OutputTokens      *int64         `json:"output_tokens,omitempty"`
	Amount            Decimal        `json:"amount"`
	Currency          string         `json:"currency"`
	Metadata          map[string]any `json:"metadata"`
}

type SupplierAppeal struct {
	ID                   string         `json:"id"`
	AppealNumber         string         `json:"appeal_number"`
	SupplierID           string         `json:"supplier_id"`
	AppealType           string         `json:"appeal_type"`
	AccrualID            *string        `json:"accrual_id,omitempty"`
	SettlementBatchID    *string        `json:"settlement_batch_id,omitempty"`
	SupplierBillID       *string        `json:"supplier_bill_id,omitempty"`
	ReconciliationCaseID *string        `json:"reconciliation_case_id,omitempty"`
	Status               string         `json:"status"`
	Reason               string         `json:"reason"`
	Evidence             map[string]any `json:"evidence"`
	ResolutionReason     string         `json:"resolution_reason,omitempty"`
	SubmittedBy          string         `json:"submitted_by"`
	ResolvedBy           *string        `json:"resolved_by,omitempty"`
	ResolvedAt           *time.Time     `json:"resolved_at,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type SupplierPayableSummary struct {
	SupplierID        string  `json:"supplier_id"`
	Currency          string  `json:"currency"`
	Accrued           Decimal `json:"accrued"`
	ReserveHeld       Decimal `json:"reserve_held"`
	RefundShare       Decimal `json:"refund_share"`
	Paid              Decimal `json:"paid"`
	Available         Decimal `json:"available"`
	OpenAppeals       int64   `json:"open_appeals"`
	UnmatchedAccruals int64   `json:"unmatched_accruals"`
}
