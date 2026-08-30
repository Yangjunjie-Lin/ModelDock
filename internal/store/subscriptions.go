package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrEntitlementExceeded = errors.New("subscription entitlement exceeded")
	ErrEntitlementRequired = errors.New("subscription entitlement is required")
	ErrSubscriptionState   = errors.New("subscription state transition is not allowed")
)

const (
	freePlanVersionID = "11000000-0000-4000-8000-000000000001"
)

type SubscriptionChangeRequest struct {
	OrganizationID    string
	PlanVersionID     string
	Mode              string
	UseTrial          bool
	CouponCode        string
	IdempotencyKey    string
	ContractReference string
	ContractStartsAt  *time.Time
	ContractEndsAt    *time.Time
	Metadata          map[string]any
	CreatedBy         *string
}

type SubscriptionCancelRequest struct {
	OrganizationID string
	Mode           string
	IdempotencyKey string
	CreatedBy      *string
}

type SubscriptionPaymentRequest struct {
	InvoiceID                string
	PaymentProvider          string
	ProviderPaymentReference string
	IdempotencyKey           string
	CreatedBy                *string
}

func scanSubscriptionPlan(row pgx.Row) (domain.SubscriptionPlan, error) {
	var out domain.SubscriptionPlan
	var metadata []byte
	err := row.Scan(&out.ID, &out.Slug, &out.Name, &out.Description, &out.PlanKind, &out.Enabled,
		&out.CurrentVersionID, &metadata, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(metadata, &out.Metadata)
	}
	return out, err
}

const subscriptionPlanColumns = `id,slug,name,description,plan_kind,enabled,current_version_id,metadata,created_by,created_at,updated_at`

