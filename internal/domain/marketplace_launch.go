package domain

import "time"

type MarketplaceLaunchReview struct {
	ID                       string                  `json:"id"`
	ListingID                string                  `json:"listing_id"`
	ProviderID               string                  `json:"provider_id"`
	ProviderName             string                  `json:"provider_name,omitempty"`
	SupplierID               string                  `json:"supplier_id"`
	SupplierName             string                  `json:"supplier_name,omitempty"`
	Revision                 int                     `json:"revision"`
	PolicyVersion            string                  `json:"policy_version"`
	ListingFingerprintSHA256 string                  `json:"listing_fingerprint_sha256"`
	Status                   string                  `json:"status"`
	Reason                   string                  `json:"reason"`
	CreatedBy                string                  `json:"created_by"`
	ApprovedBy               *string                 `json:"approved_by,omitempty"`
	ApprovedAt               *time.Time              `json:"approved_at,omitempty"`
	RevokedBy                *string                 `json:"revoked_by,omitempty"`
	RevokedAt                *time.Time              `json:"revoked_at,omitempty"`
	CreatedAt                time.Time               `json:"created_at"`
	UpdatedAt                time.Time               `json:"updated_at"`
	Gates                    []MarketplaceLaunchGate `json:"gates"`
	PassedGateCount          int                     `json:"passed_gate_count"`
	GateCount                int                     `json:"gate_count"`
}

type MarketplaceLaunchGate struct {
	ID                string         `json:"id"`
	ReviewID          string         `json:"review_id"`
	GateCode          string         `json:"gate_code"`
	EvidenceSource    string         `json:"evidence_source"`
	Status            string         `json:"status"`
	EvidenceReference string         `json:"evidence_reference"`
	Evidence          map[string]any `json:"evidence"`
	EvaluatedBy       *string        `json:"evaluated_by,omitempty"`
	EvaluatedAt       *time.Time     `json:"evaluated_at,omitempty"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type SupplierPayoutReadinessReview struct {
	SupplierID                string     `json:"supplier_id"`
	SupplierName              string     `json:"supplier_name,omitempty"`
	ContractStatus            string     `json:"contract_status"`
	ContractEvidenceReference string     `json:"contract_evidence_reference"`
	TaxStatus                 string     `json:"tax_status"`
	TaxEvidenceReference      string     `json:"tax_evidence_reference"`
	PaymentStatus             string     `json:"payment_status"`
	PaymentEvidenceReference  string     `json:"payment_evidence_reference"`
	SecurityStatus            string     `json:"security_status"`
	SecurityEvidenceReference string     `json:"security_evidence_reference"`
	ProductionPayoutEnabled   bool       `json:"production_payout_enabled"`
	ReviewReason              string     `json:"review_reason"`
	ReviewedBy                *string    `json:"reviewed_by,omitempty"`
	ReviewedAt                *time.Time `json:"reviewed_at,omitempty"`
	Version                   int64      `json:"version"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

type MarketplaceProviderLifecycleEvent struct {
	ID                string    `json:"id"`
	ListingID         string    `json:"listing_id"`
	ProviderID        string    `json:"provider_id"`
	SupplierID        string    `json:"supplier_id"`
	Action            string    `json:"action"`
	FromListingStatus string    `json:"from_listing_status"`
	ToListingStatus   string    `json:"to_listing_status"`
	Reason            string    `json:"reason"`
	ActorID           string    `json:"actor_id"`
	CreatedAt         time.Time `json:"created_at"`
}
