package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/store"
	suppliersecurity "github.com/relayedock/relayedock/internal/supplier"
)

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)
var supplierCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func registerSupplierConsoleRoutes(g *gin.RouterGroup, d Dependencies) {
	registerSupplierSettlementConsoleRoutes(g, d)
	g.GET("/suppliers", func(c *gin.Context) {
		out, err := d.Store.SupplierByOwner(c.Request.Context(), claimsFrom(c).Subject)
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"data": nil})
			return
		}
		if err == nil {
			out, err = d.Store.SupplierByID(c.Request.Context(), out.ID, true)
		}
		respond(c, out, err)
	})
	g.POST("/suppliers", func(c *gin.Context) { createSupplierHandler(c, d) })
	g.GET("/suppliers/:id", func(c *gin.Context) {
		out, err := supplierOwnedBy(c, d, c.Param("id"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		_ = out
		value, err := d.Store.SupplierByID(c.Request.Context(), c.Param("id"), true)
		respond(c, value, err)
	})
	g.PATCH("/suppliers/:id", func(c *gin.Context) { updateSupplierHandler(c, d) })
	g.POST("/suppliers/:id/submit", func(c *gin.Context) {
		actor := claimsFrom(c).Subject
		out, err := d.Store.SubmitSupplier(c.Request.Context(), c.Param("id"), actor)
		respond(c, out, err)
	})
	g.POST("/suppliers/:id/exit", func(c *gin.Context) {
		respondNoContent(c, d.Store.RequestSupplierExit(c.Request.Context(), c.Param("id"), claimsFrom(c).Subject))
	})
	g.POST("/suppliers/:id/endpoints", func(c *gin.Context) { createSupplierEndpointHandler(c, d) })
	g.POST("/suppliers/:id/endpoints/:endpointID/verify", func(c *gin.Context) { verifySupplierEndpointHandler(c, d) })
	g.POST("/suppliers/:id/credentials", func(c *gin.Context) { createSupplierCredentialHandler(c, d) })
	g.POST("/suppliers/:id/residency", func(c *gin.Context) { createSupplierResidencyHandler(c, d) })
	g.POST("/suppliers/:id/questionnaires", func(c *gin.Context) { createSupplierQuestionnaireHandler(c, d) })
	g.POST("/suppliers/:id/models", func(c *gin.Context) { createSupplierModelHandler(c, d) })
	g.POST("/suppliers/:id/prices", func(c *gin.Context) { createSupplierPriceHandler(c, d) })
}

func registerSupplierAdminRoutes(g *gin.RouterGroup, d Dependencies) {
	registerSupplierSettlementAdminRoutes(g, d)
	g.GET("/suppliers", func(c *gin.Context) {
		limit, offset := page(c)
		rows, err := d.Store.ListSuppliers(c.Request.Context(), c.Query("status"), limit, offset)
		respondList(c, rows, err)
	})
	g.GET("/suppliers/:id", func(c *gin.Context) {
		out, err := d.Store.SupplierByID(c.Request.Context(), c.Param("id"), true)
		respond(c, out, err)
	})
	g.PATCH("/suppliers/:id/review", func(c *gin.Context) {
		var in struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil || len(in.Reason) > 10000 {
			openAIError(c, 400, "invalid_request", "decision and a bounded reason are required.")
			return
		}
		out, err := d.Store.ReviewSupplier(c.Request.Context(), c.Param("id"), in.Decision, in.Reason, claimsFrom(c).Subject)
		if err == nil {
			audit(c, d, "supplier.reviewed", "supplier_organization", out.ID, gin.H{"decision": strings.ToUpper(in.Decision)})
		}
		respond(c, out, err)
	})
	g.PATCH("/suppliers/:id/compliance", func(c *gin.Context) {
		var in struct {
			KYBStatus       string     `json:"kyb_status"`
			ContractStatus  string     `json:"contract_status"`
			ContractVersion string     `json:"contract_version"`
			ContractStartAt *time.Time `json:"contract_start_at"`
			ContractEndAt   *time.Time `json:"contract_end_at"`
		}
		if c.ShouldBindJSON(&in) != nil {
			openAIError(c, 400, "invalid_request", "A valid compliance body is required.")
			return
		}
		out, err := d.Store.UpdateSupplierCompliance(c.Request.Context(), c.Param("id"), in.KYBStatus, in.ContractStatus, in.ContractVersion, in.ContractStartAt, in.ContractEndAt, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.PATCH("/suppliers/:id/status", func(c *gin.Context) {
		var in struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil || strings.EqualFold(in.Status, "APPROVED") {
			openAIError(c, 400, "invalid_request", "APPROVED must use the review endpoint.")
			return
		}
		decision := strings.ToUpper(strings.TrimSpace(in.Status))
		if decision == "EXIT_REQUESTED" {
			decision = "EXITED"
		}
		out, err := d.Store.ReviewSupplier(c.Request.Context(), c.Param("id"), decision, in.Reason, claimsFrom(c).Subject)
		respond(c, out, err)
	})
	g.PATCH("/supplier-evidence/:type/:id", func(c *gin.Context) {
		var in struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if c.ShouldBindJSON(&in) != nil || len(in.Reason) > 10000 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid evidence status and bounded reason are required.")
			return
		}
		err := d.Store.ReviewSupplierEvidence(c.Request.Context(), c.Param("type"), c.Param("id"), in.Status, in.Reason, claimsFrom(c).Subject)
		respondNoContent(c, err)
	})
}

func supplierOwnedBy(c *gin.Context, d Dependencies, supplierID string) (domain.SupplierOrganization, error) {
	out, err := d.Store.SupplierByID(c.Request.Context(), supplierID, false)
	if err != nil {
		return out, err
	}
	if out.OwnerUserID != claimsFrom(c).Subject {
		return out, store.ErrNotFound
	}
	return out, nil
}

func createSupplierHandler(c *gin.Context, d Dependencies) {
	var in struct {
		domain.SupplierOrganization
		PayoutAccount string                 `json:"payout_account"`
		Contact       domain.SupplierContact `json:"contact"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.LegalName) == "" || strings.TrimSpace(in.DisplayName) == "" || strings.TrimSpace(in.RegistrationNumber) == "" || len(in.LegalName) > 300 || len(in.DisplayName) > 200 || len(in.RegistrationNumber) > 120 || len(in.PayoutAccount) > 1024 || !countryCodePattern.MatchString(strings.ToUpper(strings.TrimSpace(in.IncorporationCountry))) || strings.TrimSpace(in.Contact.FullName) == "" || strings.TrimSpace(in.Contact.Email) == "" {
		openAIError(c, 400, "invalid_request", "legal, registration, country, display name, and primary contact are required.")
		return
	}
	var encrypted []byte
	var err error
	if in.PayoutAccount != "" {
		if d.Vault == nil {
			respond(c, nil, errors.New("credential encryption is unavailable"))
			return
		}
		encrypted, err = d.Vault.Encrypt(in.PayoutAccount, "supplier-payout:"+claimsFrom(c).Subject)
		if err != nil {
			respond(c, nil, err)
			return
		}
		in.PayoutAccount = ""
	}
	in.IncorporationCountry = strings.ToUpper(strings.TrimSpace(in.IncorporationCountry))
	in.PayoutCurrency = strings.ToUpper(strings.TrimSpace(in.PayoutCurrency))
	if in.PayoutCurrency == "" {
		in.PayoutCurrency = "USD"
	}
	if !supplierCurrencyPattern.MatchString(in.PayoutCurrency) {
		openAIError(c, 400, "invalid_request", "payout_currency must be a three-letter currency.")
		return
	}
	in.Contact.ContactType = "PRIMARY"
	out, err := d.Store.CreateSupplier(c.Request.Context(), in.SupplierOrganization, claimsFrom(c).Subject, encrypted, secretcrypto.Last4(in.PayoutAccount), in.Contact)
	if err == nil {
		audit(c, d, "supplier.created", "supplier_organization", out.ID, gin.H{"organization_id": out.OrganizationID, "status": out.Status, "kyb_status": out.KYBStatus, "contract_status": out.ContractStatus})
	}
	respondCreated(c, out, err)
}

func updateSupplierHandler(c *gin.Context, d Dependencies) {
	var in struct {
		domain.SupplierOrganization
		PayoutAccount string `json:"payout_account"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, 400, "invalid_request", "A valid supplier profile is required.")
		return
	}
	current, err := supplierOwnedBy(c, d, c.Param("id"))
	if err != nil {
		respond(c, nil, err)
		return
	}
	in.ID = current.ID
	in.PayoutCurrency = strings.ToUpper(strings.TrimSpace(in.PayoutCurrency))
	if in.PayoutCurrency == "" {
		in.PayoutCurrency = current.PayoutCurrency
	}
	if !supplierCurrencyPattern.MatchString(in.PayoutCurrency) {
		openAIError(c, 400, "invalid_request", "payout_currency must be a three-letter currency.")
		return
	}
	var encrypted []byte
	if in.PayoutAccount != "" {
		if d.Vault == nil {
			respond(c, nil, errors.New("credential encryption is unavailable"))
			return
		}
		encrypted, err = d.Vault.Encrypt(in.PayoutAccount, "supplier-payout:"+claimsFrom(c).Subject)
		if err != nil {
			respond(c, nil, err)
			return
		}
	}
	out, err := d.Store.UpdateSupplierProfile(c.Request.Context(), in.SupplierOrganization, claimsFrom(c).Subject, encrypted, secretcrypto.Last4(in.PayoutAccount))
	if err == nil {
		audit(c, d, "supplier.profile_updated", "supplier_organization", out.ID, gin.H{"version": out.Version, "payout_account_updated": len(encrypted) > 0, "tax_profile_updated": in.TaxID != "" || in.TaxCountry != "" || in.TaxResidency != "" || in.TaxFormType != ""})
	}
	respond(c, out, err)
}

func createSupplierEndpointHandler(c *gin.Context, d Dependencies) {
	var in struct {
		EndpointURL string `json:"endpoint_url"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, 400, "invalid_request", "endpoint_url is required.")
		return
	}
	if _, _, err := suppliersecurity.ValidateEndpointURL(c.Request.Context(), in.EndpointURL, nil); err != nil {
		openAIError(c, 400, "unsafe_endpoint", "Endpoint must be an HTTPS public network address.")
		return
	}
	token, err := secretcryptoToken()
	if err != nil {
		respond(c, nil, err)
		return
	}
	out, err := d.Store.CreateSupplierEndpoint(c.Request.Context(), domain.SupplierEndpoint{SupplierID: c.Param("id"), EndpointURL: strings.TrimRight(strings.TrimSpace(in.EndpointURL), "/")}, claimsFrom(c).Subject, suppliersecurity.ChallengeHash(token))
	if err == nil {
		out.ChallengeToken = token
		audit(c, d, "supplier.endpoint_created", "supplier_endpoint", out.ID, gin.H{"supplier_id": out.SupplierID, "endpoint_url": out.EndpointURL})
	}
	respondCreated(c, out, err)
}
func verifySupplierEndpointHandler(c *gin.Context, d Dependencies) {
	if _, err := supplierOwnedBy(c, d, c.Param("id")); err != nil {
		respond(c, nil, err)
		return
	}
	endpoint, err := d.Store.SupplierEndpointByID(c.Request.Context(), c.Param("endpointID"))
	if err != nil || endpoint.SupplierID != c.Param("id") {
		respond(c, nil, store.ErrNotFound)
		return
	}
	var in struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.ChallengeToken) == "" {
		openAIError(c, 400, "invalid_request", "challenge_token is required.")
		return
	}
	if !d.Store.SupplierEndpointChallengeValid(c.Request.Context(), endpoint.ID, in.ChallengeToken) {
		_ = d.Store.MarkSupplierEndpointVerification(c.Request.Context(), endpoint.ID, claimsFrom(c).Subject, "", false, "challenge token mismatch")
		openAIError(c, http.StatusUnprocessableEntity, "endpoint_verification_failed", "Endpoint ownership or network isolation verification failed.")
		return
	}
	ip, verifyErr := suppliersecurity.VerifyEndpoint(c.Request.Context(), endpoint.EndpointURL, in.ChallengeToken)
	if verifyErr != nil {
		_ = d.Store.MarkSupplierEndpointVerification(c.Request.Context(), endpoint.ID, claimsFrom(c).Subject, "", false, verifyErr.Error())
		openAIError(c, http.StatusUnprocessableEntity, "endpoint_verification_failed", "Endpoint ownership or network isolation verification failed.")
		return
	}
	if err = d.Store.MarkSupplierEndpointVerification(c.Request.Context(), endpoint.ID, claimsFrom(c).Subject, ip, true, ""); err != nil {
		respond(c, nil, err)
		return
	}
	out, err := d.Store.SupplierEndpointByID(c.Request.Context(), endpoint.ID)
	respond(c, out, err)
}
func createSupplierCredentialHandler(c *gin.Context, d Dependencies) {
	var in struct {
		ProviderID     *string `json:"provider_id"`
		Name           string  `json:"name"`
		CredentialType string  `json:"credential_type"`
		Secret         string  `json:"secret"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Secret) == "" || len(in.Secret) > 4096 || len(in.Name) > 200 {
		openAIError(c, 400, "invalid_request", "name and secret are required.")
		return
	}
	if in.CredentialType == "" {
		in.CredentialType = "api_key"
	}
	if in.CredentialType != "api_key" && in.CredentialType != "service_account" && in.CredentialType != "workload_identity" {
		openAIError(c, http.StatusBadRequest, "invalid_request", "credential_type is invalid.")
		return
	}
	if d.Vault == nil {
		respond(c, nil, errors.New("credential encryption is unavailable"))
		return
	}
	credentialID := id.UUID()
	encrypted, err := d.Vault.Encrypt(in.Secret, "supplier-credential:"+credentialID)
	if err != nil {
		respond(c, nil, err)
		return
	}
	out, err := d.Store.CreateSupplierCredential(c.Request.Context(), domain.SupplierCredential{ID: credentialID, SupplierID: c.Param("id"), ProviderID: in.ProviderID, Name: in.Name, CredentialType: in.CredentialType, SecretLast4: secretcrypto.Last4(in.Secret)}, claimsFrom(c).Subject, encrypted)
	if err == nil {
		audit(c, d, "supplier.credential_created", "supplier_credential", out.ID, gin.H{"supplier_id": out.SupplierID})
	}
	respondCreated(c, out, err)
}
func createSupplierResidencyHandler(c *gin.Context, d Dependencies) {
	var in domain.SupplierResidencyDeclaration
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Attestation) == "" {
		openAIError(c, 400, "invalid_request", "attestation is required.")
		return
	}
	in.SupplierID = c.Param("id")
	out, err := d.Store.CreateSupplierResidency(c.Request.Context(), in, claimsFrom(c).Subject)
	respondCreated(c, out, err)
}
func createSupplierQuestionnaireHandler(c *gin.Context, d Dependencies) {
	var in domain.SupplierSecurityQuestionnaire
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Version) == "" || in.Answers == nil {
		openAIError(c, 400, "invalid_request", "version and answers are required.")
		return
	}
	in.SupplierID = c.Param("id")
	out, err := d.Store.CreateSupplierQuestionnaire(c.Request.Context(), in, claimsFrom(c).Subject)
	respondCreated(c, out, err)
}
func createSupplierModelHandler(c *gin.Context, d Dependencies) {
	var in domain.SupplierModelApplication
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.ModelName) == "" || strings.TrimSpace(in.EndpointID) == "" {
		openAIError(c, 400, "invalid_request", "model_name and endpoint_id are required.")
		return
	}
	in.SupplierID = c.Param("id")
	if in.ModelType == "" {
		in.ModelType = "text"
	}
	out, err := d.Store.CreateSupplierModel(c.Request.Context(), in, claimsFrom(c).Subject)
	respondCreated(c, out, err)
}
func createSupplierPriceHandler(c *gin.Context, d Dependencies) {
	var in domain.SupplierPriceApplication
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.ModelApplicationID) == "" || !supplierCurrencyPattern.MatchString(strings.ToUpper(in.Currency)) || in.Unit <= 0 {
		openAIError(c, 400, "invalid_request", "model_application_id, currency, and positive unit are required.")
		return
	}
	if in.CachedInputTokenPrice == "" {
		in.CachedInputTokenPrice = "0"
	}
	if in.RequestFixedPrice == "" {
		in.RequestFixedPrice = "0"
	}
	if !financeDecimalPattern.MatchString(in.InputTokenPrice) || !financeDecimalPattern.MatchString(in.CachedInputTokenPrice) || !financeDecimalPattern.MatchString(in.OutputTokenPrice) || !financeDecimalPattern.MatchString(in.RequestFixedPrice) {
		openAIError(c, http.StatusBadRequest, "invalid_request", "price fields must be exact non-negative decimal strings with at most 12 fractional digits.")
		return
	}
	in.SupplierID = c.Param("id")
	out, err := d.Store.CreateSupplierPrice(c.Request.Context(), in, claimsFrom(c).Subject)
	respondCreated(c, out, err)
}

func secretcryptoToken() (string, error) { return auth.NewOpaqueToken() }