func (s *Store) ListSubscriptionPlans(ctx context.Context, includeDisabled bool) ([]domain.SubscriptionPlan, error) {
	query := `SELECT ` + subscriptionPlanColumns + ` FROM subscription_plan`
	if !includeDisabled {
		query += ` WHERE enabled`
	}
	query += ` ORDER BY CASE slug WHEN 'free' THEN 0 WHEN 'developer' THEN 1 WHEN 'team' THEN 2 WHEN 'enterprise' THEN 3 ELSE 4 END,name,id`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SubscriptionPlan, 0)
	for rows.Next() {
		plan, scanErr := scanSubscriptionPlan(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func scanPlanVersion(row pgx.Row) (domain.PlanVersion, error) {
	var out domain.PlanVersion
	var fee string
	var metadata []byte
	err := row.Scan(&out.ID, &out.SubscriptionPlanID, &out.Version, &out.Status, &out.BillingInterval,
		&fee, &out.Currency, &out.TrialDays, &out.GracePeriodDays, &out.TokenBillingMode,
		&out.EnterpriseContract, &out.EffectiveAt, &out.FrozenAt, &out.RetiredAt, &metadata,
		&out.CreatedBy, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		out.SubscriptionFee, err = parseStoredDecimal(fee, "plan_version.subscription_fee")
		_ = json.Unmarshal(metadata, &out.Metadata)
	}
	return out, err
}

const planVersionColumns = `id,subscription_plan_id,version,status,billing_interval,subscription_fee::text,currency,
	trial_days,grace_period_days,token_billing_mode,enterprise_contract,effective_at,frozen_at,retired_at,metadata,created_by,created_at`

func (s *Store) PlanVersionByID(ctx context.Context, versionID string) (domain.PlanVersion, error) {
	out, err := scanPlanVersion(s.pool.QueryRow(ctx, `SELECT `+planVersionColumns+` FROM plan_version WHERE id=$1`, versionID))
	if err != nil {
		return out, err
	}
	out.Entitlements, err = s.listPlanEntitlements(ctx, versionID)
	return out, err
}

func (s *Store) ListPlanVersions(ctx context.Context, planID string) ([]domain.PlanVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+planVersionColumns+` FROM plan_version WHERE subscription_plan_id=$1 ORDER BY version DESC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PlanVersion, 0)
	for rows.Next() {
		version, scanErr := scanPlanVersion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, version)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for index := range out {
		out[index].Entitlements, err = s.listPlanEntitlements(ctx, out[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) listPlanEntitlements(ctx context.Context, versionID string) ([]domain.PlanEntitlement, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,plan_version_id,entitlement_key,value_type,integer_value,boolean_value,string_value,created_at
		FROM plan_entitlement WHERE plan_version_id=$1 ORDER BY entitlement_key`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PlanEntitlement, 0)
	for rows.Next() {
		var item domain.PlanEntitlement
		if err = rows.Scan(&item.ID, &item.PlanVersionID, &item.EntitlementKey, &item.ValueType,
			&item.IntegerValue, &item.BooleanValue, &item.StringValue, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateSubscriptionPlan(ctx context.Context, plan domain.SubscriptionPlan) (domain.SubscriptionPlan, error) {
	plan.ID = id.UUID()
	if plan.PlanKind == "" {
		plan.PlanKind = "STANDARD"
	}
	if plan.Metadata == nil {
		plan.Metadata = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO subscription_plan(id,slug,name,description,plan_kind,enabled,metadata,created_by)
		VALUES($1,lower($2),$3,$4,$5,$6,$7,$8)`, plan.ID, plan.Slug, plan.Name, plan.Description,
		strings.ToUpper(plan.PlanKind), plan.Enabled, jsonBytes(plan.Metadata), plan.CreatedBy)
	if err != nil {
		return domain.SubscriptionPlan{}, err
	}
	return scanSubscriptionPlan(s.pool.QueryRow(ctx, `SELECT `+subscriptionPlanColumns+` FROM subscription_plan WHERE id=$1`, plan.ID))
}

func (s *Store) CreatePlanVersion(ctx context.Context, version domain.PlanVersion) (domain.PlanVersion, error) {
	if version.TokenBillingMode != "" && version.TokenBillingMode != "METERED_SEPARATE" {
		return domain.PlanVersion{}, errors.New("Token usage must use METERED_SEPARATE billing")
	}
	version.ID = id.UUID()
	version.Status = "DRAFT"
	version.TokenBillingMode = "METERED_SEPARATE"
	if version.Metadata == nil {
		version.Metadata = map[string]any{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PlanVersion{}, err
	}
	defer tx.Rollback(ctx)
	if version.Version <= 0 {
		var lockedPlan string
		if err = tx.QueryRow(ctx, `SELECT id FROM subscription_plan WHERE id=$1 FOR UPDATE`, version.SubscriptionPlanID).Scan(&lockedPlan); err != nil {
			return domain.PlanVersion{}, err
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM plan_version WHERE subscription_plan_id=$1`, version.SubscriptionPlanID).Scan(&version.Version)
		if err != nil {
			return domain.PlanVersion{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO plan_version(id,subscription_plan_id,version,status,billing_interval,subscription_fee,currency,
		trial_days,grace_period_days,token_billing_mode,enterprise_contract,effective_at,metadata,created_by)
		VALUES($1,$2,$3,'DRAFT',$4,$5,$6,$7,$8,'METERED_SEPARATE',$9,$10,$11,$12)`, version.ID,
		version.SubscriptionPlanID, version.Version, strings.ToUpper(version.BillingInterval), version.SubscriptionFee.String(),
		strings.ToUpper(version.Currency), version.TrialDays, version.GracePeriodDays, version.EnterpriseContract,
		version.EffectiveAt, jsonBytes(version.Metadata), version.CreatedBy)
	if err != nil {
		return domain.PlanVersion{}, err
	}
	for _, entitlement := range version.Entitlements {
		if strings.Contains(strings.ToLower(entitlement.EntitlementKey), "token") {
			return domain.PlanVersion{}, errors.New("Token entitlements are forbidden")
		}
		_, err = tx.Exec(ctx, `INSERT INTO plan_entitlement(id,plan_version_id,entitlement_key,value_type,integer_value,boolean_value,string_value)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), version.ID, entitlement.EntitlementKey,
			strings.ToUpper(entitlement.ValueType), entitlement.IntegerValue, entitlement.BooleanValue, entitlement.StringValue)
		if err != nil {
			return domain.PlanVersion{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.PlanVersion{}, err
	}
	return s.PlanVersionByID(ctx, version.ID)
}

func (s *Store) FreezePlanVersion(ctx context.Context, versionID string, actorID *string) (domain.PlanVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PlanVersion{}, err
	}
	defer tx.Rollback(ctx)
	var planID, status string
	if err = tx.QueryRow(ctx, `SELECT subscription_plan_id,status FROM plan_version WHERE id=$1 FOR UPDATE`, versionID).Scan(&planID, &status); errors.Is(err, pgx.ErrNoRows) {
		return domain.PlanVersion{}, ErrNotFound
	} else if err != nil {
		return domain.PlanVersion{}, err
	}
	if status != "DRAFT" {
		return domain.PlanVersion{}, ErrSubscriptionState
	}
	var entitlementCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM plan_entitlement WHERE plan_version_id=$1`, versionID).Scan(&entitlementCount); err != nil {
		return domain.PlanVersion{}, err
	}
	if entitlementCount != 11 {
		return domain.PlanVersion{}, errors.New("every supported entitlement must be configured before freezing")
	}
	if _, err = tx.Exec(ctx, `UPDATE plan_version SET status='FROZEN',frozen_at=now() WHERE id=$1`, versionID); err != nil {
		return domain.PlanVersion{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE subscription_plan SET current_version_id=$2,updated_at=now() WHERE id=$1`, planID, versionID); err != nil {
		return domain.PlanVersion{}, err
	}
	if err = writeAuditTx(ctx, tx, actorID, "subscription.plan_version_frozen", "plan_version", versionID, map[string]any{"plan_id": planID}); err != nil {
		return domain.PlanVersion{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.PlanVersion{}, err
	}
	return s.PlanVersionByID(ctx, versionID)
}

func scanOrganizationSubscription(row pgx.Row) (domain.OrganizationSubscription, error) {
	var out domain.OrganizationSubscription
	var metadata []byte
	err := row.Scan(&out.ID, &out.OrganizationID, &out.PlanVersionID, &out.PendingPlanVersionID,
		&out.PlanSlug, &out.PlanName, &out.PlanVersion, &out.Status, &out.CurrentPeriodStart,
		&out.CurrentPeriodEnd, &out.GracePeriodEnd, &out.CancelAtPeriodEnd, &out.CanceledAt, &out.EndedAt,
		&out.ContractReference, &out.ContractStartsAt, &out.ContractEndsAt, &out.CouponID, &out.Version,
		&metadata, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(metadata, &out.Metadata)
	}
	return out, err
}

const organizationSubscriptionSelect = `SELECT subscription.id,subscription.organization_id,subscription.plan_version_id,
	subscription.pending_plan_version_id,plan.slug,plan.name,version.version,subscription.status,
	subscription.current_period_start,subscription.current_period_end,subscription.grace_period_end,
	subscription.cancel_at_period_end,subscription.canceled_at,subscription.ended_at,
	COALESCE(subscription.contract_reference,''),subscription.contract_starts_at,subscription.contract_ends_at,
	subscription.coupon_id,subscription.version,subscription.metadata,subscription.created_by,
	subscription.created_at,subscription.updated_at FROM organization_subscription subscription
	JOIN plan_version version ON version.id=subscription.plan_version_id
	JOIN subscription_plan plan ON plan.id=version.subscription_plan_id`

func (s *Store) CurrentSubscription(ctx context.Context, organizationID string) (domain.OrganizationSubscription, error) {
	out, err := scanOrganizationSubscription(s.pool.QueryRow(ctx, organizationSubscriptionSelect+`
		WHERE subscription.organization_id=$1 AND subscription.status IN ('TRIALING','ACTIVE','PAST_DUE','GRACE_PERIOD')
		ORDER BY subscription.created_at DESC LIMIT 1`, organizationID))
	if err != nil {
		return out, err
	}
	out.Entitlements, err = s.listPlanEntitlements(ctx, out.PlanVersionID)
	return out, err
}

func (s *Store) ListOrganizationSubscriptions(ctx context.Context, organizationID string, limit, offset int) ([]domain.OrganizationSubscription, error) {
	rows, err := s.pool.Query(ctx, organizationSubscriptionSelect+` WHERE subscription.organization_id=$1
		ORDER BY subscription.created_at DESC,subscription.id DESC LIMIT $2 OFFSET $3`, organizationID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OrganizationSubscription, 0)
	for rows.Next() {
		item, scanErr := scanOrganizationSubscription(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) EffectiveEntitlements(ctx context.Context, organizationID string) (domain.EffectiveEntitlements, error) {
	var out domain.EffectiveEntitlements
	err := s.pool.QueryRow(ctx, `WITH effective AS (
		SELECT subscription.id subscription_id,subscription.status subscription_status,subscription.plan_version_id
		FROM organization_subscription subscription
		WHERE subscription.organization_id=$1 AND (
		  (subscription.status IN ('TRIALING','ACTIVE') AND subscription.current_period_end>now()) OR
		  (subscription.status IN ('PAST_DUE','GRACE_PERIOD') AND COALESCE(subscription.grace_period_end,subscription.current_period_end)>now()
		   AND COALESCE(subscription.metadata->>'awaiting_initial_payment','false')<>'true')
		) ORDER BY subscription.created_at DESC LIMIT 1
	), selected AS (
		SELECT COALESCE((SELECT subscription_id::text FROM effective),'') subscription_id,
		       COALESCE((SELECT subscription_status FROM effective),'EXPIRED') subscription_status,
		       COALESCE((SELECT plan_version_id FROM effective),$2::uuid) plan_version_id
	)
	SELECT $1,selected.subscription_id,selected.plan_version_id,plan.slug,selected.subscription_status,
		COALESCE((SELECT integer_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='api_key_count'),0),
		COALESCE((SELECT integer_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='organization_member_count'),0),
		COALESCE((SELECT integer_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='concurrency'),0),
		COALESCE((SELECT integer_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='requests_per_minute'),0),
		COALESCE((SELECT integer_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='log_retention_days'),0),
		COALESCE((SELECT boolean_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='advanced_routing'),false),
		COALESCE((SELECT boolean_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='cost_analysis'),false),
		COALESCE((SELECT boolean_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='custom_budget'),false),
		COALESCE((SELECT integer_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='webhook_count'),0),
		COALESCE((SELECT boolean_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='priority_support'),false),
		COALESCE((SELECT string_value FROM plan_entitlement WHERE plan_version_id=selected.plan_version_id AND entitlement_key='sla_level'),'NONE'),
		version.token_billing_mode
	FROM selected JOIN plan_version version ON version.id=selected.plan_version_id
	JOIN subscription_plan plan ON plan.id=version.subscription_plan_id`, organizationID, freePlanVersionID).
		Scan(&out.OrganizationID, &out.SubscriptionID, &out.PlanVersionID, &out.PlanSlug, &out.SubscriptionStatus,
			&out.APIKeyCount, &out.OrganizationMembers, &out.Concurrency, &out.RequestsPerMinute,
			&out.LogRetentionDays, &out.AdvancedRouting, &out.CostAnalysis, &out.CustomBudget,
			&out.WebhookCount, &out.PrioritySupport, &out.SLALevel, &out.TokenBillingMode)
	return out, err
}

func (s *Store) RequireBooleanEntitlement(ctx context.Context, organizationID, key string) error {
	entitlements, err := s.EffectiveEntitlements(ctx, organizationID)
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		"advanced_routing": entitlements.AdvancedRouting,
		"cost_analysis":    entitlements.CostAnalysis,
		"custom_budget":    entitlements.CustomBudget,
		"priority_support": entitlements.PrioritySupport,
	}[key]
	if !allowed {
		return ErrEntitlementRequired
	}
	return nil
}

func effectivePlanVersionTx(ctx context.Context, tx pgx.Tx, organizationID string) (string, error) {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	var versionID string
	err := tx.QueryRow(ctx, `SELECT plan_version_id FROM organization_subscription WHERE organization_id=$1 AND (
		(status IN ('TRIALING','ACTIVE') AND current_period_end>now()) OR
		(status IN ('PAST_DUE','GRACE_PERIOD') AND COALESCE(grace_period_end,current_period_end)>now()
		 AND COALESCE(metadata->>'awaiting_initial_payment','false')<>'true'))
		ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, organizationID).Scan(&versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return freePlanVersionID, nil
	}
	return versionID, err
}

func enforceIntegerEntitlementTx(ctx context.Context, tx pgx.Tx, organizationID, entitlementKey string, currentCount, increment int64) error {
	versionID, err := effectivePlanVersionTx(ctx, tx, organizationID)
	if err != nil {
		return err
	}
	var limit int64
	if err = tx.QueryRow(ctx, `SELECT integer_value FROM plan_entitlement WHERE plan_version_id=$1 AND entitlement_key=$2`, versionID, entitlementKey).Scan(&limit); errors.Is(err, pgx.ErrNoRows) {
		return ErrEntitlementRequired
	} else if err != nil {
		return err
	}
	if currentCount+increment > limit {
		return ErrEntitlementExceeded
	}
	return nil
}

func enforceOrganizationMemberActivationTx(ctx context.Context, tx pgx.Tx, organizationID, userID string) error {
	if _, err := effectivePlanVersionTx(ctx, tx, organizationID); err != nil {
		return err
	}
	var alreadyActive bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships
		WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE')`, organizationID, userID).Scan(&alreadyActive); err != nil {
		return err
	}
	if alreadyActive {
		return nil
	}
	// Platform administrators act as operational custodians when they create or
	// recover a tenant. They are not customer seats and must not consume the
	// organization's subscription member entitlement.
	var platformAdministrator bool
	if err := tx.QueryRow(ctx, `SELECT role IN ('SUPER_ADMIN','ADMIN') FROM users WHERE id=$1`, userID).Scan(&platformAdministrator); err != nil {
		return err
	}
	if platformAdministrator {
		return nil
	}
	var activeMembers int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships membership
		JOIN users member_user ON member_user.id=membership.user_id
		WHERE membership.organization_id=$1 AND membership.status='ACTIVE'
		AND member_user.role NOT IN ('SUPER_ADMIN','ADMIN')`, organizationID).Scan(&activeMembers); err != nil {
		return err
	}
	return enforceIntegerEntitlementTx(ctx, tx, organizationID, "organization_member_count", activeMembers, 1)
}

func subscriptionFingerprint(values ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(hash[:])
}

func subscriptionPeriod(start time.Time, interval string, contractEnd *time.Time) (time.Time, error) {
	switch interval {
	case "MONTHLY":
		return start.AddDate(0, 1, 0), nil
	case "YEARLY":
		return start.AddDate(1, 0, 0), nil
	case "CUSTOM":
		if contractEnd == nil || !contractEnd.After(start) {
			return time.Time{}, errors.New("a future contract_ends_at is required for CUSTOM billing")
		}
		return contractEnd.UTC(), nil
	default:
		return time.Time{}, errors.New("unsupported billing interval")
	}
}

func (s *Store) ChangeSubscription(ctx context.Context, request SubscriptionChangeRequest) (domain.OrganizationSubscription, *domain.SubscriptionInvoice, error) {
	request.Mode = strings.ToUpper(strings.TrimSpace(request.Mode))
	if request.Mode == "" {
		request.Mode = "IMMEDIATE"
	}
	if request.Mode != "IMMEDIATE" && request.Mode != "PERIOD_END" {
		return domain.OrganizationSubscription{}, nil, ErrSubscriptionState
	}
	if request.OrganizationID == "" || request.PlanVersionID == "" || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 200 {
		return domain.OrganizationSubscription{}, nil, errors.New("organization, plan version, and idempotency key are required")
	}
	fingerprint := subscriptionFingerprint(request.OrganizationID, request.PlanVersionID, request.Mode,
		fmt.Sprint(request.UseTrial), strings.ToUpper(request.CouponCode), request.ContractReference,
		formatOptionalTime(request.ContractStartsAt), formatOptionalTime(request.ContractEndsAt))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockSubscriptionOrganizationTx(ctx, tx, request.OrganizationID); err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	var replayID, replayFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,request_fingerprint FROM organization_subscription WHERE organization_id=$1 AND idempotency_key=$2`,
		request.OrganizationID, request.IdempotencyKey).Scan(&replayID, &replayFingerprint)
	if err == nil {
		if replayFingerprint != fingerprint {
			return domain.OrganizationSubscription{}, nil, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
		out, loadErr := s.subscriptionByID(ctx, replayID)
		invoice, invoiceErr := s.invoiceBySubscriptionIdempotency(ctx, replayID, request.IdempotencyKey)
		if errors.Is(invoiceErr, ErrNotFound) {
			invoiceErr = nil
			invoice = nil
		}
		return out, invoice, errors.Join(loadErr, invoiceErr)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationSubscription{}, nil, err
	}
	var target domain.PlanVersion
	var fee string
	err = tx.QueryRow(ctx, `SELECT id,subscription_plan_id,version,status,billing_interval,subscription_fee::text,currency,
		trial_days,grace_period_days,token_billing_mode,enterprise_contract,effective_at,frozen_at,retired_at,metadata,created_by,created_at
		FROM plan_version WHERE id=$1 FOR SHARE`, request.PlanVersionID).Scan(&target.ID, &target.SubscriptionPlanID,
		&target.Version, &target.Status, &target.BillingInterval, &fee, &target.Currency, &target.TrialDays,
		&target.GracePeriodDays, &target.TokenBillingMode, &target.EnterpriseContract, &target.EffectiveAt,
		&target.FrozenAt, &target.RetiredAt, &target.Metadata, &target.CreatedBy, &target.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationSubscription{}, nil, ErrNotFound
	}
	if err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	target.SubscriptionFee, err = parseStoredDecimal(fee, "organization_subscription.subscription_fee")
	if err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	if target.Status != "FROZEN" || target.TokenBillingMode != "METERED_SEPARATE" {
		return domain.OrganizationSubscription{}, nil, ErrSubscriptionState
	}
	if target.EnterpriseContract && (request.ContractReference == "" || request.ContractEndsAt == nil) {
		return domain.OrganizationSubscription{}, nil, errors.New("enterprise plans require a contract reference and end date")
	}
	var currentID, currentVersionID, currentStatus string
	var currentEnd time.Time
	err = tx.QueryRow(ctx, `SELECT id,plan_version_id,status,current_period_end FROM organization_subscription
		WHERE organization_id=$1 AND status IN ('TRIALING','ACTIVE','PAST_DUE','GRACE_PERIOD') FOR UPDATE`, request.OrganizationID).
		Scan(&currentID, &currentVersionID, &currentStatus, &currentEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		currentID = ""
	} else if err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	if request.Mode == "PERIOD_END" {
		if target.EnterpriseContract {
			return domain.OrganizationSubscription{}, nil, errors.New("enterprise manual contracts must be activated immediately with contract dates")
		}
		if currentID == "" || currentVersionID == target.ID {
			return domain.OrganizationSubscription{}, nil, ErrSubscriptionState
		}
		var existingFingerprint string
		err = tx.QueryRow(ctx, `SELECT payload->>'request_fingerprint' FROM subscription_event WHERE organization_id=$1 AND idempotency_key=$2`, request.OrganizationID, request.IdempotencyKey).Scan(&existingFingerprint)
		if err == nil {
			if existingFingerprint != fingerprint {
				return domain.OrganizationSubscription{}, nil, ErrIdempotencyConflict
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `UPDATE organization_subscription SET pending_plan_version_id=$2,version=version+1,updated_at=now() WHERE id=$1`, currentID, target.ID)
			if err == nil {
				err = insertSubscriptionEvent(ctx, tx, request.OrganizationID, currentID, "PLAN_CHANGE_SCHEDULED", currentStatus, currentStatus, request.CreatedBy, request.IdempotencyKey,
					map[string]any{"request_fingerprint": fingerprint, "pending_plan_version_id": target.ID, "effective_at": currentEnd})
			}
		}
		if err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
		out, loadErr := s.subscriptionByID(ctx, currentID)
		return out, nil, loadErr
	}

	now := time.Now().UTC()
	periodStart := now
	if request.ContractStartsAt != nil {
		periodStart = request.ContractStartsAt.UTC()
	}
	periodEnd, err := subscriptionPeriod(periodStart, target.BillingInterval, request.ContractEndsAt)
	if err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	trialing := false
	if request.UseTrial && target.TrialDays > 0 {
		var used bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trial WHERE organization_id=$1 AND plan_version_id=$2)`, request.OrganizationID, target.ID).Scan(&used); err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
		trialing = !used
		if trialing {
			periodEnd = now.AddDate(0, 0, target.TrialDays)
		}
	}
	if currentID != "" {
		_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='CANCELED',canceled_at=now(),ended_at=now(),
			cancel_at_period_end=false,pending_plan_version_id=NULL,version=version+1,updated_at=now() WHERE id=$1`, currentID)
		if err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
	}
	newID := id.UUID()
	newStatus := "ACTIVE"
	if trialing {
		newStatus = "TRIALING"
	} else if decimalPositive(fee) {
		newStatus = "PAST_DUE"
		if request.Metadata == nil {
			request.Metadata = map[string]any{}
		}
		request.Metadata["awaiting_initial_payment"] = true
	}
	var couponID *string
	if request.CouponCode != "" {
		var value string
		if err = tx.QueryRow(ctx, `SELECT id FROM coupon WHERE code=upper($1) AND enabled AND starts_at<=now()
			AND (expires_at IS NULL OR expires_at>now()) AND (max_redemptions IS NULL OR redeemed_count<max_redemptions) FOR UPDATE`, request.CouponCode).Scan(&value); errors.Is(err, pgx.ErrNoRows) {
			return domain.OrganizationSubscription{}, nil, ErrNotFound
		} else if err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
		couponID = &value
	}
	var initialGraceEnd *time.Time
	if newStatus == "PAST_DUE" {
		value := now.AddDate(0, 0, target.GracePeriodDays)
		initialGraceEnd = &value
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_subscription(id,organization_id,plan_version_id,status,current_period_start,
		current_period_end,grace_period_end,contract_reference,contract_starts_at,contract_ends_at,coupon_id,idempotency_key,request_fingerprint,metadata,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, newID, request.OrganizationID, target.ID,
		newStatus, periodStart, periodEnd, initialGraceEnd, nullString(request.ContractReference), request.ContractStartsAt, request.ContractEndsAt,
		couponID, request.IdempotencyKey, fingerprint, jsonBytes(request.Metadata), request.CreatedBy)
	if err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	if trialing {
		_, err = tx.Exec(ctx, `INSERT INTO trial(id,organization_subscription_id,organization_id,plan_version_id,status,starts_at,ends_at)
			VALUES($1,$2,$3,$4,'ACTIVE',$5,$6)`, id.UUID(), newID, request.OrganizationID, target.ID, now, periodEnd)
		if err != nil {
			return domain.OrganizationSubscription{}, nil, err
		}
	}
	eventType := "SUBSCRIPTION_CREATED"
	if currentID != "" {
		eventType = "PLAN_CHANGED_IMMEDIATELY"
	}
	if err = insertSubscriptionEvent(ctx, tx, request.OrganizationID, newID, eventType, currentStatus, newStatus,
		request.CreatedBy, request.IdempotencyKey, map[string]any{"request_fingerprint": fingerprint, "previous_subscription_id": currentID,
			"plan_version_id": target.ID, "token_billing_mode": "METERED_SEPARATE"}); err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	var invoice *domain.SubscriptionInvoice
	if !trialing && decimalPositive(fee) {
		invoiceType := "INITIAL"
		if currentID != "" {
			invoiceType = "UPGRADE"
		}
		if target.EnterpriseContract {
			invoiceType = "MANUAL_CONTRACT"
		}
		created, createErr := createSubscriptionInvoiceTx(ctx, tx, request.OrganizationID, newID, target,
			couponID, invoiceType, periodStart, periodEnd, request.IdempotencyKey, fingerprint, request.CreatedBy)
		if createErr != nil {
			return domain.OrganizationSubscription{}, nil, createErr
		}
		invoice = &created
		if created.Status == "PAID" {
			newStatus = "ACTIVE"
			_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='ACTIVE',grace_period_end=NULL,
				metadata=metadata-'awaiting_initial_payment',version=version+1,updated_at=now() WHERE id=$1`, newID)
			if err != nil {
				return domain.OrganizationSubscription{}, nil, err
			}
			if err = insertSubscriptionEvent(ctx, tx, request.OrganizationID, newID, "ZERO_AMOUNT_INVOICE_PAID", "PAST_DUE", "ACTIVE",
				request.CreatedBy, request.IdempotencyKey+":zero-paid", map[string]any{"invoice_id": created.ID}); err != nil {
				return domain.OrganizationSubscription{}, nil, err
			}
		}
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "subscription.change", "organization_subscription", newID,
		map[string]any{"plan_version_id": target.ID, "status": newStatus, "token_billing_mode": "METERED_SEPARATE"}); err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.OrganizationSubscription{}, nil, err
	}
	out, loadErr := s.subscriptionByID(ctx, newID)
	return out, invoice, loadErr
}

func (s *Store) CancelSubscription(ctx context.Context, request SubscriptionCancelRequest) (domain.OrganizationSubscription, error) {
	request.Mode = strings.ToUpper(strings.TrimSpace(request.Mode))
	if request.Mode != "IMMEDIATE" && request.Mode != "PERIOD_END" {
		return domain.OrganizationSubscription{}, ErrSubscriptionState
	}
	if request.IdempotencyKey == "" {
		return domain.OrganizationSubscription{}, errors.New("idempotency key is required")
	}
	fingerprint := subscriptionFingerprint(request.OrganizationID, request.Mode)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationSubscription{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockSubscriptionOrganizationTx(ctx, tx, request.OrganizationID); err != nil {
		return domain.OrganizationSubscription{}, err
	}
	var subscriptionID, status string
	err = tx.QueryRow(ctx, `SELECT id,status FROM organization_subscription WHERE organization_id=$1
		AND status IN ('TRIALING','ACTIVE','PAST_DUE','GRACE_PERIOD') FOR UPDATE`, request.OrganizationID).Scan(&subscriptionID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationSubscription{}, ErrNotFound
	}
	if err != nil {
		return domain.OrganizationSubscription{}, err
	}
	var prior string
	err = tx.QueryRow(ctx, `SELECT payload->>'request_fingerprint' FROM subscription_event WHERE organization_id=$1 AND idempotency_key=$2`, request.OrganizationID, request.IdempotencyKey).Scan(&prior)
	if err == nil {
		if prior != fingerprint {
			return domain.OrganizationSubscription{}, ErrIdempotencyConflict
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationSubscription{}, err
	} else if request.Mode == "PERIOD_END" {
		_, err = tx.Exec(ctx, `UPDATE organization_subscription SET cancel_at_period_end=true,canceled_at=now(),
			pending_plan_version_id=NULL,version=version+1,updated_at=now() WHERE id=$1`, subscriptionID)
		if err == nil {
			err = insertSubscriptionEvent(ctx, tx, request.OrganizationID, subscriptionID, "CANCEL_SCHEDULED", status, status,
				request.CreatedBy, request.IdempotencyKey, map[string]any{"request_fingerprint": fingerprint})
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='CANCELED',cancel_at_period_end=false,canceled_at=now(),ended_at=now(),
			pending_plan_version_id=NULL,version=version+1,updated_at=now() WHERE id=$1`, subscriptionID)
		if err == nil {
			err = insertSubscriptionEvent(ctx, tx, request.OrganizationID, subscriptionID, "CANCELED_IMMEDIATELY", status, "CANCELED",
				request.CreatedBy, request.IdempotencyKey, map[string]any{"request_fingerprint": fingerprint})
		}
		if err == nil {
			_, err = createFreeSubscriptionTx(ctx, tx, request.OrganizationID, "cancel:"+request.IdempotencyKey, request.CreatedBy)
		}
	}
	if err != nil {
		return domain.OrganizationSubscription{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.OrganizationSubscription{}, err
	}
	return s.CurrentSubscription(ctx, request.OrganizationID)
}

func (s *Store) ListSubscriptionInvoices(ctx context.Context, organizationID string, limit, offset int) ([]domain.SubscriptionInvoice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,invoice_number,organization_id,organization_subscription_id,plan_version_id,coupon_id,
		invoice_type,status,subtotal::text,discount_amount::text,tax_amount::text,total_amount::text,currency,period_start,period_end,
		due_at,paid_at,failed_at,COALESCE(payment_provider,''),COALESCE(provider_payment_reference,''),ledger_journal_id,
		plan_snapshot,created_by,created_at,updated_at FROM subscription_invoice WHERE ($1='' OR organization_id=$1)
		ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, organizationID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SubscriptionInvoice, 0)
	for rows.Next() {
		invoice, scanErr := scanSubscriptionInvoice(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, invoice)
	}
	return out, rows.Err()
}

func scanSubscriptionInvoice(row pgx.Row) (domain.SubscriptionInvoice, error) {
	var out domain.SubscriptionInvoice
	var subtotal, discount, tax, total string
	var snapshot []byte
	err := row.Scan(&out.ID, &out.InvoiceNumber, &out.OrganizationID, &out.OrganizationSubscriptionID,
		&out.PlanVersionID, &out.CouponID, &out.InvoiceType, &out.Status, &subtotal, &discount, &tax, &total,
		&out.Currency, &out.PeriodStart, &out.PeriodEnd, &out.DueAt, &out.PaidAt, &out.FailedAt,
		&out.PaymentProvider, &out.ProviderPaymentReference, &out.LedgerJournalID, &snapshot, &out.CreatedBy,
		&out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		out.Subtotal, err = parseStoredDecimal(subtotal, "subscription_invoice.subtotal")
		if err == nil {
			out.DiscountAmount, err = parseStoredDecimal(discount, "subscription_invoice.discount_amount")
		}
		if err == nil {
			out.TaxAmount, err = parseStoredDecimal(tax, "subscription_invoice.tax_amount")
		}
		if err == nil {
			out.TotalAmount, err = parseStoredDecimal(total, "subscription_invoice.total_amount")
		}
		_ = json.Unmarshal(snapshot, &out.PlanSnapshot)
	}
	return out, err
}

const subscriptionInvoiceColumns = `id,invoice_number,organization_id,organization_subscription_id,plan_version_id,coupon_id,
	invoice_type,status,subtotal::text,discount_amount::text,tax_amount::text,total_amount::text,currency,period_start,period_end,
	due_at,paid_at,failed_at,COALESCE(payment_provider,''),COALESCE(provider_payment_reference,''),ledger_journal_id,
	plan_snapshot,created_by,created_at,updated_at`

func (s *Store) SubscriptionInvoiceByID(ctx context.Context, invoiceID string) (domain.SubscriptionInvoice, error) {
	return scanSubscriptionInvoice(s.pool.QueryRow(ctx, `SELECT `+subscriptionInvoiceColumns+` FROM subscription_invoice WHERE id=$1`, invoiceID))
}

func (s *Store) PaySubscriptionInvoice(ctx context.Context, request SubscriptionPaymentRequest) (domain.SubscriptionInvoice, error) {
	if request.InvoiceID == "" || request.PaymentProvider == "" || request.ProviderPaymentReference == "" || request.IdempotencyKey == "" {
		return domain.SubscriptionInvoice{}, errors.New("invoice, payment provider, reference, and idempotency key are required")
	}
	fingerprint := subscriptionFingerprint(request.InvoiceID, request.PaymentProvider, request.ProviderPaymentReference)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	defer tx.Rollback(ctx)
	invoice, err := scanSubscriptionInvoice(tx.QueryRow(ctx, `SELECT `+subscriptionInvoiceColumns+` FROM subscription_invoice WHERE id=$1 FOR UPDATE`, request.InvoiceID))
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	if invoice.Status == "PAID" {
		if invoice.PaymentProvider != request.PaymentProvider || invoice.ProviderPaymentReference != request.ProviderPaymentReference {
			return domain.SubscriptionInvoice{}, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.SubscriptionInvoice{}, err
		}
		return invoice, nil
	}
	if invoice.Status != "OPEN" && invoice.Status != "FAILED" {
		return domain.SubscriptionInvoice{}, ErrSubscriptionState
	}
	journalID, err := postSubscriptionJournalTx(ctx, tx, invoice, request, fingerprint)
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE subscription_invoice SET status='PAID',paid_at=now(),failed_at=NULL,payment_provider=$2,
		provider_payment_reference=$3,ledger_journal_id=$4,updated_at=now() WHERE id=$1`, invoice.ID, request.PaymentProvider,
		request.ProviderPaymentReference, journalID)
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='ACTIVE',grace_period_end=NULL,
		metadata=metadata-'awaiting_initial_payment',version=version+1,updated_at=now()
		,current_period_start=CASE WHEN $2='RENEWAL' THEN $3 ELSE current_period_start END
		,current_period_end=CASE WHEN $2='RENEWAL' THEN $4 ELSE current_period_end END
		WHERE id=$1 AND status IN ('PAST_DUE','GRACE_PERIOD')`, invoice.OrganizationSubscriptionID, invoice.InvoiceType,
		invoice.PeriodStart, invoice.PeriodEnd)
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	if err = insertSubscriptionEvent(ctx, tx, invoice.OrganizationID, invoice.OrganizationSubscriptionID, "INVOICE_PAID", "", "ACTIVE",
		request.CreatedBy, request.IdempotencyKey, map[string]any{"request_fingerprint": fingerprint, "invoice_id": invoice.ID,
			"amount": invoice.TotalAmount.String(), "currency": invoice.Currency, "ledger_journal_id": journalID}); err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "subscription.invoice_paid", "subscription_invoice", invoice.ID,
		map[string]any{"amount": invoice.TotalAmount.String(), "currency": invoice.Currency, "ledger_journal_id": journalID}); err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	return s.SubscriptionInvoiceByID(ctx, invoice.ID)
}

func (s *Store) FailSubscriptionInvoice(ctx context.Context, invoiceID, idempotencyKey string, actor *string) (domain.SubscriptionInvoice, error) {
	if invoiceID == "" || idempotencyKey == "" {
		return domain.SubscriptionInvoice{}, errors.New("invoice and idempotency key are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	defer tx.Rollback(ctx)
	invoice, err := scanSubscriptionInvoice(tx.QueryRow(ctx, `SELECT `+subscriptionInvoiceColumns+` FROM subscription_invoice WHERE id=$1 FOR UPDATE`, invoiceID))
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	fingerprint := subscriptionFingerprint(invoiceID, "FAILED")
	if invoice.Status == "OPEN" {
		_, err = tx.Exec(ctx, `UPDATE subscription_invoice SET status='FAILED',failed_at=now(),updated_at=now() WHERE id=$1`, invoiceID)
		if err != nil {
			return domain.SubscriptionInvoice{}, err
		}
		var graceDays int
		if err = tx.QueryRow(ctx, `SELECT grace_period_days FROM plan_version WHERE id=$1`, invoice.PlanVersionID).Scan(&graceDays); err != nil {
			return domain.SubscriptionInvoice{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='PAST_DUE',grace_period_end=GREATEST(current_period_end,now())+($2||' days')::interval,
			version=version+1,updated_at=now() WHERE id=$1 AND status IN ('ACTIVE','TRIALING')`, invoice.OrganizationSubscriptionID, graceDays)
		if err != nil {
			return domain.SubscriptionInvoice{}, err
		}
	}
	if invoice.Status != "OPEN" && invoice.Status != "FAILED" {
		return domain.SubscriptionInvoice{}, ErrSubscriptionState
	}
	if err = insertSubscriptionEvent(ctx, tx, invoice.OrganizationID, invoice.OrganizationSubscriptionID, "RENEWAL_PAYMENT_FAILED", "ACTIVE", "PAST_DUE",
		actor, idempotencyKey, map[string]any{"request_fingerprint": fingerprint, "invoice_id": invoiceID}); err != nil {
		if !isUniqueViolation(err) {
			return domain.SubscriptionInvoice{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	return s.SubscriptionInvoiceByID(ctx, invoiceID)
}

func (s *Store) ProcessSubscriptionLifecycle(ctx context.Context, now time.Time, limit int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,organization_id,plan_version_id,status,cancel_at_period_end,current_period_end,grace_period_end,pending_plan_version_id
		FROM organization_subscription WHERE
		(status IN ('TRIALING','ACTIVE') AND current_period_end<=$1) OR
		(status='PAST_DUE' AND (
		  (COALESCE(metadata->>'awaiting_initial_payment','false')='true' AND grace_period_end<=$1) OR
		  (COALESCE(metadata->>'awaiting_initial_payment','false')<>'true' AND current_period_end<=$1)
		)) OR
		(status='GRACE_PERIOD' AND grace_period_end<=$1)
		ORDER BY COALESCE(grace_period_end,current_period_end),id FOR UPDATE SKIP LOCKED LIMIT $2`, now.UTC(), clamp(limit))
	if err != nil {
		return 0, err
	}
	type dueSubscription struct {
		id, organizationID, planVersionID, status string
		cancelAtEnd                               bool
		periodEnd                                 time.Time
		graceEnd                                  *time.Time
		pending                                   *string
	}
	items := make([]dueSubscription, 0)
	for rows.Next() {
		var item dueSubscription
		if err = rows.Scan(&item.id, &item.organizationID, &item.planVersionID, &item.status, &item.cancelAtEnd, &item.periodEnd, &item.graceEnd, &item.pending); err != nil {
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
		key := "lifecycle:" + item.id + ":" + strings.ToLower(item.status) + ":" + now.UTC().Format("200601021504")
		switch {
		case item.status == "PAST_DUE":
			var awaitingInitial bool
			err = tx.QueryRow(ctx, `SELECT COALESCE(metadata->>'awaiting_initial_payment','false')='true' FROM organization_subscription WHERE id=$1`, item.id).Scan(&awaitingInitial)
			if err != nil {
				break
			}
			if awaitingInitial && item.graceEnd != nil && !item.graceEnd.After(now.UTC()) {
				_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='EXPIRED',ended_at=$2,version=version+1,updated_at=now() WHERE id=$1`, item.id, now.UTC())
				if err == nil {
					err = insertSubscriptionEvent(ctx, tx, item.organizationID, item.id, "INITIAL_PAYMENT_EXPIRED", "PAST_DUE", "EXPIRED", nil, key, map[string]any{})
				}
				if err == nil {
					_, err = createFreeSubscriptionTx(ctx, tx, item.organizationID, key+":free", nil)
				}
			} else if !awaitingInitial {
				_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='GRACE_PERIOD',version=version+1,updated_at=now() WHERE id=$1`, item.id)
				if err == nil {
					err = insertSubscriptionEvent(ctx, tx, item.organizationID, item.id, "GRACE_PERIOD_STARTED", "PAST_DUE", "GRACE_PERIOD", nil, key, map[string]any{})
				}
			}
		case item.status == "GRACE_PERIOD":
			_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='EXPIRED',ended_at=$2,version=version+1,updated_at=now() WHERE id=$1`, item.id, now.UTC())
			if err == nil {
				err = insertSubscriptionEvent(ctx, tx, item.organizationID, item.id, "SUBSCRIPTION_EXPIRED", "GRACE_PERIOD", "EXPIRED", nil, key, map[string]any{})
			}
			if err == nil {
				_, err = createFreeSubscriptionTx(ctx, tx, item.organizationID, key+":free", nil)
			}
		case item.cancelAtEnd:
			_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='CANCELED',ended_at=$2,version=version+1,updated_at=now() WHERE id=$1`, item.id, now.UTC())
			if err == nil {
				err = insertSubscriptionEvent(ctx, tx, item.organizationID, item.id, "CANCELED_AT_PERIOD_END", item.status, "CANCELED", nil, key, map[string]any{})
			}
			if err == nil {
				_, err = createFreeSubscriptionTx(ctx, tx, item.organizationID, key+":free", nil)
			}
		case item.pending != nil:
			_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='EXPIRED',ended_at=$2,version=version+1,updated_at=now() WHERE id=$1`, item.id, now.UTC())
			if err == nil {
				_, err = createPendingSubscriptionTx(ctx, tx, item.organizationID, *item.pending, key, nil)
			}
		default:
			var version domain.PlanVersion
			var fee string
			var metadata []byte
			err = tx.QueryRow(ctx, `SELECT `+planVersionColumns+` FROM plan_version WHERE id=$1 FOR SHARE`, item.planVersionID).
				Scan(&version.ID, &version.SubscriptionPlanID, &version.Version, &version.Status, &version.BillingInterval,
					&fee, &version.Currency, &version.TrialDays, &version.GracePeriodDays, &version.TokenBillingMode,
					&version.EnterpriseContract, &version.EffectiveAt, &version.FrozenAt, &version.RetiredAt, &metadata,
					&version.CreatedBy, &version.CreatedAt)
			if err != nil {
				break
			}
			version.SubscriptionFee, err = parseStoredDecimal(fee, "plan_version.subscription_fee")
			if err != nil {
				break
			}
			nextEnd, periodErr := subscriptionPeriod(item.periodEnd, version.BillingInterval, nil)
			if periodErr != nil {
				err = periodErr
				break
			}
			if decimalPositive(fee) {
				_, err = createSubscriptionInvoiceTx(ctx, tx, item.organizationID, item.id, version, nil, "RENEWAL",
					item.periodEnd, nextEnd, key, subscriptionFingerprint(item.id, key, fee), nil)
				if err == nil {
					_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='PAST_DUE',grace_period_end=$2::timestamptz+make_interval(days=>$3::int),
						version=version+1,updated_at=now() WHERE id=$1`, item.id, item.periodEnd, version.GracePeriodDays)
				}
				if err == nil {
					err = insertSubscriptionEvent(ctx, tx, item.organizationID, item.id, "RENEWAL_INVOICE_CREATED", item.status,
						"PAST_DUE", nil, key+":event", map[string]any{"period_start": item.periodEnd, "period_end": nextEnd})
				}
			} else {
				_, err = tx.Exec(ctx, `UPDATE organization_subscription SET status='ACTIVE',current_period_start=$2,current_period_end=$3,
					grace_period_end=NULL,version=version+1,updated_at=now() WHERE id=$1`, item.id, item.periodEnd, nextEnd)
				if err == nil {
					err = insertSubscriptionEvent(ctx, tx, item.organizationID, item.id, "ZERO_FEE_RENEWED", item.status,
						"ACTIVE", nil, key+":event", map[string]any{"period_start": item.periodEnd, "period_end": nextEnd})
				}
			}
			if err == nil && item.status == "TRIALING" {
				_, err = tx.Exec(ctx, `UPDATE trial SET status='CONVERTED',converted_at=now() WHERE organization_subscription_id=$1 AND status='ACTIVE'`, item.id)
			}
		}
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(items), nil
}

func (s *Store) ListSubscriptionEvents(ctx context.Context, organizationID string, limit, offset int) ([]domain.SubscriptionEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,organization_id,organization_subscription_id,event_type,COALESCE(from_status,''),
		COALESCE(to_status,''),actor_id,payload,created_at FROM subscription_event WHERE ($1='' OR organization_id=$1)
		ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, organizationID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SubscriptionEvent, 0)
	for rows.Next() {
		var item domain.SubscriptionEvent
		var payload []byte
		if err = rows.Scan(&item.ID, &item.OrganizationID, &item.OrganizationSubscriptionID, &item.EventType,
			&item.FromStatus, &item.ToStatus, &item.ActorID, &payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &item.Payload)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateCoupon(ctx context.Context, coupon domain.Coupon) (domain.Coupon, error) {
	coupon.ID = id.UUID()
	if coupon.Metadata == nil {
		coupon.Metadata = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO coupon(id,code,name,discount_type,percent_bps,fixed_amount,currency,max_redemptions,
		starts_at,expires_at,enabled,metadata,created_by) VALUES($1,upper($2),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		coupon.ID, coupon.Code, coupon.Name, strings.ToUpper(coupon.DiscountType), coupon.PercentBPS,
		decimalPointerString(coupon.FixedAmount), coupon.Currency, coupon.MaxRedemptions, coupon.StartsAt, coupon.ExpiresAt,
		coupon.Enabled, jsonBytes(coupon.Metadata), coupon.CreatedBy)
	if err != nil {
		return domain.Coupon{}, err
	}
	return s.CouponByID(ctx, coupon.ID)
}

func (s *Store) CouponByID(ctx context.Context, couponID string) (domain.Coupon, error) {
	return scanCoupon(s.pool.QueryRow(ctx, `SELECT id,code,name,discount_type,percent_bps,fixed_amount::text,currency,max_redemptions,
		redeemed_count,starts_at,expires_at,enabled,metadata,created_by,created_at,updated_at FROM coupon WHERE id=$1`, couponID))
}

func (s *Store) ListCoupons(ctx context.Context) ([]domain.Coupon, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,code,name,discount_type,percent_bps,fixed_amount::text,currency,max_redemptions,
		redeemed_count,starts_at,expires_at,enabled,metadata,created_by,created_at,updated_at FROM coupon ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Coupon, 0)
	for rows.Next() {
		item, scanErr := scanCoupon(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanCoupon(row pgx.Row) (domain.Coupon, error) {
	var out domain.Coupon
	var fixed *string
	var metadata []byte
	err := row.Scan(&out.ID, &out.Code, &out.Name, &out.DiscountType, &out.PercentBPS, &fixed, &out.Currency,
		&out.MaxRedemptions, &out.RedeemedCount, &out.StartsAt, &out.ExpiresAt, &out.Enabled, &metadata,
		&out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		if fixed != nil {
			value, parseErr := parseStoredDecimal(*fixed, "promotion_rule.fixed_amount")
			if parseErr != nil {
				return out, parseErr
			}
			out.FixedAmount = &value
		}
		_ = json.Unmarshal(metadata, &out.Metadata)
	}
	return out, err
}

func createSubscriptionInvoiceTx(ctx context.Context, tx pgx.Tx, organizationID, subscriptionID string,
	version domain.PlanVersion, couponID *string, invoiceType string, periodStart, periodEnd time.Time,
	idempotencyKey, fingerprint string, actor *string) (domain.SubscriptionInvoice, error) {
	subtotal := version.SubscriptionFee.String()
	discount := "0"
	if couponID != nil {
		var discountType string
		var percent *int
		var fixed *string
		var currency *string
		if err := tx.QueryRow(ctx, `SELECT discount_type,percent_bps,fixed_amount::text,currency FROM coupon WHERE id=$1 FOR UPDATE`, *couponID).
			Scan(&discountType, &percent, &fixed, &currency); err != nil {
			return domain.SubscriptionInvoice{}, err
		}
		if discountType == "PERCENT" && percent != nil {
			discount = multiplyDecimalFraction(subtotal, int64(*percent), 10000)
		} else if discountType == "FIXED" && fixed != nil && currency != nil && *currency == version.Currency {
			discount = decimalMin(*fixed, subtotal)
		} else {
			return domain.SubscriptionInvoice{}, errors.New("coupon currency does not match subscription currency")
		}
	}
	total := subtractDecimal(subtotal, discount)
	invoiceID := id.UUID()
	snapshot := map[string]any{"plan_version_id": version.ID, "version": version.Version, "billing_interval": version.BillingInterval,
		"subscription_fee": subtotal, "currency": version.Currency, "token_billing_mode": "METERED_SEPARATE"}
	invoiceNumber := "MD-SUB-" + time.Now().UTC().Format("20060102") + "-" + strings.ReplaceAll(invoiceID[:12], "-", "")
	invoiceStatus := "OPEN"
	var paidAt any
	if !decimalPositive(total) {
		invoiceStatus = "PAID"
		paidAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, `INSERT INTO subscription_invoice(id,invoice_number,organization_id,organization_subscription_id,
		plan_version_id,coupon_id,invoice_type,status,subtotal,discount_amount,tax_amount,total_amount,currency,
		period_start,period_end,due_at,paid_at,idempotency_key,request_fingerprint,plan_snapshot,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,$12,$13,$14,now(),$15,$16,$17,$18,$19)`, invoiceID,
		invoiceNumber, organizationID, subscriptionID, version.ID, couponID, invoiceType, invoiceStatus, subtotal, discount, total,
		version.Currency, periodStart, periodEnd, paidAt, idempotencyKey, fingerprint, jsonBytes(snapshot), actor)
	if err != nil {
		return domain.SubscriptionInvoice{}, err
	}
	if couponID != nil && decimalPositive(discount) {
		_, err = tx.Exec(ctx, `INSERT INTO subscription_coupon_redemption(id,coupon_id,organization_id,subscription_invoice_id,discount_amount,currency)
			VALUES($1,$2,$3,$4,$5,$6)`, id.UUID(), *couponID, organizationID, invoiceID, discount, version.Currency)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE coupon SET redeemed_count=redeemed_count+1,updated_at=now() WHERE id=$1`, *couponID)
		}
		if err != nil {
			return domain.SubscriptionInvoice{}, err
		}
	}
	return scanSubscriptionInvoice(tx.QueryRow(ctx, `SELECT `+subscriptionInvoiceColumns+` FROM subscription_invoice WHERE id=$1`, invoiceID))
}

func postSubscriptionJournalTx(ctx context.Context, tx pgx.Tx, invoice domain.SubscriptionInvoice,
	request SubscriptionPaymentRequest, fingerprint string) (string, error) {
	journalID := id.UUID()
	_, err := tx.Exec(ctx, `INSERT INTO ledger_journal(id,journal_type,external_key,currency,reference,metadata,created_by,subscription_invoice_id)
		VALUES($1,'SUBSCRIPTION_PAYMENT',$2,$3,$4,$5,$6,$7)`, journalID,
		"subscription-payment:"+invoice.ID+":"+request.IdempotencyKey, invoice.Currency, invoice.InvoiceNumber,
		jsonBytes(map[string]any{"invoice_id": invoice.ID, "request_fingerprint": fingerprint, "billing_stream": "subscription"}),
		request.CreatedBy, invoice.ID)
	if err != nil {
		return "", err
	}
	for _, line := range []journalLine{
		{accountKey: systemAccountKey("subscription-cash", invoice.Currency), side: "DEBIT", amount: invoice.TotalAmount.String(), description: "Subscription payment received"},
		{accountKey: systemAccountKey("subscription-revenue", invoice.Currency), side: "CREDIT", amount: invoice.TotalAmount.String(), description: "Subscription revenue recognized"},
	} {
		var accountID string
		if err = tx.QueryRow(ctx, `SELECT id FROM ledger_account WHERE account_key=$1 AND currency=$2 AND status='ACTIVE'`, line.accountKey, invoice.Currency).Scan(&accountID); err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger_journal_entry(id,journal_id,account_id,currency,entry_side,amount,description)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), journalID, accountID, invoice.Currency, line.side, line.amount, line.description)
		if err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ledger_journal SET status='POSTED',posted_at=now() WHERE id=$1`, journalID)
	return journalID, err
}

func insertSubscriptionEvent(ctx context.Context, tx pgx.Tx, organizationID, subscriptionID, eventType,
	fromStatus, toStatus string, actor *string, idempotencyKey string, payload map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO subscription_event(id,organization_id,organization_subscription_id,event_type,
		from_status,to_status,actor_id,idempotency_key,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id.UUID(), organizationID, nullableSubscriptionID(subscriptionID), eventType, nullString(fromStatus), nullString(toStatus),
		actor, idempotencyKey, jsonBytes(payload))
	return err
}

func nullableSubscriptionID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func lockSubscriptionOrganizationTx(ctx context.Context, tx pgx.Tx, organizationID string) error {
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else {
		return err
	}
}

func createFreeSubscriptionTx(ctx context.Context, tx pgx.Tx, organizationID, idempotencyKey string, actor *string) (string, error) {
	newID := id.UUID()
	_, err := tx.Exec(ctx, `INSERT INTO organization_subscription(id,organization_id,plan_version_id,status,current_period_start,
		current_period_end,idempotency_key,request_fingerprint,metadata,created_by)
		VALUES($1,$2,$3,'ACTIVE',now(),now()+interval '100 years',$4,$5,'{"source":"lifecycle_fallback"}'::jsonb,$6)`,
		newID, organizationID, freePlanVersionID, idempotencyKey, subscriptionFingerprint(organizationID, freePlanVersionID, idempotencyKey), actor)
	if err != nil {
		return "", err
	}
	return newID, insertSubscriptionEvent(ctx, tx, organizationID, newID, "FREE_FALLBACK_STARTED", "", "ACTIVE", actor,
		idempotencyKey+":event", map[string]any{"plan_version_id": freePlanVersionID})
}

func createPendingSubscriptionTx(ctx context.Context, tx pgx.Tx, organizationID, versionID, idempotencyKey string, actor *string) (string, error) {
	var version domain.PlanVersion
	var fee string
	var metadata []byte
	err := tx.QueryRow(ctx, `SELECT `+planVersionColumns+` FROM plan_version WHERE id=$1 AND status='FROZEN' FOR SHARE`, versionID).
		Scan(&version.ID, &version.SubscriptionPlanID, &version.Version, &version.Status, &version.BillingInterval,
			&fee, &version.Currency, &version.TrialDays, &version.GracePeriodDays, &version.TokenBillingMode,
			&version.EnterpriseContract, &version.EffectiveAt, &version.FrozenAt, &version.RetiredAt, &metadata,
			&version.CreatedBy, &version.CreatedAt)
	if err != nil {
		return "", err
	}
	version.SubscriptionFee, err = parseStoredDecimal(fee, "plan_version.subscription_fee")
	if err != nil {
		return "", err
	}
	start := time.Now().UTC()
	end, err := subscriptionPeriod(start, version.BillingInterval, nil)
	if err != nil {
		return "", err
	}
	newID := id.UUID()
	status := "ACTIVE"
	metadataJSON := `{"source":"scheduled_plan_change"}`
	var graceEnd *time.Time
	if decimalPositive(fee) {
		status = "PAST_DUE"
		metadataJSON = `{"source":"scheduled_plan_change","awaiting_initial_payment":true}`
		value := start.AddDate(0, 0, version.GracePeriodDays)
		graceEnd = &value
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_subscription(id,organization_id,plan_version_id,status,current_period_start,
		current_period_end,grace_period_end,idempotency_key,request_fingerprint,metadata,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,
		$10::jsonb,$11)`, newID, organizationID, versionID, status, start, end, graceEnd, idempotencyKey,
		subscriptionFingerprint(organizationID, versionID, idempotencyKey), metadataJSON, actor)
	if err != nil {
		return "", err
	}
	if decimalPositive(fee) {
		_, err = createSubscriptionInvoiceTx(ctx, tx, organizationID, newID, version, nil, "RENEWAL", start, end,
			idempotencyKey, subscriptionFingerprint(organizationID, versionID, idempotencyKey), actor)
	}
	if err == nil {
		err = insertSubscriptionEvent(ctx, tx, organizationID, newID, "SCHEDULED_PLAN_CHANGE_APPLIED", "", status, actor,
			idempotencyKey+":event", map[string]any{"plan_version_id": versionID})
	}
	return newID, err
}

func (s *Store) subscriptionByID(ctx context.Context, subscriptionID string) (domain.OrganizationSubscription, error) {
	out, err := scanOrganizationSubscription(s.pool.QueryRow(ctx, organizationSubscriptionSelect+` WHERE subscription.id=$1`, subscriptionID))
	if err == nil {
		out.Entitlements, err = s.listPlanEntitlements(ctx, out.PlanVersionID)
	}
	return out, err
}

func (s *Store) invoiceBySubscriptionIdempotency(ctx context.Context, subscriptionID, idempotencyKey string) (*domain.SubscriptionInvoice, error) {
	invoice, err := scanSubscriptionInvoice(s.pool.QueryRow(ctx, `SELECT `+subscriptionInvoiceColumns+`
		FROM subscription_invoice WHERE organization_subscription_id=$1 AND idempotency_key=$2`, subscriptionID, idempotencyKey))
	if err != nil {
		return nil, err
	}
	return &invoice, nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func decimalPointerString(value *domain.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func multiplyDecimalFraction(value string, numerator, denominator int64) string {
	decimal, ok := new(big.Rat).SetString(value)
	if !ok || denominator <= 0 {
		return "0"
	}
	decimal.Mul(decimal, new(big.Rat).SetFrac(big.NewInt(numerator), big.NewInt(denominator)))
	return formatRat(decimal)
}

func subtractDecimal(left, right string) string {
	a, okA := new(big.Rat).SetString(left)
	b, okB := new(big.Rat).SetString(right)
	if !okA || !okB {
		return "0"
	}
	a.Sub(a, b)
	if a.Sign() < 0 {
		return "0"
	}
	return formatRat(a)
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
