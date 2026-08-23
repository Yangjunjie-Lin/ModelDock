package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/pricing"
)

var (
	ErrFundingInProgress = errors.New("funding operation is already in progress")
	ErrFundingTerminal   = errors.New("funding operation is already terminal")
)

type FundingReservationRequest struct {
	OrganizationID     string
	ProjectID          string
	APIKeyID           string
	RequestID          string
	IdempotencyKey     string
	RequestFingerprint string
	PricingVersionID   string
	Currency           string
	MaximumAmount      string
	PromotionAmount    string
	TaxRate            string
	ExchangeRate       string
	EstimatedInput     int64
	MaxOutput          int64
	CreatedBy          *string
}

type FundingSettlementRequest struct {
	OperationID      string
	InputTokens      int64
	CachedInput      int64
	OutputTokens     int64
	ObservedBytes    int64
	UsageSource      string
	FailureCode      string
	PartialFailure   bool
	Waive            bool
	SettlementSource string
}

type LateUsageRequest struct {
	OperationID    string
	IdempotencyKey string
	InputTokens    int64
	CachedInput    int64
	OutputTokens   int64
	UsageSource    string
	CreatedBy      *string
}

type journalLine struct {
	accountKey  string
	side        string
	amount      string
	description string
}

func (s *Store) ReserveFunding(ctx context.Context, request FundingReservationRequest) (domain.FundingOperation, bool, error) {
	if strings.TrimSpace(request.OrganizationID) == "" || strings.TrimSpace(request.ProjectID) == "" ||
		strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" ||
		strings.TrimSpace(request.RequestFingerprint) == "" || request.EstimatedInput < 0 || request.MaxOutput < 0 {
		return domain.FundingOperation{}, false, errors.New("invalid funding reservation")
	}
	if request.Currency == "" {
		request.Currency = "USD"
	}
	maximum, ok := new(big.Rat).SetString(zeroIfEmpty(request.MaximumAmount))
	if !ok || maximum.Sign() < 0 {
		return domain.FundingOperation{}, false, errors.New("invalid maximum funding amount")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	defer tx.Rollback(ctx)

	var walletID, currency, billingMode, walletStatus, available, reserved, creditLimit, riskLimit, riskExposure string
	var creditEnforced bool
	err = tx.QueryRow(ctx, `SELECT id,currency,billing_mode,status,available_balance::text,reserved_balance::text,
		credit_limit::text,risk_limit::text,risk_exposure::text,credit_enforced
		FROM wallets WHERE organization_id=$1 FOR UPDATE`, request.OrganizationID).
		Scan(&walletID, &currency, &billingMode, &walletStatus, &available, &reserved, &creditLimit, &riskLimit, &riskExposure, &creditEnforced)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FundingOperation{}, false, ErrNotFound
	}
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	var existing domain.FundingOperation
	err = scanFundingOperation(tx.QueryRow(ctx, fundingOperationSelect+` WHERE organization_id=$1 AND idempotency_key=$2`, request.OrganizationID, request.IdempotencyKey), &existing)
	if err == nil {
		existingAPIKeyID := ""
		if existing.APIKeyID != nil {
			existingAPIKeyID = *existing.APIKeyID
		}
		if existing.RequestFingerprint != request.RequestFingerprint || existing.ProjectID != request.ProjectID ||
			existingAPIKeyID != request.APIKeyID {
			return domain.FundingOperation{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.FundingOperation{}, false, err
	}
	if walletStatus != "ACTIVE" || currency != strings.ToUpper(request.Currency) {
		return domain.FundingOperation{}, false, ErrWalletUnavailable
	}
	availableAmount, err := fundingRat(available)
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	creditAmount, err := fundingRat(creditLimit)
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	riskLimitAmount, err := fundingRat(riskLimit)
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	riskExposureAmount, err := fundingRat(riskExposure)
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	remaining := new(big.Rat).Sub(new(big.Rat).Set(availableAmount), maximum)
	allowed := true
	if billingMode == "PREPAID" {
		allowed = remaining.Sign() >= 0 && riskExposureAmount.Cmp(riskLimitAmount) <= 0
	} else if creditEnforced {
		allowed = new(big.Rat).Add(remaining, creditAmount).Sign() >= 0 && riskExposureAmount.Cmp(riskLimitAmount) <= 0
	}
	if !allowed {
		return domain.FundingOperation{}, false, ErrWalletUnavailable
	}

	operationID := id.UUID()
	promotionRequested, promotionOK := new(big.Rat).SetString(zeroIfEmpty(request.PromotionAmount))
	if !promotionOK || promotionRequested.Sign() < 0 {
		return domain.FundingOperation{}, false, errors.New("invalid promotion funding amount")
	}
	var pricingVersion any
	if request.PricingVersionID != "" {
		pricingVersion = request.PricingVersionID
	}
	_, err = tx.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,api_key_id,request_id,
		idempotency_key,request_fingerprint,pricing_version_id,status,currency,maximum_amount,promotion_amount,tax_rate,exchange_rate,estimated_input_tokens,max_output_tokens)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'RESERVED',$10,$11,$12,$13,$14,$15,$16)`, operationID, walletID, request.OrganizationID,
		request.ProjectID, nullString(request.APIKeyID), request.RequestID, request.IdempotencyKey, request.RequestFingerprint,
		pricingVersion, currency, formatRat(maximum), formatRat(promotionRequested), zeroIfEmpty(request.TaxRate), oneIfEmpty(request.ExchangeRate), request.EstimatedInput, request.MaxOutput)
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	if err = reserveCashLotsTx(ctx, tx, operationID, walletID, formatRat(maximum)); err != nil {
		return domain.FundingOperation{}, false, err
	}
	reservedPromotion, err := reservePromotionTx(ctx, tx, operationID, request.OrganizationID, currency, formatRat(promotionRequested))
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	if !decimalEqual(reservedPromotion, formatRat(promotionRequested)) {
		return domain.FundingOperation{}, false, ErrWalletUnavailable
	}
	if maximum.Sign() > 0 {
		_, err = postJournalTx(ctx, tx, walletID, "RESERVATION", "funding:"+operationID+":reservation", currency,
			request.RequestID, map[string]any{"funding_operation_id": operationID, "request_id": request.RequestID}, request.CreatedBy, []journalLine{
				{walletAccountKey(walletID, "available"), "DEBIT", formatRat(maximum), "Reserve maximum cash request cost"},
				{walletAccountKey(walletID, "reserved"), "CREDIT", formatRat(maximum), "Reserved cash request funds"},
			})
		if err != nil {
			return domain.FundingOperation{}, false, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE wallets SET available_balance=available_balance-$2::numeric,
		reserved_balance=reserved_balance+$2::numeric,version=version+1,updated_at=now() WHERE id=$1`, walletID, formatRat(maximum))
	if err != nil {
		return domain.FundingOperation{}, false, err
	}
	if err = insertFundingEvent(ctx, tx, operationID, "RESERVED", formatRat(maximum), "admission", map[string]any{
		"request_id": request.RequestID, "estimated_input_tokens": request.EstimatedInput, "max_output_tokens": request.MaxOutput, "promotion_reserved": reservedPromotion,
	}); err != nil {
		return domain.FundingOperation{}, false, err
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "wallet.funding_reserved", "funding_operation", operationID, map[string]any{
		"wallet_id": walletID, "request_id": request.RequestID, "maximum_amount": formatRat(maximum), "promotion_reserved": reservedPromotion, "currency": currency,
	}); err != nil {
		return domain.FundingOperation{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.FundingOperation{}, false, err
	}
	operation, err := s.FundingOperationByID(ctx, operationID)
	return operation, false, err
}

