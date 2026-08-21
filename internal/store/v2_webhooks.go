package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func scanWebhookEndpoint(row pgx.Row) (domain.WebhookEndpoint, error) {
	var endpoint domain.WebhookEndpoint
	var eventTypes []byte
	err := row.Scan(&endpoint.ID, &endpoint.OrganizationID, &endpoint.ProjectID, &endpoint.Name, &endpoint.URL,
		&endpoint.EncryptedSecret, &endpoint.SecretLast4, &eventTypes, &endpoint.Enabled, &endpoint.CreatedAt,
		&endpoint.UpdatedAt, &endpoint.LastDeliveryAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return endpoint, ErrNotFound
	}
	if err != nil {
		return endpoint, err
	}
	_ = json.Unmarshal(eventTypes, &endpoint.EventTypes)
	if endpoint.EventTypes == nil {
		endpoint.EventTypes = []string{}
	}
	return endpoint, nil
}

const webhookEndpointColumns = `id,organization_id,project_id,name,url,encrypted_secret,secret_last4,event_types,enabled,created_at,updated_at,last_delivery_at`

func (s *Store) WebhookEndpointByID(ctx context.Context, endpointID string) (domain.WebhookEndpoint, error) {
	return scanWebhookEndpoint(s.pool.QueryRow(ctx, `SELECT `+webhookEndpointColumns+` FROM webhook_endpoints WHERE id=$1`, endpointID))
}

