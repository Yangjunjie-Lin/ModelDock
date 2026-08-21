package domain

import "time"

type SupplierOrganization struct {
	ID                   string                          `json:"id"`
	OrganizationID       string                          `json:"organization_id"`
	OwnerUserID          string                          `json:"owner_user_id,omitempty"`
	LegalName            string                          `json:"legal_name"`
	DisplayName          string                          `json:"display_name"`
	RegistrationNumber   string                          `json:"registration_number"`
	IncorporationCountry string                          `json:"incorporation_country"`
	Website              string                          `json:"website"`
	KYBStatus            string                          `json:"kyb_status"`
	ContractStatus       string                          `json:"contract_status"`
	ContractVersion      string                          `json:"contract_version"`
	ContractStartAt      *time.Time                      `json:"contract_start_at,omitempty"`
	ContractEndAt        *time.Time                      `json:"contract_end_at,omitempty"`
	Status               string                          `json:"status"`
	PayoutAccountLast4   string                          `json:"payout_account_last4,omitempty"`
	PayoutCurrency       string                          `json:"payout_currency"`
	TaxID                string                          `json:"tax_id"`
	TaxCountry           string                          `json:"tax_country"`
	TaxResidency         string                          `json:"tax_residency"`
	TaxFormType          string                          `json:"tax_form_type"`
	Version              int64                           `json:"version"`
	CreatedAt            time.Time                       `json:"created_at"`
	UpdatedAt            time.Time                       `json:"updated_at"`
	Contacts             []SupplierContact               `json:"contacts,omitempty"`
	Endpoints            []SupplierEndpoint              `json:"endpoints,omitempty"`
	Credentials          []SupplierCredential            `json:"credentials,omitempty"`
	Models               []SupplierModelApplication      `json:"models,omitempty"`
	Prices               []SupplierPriceApplication      `json:"prices,omitempty"`
	Residency            []SupplierResidencyDeclaration  `json:"residency,omitempty"`
	Questionnaires       []SupplierSecurityQuestionnaire `json:"questionnaires,omitempty"`
}

type SupplierContact struct {
	ID          string    `json:"id"`
	SupplierID  string    `json:"supplier_id"`
	ContactType string    `json:"contact_type"`
	FullName    string    `json:"full_name"`
	Title       string    `json:"title"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SupplierEndpoint struct {
	ID                 string     `json:"id"`
	SupplierID         string     `json:"supplier_id"`
	EndpointURL        string     `json:"endpoint_url"`
	VerificationStatus string     `json:"verification_status"`
	IsolationStatus    string     `json:"isolation_status"`
	VerifiedAt         *time.Time `json:"verified_at,omitempty"`
	LastCheckedIP      string     `json:"last_checked_ip,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ChallengeToken     string     `json:"challenge_token,omitempty"`
}

type SupplierCredential struct {
	ID             string    `json:"id"`
	SupplierID     string    `json:"supplier_id"`
	ProviderID     *string   `json:"provider_id,omitempty"`
	Name           string    `json:"name"`
	CredentialType string    `json:"credential_type"`
	SecretLast4    string    `json:"secret_last4"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SupplierResidencyDeclaration struct {
	ID                  string    `json:"id"`
	SupplierID          string    `json:"supplier_id"`
	EndpointID          *string   `json:"endpoint_id,omitempty"`
	ProcessingRegions   []string  `json:"processing_regions"`
	StorageRegions      []string  `json:"storage_regions"`
	CrossBorderTransfer bool      `json:"cross_border_transfer"`
	RetentionDays       *int      `json:"retention_days,omitempty"`
	Subprocessors       []string  `json:"subprocessors"`
	Attestation         string    `json:"attestation"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SupplierSecurityQuestionnaire struct {
	ID          string         `json:"id"`
	SupplierID  string         `json:"supplier_id"`
	Version     string         `json:"version"`
	Answers     map[string]any `json:"answers"`
	Status      string         `json:"status"`
	SubmittedAt *time.Time     `json:"submitted_at,omitempty"`
	ReviewedAt  *time.Time     `json:"reviewed_at,omitempty"`
	ReviewedBy  *string        `json:"reviewed_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type SupplierModelApplication struct {
	ID                     string    `json:"id"`
	SupplierID             string    `json:"supplier_id"`
	EndpointID             string    `json:"endpoint_id"`
	ModelName              string    `json:"model_name"`
	ModelType              string    `json:"model_type"`
	Capabilities           []string  `json:"capabilities"`
	ResidencyDeclarationID *string   `json:"data_residency_declaration_id,omitempty"`
	Status                 string    `json:"status"`
	ReviewReason           string    `json:"review_reason,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type SupplierPriceApplication struct {
	ID                    string    `json:"id"`
	SupplierID            string    `json:"supplier_id"`
	ModelApplicationID    string    `json:"model_application_id"`
	InputTokenPrice       string    `json:"input_token_price"`
	CachedInputTokenPrice string    `json:"cached_input_token_price"`
	OutputTokenPrice      string    `json:"output_token_price"`
	RequestFixedPrice     string    `json:"request_fixed_price"`
	Currency              string    `json:"currency"`
	Unit                  int64     `json:"unit"`
	Status                string    `json:"status"`
	ReviewReason          string    `json:"review_reason,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}
