package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrMarketplaceLaunchState     = errors.New("marketplace launch review state does not allow this operation")
	ErrMarketplaceGateEvidence    = errors.New("marketplace gate evidence is invalid")
	ErrMarketplacePayoutReadiness = errors.New("supplier production payout readiness is incomplete")
)

const marketplacePolicyVersion = "marketplace-launch-2026-08-21"

var marketplaceGateCodes = []string{
	"SUPPLIER_REGISTRATION", "QUALIFICATION_REVIEW", "ENDPOINT_VERIFICATION", "MODEL_PUBLICATION",
	"PRICE_APPROVAL", "HEALTH_TEST", "CANARY_RAMP", "ROUTING_RANKING", "USER_INVOCATION", "USER_CHARGE",
	"PLATFORM_COMMISSION", "SUPPLIER_PAYABLE", "REFUND_ALLOCATION", "SETTLEMENT", "RECONCILIATION", "DISPUTE",
	"SUPPLIER_SUSPENSION_DRILL", "EMERGENCY_CUTOVER_DRILL", "SUPPLIER_EXIT_DRILL",
	"CONTRACT_REVIEW", "TAX_REVIEW", "PAYMENT_REVIEW", "SECURITY_REVIEW",
}

var marketplaceManualGates = map[string]bool{
	"SUPPLIER_SUSPENSION_DRILL": true,
	"EMERGENCY_CUTOVER_DRILL":   true,
	"SUPPLIER_EXIT_DRILL":       true,
}

func marketplaceFingerprint(listingID, policyVersion, listingFingerprint string) string {
	sum := sha256.Sum256([]byte(listingID + "\x00" + policyVersion + "\x00" + listingFingerprint))
	return hex.EncodeToString(sum[:])
}

const marketplaceLaunchReviewColumns = `review.id,review.listing_id,review.provider_id,provider.name,review.supplier_id,
	supplier.display_name,review.revision,review.policy_version,review.listing_fingerprint_sha256,review.status,review.reason,review.created_by,
	review.approved_by,review.approved_at,review.revoked_by,review.revoked_at,review.created_at,review.updated_at`

func scanMarketplaceLaunchReview(row pgx.Row) (domain.MarketplaceLaunchReview, error) {
	var review domain.MarketplaceLaunchReview
	err := row.Scan(&review.ID, &review.ListingID, &review.ProviderID, &review.ProviderName, &review.SupplierID,
		&review.SupplierName, &review.Revision, &review.PolicyVersion, &review.ListingFingerprintSHA256, &review.Status, &review.Reason, &review.CreatedBy,
		&review.ApprovedBy, &review.ApprovedAt, &review.RevokedBy, &review.RevokedAt, &review.CreatedAt, &review.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return review, ErrNotFound
	}
	return review, err
}

