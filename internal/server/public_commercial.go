package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/store"
)

var publicPaymentProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

func registerPublicCommercialRoutes(r *gin.Engine, d Dependencies) {
	public := r.Group("/api/public")
	public.GET("/config", func(c *gin.Context) {
		dimensions := store.PublicCatalogDimensions{Regions: []string{}, Currencies: []string{}}
		if d.Store != nil {
			var err error
			dimensions, err = d.Store.PublicCatalogDimensions(c.Request.Context())
			if err != nil {
				respond(c, nil, err)
				return
			}
		}
		dimensions.Regions = mergePublicDimensions(dimensions.Regions, d.Config.PaymentAllowedRegions)
		c.Header("Cache-Control", "public, max-age=60")
		c.JSON(http.StatusOK, gin.H{
			"product": "ModelDock", "compatibility_name": "RelayDock",
			"registration_mode": d.Config.RegistrationMode, "email_verification_required": true,
			"support_email": d.Config.PublicSupportEmail, "enterprise_email": d.Config.PublicEnterpriseEmail,
			"api_base_path": "/v1", "api_key_prefixes": []string{"rdk_live_", "rdk_test_"},
			"payment_regions":   d.Config.PaymentAllowedRegions,
			"supported_regions": dimensions.Regions, "supported_currencies": dimensions.Currencies,
			"legal_review_required": true,
			"legal_review_notice":   "Legal and policy text is a draft until reviewed by qualified counsel.",
			"funnel_tracking":       gin.H{"anonymous_event": store.FunnelHomepageVisited, "server_side_milestones": true},
		})
	})
	public.GET("/catalog/providers", func(c *gin.Context) {
		region, ok := publicRegion(c)
		if !ok {
			return
		}
		items, err := d.Store.ListPublicProviders(c.Request.Context(), region)
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.Header("Cache-Control", "public, max-age=30")
		c.JSON(http.StatusOK, gin.H{"items": items, "region": region, "updated_at": time.Now().UTC()})
	})
	public.GET("/catalog/models", func(c *gin.Context) {
		region, ok := publicRegion(c)
		if !ok {
			return
		}
		currency, ok := publicCurrency(c, d)
		if !ok {
			return
		}
		items, err := d.Store.ListPublicModels(c.Request.Context(), region, currency)
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.Header("Cache-Control", "public, max-age=30")
		c.JSON(http.StatusOK, gin.H{"items": items, "region": region, "currency": currency, "updated_at": time.Now().UTC()})
	})
	public.GET("/pricing", func(c *gin.Context) {
		region, ok := publicRegion(c)
		if !ok {
			return
		}
		currency, ok := publicCurrency(c, d)
		if !ok {
			return
		}
		catalog, err := d.Store.PublicPricing(c.Request.Context(), region, currency)
		if err != nil {
			respond(c, nil, err)
			return
		}
		catalog.PaymentRegionSupported = publicPaymentRegionSupported(d.Payments, d.Config.PaymentAllowedRegions, region, catalog.PaymentFees)
		c.Header("Cache-Control", "public, max-age=30")
		c.JSON(http.StatusOK, catalog)
	})
	public.GET("/status", func(c *gin.Context) { publicStatusHandler(c, d) })
	public.POST("/funnel/events", func(c *gin.Context) { recordPublicFunnelEvent(c, d) })
}

func registerAdminCommercialRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/public/commercial-terms", func(c *gin.Context) {
		limit, offset := page(c)
		items, err := d.Store.ListCommercialTermsEvidence(c.Request.Context(), limit, offset)
		respondList(c, items, err)
	})
	g.POST("/public/commercial-terms", func(c *gin.Context) {
		var input struct {
			Region                  string     `json:"region"`
			Currency                string     `json:"currency"`
			SubscriptionTaxIncluded *bool      `json:"subscription_tax_included"`
			TokenTaxIncluded        *bool      `json:"token_tax_included"`
			TaxDisclosure           string     `json:"tax_disclosure"`
			RefundSummary           string     `json:"refund_summary"`
			RefundPolicyURL         string     `json:"refund_policy_url"`
			BonusCreditAmount       string     `json:"bonus_credit_amount"`
			BonusNonRefundable      bool       `json:"bonus_non_refundable"`
			EffectiveAt             time.Time  `json:"effective_at"`
			ExpiresAt               *time.Time `json:"expires_at"`
			LegalReviewStatus       string     `json:"legal_review_status"`
			LegalReviewConfirmed    bool       `json:"legal_review_confirmed"`
			IdempotencyKey          string     `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid commercial terms body is required.")
			return
		}
		if input.IdempotencyKey == "" {
			input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		input.Region = strings.ToUpper(strings.TrimSpace(input.Region))
		input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
		input.LegalReviewStatus = strings.ToUpper(strings.TrimSpace(input.LegalReviewStatus))
		if input.LegalReviewStatus == "" {
			input.LegalReviewStatus = "PENDING"
		}
		if input.BonusCreditAmount == "" {
			input.BonusCreditAmount = "0"
		}
		if input.EffectiveAt.IsZero() || !validPublicEvidenceScope(input.Region, input.Currency) ||
			!financeDecimalPattern.MatchString(input.BonusCreditAmount) || len(input.IdempotencyKey) < 1 || len(input.IdempotencyKey) > 200 ||
			strings.TrimSpace(input.TaxDisclosure) == "" || len(input.TaxDisclosure) > 4000 ||
			strings.TrimSpace(input.RefundSummary) == "" || len(input.RefundSummary) > 4000 ||
			!validPublicPolicyURL(input.RefundPolicyURL) ||
			(input.ExpiresAt != nil && !input.ExpiresAt.After(input.EffectiveAt)) ||
			(input.LegalReviewStatus != "PENDING" && input.LegalReviewStatus != "APPROVED") ||
			(input.LegalReviewStatus == "APPROVED" && !input.LegalReviewConfirmed) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Commercial terms, effective period, idempotency key, or legal-review evidence is invalid.")
			return
		}
		var reviewedAt *time.Time
		if input.LegalReviewStatus == "APPROVED" {
			now := time.Now().UTC()
			reviewedAt = &now
		}
		out, replayed, err := d.Store.PublishCommercialTerms(c.Request.Context(), store.PublishCommercialTermsRequest{
			Region: input.Region, Currency: input.Currency,
			SubscriptionTaxIncluded: input.SubscriptionTaxIncluded, TokenTaxIncluded: input.TokenTaxIncluded,
			TaxDisclosure: strings.TrimSpace(input.TaxDisclosure), RefundSummary: strings.TrimSpace(input.RefundSummary),
			RefundPolicyURL: strings.TrimSpace(input.RefundPolicyURL), BonusCreditAmount: input.BonusCreditAmount,
			BonusNonRefundable: input.BonusNonRefundable, EffectiveAt: input.EffectiveAt, ExpiresAt: input.ExpiresAt,
			LegalReviewStatus: input.LegalReviewStatus, ReviewedAt: reviewedAt,
			IdempotencyKey: input.IdempotencyKey, CreatedBy: claimsFrom(c).Subject,
		})
		if err != nil {
			respond(c, nil, err)
			return
		}
		status := http.StatusCreated
		if replayed {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"commercial_terms": out, "replayed": replayed})
	})
	g.GET("/public/payment-fees", func(c *gin.Context) {
		limit, offset := page(c)
		items, err := d.Store.ListPaymentFeeEvidence(c.Request.Context(), limit, offset)
		respondList(c, items, err)
	})
	g.POST("/public/payment-fees", func(c *gin.Context) {
		var input struct {
			FeeCategory          string     `json:"fee_category"`
			PaymentProvider      string     `json:"payment_provider"`
			Region               string     `json:"region"`
			Currency             string     `json:"currency"`
			FeeKind              string     `json:"fee_kind"`
			FixedAmount          string     `json:"fixed_amount"`
			RateBPS              int        `json:"rate_bps"`
			ChargedToCustomer    bool       `json:"charged_to_customer"`
			Description          string     `json:"description"`
			EffectiveAt          time.Time  `json:"effective_at"`
			ExpiresAt            *time.Time `json:"expires_at"`
			LegalReviewStatus    string     `json:"legal_review_status"`
			LegalReviewConfirmed bool       `json:"legal_review_confirmed"`
			IdempotencyKey       string     `json:"idempotency_key"`
		}
		if c.ShouldBindJSON(&input) != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid payment fee body is required.")
			return
		}
		if input.IdempotencyKey == "" {
			input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		}
		input.FeeCategory = strings.ToUpper(strings.TrimSpace(input.FeeCategory))
		input.PaymentProvider = strings.ToLower(strings.TrimSpace(input.PaymentProvider))
		input.Region = strings.ToUpper(strings.TrimSpace(input.Region))
		input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
		input.FeeKind = strings.ToUpper(strings.TrimSpace(input.FeeKind))
		input.LegalReviewStatus = strings.ToUpper(strings.TrimSpace(input.LegalReviewStatus))
		if input.LegalReviewStatus == "" {
			input.LegalReviewStatus = "PENDING"
		}
		if input.FixedAmount == "" {
			input.FixedAmount = "0"
		}
		if input.EffectiveAt.IsZero() || !validPublicEvidenceScope(input.Region, input.Currency) || !publicPaymentProviderPattern.MatchString(input.PaymentProvider) ||
			(input.FeeCategory != "PAYMENT_CHANNEL" && input.FeeCategory != "PLATFORM_SERVICE") ||
			!validPaymentFee(input.FeeKind, input.FixedAmount, input.RateBPS) ||
			len(input.IdempotencyKey) < 1 || len(input.IdempotencyKey) > 200 ||
			strings.TrimSpace(input.Description) == "" || len(input.Description) > 4000 ||
			(input.ExpiresAt != nil && !input.ExpiresAt.After(input.EffectiveAt)) ||
			(input.LegalReviewStatus != "PENDING" && input.LegalReviewStatus != "APPROVED") ||
			(input.LegalReviewStatus == "APPROVED" && !input.LegalReviewConfirmed) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Payment fee, effective period, idempotency key, or legal-review evidence is invalid.")
			return
		}
		var reviewedAt *time.Time
		if input.LegalReviewStatus == "APPROVED" {
			now := time.Now().UTC()
			reviewedAt = &now
		}
		out, replayed, err := d.Store.PublishPaymentFee(c.Request.Context(), store.PublishPaymentFeeRequest{
			FeeCategory: input.FeeCategory, PaymentProvider: input.PaymentProvider,
			Region: input.Region, Currency: input.Currency, FeeKind: input.FeeKind,
			FixedAmount: input.FixedAmount, RateBPS: input.RateBPS,
			ChargedToCustomer: input.ChargedToCustomer, Description: strings.TrimSpace(input.Description),
			EffectiveAt: input.EffectiveAt, ExpiresAt: input.ExpiresAt,
			LegalReviewStatus: input.LegalReviewStatus, ReviewedAt: reviewedAt,
			IdempotencyKey: input.IdempotencyKey, CreatedBy: claimsFrom(c).Subject,
		})
		if err != nil {
			respond(c, nil, err)
			return
		}
		status := http.StatusCreated
		if replayed {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"payment_fee": out, "replayed": replayed})
	})
	g.GET("/funnel/summary", func(c *gin.Context) {
		from, to, err := queryTimeRange(c, 30*24*time.Hour, 366*24*time.Hour)
		if err != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		summary, err := d.Store.CommercialFunnelSummary(c.Request.Context(), from, to)
		if err != nil {
			respond(c, nil, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"from": summary.From, "to": summary.To, "counts": summary.Counts,
			"stages":                 funnelStagesWithConversion(summary.Counts),
			"cohort_scope":           []string{"SELF_REGISTRATION", "INVITATION"},
			"call_success_semantics": "HTTP_2XX_AND_SETTLEMENT_TERMINAL",
		})
	})
}

func registerConsoleOnboardingRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/onboarding", func(c *gin.Context) {
		status, err := d.Store.UserOnboardingStatus(c.Request.Context(), claimsFrom(c).Subject,
			strings.TrimSpace(c.Query("organization_id")), strings.TrimSpace(c.Query("project_id")))
		respond(c, status, err)
	})
}

func recordPublicFunnelEvent(c *gin.Context, d Dependencies) {
	var input struct {
		EventType      string `json:"event_type"`
		AnonymousID    string `json:"anonymous_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if c.ShouldBindJSON(&input) != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A valid funnel event body is required.")
		return
	}
	input.EventType = strings.ToUpper(strings.TrimSpace(input.EventType))
	input.AnonymousID = strings.TrimSpace(input.AnonymousID)
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	if input.EventType != store.FunnelHomepageVisited || len(input.AnonymousID) < 16 || len(input.AnonymousID) > 200 ||
		len(input.IdempotencyKey) < 1 || len(input.IdempotencyKey) > 200 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "Only a bounded HOMEPAGE_VISITED event with anonymous_id and Idempotency-Key is accepted.")
		return
	}
	if d.Limiter != nil {
		result, err := d.Limiter.AllowIdentity(c.Request.Context(), "public_funnel", c.ClientIP(),
			d.Config.PublicFunnelRateLimit, d.Config.IdentityRateWindow)
		if err != nil {
			openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Funnel abuse protection is temporarily unavailable.")
			return
		}
		if !result.Allowed {
			c.Header("Retry-After", strconv.Itoa(maxInt(1, int(result.RetryAfter.Seconds()))))
			openAIError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Too many funnel events. Please try again later.")
			return
		}
	}
	hash := commercialAnonymousHash(d.Config.APIKeyHMACSecret, input.AnonymousID)
	event, replayed, err := d.Store.RecordAnonymousHomepageVisit(c.Request.Context(), hash, input.IdempotencyKey)
	if err != nil {
		respond(c, nil, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{
		"event_id": event.ID, "event_type": event.EventType,
		"occurred_at": event.OccurredAt, "replayed": replayed,
	})
}