func (s *Store) ListWebhookEndpoints(ctx context.Context, projectID string) ([]domain.WebhookEndpoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+webhookEndpointColumns+` FROM webhook_endpoints WHERE project_id=$1 ORDER BY created_at,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.WebhookEndpoint, 0)
	for rows.Next() {
		endpoint, err := scanWebhookEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, endpoint)
	}
	return out, rows.Err()
}

func (s *Store) CreateWebhookEndpoint(ctx context.Context, endpoint domain.WebhookEndpoint) (domain.WebhookEndpoint, error) {
	endpoint.ID = id.UUID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WebhookEndpoint{}, err
	}
	defer tx.Rollback(ctx)
	if endpoint.OrganizationID == "" {
		if err := tx.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, endpoint.ProjectID).Scan(&endpoint.OrganizationID); errors.Is(err, pgx.ErrNoRows) {
			return domain.WebhookEndpoint{}, ErrNotFound
		} else if err != nil {
			return domain.WebhookEndpoint{}, err
		}
	}
	if len(endpoint.EncryptedSecret) == 0 {
		return domain.WebhookEndpoint{}, errors.New("encrypted webhook signing secret is required")
	}
	if _, err = effectivePlanVersionTx(ctx, tx, endpoint.OrganizationID); err != nil {
		return domain.WebhookEndpoint{}, err
	}
	var activeWebhooks int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoints WHERE organization_id=$1 AND enabled`, endpoint.OrganizationID).Scan(&activeWebhooks); err != nil {
		return domain.WebhookEndpoint{}, err
	}
	if err = enforceIntegerEntitlementTx(ctx, tx, endpoint.OrganizationID, "webhook_count", activeWebhooks, 1); err != nil {
		return domain.WebhookEndpoint{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_endpoints(id,organization_id,project_id,name,url,encrypted_secret,secret_last4,event_types,enabled)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, endpoint.ID, endpoint.OrganizationID, endpoint.ProjectID, endpoint.Name,
		endpoint.URL, endpoint.EncryptedSecret, endpoint.SecretLast4, jsonBytes(endpoint.EventTypes), endpoint.Enabled)
	if err != nil {
		return domain.WebhookEndpoint{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WebhookEndpoint{}, err
	}
	return s.WebhookEndpointByID(ctx, endpoint.ID)
}

func (s *Store) UpdateWebhookEndpoint(ctx context.Context, endpoint domain.WebhookEndpoint, replaceSecret bool) (domain.WebhookEndpoint, error) {
	if endpoint.Enabled {
		tx, beginErr := s.pool.Begin(ctx)
		if beginErr != nil {
			return domain.WebhookEndpoint{}, beginErr
		}
		defer tx.Rollback(ctx)
		var currentEnabled bool
		if beginErr = tx.QueryRow(ctx, `SELECT enabled FROM webhook_endpoints WHERE id=$1 AND project_id=$2 FOR UPDATE`, endpoint.ID, endpoint.ProjectID).Scan(&currentEnabled); errors.Is(beginErr, pgx.ErrNoRows) {
			return domain.WebhookEndpoint{}, ErrNotFound
		} else if beginErr != nil {
			return domain.WebhookEndpoint{}, beginErr
		}
		if !currentEnabled {
			if _, beginErr = effectivePlanVersionTx(ctx, tx, endpoint.OrganizationID); beginErr != nil {
				return domain.WebhookEndpoint{}, beginErr
			}
			var activeWebhooks int64
			if beginErr = tx.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoints WHERE organization_id=$1 AND enabled`, endpoint.OrganizationID).Scan(&activeWebhooks); beginErr != nil {
				return domain.WebhookEndpoint{}, beginErr
			}
			if beginErr = enforceIntegerEntitlementTx(ctx, tx, endpoint.OrganizationID, "webhook_count", activeWebhooks, 1); beginErr != nil {
				return domain.WebhookEndpoint{}, beginErr
			}
		}
		if replaceSecret {
			if len(endpoint.EncryptedSecret) == 0 {
				return domain.WebhookEndpoint{}, errors.New("encrypted webhook signing secret is required")
			}
			_, beginErr = tx.Exec(ctx, `UPDATE webhook_endpoints SET name=$3,url=$4,encrypted_secret=$5,secret_last4=$6,event_types=$7,enabled=true,updated_at=now()
				WHERE id=$1 AND project_id=$2`, endpoint.ID, endpoint.ProjectID, endpoint.Name, endpoint.URL, endpoint.EncryptedSecret,
				endpoint.SecretLast4, jsonBytes(endpoint.EventTypes))
		} else {
			_, beginErr = tx.Exec(ctx, `UPDATE webhook_endpoints SET name=$3,url=$4,event_types=$5,enabled=true,updated_at=now()
				WHERE id=$1 AND project_id=$2`, endpoint.ID, endpoint.ProjectID, endpoint.Name, endpoint.URL, jsonBytes(endpoint.EventTypes))
		}
		if beginErr != nil {
			return domain.WebhookEndpoint{}, beginErr
		}
		if beginErr = tx.Commit(ctx); beginErr != nil {
			return domain.WebhookEndpoint{}, beginErr
		}
		return s.WebhookEndpointByID(ctx, endpoint.ID)
	}
	var err error
	var affected int64
	if replaceSecret {
		if len(endpoint.EncryptedSecret) == 0 {
			return domain.WebhookEndpoint{}, errors.New("encrypted webhook signing secret is required")
		}
		commandTag, execErr := s.pool.Exec(ctx, `UPDATE webhook_endpoints SET name=$3,url=$4,encrypted_secret=$5,secret_last4=$6,event_types=$7,enabled=$8,updated_at=now()
			WHERE id=$1 AND project_id=$2`, endpoint.ID, endpoint.ProjectID, endpoint.Name, endpoint.URL, endpoint.EncryptedSecret,
			endpoint.SecretLast4, jsonBytes(endpoint.EventTypes), endpoint.Enabled)
		err = execErr
		affected = commandTag.RowsAffected()
	} else {
		commandTag, execErr := s.pool.Exec(ctx, `UPDATE webhook_endpoints SET name=$3,url=$4,event_types=$5,enabled=$6,updated_at=now()
			WHERE id=$1 AND project_id=$2`, endpoint.ID, endpoint.ProjectID, endpoint.Name, endpoint.URL,
			jsonBytes(endpoint.EventTypes), endpoint.Enabled)
		err = execErr
		affected = commandTag.RowsAffected()
	}
	if err != nil {
		return domain.WebhookEndpoint{}, err
	}
	if affected == 0 {
		return domain.WebhookEndpoint{}, ErrNotFound
	}
	return s.WebhookEndpointByID(ctx, endpoint.ID)
}

// DeleteWebhookEndpoint is intentionally a soft delete.  Delivery history is
// financial/audit evidence and must not disappear with endpoint configuration.
func (s *Store) DeleteWebhookEndpoint(ctx context.Context, projectID, endpointID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE webhook_endpoints SET enabled=false,updated_at=now() WHERE id=$1 AND project_id=$2 AND enabled`, endpointID, projectID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// EnqueueWebhookEvent fans one immutable event out to every matching enabled
// endpoint.  endpoint_id/event_id uniqueness makes retries by the producer
// idempotent.
func (s *Store) EnqueueWebhookEvent(ctx context.Context, projectID, eventID, eventType string, payload map[string]any, maxAttempts int) (int64, error) {
	if eventID == "" || eventType == "" {
		return 0, errors.New("webhook event ID and type are required")
	}
	if maxAttempts <= 0 {
		maxAttempts = 6
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO webhook_outbox(id,endpoint_id,organization_id,project_id,event_id,event_type,payload,max_attempts)
		SELECT gen_random_uuid(),eligible.id,eligible.organization_id,eligible.project_id,$2,$3,$4,$5 FROM (
		  SELECT endpoint.* FROM webhook_endpoints endpoint
		  WHERE endpoint.project_id=$1 AND endpoint.enabled
		  AND (endpoint.event_types='[]'::jsonb OR endpoint.event_types ? $3)
		  ORDER BY endpoint.created_at,endpoint.id
		  LIMIT COALESCE((SELECT entitlement.integer_value FROM projects project
		    JOIN organization_subscription subscription ON subscription.organization_id=project.organization_id
		    JOIN plan_entitlement entitlement ON entitlement.plan_version_id=subscription.plan_version_id
		      AND entitlement.entitlement_key='webhook_count'
		    WHERE project.id=$1 AND (
		      (subscription.status IN ('TRIALING','ACTIVE') AND subscription.current_period_end>now()) OR
		      (subscription.status IN ('PAST_DUE','GRACE_PERIOD') AND COALESCE(subscription.grace_period_end,subscription.current_period_end)>now())
		    ) ORDER BY subscription.created_at DESC LIMIT 1),0)
		) eligible
		ON CONFLICT(endpoint_id,event_id) DO NOTHING`, projectID, eventID, eventType, jsonBytes(payload), maxAttempts)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) EnqueueWebhookOutbox(ctx context.Context, event domain.WebhookOutbox) (domain.WebhookOutbox, error) {
	if event.ID == "" {
		event.ID = id.UUID()
	}
	if event.MaxAttempts <= 0 {
		event.MaxAttempts = 6
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO webhook_outbox(id,endpoint_id,organization_id,project_id,event_id,event_type,payload,max_attempts,available_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(endpoint_id,event_id) DO NOTHING`, event.ID, event.EndpointID,
		event.OrganizationID, event.ProjectID, event.EventID, event.EventType, jsonBytes(event.Payload), event.MaxAttempts, event.AvailableAt)
	if err != nil {
		return domain.WebhookOutbox{}, err
	}
	return s.WebhookOutboxByEvent(ctx, event.EndpointID, event.EventID)
}

func scanWebhookOutbox(row pgx.Row) (domain.WebhookOutbox, error) {
	var out domain.WebhookOutbox
	var payload []byte
	err := row.Scan(&out.ID, &out.EndpointID, &out.OrganizationID, &out.ProjectID, &out.EventID, &out.EventType,
		&payload, &out.Status, &out.Attempts, &out.MaxAttempts, &out.AvailableAt, &out.LockedAt, &out.LockedUntil,
		&out.LockedBy, &out.ClaimToken, &out.DeliveredAt, &out.LastHTTPStatus, &out.LastResponse, &out.LastError,
		&out.CreatedAt, &out.UpdatedAt, &out.EndpointURL, &out.EncryptedSecret)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(payload, &out.Payload)
	if out.Payload == nil {
		out.Payload = map[string]any{}
	}
	return out, nil
}

const webhookOutboxProjection = `SELECT o.id,o.endpoint_id,o.organization_id,o.project_id,o.event_id,o.event_type,o.payload,o.status,
	o.attempts,o.max_attempts,o.available_at,o.locked_at,o.locked_until,COALESCE(o.locked_by,''),COALESCE(o.claim_token::text,''),
	o.delivered_at,o.last_http_status,COALESCE(o.last_response,''),COALESCE(o.last_error,''),o.created_at,o.updated_at,
	e.url,e.encrypted_secret`

const webhookOutboxSelect = webhookOutboxProjection + ` FROM webhook_outbox o JOIN webhook_endpoints e ON e.id=o.endpoint_id`

func (s *Store) WebhookOutboxByEvent(ctx context.Context, endpointID, eventID string) (domain.WebhookOutbox, error) {
	return scanWebhookOutbox(s.pool.QueryRow(ctx, webhookOutboxSelect+` WHERE o.endpoint_id=$1 AND o.event_id=$2`, endpointID, eventID))
}

func (s *Store) ClaimWebhookOutbox(ctx context.Context, workerID string, limit int, lease time.Duration) ([]domain.WebhookOutbox, error) {
	if workerID == "" {
		return nil, errors.New("webhook worker ID is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if lease <= 0 {
		lease = time.Minute
	}
	rows, err := s.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM webhook_outbox
		WHERE attempts < max_attempts AND (
			(status IN ('PENDING','RETRY') AND available_at<=now()) OR
			(status='PROCESSING' AND locked_until<=now())
		) ORDER BY available_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT $1
	), claimed AS (
		UPDATE webhook_outbox o SET status='PROCESSING',attempts=o.attempts+1,locked_by=$2,
			claim_token=gen_random_uuid(),locked_at=now(),locked_until=now()+($3::bigint * interval '1 millisecond'),updated_at=now()
		FROM picked WHERE o.id=picked.id RETURNING o.*
	) `+webhookOutboxProjection+` FROM claimed o JOIN webhook_endpoints e ON e.id=o.endpoint_id
	ORDER BY o.available_at,o.created_at,o.id`,
		limit, workerID, lease.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.WebhookOutbox, 0)
	for rows.Next() {
		delivery, err := scanWebhookOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

// RecordWebhookResult uses the claim token as a lease-generation CAS.  A
// timed-out worker therefore cannot overwrite the result of a later claimant.
func (s *Store) RecordWebhookResult(ctx context.Context, outboxID, claimToken string, delivered bool, httpStatus int, response, deliveryError string, retryAt time.Time) error {
	if claimToken == "" {
		return errors.New("webhook claim token is required")
	}
	status := "RETRY"
	if delivered {
		status = "DELIVERED"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var attempts, maxAttempts int
	var endpointID string
	err = tx.QueryRow(ctx, `SELECT attempts,max_attempts,endpoint_id FROM webhook_outbox
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2::uuid FOR UPDATE`, outboxID, claimToken).
		Scan(&attempts, &maxAttempts, &endpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !delivered && attempts >= maxAttempts {
		status = "DEAD"
	}
	if retryAt.IsZero() {
		retryAt = time.Now().UTC()
	}
	tag, err := tx.Exec(ctx, `UPDATE webhook_outbox SET status=$3,available_at=$4,last_http_status=NULLIF($5,0),
		last_response=NULLIF($6,''),last_error=NULLIF($7,''),delivered_at=CASE WHEN $3='DELIVERED' THEN now() ELSE delivered_at END,
		locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,updated_at=now()
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2::uuid`, outboxID, claimToken, status, retryAt.UTC(), httpStatus, response, deliveryError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if delivered {
		if _, err = tx.Exec(ctx, `UPDATE webhook_endpoints SET last_delivery_at=now(),updated_at=now() WHERE id=$1`, endpointID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteWebhookOutbox(ctx context.Context, outboxID, claimToken string, httpStatus int, response string) error {
	return s.RecordWebhookResult(ctx, outboxID, claimToken, true, httpStatus, response, "", time.Time{})
}

func (s *Store) RetryWebhookOutbox(ctx context.Context, outboxID, claimToken string, httpStatus int, response, deliveryError string, retryAt time.Time) error {
	return s.RecordWebhookResult(ctx, outboxID, claimToken, false, httpStatus, response, deliveryError, retryAt)
}

// ExpireWebhookLeases moves crashed final-attempt deliveries to DEAD.  Other
// expired leases are intentionally reclaimed by ClaimWebhookOutbox.
func (s *Store) ExpireWebhookLeases(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE webhook_outbox SET status='DEAD',last_error=COALESCE(last_error,'worker lease expired'),
		locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,updated_at=now()
		WHERE status='PROCESSING' AND locked_until <= $1 AND attempts >= max_attempts`, now.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ListWebhookOutbox(ctx context.Context, projectID, status string, limit, offset int) ([]domain.WebhookOutbox, error) {
	query := webhookOutboxSelect + ` WHERE o.project_id=$1`
	args := []any{projectID}
	if status != "" {
		query += ` AND o.status=$2`
		args = append(args, status)
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY o.created_at DESC,o.id DESC LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.WebhookOutbox, 0)
	for rows.Next() {
		delivery, err := scanWebhookOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}