func (s *Store) MarketplaceLaunchReviewByID(ctx context.Context, reviewID string) (domain.MarketplaceLaunchReview, error) {
	review, err := scanMarketplaceLaunchReview(s.pool.QueryRow(ctx, `SELECT `+marketplaceLaunchReviewColumns+`
		FROM marketplace_launch_review review JOIN providers provider ON provider.id=review.provider_id
		JOIN supplier_organizations supplier ON supplier.id=review.supplier_id WHERE review.id=$1`, reviewID))
	if err != nil {
		return review, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,review_id,gate_code,evidence_source,status,evidence_reference,evidence,
		evaluated_by,evaluated_at,updated_at FROM marketplace_launch_gate WHERE review_id=$1 ORDER BY gate_code`, reviewID)
	if err != nil {
		return review, err
	}
	defer rows.Close()
	review.Gates = make([]domain.MarketplaceLaunchGate, 0, len(marketplaceGateCodes))
	for rows.Next() {
		var gate domain.MarketplaceLaunchGate
		var evidence []byte
		if err = rows.Scan(&gate.ID, &gate.ReviewID, &gate.GateCode, &gate.EvidenceSource, &gate.Status,
			&gate.EvidenceReference, &evidence, &gate.EvaluatedBy, &gate.EvaluatedAt, &gate.UpdatedAt); err != nil {
			return review, err
		}
		_ = json.Unmarshal(evidence, &gate.Evidence)
		if gate.Evidence == nil {
			gate.Evidence = map[string]any{}
		}
		if gate.Status == "PASSED" {
			review.PassedGateCount++
		}
		review.Gates = append(review.Gates, gate)
	}
	review.GateCount = len(review.Gates)
	return review, rows.Err()
}

func (s *Store) ListMarketplaceLaunchReviews(ctx context.Context, listingID, status string, limit, offset int) ([]domain.MarketplaceLaunchReview, error) {
	rows, err := s.pool.Query(ctx, `SELECT review.id FROM marketplace_launch_review review
		WHERE ($1='' OR review.listing_id=$1::uuid) AND ($2='' OR review.status=$2)
		ORDER BY review.created_at DESC,review.id LIMIT $3 OFFSET $4`, strings.TrimSpace(listingID),
		strings.ToUpper(strings.TrimSpace(status)), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var reviewID string
		if err = rows.Scan(&reviewID); err != nil {
			return nil, err
		}
		ids = append(ids, reviewID)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.MarketplaceLaunchReview, 0, len(ids))
	for _, reviewID := range ids {
		review, loadErr := s.MarketplaceLaunchReviewByID(ctx, reviewID)
		if loadErr != nil {
			return nil, loadErr
		}
		out = append(out, review)
	}
	return out, nil
}

func (s *Store) CreateMarketplaceLaunchReview(ctx context.Context, listingID, idempotencyKey, policyVersion, actor string) (domain.MarketplaceLaunchReview, bool, error) {
	idempotencyKey, actor = strings.TrimSpace(idempotencyKey), strings.TrimSpace(actor)
	if policyVersion = strings.TrimSpace(policyVersion); policyVersion == "" {
		policyVersion = marketplacePolicyVersion
	}
	if listingID == "" || actor == "" || idempotencyKey == "" || len(idempotencyKey) > 200 || len(policyVersion) > 100 {
		return domain.MarketplaceLaunchReview{}, false, ErrMarketplaceLaunchState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "marketplace-launch:"+idempotencyKey); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	var providerID, supplierID, listingStatus, listingFingerprint string
	err = tx.QueryRow(ctx, `SELECT listing.provider_id,link.supplier_id,listing.status,
		marketplace_listing_release_fingerprint(listing.provider_id,listing.endpoint,listing.supported_models,listing.price,listing.metadata)
		FROM provider_marketplace_listings listing JOIN supplier_provider_links link ON link.provider_id=listing.provider_id
		WHERE listing.id=$1 AND link.status='ACTIVE' FOR UPDATE OF listing,link`, listingID).Scan(&providerID, &supplierID, &listingStatus, &listingFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MarketplaceLaunchReview{}, false, ErrMarketplaceLaunchState
	}
	if err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	fingerprint := marketplaceFingerprint(listingID, policyVersion, listingFingerprint)
	var existingID, existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,request_fingerprint FROM marketplace_launch_review WHERE idempotency_key=$1 FOR SHARE`, idempotencyKey).Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return domain.MarketplaceLaunchReview{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.MarketplaceLaunchReview{}, false, err
		}
		review, loadErr := s.MarketplaceLaunchReviewByID(ctx, existingID)
		return review, true, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	if listingStatus == "EXITED" || listingStatus == "REJECTED" {
		return domain.MarketplaceLaunchReview{}, false, ErrMarketplaceLaunchState
	}
	var openReview bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM marketplace_launch_review WHERE listing_id=$1 AND status IN ('DRAFT','IN_REVIEW'))`, listingID).Scan(&openReview); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	if openReview {
		return domain.MarketplaceLaunchReview{}, false, ErrMarketplaceLaunchState
	}
	var revision int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM marketplace_launch_review WHERE listing_id=$1`, listingID).Scan(&revision); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	reviewID := id.UUID()
	if _, err = tx.Exec(ctx, `UPDATE marketplace_launch_review SET status='REVOKED',revoked_by=$2,revoked_at=now(),
		reason='Superseded by a newer Marketplace release revision',updated_at=now() WHERE listing_id=$1 AND status='APPROVED'`, listingID, actor); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO marketplace_launch_review(id,idempotency_key,request_fingerprint,listing_id,provider_id,
		supplier_id,revision,policy_version,listing_fingerprint_sha256,status,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'IN_REVIEW',$10)`,
		reviewID, idempotencyKey, fingerprint, listingID, providerID, supplierID, revision, policyVersion, listingFingerprint, actor)
	if err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	for _, gateCode := range marketplaceGateCodes {
		source := "PLATFORM_AUTOMATED"
		if marketplaceManualGates[gateCode] {
			source = "ADMIN_ATTESTATION"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO marketplace_launch_gate(id,review_id,gate_code,evidence_source) VALUES($1,$2,$3,$4)`, id.UUID(), reviewID, gateCode, source); err != nil {
			return domain.MarketplaceLaunchReview{}, false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='REVIEW',updated_at=now() WHERE id=$1 AND status NOT IN ('SUSPENDED','EXITED')`, listingID); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "marketplace.launch_review_created", "marketplace_launch_review", reviewID,
		map[string]any{"listing_id": listingID, "provider_id": providerID, "supplier_id": supplierID, "revision": revision, "policy_version": policyVersion}); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MarketplaceLaunchReview{}, false, err
	}
	review, err := s.MarketplaceLaunchReviewByID(ctx, reviewID)
	return review, false, err
}

type marketplaceReviewSubject struct {
	ReviewID   string
	ListingID  string
	ProviderID string
	SupplierID string
	Status     string
}

func marketplaceGateQuery(ctx context.Context, tx pgx.Tx, query string, args ...any) (bool, string, error) {
	var passed bool
	var reference string
	err := tx.QueryRow(ctx, query, args...).Scan(&passed, &reference)
	return passed, reference, err
}

func evaluateMarketplaceAutomaticGate(ctx context.Context, tx pgx.Tx, subject marketplaceReviewSubject, gateCode string) (bool, string, map[string]any, error) {
	var passed bool
	var reference string
	var err error
	switch gateCode {
	case "SUPPLIER_REGISTRATION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_organizations
			WHERE id=$1 AND legal_name<>'' AND registration_number<>'' AND incorporation_country<>'') ,$1::text`, subject.SupplierID)
	case "QUALIFICATION_REVIEW":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_organizations
			WHERE id=$1 AND status='APPROVED' AND kyb_status='VERIFIED'),$1::text`, subject.SupplierID)
	case "ENDPOINT_VERIFICATION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_endpoints endpoint
			JOIN provider_marketplace_listings listing ON listing.id=$2
			WHERE endpoint.supplier_id=$1 AND lower(rtrim(endpoint.endpoint_url,'/'))=lower(rtrim(listing.endpoint,'/'))
			AND endpoint.verification_status='VERIFIED' AND endpoint.isolation_status='PASSED'),
			COALESCE((SELECT endpoint.id::text FROM supplier_endpoints endpoint JOIN provider_marketplace_listings listing ON listing.id=$2
			WHERE endpoint.supplier_id=$1 AND lower(rtrim(endpoint.endpoint_url,'/'))=lower(rtrim(listing.endpoint,'/'))
			AND endpoint.verification_status='VERIFIED' AND endpoint.isolation_status='PASSED' LIMIT 1),'')`, subject.SupplierID, subject.ListingID)
	case "MODEL_PUBLICATION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT
			jsonb_array_length(listing.supported_models)>0 AND NOT EXISTS(
				SELECT 1 FROM jsonb_array_elements_text(listing.supported_models) declared(model_name)
				WHERE NOT EXISTS(SELECT 1 FROM supplier_model_applications app JOIN models model
					ON model.provider_id=$2 AND lower(model.provider_model_id)=lower(app.model_name) AND model.enabled
					WHERE app.supplier_id=$1 AND lower(app.model_name)=lower(declared.model_name) AND app.status='APPROVED')),
			listing.id::text FROM provider_marketplace_listings listing WHERE listing.id=$3`, subject.SupplierID, subject.ProviderID, subject.ListingID)
	case "PRICE_APPROVAL":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT jsonb_array_length(listing.supported_models)>0 AND NOT EXISTS(
			SELECT 1 FROM jsonb_array_elements_text(listing.supported_models) declared(model_name) WHERE NOT EXISTS(
				SELECT 1 FROM supplier_model_applications app
				JOIN supplier_price_applications price ON price.model_application_id=app.id AND price.status='APPROVED'
				JOIN models model ON model.provider_id=$2 AND lower(model.provider_model_id)=lower(app.model_name) AND model.enabled
				JOIN provider_cost_price_book book ON book.provider_id=$2 AND book.model_id=model.id
					AND book.approval_status IN ('APPROVED','FORCED_APPROVED') AND book.effective_at<=now() AND (book.expires_at IS NULL OR book.expires_at>now())
				JOIN provider_price_verifications verification ON verification.provider_id=$2 AND verification.model_id=model.id AND verification.result='MATCH'
					AND verification.observed_at<=now() AND (verification.expires_at IS NULL OR verification.expires_at>now())
				WHERE app.supplier_id=$1 AND lower(app.model_name)=lower(declared.model_name))),
			COALESCE((SELECT verification.id::text FROM provider_price_verifications verification
			WHERE verification.provider_id=$2 AND verification.result='MATCH' AND (verification.expires_at IS NULL OR verification.expires_at>now())
			ORDER BY verification.observed_at DESC LIMIT 1),'') FROM provider_marketplace_listings listing WHERE listing.id=$3`, subject.SupplierID, subject.ProviderID, subject.ListingID)
	case "HEALTH_TEST":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT policy.enabled AND state.circuit_state='CLOSED'
			AND state.grade IN ('A','B','C') AND state.measurement_count>=policy.minimum_samples
			AND state.last_evaluated_at>=now()-make_interval(mins=>policy.evaluation_window_minutes*2),state.provider_id::text
			FROM provider_quality_policies policy JOIN provider_quality_states state ON state.provider_id=policy.provider_id WHERE policy.provider_id=$1`, subject.ProviderID)
	case "CANARY_RAMP":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM marketplace_provider_lifecycle_event event
			WHERE event.listing_id=$1 AND event.action='CANARY_START'),COALESCE((SELECT event.id::text FROM marketplace_provider_lifecycle_event event
			WHERE event.listing_id=$1 AND event.action='CANARY_START' ORDER BY event.created_at DESC LIMIT 1),'')`, subject.ListingID)
	case "ROUTING_RANKING":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM provider_quality_states state
			JOIN model_routes route ON route.provider_id=state.provider_id AND route.enabled
			JOIN project_model_routes project_route ON project_route.model_route_id=route.id AND project_route.enabled AND project_route.deleted_at IS NULL
			WHERE state.provider_id=$1 AND state.grade IN ('A','B','C') AND state.routing_multiplier>0),$1::text`, subject.ProviderID)
	case "USER_INVOCATION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM request_logs request JOIN marketplace_launch_review review ON review.id=$2
			WHERE request.provider_id=$1 AND request.status_code BETWEEN 200 AND 299 AND request.created_at>=review.created_at),
			COALESCE((SELECT request.request_id FROM request_logs request JOIN marketplace_launch_review review ON review.id=$2
			WHERE request.provider_id=$1 AND request.status_code BETWEEN 200 AND 299 AND request.created_at>=review.created_at ORDER BY request.created_at DESC LIMIT 1),'')`, subject.ProviderID, subject.ReviewID)
	case "USER_CHARGE":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM billing_usage_records usage JOIN marketplace_launch_review review ON review.id=$2
			WHERE usage.provider_id=$1 AND usage.status='CHARGED' AND usage.created_at>=review.created_at),
			COALESCE((SELECT usage.id::text FROM billing_usage_records usage JOIN marketplace_launch_review review ON review.id=$2
			WHERE usage.provider_id=$1 AND usage.status='CHARGED' AND usage.created_at>=review.created_at ORDER BY usage.created_at DESC LIMIT 1),'')`, subject.ProviderID, subject.ReviewID)
	case "PLATFORM_COMMISSION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_payable_accrual WHERE supplier_id=$1 AND provider_id=$2 AND commission_amount>0),
			COALESCE((SELECT id::text FROM supplier_payable_accrual WHERE supplier_id=$1 AND provider_id=$2 AND commission_amount>0 ORDER BY created_at DESC LIMIT 1),'')`, subject.SupplierID, subject.ProviderID)
	case "SUPPLIER_PAYABLE":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_payable_accrual WHERE supplier_id=$1 AND provider_id=$2 AND initial_payable_amount>0),
			COALESCE((SELECT id::text FROM supplier_payable_accrual WHERE supplier_id=$1 AND provider_id=$2 AND initial_payable_amount>0 ORDER BY created_at DESC LIMIT 1),'')`, subject.SupplierID, subject.ProviderID)
	case "REFUND_ALLOCATION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_payable_entry WHERE supplier_id=$1 AND provider_id=$2 AND entry_type='REFUND_SHARE'),
			COALESCE((SELECT id::text FROM supplier_payable_entry WHERE supplier_id=$1 AND provider_id=$2 AND entry_type='REFUND_SHARE' ORDER BY created_at DESC LIMIT 1),'')`, subject.SupplierID, subject.ProviderID)
	case "SETTLEMENT":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_settlement_batch WHERE supplier_id=$1 AND provider_id=$2 AND status='PAID'),
			COALESCE((SELECT id::text FROM supplier_settlement_batch WHERE supplier_id=$1 AND provider_id=$2 AND status='PAID' ORDER BY paid_at DESC LIMIT 1),'')`, subject.SupplierID, subject.ProviderID)
	case "RECONCILIATION":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_bill WHERE supplier_id=$1 AND provider_id=$2 AND status='RECONCILED'),
			COALESCE((SELECT id::text FROM supplier_bill WHERE supplier_id=$1 AND provider_id=$2 AND status='RECONCILED' ORDER BY declared_at DESC LIMIT 1),'')`, subject.SupplierID, subject.ProviderID)
	case "DISPUTE":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT EXISTS(SELECT 1 FROM supplier_appeal WHERE supplier_id=$1 AND status IN ('UPHELD','REJECTED','WITHDRAWN')),
			COALESCE((SELECT id::text FROM supplier_appeal WHERE supplier_id=$1 AND status IN ('UPHELD','REJECTED','WITHDRAWN') ORDER BY updated_at DESC LIMIT 1),'')`, subject.SupplierID)
	case "CONTRACT_REVIEW":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT readiness.contract_status='APPROVED'
			AND supplier.contract_status='ACTIVE' AND supplier.kyb_status='VERIFIED' AND supplier.contract_start_at IS NOT NULL
			AND supplier.contract_start_at<=now() AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now()),
			readiness.contract_evidence_reference FROM supplier_payout_readiness_review readiness
			JOIN supplier_organizations supplier ON supplier.id=readiness.supplier_id WHERE readiness.supplier_id=$1`, subject.SupplierID)
	case "TAX_REVIEW":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT readiness.tax_status='APPROVED'
			AND supplier.tax_id<>'' AND supplier.tax_country<>'' AND supplier.tax_form_type<>'',readiness.tax_evidence_reference
			FROM supplier_payout_readiness_review readiness JOIN supplier_organizations supplier ON supplier.id=readiness.supplier_id
			WHERE readiness.supplier_id=$1`, subject.SupplierID)
	case "PAYMENT_REVIEW":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT readiness.payment_status='APPROVED'
			AND supplier.payout_account_encrypted IS NOT NULL,readiness.payment_evidence_reference
			FROM supplier_payout_readiness_review readiness JOIN supplier_organizations supplier ON supplier.id=readiness.supplier_id
			WHERE readiness.supplier_id=$1`, subject.SupplierID)
	case "SECURITY_REVIEW":
		passed, reference, err = marketplaceGateQuery(ctx, tx, `SELECT readiness.security_status='APPROVED' AND EXISTS(
			SELECT 1 FROM supplier_security_questionnaires questionnaire WHERE questionnaire.supplier_id=readiness.supplier_id AND questionnaire.status='APPROVED'),
			readiness.security_evidence_reference FROM supplier_payout_readiness_review readiness WHERE readiness.supplier_id=$1`, subject.SupplierID)
	default:
		return false, "", nil, ErrMarketplaceGateEvidence
	}
	if errors.Is(err, pgx.ErrNoRows) {
		passed, reference, err = false, "", nil
	}
	if reference == "" {
		reference = "platform:none:" + strings.ToLower(gateCode)
	}
	return passed, reference, map[string]any{"source": "PLATFORM_DATABASE", "gate_code": gateCode, "passed": passed}, err
}