func commercialAnonymousHash(secret []byte, anonymousID string) []byte {
	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte("relaydock:commercial-funnel:v1:" + anonymousID))
	return digest.Sum(nil)
}

func publicRegion(c *gin.Context) (string, bool) {
	region := strings.ToUpper(strings.TrimSpace(c.Query("region")))
	if region == "" {
		region = "CN"
	}
	if len(region) != 2 || region[0] < 'A' || region[0] > 'Z' || region[1] < 'A' || region[1] > 'Z' {
		openAIError(c, http.StatusBadRequest, "invalid_request", "region must be a two-letter uppercase region code.")
		return "", false
	}
	return region, true
}

func publicCurrency(c *gin.Context, d Dependencies) (string, bool) {
	currency := strings.ToUpper(strings.TrimSpace(c.Query("currency")))
	if currency == "" {
		if d.Store == nil {
			openAIError(c, http.StatusServiceUnavailable, "pricing_unavailable", "Published pricing currencies are unavailable.")
			return "", false
		}
		dimensions, err := d.Store.PublicCatalogDimensions(c.Request.Context())
		if err != nil {
			respond(c, nil, err)
			return "", false
		}
		if len(dimensions.Currencies) == 0 {
			openAIError(c, http.StatusUnprocessableEntity, "pricing_unavailable", "No published pricing currency is available.")
			return "", false
		}
		currency = dimensions.Currencies[0]
	}
	if len(currency) != 3 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "currency must be a three-letter currency code.")
		return "", false
	}
	for _, char := range currency {
		if char < 'A' || char > 'Z' {
			openAIError(c, http.StatusBadRequest, "invalid_request", "currency must be a three-letter currency code.")
			return "", false
		}
	}
	return currency, true
}

