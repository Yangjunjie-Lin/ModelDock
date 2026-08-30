package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrProvisioningState   = errors.New("provider provisioning state does not allow this operation")
	ErrBindingMode         = errors.New("provider account binding mode conflicts with the existing binding")
	ErrInvalidProvisioning = errors.New("invalid provider account provisioning request")
)

type CreateProviderAccountBindingRequest struct {
	OrganizationID    string
	UserID            string
	ProviderID        string
	ProvisioningMode  string
	ExternalAccountID string
	ExternalProjectID string
	CredentialID      *string
	IdempotencyKey    string
	EnqueueAutomatic  bool
	CreatedBy         *string
}

type ProviderBindingCompletion struct {
	ExternalAccountID string
	ExternalProjectID string
	Metadata          map[string]any
	CredentialID      string
	CredentialName    string
	CredentialType    string
	EncryptedSecret   []byte
	SecretLast4       string
}

const providerBindingSelect = `SELECT b.id,b.organization_id,b.user_id,u.email,b.provider_id,p.name,p.provider_type,
	b.provisioning_mode,b.status,COALESCE(b.external_account_id,''),COALESCE(b.external_project_id,''),b.credential_id,
	b.allocated_amount::text,COALESCE(b.currency,''),b.metadata,b.last_synced_at,b.created_at,b.updated_at
	FROM provider_account_binding b JOIN users u ON u.id=b.user_id JOIN providers p ON p.id=b.provider_id`