func (s *Store) EvaluateMarketplaceLaunchReview(ctx context.Context, reviewID, actor string) (domain.MarketplaceLaunchReview, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	defer tx.Rollback(ctx)
	var subject marketplaceReviewSubject
	err = tx.QueryRow(ctx, `SELECT id,listing_id,provider_id,supplier_id,status FROM marketplace_launch_review WHERE id=$1 FOR UPDATE`, reviewID).
		Scan(&subject.ReviewID, &subject.ListingID, &subject.ProviderID, &subject.SupplierID, &subject.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MarketplaceLaunchReview{}, ErrNotFound
	}
	if err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if subject.Status != "IN_REVIEW" && subject.Status != "DRAFT" {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	for _, gateCode := range marketplaceGateCodes {
		if marketplaceManualGates[gateCode] {
			continue
		}
		passed, reference, evidence, evalErr := evaluateMarketplaceAutomaticGate(ctx, tx, subject, gateCode)
		if evalErr != nil {
			return domain.MarketplaceLaunchReview{}, fmt.Errorf("evaluate Marketplace gate %s: %w", gateCode, evalErr)
		}
		status := "FAILED"
		if passed {
			status = "PASSED"
		}
		var gateID, fromStatus string
		if err = tx.QueryRow(ctx, `SELECT id,status FROM marketplace_launch_gate WHERE review_id=$1 AND gate_code=$2 AND evidence_source='PLATFORM_AUTOMATED' FOR UPDATE`, reviewID, gateCode).Scan(&gateID, &fromStatus); err != nil {
			return domain.MarketplaceLaunchReview{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE marketplace_launch_gate SET status=$2,evidence_reference=$3,evidence=$4,evaluated_by=$5,
			evaluated_at=now(),updated_at=now() WHERE id=$1`, gateID, status, reference, jsonBytes(evidence), actor); err != nil {
			return domain.MarketplaceLaunchReview{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO marketplace_launch_gate_event(id,gate_id,from_status,to_status,evidence_reference,evidence,actor_id)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), gateID, fromStatus, status, reference, jsonBytes(evidence), actor); err != nil {
			return domain.MarketplaceLaunchReview{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE marketplace_launch_review SET status='IN_REVIEW',updated_at=now() WHERE id=$1`, reviewID); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "marketplace.launch_review_evaluated", "marketplace_launch_review", reviewID,
		map[string]any{"evidence_source": "PLATFORM_AUTOMATED"}); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	return s.MarketplaceLaunchReviewByID(ctx, reviewID)
}

func (s *Store) AttestMarketplaceLaunchGate(ctx context.Context, reviewID, gateCode, status, evidenceReference, reason, actor string) (domain.MarketplaceLaunchReview, error) {
	gateCode, status = strings.ToUpper(strings.TrimSpace(gateCode)), strings.ToUpper(strings.TrimSpace(status))
	evidenceReference, reason, actor = strings.TrimSpace(evidenceReference), strings.TrimSpace(reason), strings.TrimSpace(actor)
	if !marketplaceManualGates[gateCode] || (status != "PASSED" && status != "FAILED") || evidenceReference == "" || len(evidenceReference) > 300 || reason == "" || len(reason) > 500 || actor == "" {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceGateEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	defer tx.Rollback(ctx)
	var reviewStatus, gateID, fromStatus string
	err = tx.QueryRow(ctx, `SELECT review.status,gate.id,gate.status FROM marketplace_launch_review review
		JOIN marketplace_launch_gate gate ON gate.review_id=review.id AND gate.gate_code=$2 AND gate.evidence_source='ADMIN_ATTESTATION'
		WHERE review.id=$1 FOR UPDATE OF review,gate`, reviewID, gateCode).Scan(&reviewStatus, &gateID, &fromStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MarketplaceLaunchReview{}, ErrNotFound
	}
	if err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if reviewStatus != "IN_REVIEW" && reviewStatus != "DRAFT" {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	evidence := map[string]any{"source": "ADMIN_ATTESTATION", "reason": reason, "gate_code": gateCode}
	if _, err = tx.Exec(ctx, `UPDATE marketplace_launch_gate SET status=$2,evidence_reference=$3,evidence=$4,evaluated_by=$5,
		evaluated_at=now(),updated_at=now() WHERE id=$1`, gateID, status, evidenceReference, jsonBytes(evidence), actor); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO marketplace_launch_gate_event(id,gate_id,from_status,to_status,evidence_reference,evidence,actor_id)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), gateID, fromStatus, status, evidenceReference, jsonBytes(evidence), actor); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "marketplace.launch_gate_attested", "marketplace_launch_review", reviewID,
		map[string]any{"gate_code": gateCode, "status": status, "evidence_reference": evidenceReference, "reason": reason}); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	return s.MarketplaceLaunchReviewByID(ctx, reviewID)
}

func (s *Store) ApproveMarketplaceLaunchReview(ctx context.Context, reviewID, reason, actor string) (domain.MarketplaceLaunchReview, error) {
	reason, actor = strings.TrimSpace(reason), strings.TrimSpace(actor)
	if reason == "" || len(reason) > 500 || actor == "" {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	defer tx.Rollback(ctx)
	var listingID, providerID, supplierID, status, createdBy string
	err = tx.QueryRow(ctx, `SELECT listing_id,provider_id,supplier_id,status,created_by FROM marketplace_launch_review WHERE id=$1 FOR UPDATE`, reviewID).
		Scan(&listingID, &providerID, &supplierID, &status, &createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MarketplaceLaunchReview{}, ErrNotFound
	}
	if err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if status != "IN_REVIEW" || createdBy == actor {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	var failed int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM marketplace_launch_gate WHERE review_id=$1 AND status<>'PASSED'`, reviewID).Scan(&failed); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if failed != 0 {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	var eligible bool
	if err = tx.QueryRow(ctx, `SELECT supplier.status='APPROVED' AND supplier.kyb_status='VERIFIED' AND supplier.contract_status='ACTIVE'
		AND supplier.contract_start_at IS NOT NULL AND supplier.contract_start_at<=now()
		AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now())
		AND link.status='ACTIVE' AND provider.enabled AND NOT provider.emergency_kill_switch
		AND provider.commercial_status='COMMERCIAL_APPROVED' AND provider.commercial_resale_status='APPROVED'
		AND EXISTS(SELECT 1 FROM supplier_payout_readiness_review readiness WHERE readiness.supplier_id=supplier.id
			AND readiness.production_payout_enabled)
		AND supplier.tax_id<>'' AND supplier.tax_country<>'' AND supplier.tax_form_type<>''
		AND supplier.payout_account_encrypted IS NOT NULL
		AND EXISTS(SELECT 1 FROM supplier_security_questionnaires questionnaire
			WHERE questionnaire.supplier_id=supplier.id AND questionnaire.status='APPROVED')
		FROM supplier_organizations supplier JOIN supplier_provider_links link ON link.supplier_id=supplier.id AND link.provider_id=$2
		JOIN providers provider ON provider.id=link.provider_id WHERE supplier.id=$1 FOR SHARE OF supplier,link,provider`, supplierID, providerID).Scan(&eligible); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if !eligible {
		return domain.MarketplaceLaunchReview{}, ErrMarketplaceLaunchState
	}
	if _, err = tx.Exec(ctx, `UPDATE marketplace_launch_review SET status='APPROVED',reason=$2,approved_by=$3,approved_at=now(),updated_at=now() WHERE id=$1`, reviewID, reason, actor); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='ACTIVE',verified=true,updated_at=now() WHERE id=$1`, listingID); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "marketplace.launch_review_approved", "marketplace_launch_review", reviewID,
		map[string]any{"listing_id": listingID, "provider_id": providerID, "supplier_id": supplierID, "reason": reason}); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MarketplaceLaunchReview{}, err
	}
	return s.MarketplaceLaunchReviewByID(ctx, reviewID)
}