func funnelStagesWithConversion(counts map[string]int64) []gin.H {
	stages := make([]gin.H, 0, len(store.FunnelOrder))
	var previous int64
	for index, eventType := range store.FunnelOrder {
		count := counts[eventType]
		var conversion *float64
		if index > 0 && previous > 0 {
			value := float64(count) / float64(previous)
			conversion = &value
		}
		stages = append(stages, gin.H{
			"event_type": eventType, "count": count, "conversion_from_previous": conversion,
		})
		previous = count
	}
	return stages
}

func validPublicEvidenceScope(region, currency string) bool {
	if region != "*" && (len(region) != 2 || region[0] < 'A' || region[0] > 'Z' || region[1] < 'A' || region[1] > 'Z') {
		return false
	}
	if len(currency) != 3 {
		return false
	}
	for _, char := range currency {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validPublicPolicyURL(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func validPaymentFee(kind, fixed string, rateBPS int) bool {
	if !financeDecimalPattern.MatchString(fixed) || rateBPS < 0 || rateBPS > 100000 {
		return false
	}
	zero := fixed == "0" || strings.Trim(fixed, "0.") == ""
	switch kind {
	case "NONE":
		return zero && rateBPS == 0
	case "FIXED":
		return !zero && rateBPS == 0
	case "PERCENT":
		return zero && rateBPS > 0
	case "FIXED_PLUS_PERCENT":
		return !zero && rateBPS > 0
	default:
		return false
	}
}

func mergePublicDimensions(values ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, list := range values {
		for _, value := range list {
			value = strings.ToUpper(strings.TrimSpace(value))
			if value == "" || value == "*" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func publicDimensionContains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func publicPaymentRegionSupported(registry *payment.Registry, configuredRegions []string, region string, fees []store.PublicPaymentFee) bool {
	if registry == nil || !publicDimensionContains(configuredRegions, region) {
		return false
	}
	for _, fee := range fees {
		if fee.FeeCategory != "PAYMENT_CHANNEL" || fee.LegalReviewStatus != "APPROVED" {
			continue
		}
		if _, err := registry.Resolve(fee.PaymentProvider, region); err == nil {
			return true
		}
	}
	return false
}