func scanProviderBinding(row pgx.Row) (domain.ProviderAccountBinding, error) {
	var value domain.ProviderAccountBinding
	var amount string
	var metadata []byte
	err := row.Scan(&value.ID, &value.OrganizationID, &value.UserID, &value.UserEmail, &value.ProviderID,
		&value.ProviderName, &value.ProviderType, &value.ProvisioningMode, &value.Status, &value.ExternalAccountID,
		&value.ExternalProjectID, &value.CredentialID, &amount, &value.Currency, &metadata, &value.LastSyncedAt,
		&value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	if err == nil {
		value.AllocatedAmount, err = parseStoredDecimal(amount, "provider_account_binding.allocated_amount")
		_ = jsonUnmarshalObject(metadata, &value.Metadata)
	}
	return value, err
}

func (s *Store) ProviderAccountBindingByID(ctx context.Context, bindingID string) (domain.ProviderAccountBinding, error) {
	return scanProviderBinding(s.pool.QueryRow(ctx, providerBindingSelect+` WHERE b.id=$1`, bindingID))
}

func (s *Store) ListProviderAccountBindings(ctx context.Context, organizationID, userID, providerID string, limit, offset int) ([]domain.ProviderAccountBinding, error) {
	query := providerBindingSelect + ` WHERE ($1='' OR b.organization_id::text=$1) AND ($2='' OR b.user_id::text=$2)
		AND ($3='' OR b.provider_id::text=$3) ORDER BY b.created_at DESC,b.id DESC LIMIT $4 OFFSET $5`
	rows, err := s.pool.Query(ctx, query, strings.TrimSpace(organizationID), strings.TrimSpace(userID), strings.TrimSpace(providerID), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProviderAccountBinding, 0)
	for rows.Next() {
		value, scanErr := scanProviderBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) CreateProviderAccountBinding(ctx context.Context, request CreateProviderAccountBindingRequest) (domain.ProviderAccountBinding, *domain.ProviderProvisioningJob, bool, error) {
	request.ProvisioningMode = strings.ToUpper(strings.TrimSpace(request.ProvisioningMode))
	request.ExternalAccountID = strings.TrimSpace(request.ExternalAccountID)
	request.ExternalProjectID = strings.TrimSpace(request.ExternalProjectID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.OrganizationID == "" || request.UserID == "" || request.ProviderID == "" ||
		!validProvisioningMode(request.ProvisioningMode) || len(request.IdempotencyKey) == 0 || len(request.IdempotencyKey) > 200 {
		return domain.ProviderAccountBinding{}, nil, false, ErrInvalidProvisioning
	}
	if request.EnqueueAutomatic && request.ProvisioningMode != "OFFICIAL_ENTERPRISE" && request.ProvisioningMode != "MOCK_ENTERPRISE" {
		return domain.ProviderAccountBinding{}, nil, false, ErrProvisioningState
	}
	if !request.EnqueueAutomatic && request.ExternalAccountID == "" && request.ExternalProjectID == "" && request.CredentialID == nil {
		return domain.ProviderAccountBinding{}, nil, false, ErrInvalidProvisioning
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		providerBindingAdvisoryKey(request.OrganizationID, request.UserID, request.ProviderID)); err != nil {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE')
		AND EXISTS(SELECT 1 FROM providers WHERE id=$3 AND enabled)`, request.OrganizationID, request.UserID, request.ProviderID).Scan(&allowed); err != nil {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	if !allowed {
		return domain.ProviderAccountBinding{}, nil, false, ErrNotFound
	}
	if request.CredentialID != nil {
		var credentialAllowed bool
		ownershipSQL := `SELECT EXISTS(SELECT 1 FROM provider_credentials WHERE id=$1 AND provider_id=$2)`
		if request.ProvisioningMode == "BYOK" {
			ownershipSQL = `SELECT EXISTS(SELECT 1 FROM provider_credentials WHERE id=$1 AND provider_id=$2
				AND credential_owner='CUSTOMER' AND owner_organization_id=$3)`
		}
		args := []any{*request.CredentialID, request.ProviderID}
		if request.ProvisioningMode == "BYOK" {
			args = append(args, request.OrganizationID)
		}
		if err = tx.QueryRow(ctx, ownershipSQL, args...).Scan(&credentialAllowed); err != nil {
			return domain.ProviderAccountBinding{}, nil, false, err
		}
		if !credentialAllowed {
			return domain.ProviderAccountBinding{}, nil, false, ErrNotFound
		}
	}
	var bindingID, existingMode string
	err = tx.QueryRow(ctx, `SELECT id,provisioning_mode FROM provider_account_binding
		WHERE organization_id=$1 AND user_id=$2 AND provider_id=$3 FOR UPDATE`, request.OrganizationID, request.UserID, request.ProviderID).
		Scan(&bindingID, &existingMode)
	replayed := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	if replayed && existingMode != request.ProvisioningMode {
		return domain.ProviderAccountBinding{}, nil, false, ErrBindingMode
	}
	if !replayed {
		bindingID = id.UUID()
		status := "ACTIVE"
		if request.EnqueueAutomatic {
			status = "PENDING"
		}
		_, err = tx.Exec(ctx, `INSERT INTO provider_account_binding(id,organization_id,user_id,provider_id,provisioning_mode,status,
			external_account_id,external_project_id,credential_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, bindingID,
			request.OrganizationID, request.UserID, request.ProviderID, request.ProvisioningMode, status,
			nullString(request.ExternalAccountID), nullString(request.ExternalProjectID), request.CredentialID)
	} else if !request.EnqueueAutomatic {
		_, err = tx.Exec(ctx, `UPDATE provider_account_binding SET status='ACTIVE',external_account_id=COALESCE(NULLIF($2,''),external_account_id),
			external_project_id=COALESCE(NULLIF($3,''),external_project_id),credential_id=COALESCE($4,credential_id),updated_at=now()
			WHERE id=$1`, bindingID, request.ExternalAccountID, request.ExternalProjectID, request.CredentialID)
	}
	if err != nil {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	var job *domain.ProviderProvisioningJob
	if request.EnqueueAutomatic {
		jobValue, jobReplay, createErr := createProviderProvisioningJobTx(ctx, tx, bindingID, nil, "ENSURE_BINDING",
			"ensure-binding:"+bindingID, nil, "", request.CreatedBy)
		if createErr != nil {
			return domain.ProviderAccountBinding{}, nil, false, createErr
		}
		replayed = replayed || jobReplay
		job = &jobValue
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "provider_account.binding_created", "provider_account_binding", bindingID,
		map[string]any{"organization_id": request.OrganizationID, "user_id": request.UserID, "provider_id": request.ProviderID,
			"mode": request.ProvisioningMode, "automatic": request.EnqueueAutomatic}); err != nil {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderAccountBinding{}, nil, false, err
	}
	binding, err := s.ProviderAccountBindingByID(ctx, bindingID)
	return binding, job, replayed, err
}

func validProvisioningMode(value string) bool {
	return value == "OFFICIAL_ENTERPRISE" || value == "BYOK" || value == "MANUAL" || value == "MOCK_ENTERPRISE"
}

func providerBindingAdvisoryKey(organizationID, userID, providerID string) string {
	digest := sha256.Sum256([]byte(organizationID + "\x00" + userID + "\x00" + providerID))
	return hex.EncodeToString(digest[:])
}

const providerJobSelect = `SELECT j.id,j.binding_id,j.recharge_order_id,b.organization_id,b.user_id,b.provider_id,p.name,p.provider_type,
	j.operation,j.idempotency_key,j.status,j.amount::text,COALESCE(j.currency,''),j.attempts,j.max_attempts,j.available_at,
	j.locked_at,j.locked_until,COALESCE(j.locked_by,''),COALESCE(j.claim_token,''),COALESCE(j.external_reference,''),
	COALESCE(j.error_code,''),COALESCE(j.error_detail,''),j.result,j.created_by,j.created_at,j.updated_at,j.completed_at
	FROM provider_provisioning_job j JOIN provider_account_binding b ON b.id=j.binding_id JOIN providers p ON p.id=b.provider_id`

func scanProviderJob(row pgx.Row) (domain.ProviderProvisioningJob, error) {
	var value domain.ProviderProvisioningJob
	var amount *string
	var result []byte
	err := row.Scan(&value.ID, &value.BindingID, &value.RechargeOrderID, &value.OrganizationID, &value.UserID, &value.ProviderID,
		&value.ProviderName, &value.ProviderType, &value.Operation, &value.IdempotencyKey, &value.Status, &amount, &value.Currency,
		&value.Attempts, &value.MaxAttempts, &value.AvailableAt, &value.LockedAt, &value.LockedUntil, &value.LockedBy, &value.ClaimToken,
		&value.ExternalReference, &value.ErrorCode, &value.ErrorDetail, &result, &value.CreatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	if err == nil && amount != nil {
		parsed, parseErr := parseStoredDecimal(*amount, "provider_provisioning_job.amount")
		if parseErr != nil {
			return value, parseErr
		}
		value.Amount = &parsed
	}
	if err == nil {
		_ = jsonUnmarshalObject(result, &value.Result)
	}
	return value, err
}

func jsonUnmarshalObject(raw []byte, target *map[string]any) error {
	*target = map[string]any{}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}

func createProviderProvisioningJobTx(ctx context.Context, tx pgx.Tx, bindingID string, rechargeOrderID *string,
	operation, idempotencyKey string, amount *domain.Decimal, currency string, actor *string) (domain.ProviderProvisioningJob, bool, error) {
	var existingID string
	err := tx.QueryRow(ctx, `SELECT id FROM provider_provisioning_job WHERE binding_id=$1 AND idempotency_key=$2`, bindingID, idempotencyKey).Scan(&existingID)
	if err == nil {
		return domain.ProviderProvisioningJob{ID: existingID, BindingID: bindingID, IdempotencyKey: idempotencyKey}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderProvisioningJob{}, false, err
	}
	jobID := id.UUID()
	var amountValue any
	if amount != nil {
		amountValue = amount.String()
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_provisioning_job(id,binding_id,recharge_order_id,operation,idempotency_key,
		amount,currency,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, jobID, bindingID, rechargeOrderID, operation,
		idempotencyKey, amountValue, nullString(currency), actor)
	return domain.ProviderProvisioningJob{ID: jobID, BindingID: bindingID, RechargeOrderID: rechargeOrderID,
		Operation: operation, IdempotencyKey: idempotencyKey, Status: "PENDING", Amount: amount, Currency: currency}, false, err
}

func (s *Store) ListProviderProvisioningJobs(ctx context.Context, organizationID, userID, status string, limit, offset int) ([]domain.ProviderProvisioningJob, error) {
	rows, err := s.pool.Query(ctx, providerJobSelect+` WHERE ($1='' OR b.organization_id::text=$1) AND ($2='' OR b.user_id::text=$2)
		AND ($3='' OR j.status=$3) ORDER BY j.created_at DESC,j.id DESC LIMIT $4 OFFSET $5`, strings.TrimSpace(organizationID),
		strings.TrimSpace(userID), strings.ToUpper(strings.TrimSpace(status)), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProviderProvisioningJob, 0)
	for rows.Next() {
		value, scanErr := scanProviderJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) ClaimProviderProvisioningJobs(ctx context.Context, workerID string, lease time.Duration, limit int) ([]domain.ProviderProvisioningJob, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("provider provisioning worker ID is required")
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id FROM provider_provisioning_job WHERE
		(status IN ('PENDING','RETRYABLE') AND available_at<=now()) OR (status='PROCESSING' AND locked_until<now())
		ORDER BY available_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT $1`, clamp(limit))
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var jobID string
		if err = rows.Scan(&jobID); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, jobID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.ProviderProvisioningJob, 0, len(ids))
	for _, jobID := range ids {
		claimToken := id.UUID()
		if _, err = tx.Exec(ctx, `UPDATE provider_provisioning_job SET status='PROCESSING',attempts=attempts+1,locked_at=now(),
			locked_until=now()+make_interval(secs=>$2),locked_by=$3,claim_token=$4,error_code=NULL,error_detail=NULL,updated_at=now() WHERE id=$1`,
			jobID, int64(lease.Seconds()), workerID, claimToken); err != nil {
			return nil, err
		}
		job, scanErr := scanProviderJob(tx.QueryRow(ctx, providerJobSelect+` WHERE j.id=$1`, jobID))
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, job)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) SaveProviderBindingProvisioned(ctx context.Context, jobID, claimToken string, completion ProviderBindingCompletion) (domain.ProviderAccountBinding, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderAccountBinding{}, err
	}
	defer tx.Rollback(ctx)
	var bindingID, providerID, organizationID, userID string
	var credentialID *string
	err = tx.QueryRow(ctx, `SELECT b.id,b.provider_id,b.organization_id,b.user_id,b.credential_id FROM provider_provisioning_job j
		JOIN provider_account_binding b ON b.id=j.binding_id WHERE j.id=$1 AND j.status='PROCESSING' AND j.claim_token=$2 FOR UPDATE OF j,b`,
		jobID, claimToken).Scan(&bindingID, &providerID, &organizationID, &userID, &credentialID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderAccountBinding{}, ErrProvisioningState
	}
	if err != nil {
		return domain.ProviderAccountBinding{}, err
	}
	if credentialID == nil && len(completion.EncryptedSecret) > 0 {
		newCredentialID := completion.CredentialID
		if newCredentialID == "" {
			newCredentialID = id.UUID()
		}
		credentialType := completion.CredentialType
		if credentialType == "" {
			credentialType = "api_key"
		}
		displayName := completion.CredentialName
		if displayName == "" {
			displayName = "RelayDock provisioned service account"
		}
		_, err = tx.Exec(ctx, `INSERT INTO provider_credentials(id,provider_id,name,credential_type,encrypted_secret,secret_last4,
			organization_id,project_id,status,priority,weight,max_concurrency,current_health,credential_owner,member_filters)
			VALUES($1,$2,$3,$4,$5,$6,$7,NULL,'ACTIVE',0,100,10,'UNKNOWN','PLATFORM',$8)`, newCredentialID, providerID,
			displayName, credentialType, completion.EncryptedSecret, completion.SecretLast4, organizationID, jsonBytes([]string{userID}))
		if err != nil {
			return domain.ProviderAccountBinding{}, err
		}
		groupID := id.UUID()
		if err = tx.QueryRow(ctx, `INSERT INTO credential_groups(id,provider_id,name,description) VALUES($1,$2,'RelayDock Provisioned',
			'Official enterprise/project credentials created by the configured Provisioner') ON CONFLICT(provider_id,name)
			DO UPDATE SET updated_at=credential_groups.updated_at RETURNING id`, groupID, providerID).Scan(&groupID); err != nil {
			return domain.ProviderAccountBinding{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO credential_group_members(group_id,credential_id,weight,priority) VALUES($1,$2,100,0)
			ON CONFLICT(group_id,credential_id) DO NOTHING`, groupID, newCredentialID); err != nil {
			return domain.ProviderAccountBinding{}, err
		}
		credentialID = &newCredentialID
	}
	_, err = tx.Exec(ctx, `UPDATE provider_account_binding SET status='ACTIVE',external_account_id=COALESCE(NULLIF($2,''),external_account_id),
		external_project_id=COALESCE(NULLIF($3,''),external_project_id),credential_id=COALESCE($4,credential_id),
		metadata=metadata||$5::jsonb,last_synced_at=now(),updated_at=now() WHERE id=$1`, bindingID, completion.ExternalAccountID,
		completion.ExternalProjectID, credentialID, jsonBytes(paymentObject(completion.Metadata)))
	if err != nil {
		return domain.ProviderAccountBinding{}, err
	}
	if err = writeAuditTx(ctx, tx, nil, "provider_account.binding_provisioned", "provider_account_binding", bindingID,
		map[string]any{"provider_id": providerID, "organization_id": organizationID, "user_id": userID,
			"external_project_id": completion.ExternalProjectID, "credential_created": credentialID != nil}); err != nil {
		return domain.ProviderAccountBinding{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ProviderAccountBinding{}, err
	}
	return s.ProviderAccountBindingByID(ctx, bindingID)
}

func (s *Store) CompleteProviderProvisioningJob(ctx context.Context, jobID, claimToken, externalReference string, result map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var operation, bindingID string
	var rechargeOrderID *string
	var amount *string
	var currency *string
	err = tx.QueryRow(ctx, `SELECT operation,binding_id,recharge_order_id,amount::text,currency FROM provider_provisioning_job
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2 FOR UPDATE`, jobID, claimToken).
		Scan(&operation, &bindingID, &rechargeOrderID, &amount, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProvisioningState
	}
	if err != nil {
		return err
	}
	if operation == "ALLOCATE_CREDIT" {
		if rechargeOrderID == nil || amount == nil || currency == nil || strings.TrimSpace(externalReference) == "" {
			return ErrProvisioningState
		}
		var inserted bool
		err = tx.QueryRow(ctx, `INSERT INTO provider_credit_allocation(id,binding_id,job_id,recharge_order_id,amount,currency,
			external_reference,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(job_id) DO NOTHING RETURNING true`, id.UUID(),
			bindingID, jobID, *rechargeOrderID, *amount, *currency, externalReference, jsonBytes(paymentObject(result))).Scan(&inserted)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if inserted {
			tag, updateErr := tx.Exec(ctx, `UPDATE provider_account_binding SET allocated_amount=allocated_amount+$2::numeric,
				currency=COALESCE(currency,$3),updated_at=now() WHERE id=$1 AND (currency IS NULL OR currency=$3)`,
				bindingID, *amount, *currency)
			if updateErr != nil {
				return updateErr
			}
			if tag.RowsAffected() == 0 {
				return errors.New("provider allocation currency conflicts with binding currency")
			}
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE provider_provisioning_job SET status='SUCCEEDED',external_reference=NULLIF($3,''),result=$4,
		completed_at=now(),locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,updated_at=now()
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2`, jobID, claimToken, externalReference, jsonBytes(paymentObject(result)))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProvisioningState
	}
	if err = writeAuditTx(ctx, tx, nil, "provider_account.job_succeeded", "provider_provisioning_job", jobID,
		map[string]any{"operation": operation, "external_reference": externalReference}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailProviderProvisioningJob(ctx context.Context, jobID, claimToken, code, detail string, retryAt time.Time, terminal bool) error {
	status := "RETRYABLE"
	if terminal {
		status = "FAILED"
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	tag, err := s.pool.Exec(ctx, `UPDATE provider_provisioning_job SET status=$3,error_code=$4,error_detail=$5,
		available_at=$6,completed_at=CASE WHEN $3='FAILED' THEN now() ELSE NULL END,locked_at=NULL,locked_until=NULL,
		locked_by=NULL,claim_token=NULL,updated_at=now() WHERE id=$1 AND status='PROCESSING' AND claim_token=$2`,
		jobID, claimToken, status, strings.TrimSpace(code), strings.TrimSpace(detail), retryAt.UTC())
	if err == nil && tag.RowsAffected() == 0 {
		return ErrProvisioningState
	}
	return err
}

func (s *Store) RetryProviderProvisioningJob(ctx context.Context, jobID string, actor *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE provider_provisioning_job SET status='PENDING',attempts=0,available_at=now(),error_code=NULL,
		error_detail=NULL,completed_at=NULL,locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,updated_at=now()
		WHERE id=$1 AND status IN ('FAILED','ACTION_REQUIRED')`, jobID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProvisioningState
	}
	if err = writeAuditTx(ctx, tx, actor, "provider_account.job_retried", "provider_provisioning_job", jobID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func enqueueProviderAllocationTx(ctx context.Context, tx pgx.Tx, order domain.RechargeOrder) error {
	if order.TargetProviderID == nil || order.TargetProvisioningMode == nil || order.CreatedBy == nil {
		return nil
	}
	bindingID := id.UUID()
	err := tx.QueryRow(ctx, `INSERT INTO provider_account_binding(id,organization_id,user_id,provider_id,provisioning_mode,status)
		VALUES($1,$2,$3,$4,$5,'PENDING') ON CONFLICT(organization_id,user_id,provider_id) DO UPDATE SET updated_at=now()
		WHERE provider_account_binding.provisioning_mode=EXCLUDED.provisioning_mode RETURNING id`, bindingID, order.OrganizationID,
		*order.CreatedBy, *order.TargetProviderID, *order.TargetProvisioningMode).Scan(&bindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBindingMode
	}
	if err != nil {
		return err
	}
	_, _, err = createProviderProvisioningJobTx(ctx, tx, bindingID, &order.ID, "ALLOCATE_CREDIT", "recharge:"+order.ID,
		&order.Amount, order.Currency, order.CreatedBy)
	return err
}