const payoutReadinessColumns = `readiness.supplier_id,supplier.display_name,readiness.contract_status,
	readiness.contract_evidence_reference,readiness.tax_status,readiness.tax_evidence_reference,readiness.payment_status,
	readiness.payment_evidence_reference,readiness.security_status,readiness.security_evidence_reference,
	(readiness.production_payout_enabled AND supplier.contract_status='ACTIVE' AND supplier.kyb_status='VERIFIED'
		AND supplier.contract_start_at IS NOT NULL AND supplier.contract_start_at<=now()
		AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now())
		AND supplier.tax_id<>'' AND supplier.tax_country<>'' AND supplier.tax_form_type<>''
		AND supplier.payout_account_encrypted IS NOT NULL AND EXISTS(SELECT 1 FROM supplier_security_questionnaires questionnaire
			WHERE questionnaire.supplier_id=supplier.id AND questionnaire.status='APPROVED')),
	readiness.review_reason,readiness.reviewed_by,readiness.reviewed_at,
	readiness.version,readiness.created_at,readiness.updated_at`

func scanSupplierPayoutReadiness(row pgx.Row) (domain.SupplierPayoutReadinessReview, error) {
	var readiness domain.SupplierPayoutReadinessReview
	err := row.Scan(&readiness.SupplierID, &readiness.SupplierName, &readiness.ContractStatus,
		&readiness.ContractEvidenceReference, &readiness.TaxStatus, &readiness.TaxEvidenceReference,
		&readiness.PaymentStatus, &readiness.PaymentEvidenceReference, &readiness.SecurityStatus,
		&readiness.SecurityEvidenceReference, &readiness.ProductionPayoutEnabled, &readiness.ReviewReason,
		&readiness.ReviewedBy, &readiness.ReviewedAt, &readiness.Version, &readiness.CreatedAt, &readiness.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return readiness, ErrNotFound
	}
	return readiness, err
}