func (s *Store) SettleFunding(ctx context.Context, request FundingSettlementRequest) (domain.FundingOperation, error) {
	if request.InputTokens < 0 || request.CachedInput < 0 || request.OutputTokens < 0 || request.CachedInput > request.InputTokens || request.ObservedBytes < 0 {
		return domain.FundingOperation{}, errors.New("invalid funding settlement usage")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	defer tx.Rollback(ctx)
	var operation domain.FundingOperation
	if err = scanFundingOperation(tx.QueryRow(ctx, fundingOperationSelect+` WHERE id=$1 FOR UPDATE`, request.OperationID), &operation); errors.Is(err, pgx.ErrNoRows) {
		return domain.FundingOperation{}, ErrNotFound
	} else if err != nil {
		return domain.FundingOperation{}, err
	}
	if isFundingTerminal(operation.Status) {
		return operation, tx.Commit(ctx)
	}
	var billingMode, walletStatus, available, riskLimit, riskExposure string
	var creditEnforced bool
	err = tx.QueryRow(ctx, `SELECT billing_mode,status,available_balance::text,risk_limit::text,risk_exposure::text,credit_enforced
		FROM wallets WHERE id=$1 FOR UPDATE`, operation.WalletID).Scan(&billingMode, &walletStatus, &available, &riskLimit, &riskExposure, &creditEnforced)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	actualAmount := "0"
	actualPromotion := "0"
	if operation.PricingVersionID != nil && !request.Waive {
		actualAmount, actualPromotion, err = fundingAmountsForVersion(ctx, tx, *operation.PricingVersionID, request.InputTokens, request.CachedInput, request.OutputTokens,
			operation.PromotionAmount.String(), operation.TaxRate.String(), operation.ExchangeRate.String())
		if err != nil {
			return domain.FundingOperation{}, err
		}
	}
	platformServiceFee := "0"
	if operation.CredentialOwner == domain.CredentialOwnerCustomer && !request.Waive {
		actualPromotion = "0"
		var fixedFee, inputFee, cachedFee, outputFee string
		var unit int64
		if err = tx.QueryRow(ctx, `SELECT byok_fixed_fee::text,byok_input_token_fee::text,byok_cached_input_token_fee::text,
			byok_output_token_fee::text,byok_fee_unit FROM funding_operation WHERE id=$1`, operation.ID).
			Scan(&fixedFee, &inputFee, &cachedFee, &outputFee, &unit); err != nil {
			return domain.FundingOperation{}, err
		}
		feeResult, feeErr := pricing.Calculate(
			pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: unit, Currency: operation.Currency},
			pricing.Rate{Input: inputFee, Cached: cachedFee, Output: outputFee, Fixed: fixedFee, Unit: unit, Currency: operation.Currency},
			pricing.Tokens{Input: request.InputTokens, Cached: request.CachedInput, Output: request.OutputTokens}, "0", "0", "1")
		if feeErr != nil {
			return domain.FundingOperation{}, feeErr
		}
		platformServiceFee = feeResult.FinalAmount
		actualAmount = platformServiceFee
	}
	consumedPromotion, err := settlePromotionTx(ctx, tx, operation.ID, actualPromotion)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	maximum, err := fundingRat(operation.MaximumAmount.String())
	if err != nil {
		return domain.FundingOperation{}, err
	}
	actual, err := fundingRat(actualAmount)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	reservedUsed := new(big.Rat).Set(actual)
	if reservedUsed.Cmp(maximum) > 0 {
		reservedUsed.Set(maximum)
	}
	release := new(big.Rat).Sub(new(big.Rat).Set(maximum), reservedUsed)
	overage := new(big.Rat).Sub(new(big.Rat).Set(actual), reservedUsed)
	settlementJournalID := ""
	if actual.Sign() > 0 {
		lines := make([]journalLine, 0, 3)
		if reservedUsed.Sign() > 0 {
			lines = append(lines, journalLine{walletAccountKey(operation.WalletID, "reserved"), "DEBIT", formatRat(reservedUsed), "Settle reserved funds"})
		}
		if overage.Sign() > 0 {
			lines = append(lines, journalLine{walletAccountKey(operation.WalletID, "available"), "DEBIT", formatRat(overage), "Settle estimation variance"})
		}
		lines = append(lines, journalLine{systemAccountKey("revenue", operation.Currency), "CREDIT", actualAmount, "Recognize usage revenue"})
		settlementJournalID, err = postJournalTx(ctx, tx, operation.WalletID, "SETTLEMENT", "funding:"+operation.ID+":settlement",
			operation.Currency, operation.RequestID, map[string]any{"funding_operation_id": operation.ID, "usage_source": request.UsageSource}, nil, lines)
		if err != nil {
			return domain.FundingOperation{}, err
		}
	}
	if release.Sign() > 0 {
		_, err = postJournalTx(ctx, tx, operation.WalletID, "RELEASE", "funding:"+operation.ID+":release", operation.Currency,
			operation.RequestID, map[string]any{"funding_operation_id": operation.ID}, nil, []journalLine{
				{walletAccountKey(operation.WalletID, "reserved"), "DEBIT", formatRat(release), "Release unused reservation"},
				{walletAccountKey(operation.WalletID, "available"), "CREDIT", formatRat(release), "Return unused reservation"},
			})
		if err != nil {
			return domain.FundingOperation{}, err
		}
	}
	availableRat, err := fundingRat(available)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	newAvailable := new(big.Rat).Add(availableRat, release)
	newAvailable.Sub(newAvailable, overage)
	newRiskExposure, err := fundingRat(riskExposure)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if overage.Sign() > 0 {
		newRiskExposure.Add(newRiskExposure, overage)
	}
	freeze := walletStatus != "ACTIVE"
	riskLimitRat, err := fundingRat(riskLimit)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if newRiskExposure.Cmp(riskLimitRat) > 0 && overage.Sign() > 0 {
		freeze = true
	}
	if billingMode == "POSTPAID" && !creditEnforced {
		freeze = false
	}
	newWalletStatus := walletStatus
	if freeze && walletStatus == "ACTIVE" {
		newWalletStatus = "FROZEN"
	}
	_, err = tx.Exec(ctx, `UPDATE wallets SET available_balance=$2,reserved_balance=reserved_balance-$3::numeric,
		risk_exposure=$4,status=$5,version=version+1,updated_at=now() WHERE id=$1`, operation.WalletID,
		formatRat(newAvailable), operation.MaximumAmount.String(), formatRat(newRiskExposure), newWalletStatus)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	status := "SETTLED"
	if actual.Sign() == 0 {
		status = "RELEASED"
		if request.FailureCode != "" {
			status = "FAILED"
		}
	} else if request.PartialFailure {
		status = "PARTIALLY_SETTLED"
	}
	settledAt, releasedAt := any(nil), any(nil)
	if actual.Sign() > 0 {
		settledAt = time.Now().UTC()
	}
	if release.Sign() > 0 || actual.Sign() == 0 {
		releasedAt = time.Now().UTC()
	}
	_, err = tx.Exec(ctx, `UPDATE funding_operation SET status=$2,settled_amount=$3,released_amount=$4,consumed_promotion_amount=$13,platform_service_fee=$14,
		actual_input_tokens=$5,actual_cached_input_tokens=$6,actual_output_tokens=$7,usage_source=$8,
		observed_output_bytes=$9,failure_code=$10,settled_at=$11,released_at=$12,heartbeat_at=now(),updated_at=now() WHERE id=$1`,
		operation.ID, status, actualAmount, formatRat(release), request.InputTokens, request.CachedInput, request.OutputTokens,
		request.UsageSource, request.ObservedBytes, nullString(request.FailureCode), settledAt, releasedAt, consumedPromotion, platformServiceFee)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if release.Sign() > 0 {
		if err = insertFundingEvent(ctx, tx, operation.ID, "RELEASED", formatRat(release), firstNonEmptyStore(request.SettlementSource, "gateway"), map[string]any{"usage_source": request.UsageSource}); err != nil {
			return domain.FundingOperation{}, err
		}
	}
	if err = insertFundingEvent(ctx, tx, operation.ID, status, actualAmount, firstNonEmptyStore(request.SettlementSource, "gateway"), map[string]any{
		"released_amount": formatRat(release), "failure_code": request.FailureCode, "usage_source": request.UsageSource,
	}); err != nil {
		return domain.FundingOperation{}, err
	}
	if actual.Sign() > 0 {
		transactionID := id.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,reference,metadata,journal_id,funding_operation_id)
			VALUES($1,$2,'CHARGE',-$3::numeric,$4,$5,$6,$7,$8,$9) ON CONFLICT(wallet_id,idempotency_key) DO NOTHING`, transactionID,
			operation.WalletID, actualAmount, formatRat(newAvailable), "funding:"+operation.ID+":charge", operation.RequestID,
			jsonBytes(map[string]any{"funding_operation_id": operation.ID, "usage_source": request.UsageSource, "promotion_amount": consumedPromotion}), nullString(settlementJournalID), operation.ID)
		if err != nil {
			return domain.FundingOperation{}, err
		}
		if err = settleCashLotsTx(ctx, tx, operation.ID, operation.WalletID, transactionID, actualAmount); err != nil {
			return domain.FundingOperation{}, err
		}
	} else if err = settleCashLotsTx(ctx, tx, operation.ID, operation.WalletID, "", actualAmount); err != nil {
		return domain.FundingOperation{}, err
	}
	billingStatus := "WAIVED"
	if actual.Sign() > 0 {
		billingStatus = "CHARGED"
	}
	_, err = tx.Exec(ctx, `UPDATE billing_usage_records SET status=$2 WHERE funding_operation_id=$1`, operation.ID, billingStatus)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if err = writeAuditTx(ctx, tx, nil, "wallet.funding_settled", "funding_operation", operation.ID, map[string]any{
		"wallet_id": operation.WalletID, "request_id": operation.RequestID, "status": status, "settled_amount": actualAmount,
		"released_amount": formatRat(release), "usage_source": request.UsageSource, "risk_exposure": formatRat(newRiskExposure),
	}); err != nil {
		return domain.FundingOperation{}, err
	}
	var budgetProviderID, reservedProviderCost string
	err = tx.QueryRow(ctx, `SELECT provider_id,maximum_cost::text FROM provider_budget_reservations WHERE operation_id=$1 AND status='RESERVED' FOR UPDATE`, operation.ID).Scan(&budgetProviderID, &reservedProviderCost)
	if err == nil {
		actualProviderCost := "0"
		if !request.Waive && operation.PricingVersionID != nil {
			var costInput, costCached, costOutput, costFixed, currency string
			var costUnit int64
			if err = tx.QueryRow(ctx, `SELECT provider_input_token_cost::text,provider_cached_input_token_cost::text,provider_output_token_cost::text,
				provider_request_fixed_cost::text,provider_unit,provider_currency FROM model_price_version WHERE id=$1`, *operation.PricingVersionID).
				Scan(&costInput, &costCached, &costOutput, &costFixed, &costUnit, &currency); err != nil {
				return domain.FundingOperation{}, err
			}
			costResult, costErr := pricing.Calculate(pricing.Rate{Input: costInput, Cached: costCached, Output: costOutput, Fixed: costFixed, Unit: costUnit, Currency: currency}, pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: costUnit, Currency: currency}, pricing.Tokens{Input: request.InputTokens, Cached: request.CachedInput, Output: request.OutputTokens}, "0", "0", "1")
			if costErr != nil {
				return domain.FundingOperation{}, costErr
			}
			actualProviderCost = costResult.ProviderCost
		}
		if mustFundingRat(actualProviderCost).Cmp(mustFundingRat(reservedProviderCost)) > 0 {
			return domain.FundingOperation{}, ErrProviderBudgetExceeded
		}
		budgetStatus := "RELEASED"
		if decimalPositive(actualProviderCost) {
			budgetStatus = "SETTLED"
		}
		if _, err = tx.Exec(ctx, `UPDATE provider_usage_budget SET reserved_cost=GREATEST(0,reserved_cost-$3::numeric),provider_cost=provider_cost+$4::numeric,updated_at=now()
			WHERE provider_id=$1 AND period_month=$2`, budgetProviderID, providerBudgetMonth(time.Now()), reservedProviderCost, actualProviderCost); err != nil {
			return domain.FundingOperation{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE provider_budget_reservations SET settled_cost=$2,status=$3,settled_at=now() WHERE operation_id=$1`, operation.ID, actualProviderCost, budgetStatus); err != nil {
			return domain.FundingOperation{}, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.FundingOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.FundingOperation{}, err
	}
	return s.FundingOperationByID(ctx, operation.ID)
}