func (s *Store) SupplierPayoutReadiness(ctx context.Context, supplierID string) (domain.SupplierPayoutReadinessReview, error) {
	return scanSupplierPayoutReadiness(s.pool.QueryRow(ctx, `SELECT `+payoutReadinessColumns+`
		FROM supplier_payout_readiness_review readiness JOIN supplier_organizations supplier ON supplier.id=readiness.supplier_id
		WHERE readiness.supplier_id=$1`, supplierID))
}

func validReadinessStatus(value string) bool {
	return value == "PENDING" || value == "APPROVED" || value == "REJECTED"
}

func (s *Store) UpdateSupplierPayoutReadiness(ctx context.Context, value domain.SupplierPayoutReadinessReview, expectedVersion int64, actor string) (domain.SupplierPayoutReadinessReview, error) {
	value.ContractStatus = strings.ToUpper(strings.TrimSpace(value.ContractStatus))
	value.TaxStatus = strings.ToUpper(strings.TrimSpace(value.TaxStatus))
	value.PaymentStatus = strings.ToUpper(strings.TrimSpace(value.PaymentStatus))
	value.SecurityStatus = strings.ToUpper(strings.TrimSpace(value.SecurityStatus))
	value.ReviewReason, actor = strings.TrimSpace(value.ReviewReason), strings.TrimSpace(actor)
	if value.SupplierID == "" || actor == "" || value.ReviewReason == "" || len(value.ReviewReason) > 500 || expectedVersion < 0 ||
		!validReadinessStatus(value.ContractStatus) || !validReadinessStatus(value.TaxStatus) ||
		!validReadinessStatus(value.PaymentStatus) || !validReadinessStatus(value.SecurityStatus) {
		return domain.SupplierPayoutReadinessReview{}, ErrMarketplacePayoutReadiness
	}
	for _, item := range []struct{ status, reference string }{
		{value.ContractStatus, value.ContractEvidenceReference}, {value.TaxStatus, value.TaxEvidenceReference},
		{value.PaymentStatus, value.PaymentEvidenceReference}, {value.SecurityStatus, value.SecurityEvidenceReference},
	} {
		if len(strings.TrimSpace(item.reference)) > 300 || item.status != "PENDING" && strings.TrimSpace(item.reference) == "" {
			return domain.SupplierPayoutReadinessReview{}, ErrMarketplacePayoutReadiness
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierPayoutReadinessReview{}, err
	}
	defer tx.Rollback(ctx)
	var contractReady, taxReady, paymentReady, securityReady bool
	err = tx.QueryRow(ctx, `SELECT contract_status='ACTIVE' AND contract_version<>'' AND contract_start_at IS NOT NULL
		AND (contract_end_at IS NULL OR contract_end_at>now()),tax_id<>'' AND tax_country<>'' AND tax_form_type<>'',
		payout_account_encrypted IS NOT NULL,EXISTS(SELECT 1 FROM supplier_security_questionnaires questionnaire
			WHERE questionnaire.supplier_id=supplier.id AND questionnaire.status='APPROVED')
		FROM supplier_organizations supplier WHERE id=$1 FOR SHARE`, value.SupplierID).
		Scan(&contractReady, &taxReady, &paymentReady, &securityReady)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierPayoutReadinessReview{}, ErrNotFound
	}
	if err != nil {
		return domain.SupplierPayoutReadinessReview{}, err
	}
	if value.ContractStatus == "APPROVED" && !contractReady || value.TaxStatus == "APPROVED" && !taxReady ||
		value.PaymentStatus == "APPROVED" && !paymentReady || value.SecurityStatus == "APPROVED" && !securityReady {
		return domain.SupplierPayoutReadinessReview{}, ErrMarketplacePayoutReadiness
	}
	tag, err := tx.Exec(ctx, `UPDATE supplier_payout_readiness_review SET contract_status=$2,contract_evidence_reference=$3,
		tax_status=$4,tax_evidence_reference=$5,payment_status=$6,payment_evidence_reference=$7,
		security_status=$8,security_evidence_reference=$9,review_reason=$10,reviewed_by=$11,reviewed_at=now(),
		version=version+1,updated_at=now() WHERE supplier_id=$1 AND version=$12`, value.SupplierID, value.ContractStatus,
		strings.TrimSpace(value.ContractEvidenceReference), value.TaxStatus, strings.TrimSpace(value.TaxEvidenceReference),
		value.PaymentStatus, strings.TrimSpace(value.PaymentEvidenceReference), value.SecurityStatus,
		strings.TrimSpace(value.SecurityEvidenceReference), value.ReviewReason, actor, expectedVersion)
	if err != nil {
		return domain.SupplierPayoutReadinessReview{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.SupplierPayoutReadinessReview{}, ErrIdempotencyConflict
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.production_payout_readiness_reviewed", "supplier", value.SupplierID,
		map[string]any{"contract_status": value.ContractStatus, "tax_status": value.TaxStatus,
			"payment_status": value.PaymentStatus, "security_status": value.SecurityStatus,
			"production_payout_enabled": value.ContractStatus == "APPROVED" && value.TaxStatus == "APPROVED" && value.PaymentStatus == "APPROVED" && value.SecurityStatus == "APPROVED",
			"reason":                    value.ReviewReason}); err != nil {
		return domain.SupplierPayoutReadinessReview{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierPayoutReadinessReview{}, err
	}
	return s.SupplierPayoutReadiness(ctx, value.SupplierID)
}

func (s *Store) MarketplaceLifecycleAction(ctx context.Context, listingID, action, reason, actor string) (domain.MarketplaceProviderLifecycleEvent, error) {
	action, reason, actor = strings.ToUpper(strings.TrimSpace(action)), strings.TrimSpace(reason), strings.TrimSpace(actor)
	if !containsString([]string{"CANARY_START", "SUSPEND", "RESUME", "EMERGENCY_CUTOVER", "EXIT"}, action) || reason == "" || len(reason) > 500 || actor == "" {
		return domain.MarketplaceProviderLifecycleEvent{}, ErrMarketplaceLaunchState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MarketplaceProviderLifecycleEvent{}, err
	}
	defer tx.Rollback(ctx)
	var providerID, supplierID, fromStatus string
	err = tx.QueryRow(ctx, `SELECT listing.provider_id,link.supplier_id,listing.status FROM provider_marketplace_listings listing
		JOIN supplier_provider_links link ON link.provider_id=listing.provider_id WHERE listing.id=$1 FOR UPDATE OF listing,link`, listingID).
		Scan(&providerID, &supplierID, &fromStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MarketplaceProviderLifecycleEvent{}, ErrNotFound
	}
	if err != nil {
		return domain.MarketplaceProviderLifecycleEvent{}, err
	}
	if fromStatus == "EXITED" {
		return domain.MarketplaceProviderLifecycleEvent{}, ErrMarketplaceLaunchState
	}
	toStatus := fromStatus
	switch action {
	case "CANARY_START":
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM marketplace_launch_review review
			JOIN provider_quality_states quality ON quality.provider_id=review.provider_id
			WHERE review.listing_id=$1 AND review.status='IN_REVIEW' AND quality.circuit_state='CLOSED'
			AND quality.traffic_cap_bps BETWEEN 1 AND 2000 AND NOT EXISTS(SELECT 1 FROM marketplace_launch_gate gate
				WHERE gate.review_id=review.id AND gate.gate_code IN ('SUPPLIER_REGISTRATION','QUALIFICATION_REVIEW','ENDPOINT_VERIFICATION','MODEL_PUBLICATION','PRICE_APPROVAL','HEALTH_TEST') AND gate.status<>'PASSED'))`, listingID).Scan(&allowed)
		if err != nil || !allowed {
			if err != nil {
				return domain.MarketplaceProviderLifecycleEvent{}, err
			}
			return domain.MarketplaceProviderLifecycleEvent{}, ErrMarketplaceLaunchState
		}
		toStatus = "CANARY"
		_, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='CANARY',updated_at=now() WHERE id=$1`, listingID)
	case "SUSPEND":
		toStatus = "SUSPENDED"
		if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_organizations SET status='SUSPENDED',version=version+1,updated_at=now() WHERE id=$1 AND status<>'EXITED'`, supplierID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_provider_links SET status='SUSPENDED',reason=$2 WHERE provider_id=$1 AND status<>'ENDED'`, providerID, reason)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE providers SET enabled=false,updated_at=now() WHERE id=$1`, providerID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='SUSPENDED',updated_at=now() WHERE id=$1`, listingID)
		}
	case "RESUME":
		var approved bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM marketplace_launch_review review WHERE review.listing_id=$1 AND review.status='APPROVED'
			AND NOT EXISTS(SELECT 1 FROM marketplace_launch_gate gate WHERE gate.review_id=review.id AND gate.status<>'PASSED'))`, listingID).Scan(&approved)
		if err != nil || !approved {
			if err != nil {
				return domain.MarketplaceProviderLifecycleEvent{}, err
			}
			return domain.MarketplaceProviderLifecycleEvent{}, ErrMarketplaceLaunchState
		}
		toStatus = "ACTIVE"
		if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_organizations SET status='APPROVED',version=version+1,updated_at=now()
				WHERE id=$1 AND kyb_status='VERIFIED' AND contract_status='ACTIVE' AND status='SUSPENDED'`, supplierID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_provider_links SET status='ACTIVE',ended_at=NULL,reason=$2 WHERE provider_id=$1 AND status='SUSPENDED'`, providerID, reason)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE providers SET enabled=true,emergency_kill_switch=false,updated_at=now() WHERE id=$1`, providerID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='ACTIVE',updated_at=now() WHERE id=$1`, listingID)
		}
	case "EMERGENCY_CUTOVER":
		toStatus = "SUSPENDED"
		_, err = tx.Exec(ctx, `UPDATE providers SET enabled=false,emergency_kill_switch=true,updated_at=now() WHERE id=$1`, providerID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_provider_links SET status='SUSPENDED',reason=$2 WHERE provider_id=$1 AND status<>'ENDED'`, providerID, reason)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='SUSPENDED',updated_at=now() WHERE id=$1`, listingID)
		}
	case "EXIT":
		var processing int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM supplier_settlement_batch WHERE supplier_id=$1 AND status='PROCESSING'`, supplierID).Scan(&processing); err != nil {
			return domain.MarketplaceProviderLifecycleEvent{}, err
		}
		if processing != 0 {
			return domain.MarketplaceProviderLifecycleEvent{}, ErrMarketplaceLaunchState
		}
		toStatus = "EXITED"
		if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_organizations SET status='EXITED',contract_status='TERMINATED',version=version+1,updated_at=now() WHERE id=$1`, supplierID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE supplier_provider_links SET status='ENDED',ended_at=now(),reason=$2 WHERE supplier_id=$1 AND status<>'ENDED'`, supplierID, reason)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE providers SET enabled=false,emergency_kill_switch=true,commercial_status='TERMINATED',updated_at=now()
				WHERE id IN (SELECT provider_id FROM supplier_provider_links WHERE supplier_id=$1)`, supplierID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE provider_marketplace_listings SET status='EXITED',updated_at=now()
				WHERE provider_id IN (SELECT provider_id FROM supplier_provider_links WHERE supplier_id=$1)`, supplierID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE marketplace_launch_review SET status='REVOKED',revoked_by=$2,revoked_at=now(),reason=$3,updated_at=now()
				WHERE supplier_id=$1 AND status='APPROVED'`, supplierID, actor, reason)
		}
	}
	if err != nil {
		return domain.MarketplaceProviderLifecycleEvent{}, err
	}
	event := domain.MarketplaceProviderLifecycleEvent{ID: id.UUID(), ListingID: listingID, ProviderID: providerID,
		SupplierID: supplierID, Action: action, FromListingStatus: fromStatus, ToListingStatus: toStatus, Reason: reason, ActorID: actor}
	if _, err = tx.Exec(ctx, `INSERT INTO marketplace_provider_lifecycle_event(id,listing_id,provider_id,supplier_id,action,
		from_listing_status,to_listing_status,reason,actor_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, event.ID,
		event.ListingID, event.ProviderID, event.SupplierID, event.Action, event.FromListingStatus, event.ToListingStatus, event.Reason, event.ActorID); err != nil {
		return domain.MarketplaceProviderLifecycleEvent{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "marketplace.provider_lifecycle_"+strings.ToLower(action), "marketplace_listing", listingID,
		map[string]any{"provider_id": providerID, "supplier_id": supplierID, "from_status": fromStatus, "to_status": toStatus, "reason": reason}); err != nil {
		return domain.MarketplaceProviderLifecycleEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MarketplaceProviderLifecycleEvent{}, err
	}
	event.CreatedAt = time.Now().UTC()
	return event, nil
}

func (s *Store) ListMarketplaceLifecycleEvents(ctx context.Context, listingID string, limit int) ([]domain.MarketplaceProviderLifecycleEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,listing_id,provider_id,supplier_id,action,from_listing_status,to_listing_status,
		reason,actor_id,created_at FROM marketplace_provider_lifecycle_event WHERE listing_id=$1 ORDER BY created_at DESC,id LIMIT $2`, listingID, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.MarketplaceProviderLifecycleEvent, 0)
	for rows.Next() {
		var event domain.MarketplaceProviderLifecycleEvent
		if err = rows.Scan(&event.ID, &event.ListingID, &event.ProviderID, &event.SupplierID, &event.Action,
			&event.FromListingStatus, &event.ToListingStatus, &event.Reason, &event.ActorID, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) productionPayoutReadyTx(ctx context.Context, tx pgx.Tx, supplierID string) (bool, error) {
	var ready bool
	err := tx.QueryRow(ctx, `SELECT readiness.production_payout_enabled AND supplier.contract_status='ACTIVE'
		AND supplier.kyb_status='VERIFIED' AND supplier.contract_start_at IS NOT NULL AND supplier.contract_start_at<=now()
		AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now())
		AND supplier.tax_id<>'' AND supplier.tax_country<>'' AND supplier.tax_form_type<>''
		AND supplier.payout_account_encrypted IS NOT NULL AND EXISTS(SELECT 1 FROM supplier_security_questionnaires questionnaire
			WHERE questionnaire.supplier_id=supplier.id AND questionnaire.status='APPROVED')
		FROM supplier_payout_readiness_review readiness JOIN supplier_organizations supplier ON supplier.id=readiness.supplier_id
		WHERE readiness.supplier_id=$1`, supplierID).Scan(&ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ready, err
}