func (s *Store) HeartbeatFunding(ctx context.Context, operationID string, observedBytes int64) error {
	if observedBytes < 0 {
		return errors.New("observed bytes cannot be negative")
	}
	_, err := s.pool.Exec(ctx, `UPDATE funding_operation SET observed_output_bytes=GREATEST(observed_output_bytes,$2),
		heartbeat_at=now(),updated_at=now() WHERE id=$1 AND status IN ('PENDING','RESERVED')`, operationID, observedBytes)
	return err
}

func (s *Store) BeginFundingAttempt(ctx context.Context, operationID, providerID, credentialID, groupID string, isFallback bool) (domain.FundingProviderAttempt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.FundingProviderAttempt{}, err
	}
	defer tx.Rollback(ctx)
	var operationStatus, organizationID, operationCurrency string
	if err = tx.QueryRow(ctx, `SELECT status,organization_id,currency FROM funding_operation WHERE id=$1 FOR UPDATE`, operationID).Scan(&operationStatus, &organizationID, &operationCurrency); err != nil {
		return domain.FundingProviderAttempt{}, err
	}
	if operationStatus != "RESERVED" && operationStatus != "PENDING" {
		return domain.FundingProviderAttempt{}, ErrFundingTerminal
	}
	if err = recheckProviderDispatchTx(ctx, tx, organizationID, providerID, credentialID); err != nil {
		return domain.FundingProviderAttempt{}, err
	}
	var credentialOwner string
	if err = tx.QueryRow(ctx, `SELECT credential_owner FROM provider_credentials WHERE id=$1`, credentialID).Scan(&credentialOwner); err != nil {
		return domain.FundingProviderAttempt{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE funding_operation SET credential_owner=$2,provider_credential_id=$3,updated_at=now() WHERE id=$1`, operationID, credentialOwner, credentialID); err != nil {
		return domain.FundingProviderAttempt{}, err
	}
	if credentialOwner == domain.CredentialOwnerCustomer {
		var policyID, fixedFee, inputFee, cachedFee, outputFee, policyCurrency string
		var unit int64
		err = tx.QueryRow(ctx, `SELECT id,fixed_fee::text,input_token_fee::text,cached_input_token_fee::text,output_token_fee::text,unit,currency
			FROM byok_service_fee_policies WHERE enabled=true AND (organization_id=$1 OR organization_id IS NULL)
			AND (provider_id=$2 OR provider_id IS NULL) AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
			ORDER BY (organization_id IS NOT NULL) DESC,(provider_id IS NOT NULL) DESC,effective_at DESC,id DESC LIMIT 1 FOR SHARE`,
			organizationID, providerID).Scan(&policyID, &fixedFee, &inputFee, &cachedFee, &outputFee, &unit, &policyCurrency)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.FundingProviderAttempt{}, ErrPricingUnavailable
		}
		if err != nil {
			return domain.FundingProviderAttempt{}, err
		}
		if policyCurrency != operationCurrency {
			return domain.FundingProviderAttempt{}, ErrPricingCurrencyMismatch
		}
		if _, err = tx.Exec(ctx, `UPDATE funding_operation SET byok_service_fee_policy_id=$2,byok_fixed_fee=$3,byok_input_token_fee=$4,
			byok_cached_input_token_fee=$5,byok_output_token_fee=$6,byok_fee_unit=$7 WHERE id=$1`, operationID, policyID, fixedFee, inputFee, cachedFee, outputFee, unit); err != nil {
			return domain.FundingProviderAttempt{}, err
		}
	}
	if credentialOwner == domain.CredentialOwnerPlatform {
		month := providerBudgetMonth(time.Now())
		if _, err = tx.Exec(ctx, `INSERT INTO provider_usage_budget(provider_id,period_month,request_count,reserved_cost,provider_cost)
			VALUES($1,$2,0,0,0) ON CONFLICT(provider_id,period_month) DO NOTHING`, providerID, month); err != nil {
			return domain.FundingProviderAttempt{}, err
		}
		var reservationExists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_budget_reservations WHERE operation_id=$1)`, operationID).Scan(&reservationExists); err != nil {
			return domain.FundingProviderAttempt{}, err
		}
		if !reservationExists {
			var settlementCurrency, configuredLimit, currentCost, reservedCost string
			var costInput, costCached, costOutput, costFixed, costCurrency string
			var costUnit, estimatedInput, maxOutput int64
			if err = tx.QueryRow(ctx, `SELECT p.settlement_currency,COALESCE(p.cost_limit,0)::text,b.provider_cost::text,b.reserved_cost::text,
			v.provider_input_token_cost::text,v.provider_cached_input_token_cost::text,v.provider_output_token_cost::text,
			v.provider_request_fixed_cost::text,v.provider_currency,v.provider_unit,f.estimated_input_tokens,f.max_output_tokens
			FROM funding_operation f JOIN model_price_version v ON v.id=f.pricing_version_id JOIN providers p ON p.id=$2
			JOIN provider_usage_budget b ON b.provider_id=p.id AND b.period_month=$3::date WHERE f.id=$1 FOR UPDATE OF b`,
				operationID, providerID, month).Scan(&settlementCurrency, &configuredLimit, &currentCost, &reservedCost, &costInput,
				&costCached, &costOutput, &costFixed, &costCurrency, &costUnit, &estimatedInput, &maxOutput); err != nil {
				return domain.FundingProviderAttempt{}, err
			}
			if costCurrency != settlementCurrency {
				return domain.FundingProviderAttempt{}, ErrPricingCurrencyMismatch
			}
			maximumResult, calculateErr := pricing.Calculate(
				pricing.Rate{Input: costInput, Cached: costCached, Output: costOutput, Fixed: costFixed, Unit: costUnit, Currency: costCurrency},
				pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: costUnit, Currency: costCurrency},
				pricing.Tokens{Input: estimatedInput, Output: maxOutput}, "0", "0", "1")
			if calculateErr != nil {
				return domain.FundingProviderAttempt{}, calculateErr
			}
			maximumProviderCost := maximumResult.ProviderCost
			if configuredLimit != "" && decimalPositive(configuredLimit) {
				limit := mustFundingRat(configuredLimit)
				projected := mustFundingRat(currentCost)
				projected.Add(projected, mustFundingRat(reservedCost))
				projected.Add(projected, mustFundingRat(maximumProviderCost))
				if projected.Cmp(limit) > 0 {
					return domain.FundingProviderAttempt{}, ErrProviderBudgetExceeded
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE provider_usage_budget SET request_count=request_count+1,
			reserved_cost=reserved_cost+$3::numeric,updated_at=now() WHERE provider_id=$1 AND period_month=$2`, providerID, month, maximumProviderCost); err != nil {
				return domain.FundingProviderAttempt{}, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO provider_budget_reservations(id,operation_id,provider_id,period_month,currency,maximum_cost)
			VALUES($1,$2,$3,$4,$5,$6)`, id.UUID(), operationID, providerID, month, settlementCurrency, maximumProviderCost); err != nil {
				return domain.FundingProviderAttempt{}, err
			}
		}
	}
	var attempt domain.FundingProviderAttempt
	err = tx.QueryRow(ctx, `INSERT INTO funding_provider_attempt(id,operation_id,attempt_no,provider_id,credential_id,credential_group_id,is_fallback,status)
		SELECT $1,$2,COALESCE(max(attempt_no),0)+1,$3,$4,$5,$6,'STARTED' FROM funding_provider_attempt WHERE operation_id=$2
		RETURNING id,operation_id,attempt_no,provider_id,credential_id,credential_group_id,is_fallback,status,started_at`,
		id.UUID(), operationID, providerID, nullString(credentialID), nullString(groupID), isFallback).Scan(&attempt.ID, &attempt.OperationID,
		&attempt.AttemptNo, &attempt.ProviderID, &attempt.CredentialID, &attempt.CredentialGroupID, &attempt.IsFallback, &attempt.Status, &attempt.StartedAt)
	if err != nil {
		return attempt, err
	}
	return attempt, tx.Commit(ctx)
}

func (s *Store) FinishFundingAttempt(ctx context.Context, attemptID, status string, httpStatus int, upstreamRequestID, errorCode string) error {
	if status != "SUCCEEDED" && status != "FAILED" && status != "CANCELLED" && status != "TIMED_OUT" {
		return errors.New("invalid funding provider attempt status")
	}
	var statusValue any
	if httpStatus > 0 {
		statusValue = httpStatus
	}
	tag, err := s.pool.Exec(ctx, `UPDATE funding_provider_attempt SET status=$2,http_status=$3,upstream_request_id=$4,error_code=$5,finished_at=now()
		WHERE id=$1 AND status='STARTED'`, attemptID, status, statusValue, nullString(upstreamRequestID), nullString(errorCode))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrFundingTerminal
	}
	return err
}

func (s *Store) RecoverStaleFunding(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,estimated_input_tokens,observed_output_bytes FROM funding_operation
		WHERE status IN ('PENDING','RESERVED') AND heartbeat_at<$1 ORDER BY heartbeat_at,id FOR UPDATE SKIP LOCKED LIMIT $2`, staleBefore, clamp(limit))
	if err != nil {
		return 0, err
	}
	type stale struct {
		id              string
		input, observed int64
	}
	items := make([]stale, 0)
	for rows.Next() {
		var item stale
		if err = rows.Scan(&item.id, &item.input, &item.observed); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range items {
		_, err = tx.Exec(ctx, `UPDATE funding_operation SET heartbeat_at=now(),updated_at=now() WHERE id=$1`, item.id)
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	recovered := 0
	for _, item := range items {
		output := item.observed / 4
		_, settleErr := s.SettleFunding(ctx, FundingSettlementRequest{OperationID: item.id, InputTokens: item.input,
			OutputTokens: output, ObservedBytes: item.observed, UsageSource: "ESTIMATED_CRASH_RECOVERY", FailureCode: "settlement_recovered",
			PartialFailure: item.observed > 0, SettlementSource: "recovery_worker"})
		if settleErr != nil && !errors.Is(settleErr, ErrFundingTerminal) {
			return recovered, settleErr
		}
		recovered++
	}
	return recovered, nil
}

func (s *Store) AdjustLateFundingUsage(ctx context.Context, request LateUsageRequest) (domain.FundingOperation, error) {
	if request.IdempotencyKey == "" || request.InputTokens < 0 || request.CachedInput < 0 || request.OutputTokens < 0 || request.CachedInput > request.InputTokens {
		return domain.FundingOperation{}, errors.New("invalid late usage adjustment")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	defer tx.Rollback(ctx)
	var operation domain.FundingOperation
	if err = scanFundingOperation(tx.QueryRow(ctx, fundingOperationSelect+` WHERE id=$1 FOR UPDATE`, request.OperationID), &operation); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.FundingOperation{}, ErrNotFound
		}
		return domain.FundingOperation{}, err
	}
	if operation.Status == "RESERVED" || operation.Status == "PENDING" {
		return domain.FundingOperation{}, ErrFundingInProgress
	}
	var existingDifference string
	err = tx.QueryRow(ctx, `SELECT difference_amount::text FROM funding_usage_adjustment WHERE operation_id=$1 AND idempotency_key=$2`,
		operation.ID, request.IdempotencyKey).Scan(&existingDifference)
	if err == nil {
		return operation, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.FundingOperation{}, err
	}
	corrected := "0"
	if operation.PricingVersionID != nil {
		corrected, err = fundingAmountForVersion(ctx, tx, *operation.PricingVersionID, request.InputTokens, request.CachedInput, request.OutputTokens,
			operation.PromotionAmount.String(), operation.TaxRate.String(), operation.ExchangeRate.String())
		if err != nil {
			return domain.FundingOperation{}, err
		}
	}
	previousRat, correctedRat := mustFundingRat(operation.SettledAmount.String()), mustFundingRat(corrected)
	difference := new(big.Rat).Sub(new(big.Rat).Set(correctedRat), previousRat)
	journalID := ""
	if difference.Sign() != 0 {
		absolute := new(big.Rat).Abs(new(big.Rat).Set(difference))
		lines := []journalLine{}
		if difference.Sign() > 0 {
			lines = append(lines,
				journalLine{walletAccountKey(operation.WalletID, "available"), "DEBIT", formatRat(absolute), "Late usage debit"},
				journalLine{systemAccountKey("revenue", operation.Currency), "CREDIT", formatRat(absolute), "Late usage revenue"})
		} else {
			lines = append(lines,
				journalLine{systemAccountKey("revenue", operation.Currency), "DEBIT", formatRat(absolute), "Reverse estimated revenue"},
				journalLine{walletAccountKey(operation.WalletID, "available"), "CREDIT", formatRat(absolute), "Late usage refund"})
		}
		journalID, err = postJournalTx(ctx, tx, operation.WalletID, "LATE_USAGE_ADJUSTMENT", "funding:"+operation.ID+":late:"+request.IdempotencyKey,
			operation.Currency, operation.RequestID, map[string]any{"funding_operation_id": operation.ID, "usage_source": request.UsageSource}, request.CreatedBy, lines)
		if err != nil {
			return domain.FundingOperation{}, err
		}
		var balanceAfter string
		err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance-$2::numeric,
			risk_exposure=GREATEST(risk_exposure+$2::numeric,0),status=CASE WHEN $2::numeric>0 AND
			(billing_mode='PREPAID' OR credit_enforced) AND GREATEST(risk_exposure+$2::numeric,0)>risk_limit THEN 'FROZEN' ELSE status END,
			version=version+1,updated_at=now() WHERE id=$1 RETURNING available_balance::text`, operation.WalletID, formatRat(difference)).Scan(&balanceAfter)
		if err != nil {
			return domain.FundingOperation{}, err
		}
		transactionType := "CHARGE"
		transactionAmount := formatRat(new(big.Rat).Neg(new(big.Rat).Set(difference)))
		if difference.Sign() < 0 {
			transactionType = "REFUND"
		}
		transactionID := id.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,reference,metadata,created_by,journal_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, transactionID, operation.WalletID, transactionType, transactionAmount,
			balanceAfter, "funding:"+operation.ID+":late:"+request.IdempotencyKey, operation.RequestID,
			jsonBytes(map[string]any{"funding_operation_id": operation.ID, "usage_source": request.UsageSource}), request.CreatedBy, journalID)
		if err != nil {
			return domain.FundingOperation{}, err
		}
		if difference.Sign() > 0 {
			if err = allocateCashDebitTx(ctx, tx, operation.WalletID, transactionID, formatRat(difference)); err != nil {
				return domain.FundingOperation{}, err
			}
		} else {
			var sourceTransactionID string
			if err = tx.QueryRow(ctx, `SELECT id FROM wallet_transactions WHERE funding_operation_id=$1 AND transaction_type='CHARGE' ORDER BY created_at,id LIMIT 1`, operation.ID).Scan(&sourceTransactionID); err != nil {
				return domain.FundingOperation{}, err
			}
			if err = restoreCashAllocationTx(ctx, tx, operation.WalletID, sourceTransactionID, transactionID, formatRat(absolute)); err != nil {
				return domain.FundingOperation{}, err
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO funding_usage_adjustment(id,operation_id,idempotency_key,input_tokens,cached_input_tokens,
		output_tokens,previous_amount,corrected_amount,difference_amount,usage_source,journal_id,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id.UUID(), operation.ID, request.IdempotencyKey,
		request.InputTokens, request.CachedInput, request.OutputTokens, operation.SettledAmount.String(), corrected, formatRat(difference),
		request.UsageSource, nullString(journalID), request.CreatedBy)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	status := "SETTLED"
	if correctedRat.Sign() == 0 {
		status = "RELEASED"
	}
	_, err = tx.Exec(ctx, `UPDATE funding_operation SET status=$2,settled_amount=$3,actual_input_tokens=$4,
		actual_cached_input_tokens=$5,actual_output_tokens=$6,usage_source=$7,settled_at=CASE WHEN $3::numeric>0 THEN now() ELSE settled_at END,
		updated_at=now() WHERE id=$1`, operation.ID, status, corrected, request.InputTokens, request.CachedInput, request.OutputTokens, request.UsageSource)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	usageStatus := "CHARGED"
	if correctedRat.Sign() == 0 {
		usageStatus = "WAIVED"
	}
	_, err = tx.Exec(ctx, `UPDATE billing_usage_records SET status=$2 WHERE funding_operation_id=$1`, operation.ID, usageStatus)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if err = insertFundingEvent(ctx, tx, operation.ID, status, formatRat(new(big.Rat).Abs(new(big.Rat).Set(difference))), "late_usage", map[string]any{
		"previous_amount": operation.SettledAmount.String(), "corrected_amount": corrected, "difference_amount": formatRat(difference), "usage_source": request.UsageSource,
	}); err != nil {
		return domain.FundingOperation{}, err
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "wallet.late_usage_adjusted", "funding_operation", operation.ID, map[string]any{
		"previous_amount": operation.SettledAmount.String(), "corrected_amount": corrected, "difference_amount": formatRat(difference), "journal_id": journalID,
	}); err != nil {
		return domain.FundingOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.FundingOperation{}, err
	}
	return s.FundingOperationByID(ctx, operation.ID)
}

func (s *Store) ReverseFunding(ctx context.Context, operationID, idempotencyKey, reason string, actor *string) (domain.FundingOperation, error) {
	if idempotencyKey == "" || strings.TrimSpace(reason) == "" {
		return domain.FundingOperation{}, errors.New("idempotency key and reversal reason are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	defer tx.Rollback(ctx)
	var operation domain.FundingOperation
	if err = scanFundingOperation(tx.QueryRow(ctx, fundingOperationSelect+` WHERE id=$1 FOR UPDATE`, operationID), &operation); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.FundingOperation{}, ErrNotFound
		}
		return domain.FundingOperation{}, err
	}
	if operation.Status == "REVERSED" {
		return operation, tx.Commit(ctx)
	}
	settledPositive, decimalErr := operation.SettledAmount.IsPositive()
	if decimalErr != nil {
		return domain.FundingOperation{}, decimalErr
	}
	if !settledPositive {
		return domain.FundingOperation{}, ErrFundingTerminal
	}
	amount := operation.SettledAmount.String()
	journalID, err := postJournalTx(ctx, tx, operation.WalletID, "REVERSAL", "funding:"+operation.ID+":reversal:"+idempotencyKey,
		operation.Currency, reason, map[string]any{"funding_operation_id": operation.ID, "reason": reason}, actor, []journalLine{
			{systemAccountKey("revenue", operation.Currency), "DEBIT", amount, "Reverse usage revenue"},
			{walletAccountKey(operation.WalletID, "available"), "CREDIT", amount, "Refund settled usage"},
		})
	if err != nil {
		return domain.FundingOperation{}, err
	}
	var balanceAfter string
	err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance+$2::numeric,
		version=version+1,updated_at=now() WHERE id=$1 RETURNING available_balance::text`,
		operation.WalletID, amount).Scan(&balanceAfter)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	transactionID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,reference,metadata,created_by,journal_id)
		VALUES($1,$2,'REFUND',$3,$4,$5,$6,$7,$8,$9)`, transactionID, operation.WalletID, amount, balanceAfter,
		"funding:"+operation.ID+":reversal:"+idempotencyKey, reason, jsonBytes(map[string]any{"funding_operation_id": operation.ID}), actor, journalID)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	var sourceTransactionID string
	if err = tx.QueryRow(ctx, `SELECT id FROM wallet_transactions WHERE funding_operation_id=$1 AND transaction_type='CHARGE' ORDER BY created_at,id LIMIT 1`, operation.ID).Scan(&sourceTransactionID); err != nil {
		return domain.FundingOperation{}, err
	}
	if err = restoreCashAllocationTx(ctx, tx, operation.WalletID, sourceTransactionID, transactionID, amount); err != nil {
		return domain.FundingOperation{}, err
	}
	reversedPromotion, err := reversePromotionTx(ctx, tx, operation.ID, operation.ConsumedPromotionAmount.String())
	if err != nil {
		return domain.FundingOperation{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE funding_operation SET status='REVERSED',consumed_promotion_amount=GREATEST(consumed_promotion_amount-$2::numeric,0),updated_at=now() WHERE id=$1`, operation.ID, reversedPromotion)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if err = insertFundingEvent(ctx, tx, operation.ID, "REVERSED", amount, "admin_reversal", map[string]any{"reason": reason, "journal_id": journalID, "promotion_reversed": reversedPromotion}); err != nil {
		return domain.FundingOperation{}, err
	}
	if err = writeAuditTx(ctx, tx, actor, "wallet.funding_reversed", "funding_operation", operation.ID, map[string]any{"reason": reason, "amount": amount, "journal_id": journalID}); err != nil {
		return domain.FundingOperation{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE billing_usage_records SET status='REFUNDED' WHERE funding_operation_id=$1`, operation.ID)
	if err != nil {
		return domain.FundingOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.FundingOperation{}, err
	}
	return s.FundingOperationByID(ctx, operation.ID)
}

const fundingOperationSelect = `SELECT id,wallet_id,organization_id,project_id,api_key_id,request_id,idempotency_key,
	request_fingerprint,pricing_version_id,status,currency,maximum_amount::text,promotion_amount::text,consumed_promotion_amount::text,tax_rate::text,exchange_rate::text,settled_amount::text,released_amount::text,
	estimated_input_tokens,max_output_tokens,actual_input_tokens,actual_cached_input_tokens,actual_output_tokens,
	COALESCE(usage_source,''),observed_output_bytes,COALESCE(failure_code,''),reserved_at,settled_at,released_at,
	heartbeat_at,created_at,updated_at,credential_owner,provider_credential_id,platform_service_fee::text FROM funding_operation`

func scanFundingOperation(row pgx.Row, operation *domain.FundingOperation) error {
	var maximum, promotion, consumedPromotion, taxRate, exchangeRate, settled, released string
	err := row.Scan(&operation.ID, &operation.WalletID, &operation.OrganizationID, &operation.ProjectID, &operation.APIKeyID,
		&operation.RequestID, &operation.IdempotencyKey, &operation.RequestFingerprint, &operation.PricingVersionID, &operation.Status,
		&operation.Currency, &maximum, &promotion, &consumedPromotion, &taxRate, &exchangeRate, &settled, &released, &operation.EstimatedInputTokens, &operation.MaxOutputTokens,
		&operation.ActualInputTokens, &operation.ActualCachedInputTokens, &operation.ActualOutputTokens, &operation.UsageSource,
		&operation.ObservedOutputBytes, &operation.FailureCode, &operation.ReservedAt, &operation.SettledAt, &operation.ReleasedAt,
		&operation.HeartbeatAt, &operation.CreatedAt, &operation.UpdatedAt, &operation.CredentialOwner, &operation.ProviderCredentialID, &operation.PlatformServiceFee)
	if err != nil {
		return err
	}
	fields := []*domain.Decimal{&operation.MaximumAmount, &operation.PromotionAmount, &operation.ConsumedPromotionAmount, &operation.TaxRate, &operation.ExchangeRate, &operation.SettledAmount, &operation.ReleasedAmount}
	for index, raw := range []string{maximum, promotion, consumedPromotion, taxRate, exchangeRate, settled, released} {
		parsed, parseErr := domain.ParseDecimal(raw)
		if parseErr != nil {
			return parseErr
		}
		*fields[index] = parsed
	}
	return nil
}

func (s *Store) FundingOperationByID(ctx context.Context, operationID string) (domain.FundingOperation, error) {
	var operation domain.FundingOperation
	err := scanFundingOperation(s.pool.QueryRow(ctx, fundingOperationSelect+` WHERE id=$1`, operationID), &operation)
	if errors.Is(err, pgx.ErrNoRows) {
		return operation, ErrNotFound
	}
	return operation, err
}

func (s *Store) FundingOperationByRequestID(ctx context.Context, requestID string) (domain.FundingOperation, error) {
	var operation domain.FundingOperation
	err := scanFundingOperation(s.pool.QueryRow(ctx, fundingOperationSelect+` WHERE request_id=$1`, requestID), &operation)
	if errors.Is(err, pgx.ErrNoRows) {
		return operation, ErrNotFound
	}
	return operation, err
}

func (s *Store) ListFundingOperations(ctx context.Context, walletID string, limit, offset int) ([]domain.FundingOperation, error) {
	rows, err := s.pool.Query(ctx, fundingOperationSelect+` WHERE wallet_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, walletID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.FundingOperation, 0)
	for rows.Next() {
		var operation domain.FundingOperation
		if err = scanFundingOperation(rows, &operation); err != nil {
			return nil, err
		}
		out = append(out, operation)
	}
	return out, rows.Err()
}

func (s *Store) ListJournals(ctx context.Context, walletID string, limit, offset int) ([]domain.Journal, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,wallet_id,journal_type,external_key,currency,status,COALESCE(reference,''),metadata,created_by,posted_at,created_at
		FROM ledger_journal WHERE wallet_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, walletID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Journal, 0)
	for rows.Next() {
		var journal domain.Journal
		if err = rows.Scan(&journal.ID, &journal.WalletID, &journal.JournalType, &journal.ExternalKey, &journal.Currency,
			&journal.Status, &journal.Reference, &journal.Metadata, &journal.CreatedBy, &journal.PostedAt, &journal.CreatedAt); err != nil {
			return nil, err
		}
		entryRows, queryErr := s.pool.Query(ctx, `SELECT e.id,e.journal_id,e.account_id,a.account_key,a.name,e.currency,e.entry_side,e.amount::text,e.description,e.created_at
			FROM ledger_journal_entry e JOIN ledger_account a ON a.id=e.account_id WHERE e.journal_id=$1 ORDER BY e.id`, journal.ID)
		if queryErr != nil {
			return nil, queryErr
		}
		for entryRows.Next() {
			var entry domain.JournalEntry
			var amount string
			if queryErr = entryRows.Scan(&entry.ID, &entry.JournalID, &entry.AccountID, &entry.AccountKey, &entry.AccountName,
				&entry.Currency, &entry.EntrySide, &amount, &entry.Description, &entry.CreatedAt); queryErr != nil {
				entryRows.Close()
				return nil, queryErr
			}
			entry.Amount = domain.Decimal(amount)
			journal.Entries = append(journal.Entries, entry)
		}
		if queryErr = entryRows.Err(); queryErr != nil {
			entryRows.Close()
			return nil, queryErr
		}
		entryRows.Close()
		out = append(out, journal)
	}
	return out, rows.Err()
}

func postJournalTx(ctx context.Context, tx pgx.Tx, walletID, journalType, externalKey, currency, reference string, metadata map[string]any, actor *string, lines []journalLine) (string, error) {
	return postLinkedJournalTx(ctx, tx, walletID, journalType, externalKey, currency, reference, metadata, actor, nil, nil, lines)
}

func postLinkedJournalTx(ctx context.Context, tx pgx.Tx, walletID, journalType, externalKey, currency, reference string, metadata map[string]any, actor, rechargeOrderID, refundOrderID *string, lines []journalLine) (string, error) {
	if len(lines) < 2 {
		return "", errors.New("a journal requires at least two entries")
	}
	journalID := id.UUID()
	_, err := tx.Exec(ctx, `INSERT INTO ledger_journal(id,wallet_id,journal_type,external_key,currency,reference,metadata,created_by,recharge_order_id,refund_order_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, journalID, nullString(walletID), journalType, externalKey, currency, nullString(reference), jsonBytes(metadata), actor, rechargeOrderID, refundOrderID)
	if err != nil {
		return "", err
	}
	for _, line := range lines {
		if !decimalPositive(line.amount) {
			return "", errors.New("journal amounts must be positive")
		}
		var accountID string
		if err = tx.QueryRow(ctx, `SELECT id FROM ledger_account WHERE account_key=$1 AND currency=$2 AND status='ACTIVE'`, line.accountKey, currency).Scan(&accountID); err != nil {
			return "", fmt.Errorf("resolve journal account %s: %w", line.accountKey, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger_journal_entry(id,journal_id,account_id,currency,entry_side,amount,description)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), journalID, accountID, currency, line.side, line.amount, line.description)
		if err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ledger_journal SET status='POSTED',posted_at=now() WHERE id=$1`, journalID)
	return journalID, err
}

func fundingAmountsForVersion(ctx context.Context, tx pgx.Tx, versionID string, input, cached, output int64, promotion, taxRate, exchangeRate string) (string, string, error) {
	var inputPrice, cachedPrice, outputPrice, fixedPrice, currency string
	var unit int64
	err := tx.QueryRow(ctx, `SELECT retail_input_token_price::text,retail_cached_input_token_price::text,
		retail_output_token_price::text,retail_request_fixed_price::text,retail_currency,retail_unit
		FROM model_price_version WHERE id=$1 FOR SHARE`, versionID).Scan(&inputPrice, &cachedPrice, &outputPrice, &fixedPrice, &currency, &unit)
	if err != nil {
		return "", "", err
	}
	result, err := pricing.Calculate(pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: unit, Currency: currency},
		pricing.Rate{Input: inputPrice, Cached: cachedPrice, Output: outputPrice, Fixed: fixedPrice, Unit: unit, Currency: currency},
		pricing.Tokens{Input: input, Cached: cached, Output: output}, promotion, taxRate, exchangeRate)
	if err != nil {
		return "", "", err
	}
	return result.FinalAmount, result.PromotionAmount, nil
}

func fundingAmountForVersion(ctx context.Context, tx pgx.Tx, versionID string, input, cached, output int64, promotion, taxRate, exchangeRate string) (string, error) {
	amount, _, err := fundingAmountsForVersion(ctx, tx, versionID, input, cached, output, promotion, taxRate, exchangeRate)
	return amount, err
}

func insertFundingEvent(ctx context.Context, tx pgx.Tx, operationID, status, amount, source string, metadata map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO funding_operation_event(id,operation_id,status,amount,source,metadata) VALUES($1,$2,$3,$4,$5,$6)`,
		id.UUID(), operationID, status, zeroIfEmpty(amount), source, jsonBytes(metadata))
	return err
}

func walletAccountKey(walletID, kind string) string { return "wallet:" + walletID + ":" + kind }
func systemAccountKey(kind, currency string) string { return "system:" + kind + ":" + currency }
func fundingRat(value string) (*big.Rat, error) {
	decimal, err := domain.ParseDecimal(value)
	if err != nil {
		return nil, err
	}
	result, ok := new(big.Rat).SetString(decimal.String())
	if !ok {
		return nil, errors.New("invalid funding decimal")
	}
	return result, nil
}
func mustFundingRat(value string) *big.Rat {
	result, err := fundingRat(zeroIfEmpty(value))
	if err != nil {
		panic(err)
	}
	return result
}
func isFundingTerminal(status string) bool {
	return status == "SETTLED" || status == "PARTIALLY_SETTLED" || status == "RELEASED" || status == "FAILED" || status == "REVERSED"
}
func firstNonEmptyStore(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func providerBudgetMonth(now time.Time) string {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}
