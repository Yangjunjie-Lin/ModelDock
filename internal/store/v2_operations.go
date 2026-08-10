package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
)

// AcknowledgeAlert is idempotent: repeated acknowledgements retain the first
// actor and timestamp so the audit trail cannot be silently reassigned.
func (s *Store) AcknowledgeAlert(ctx context.Context, alertID, actorID string) (time.Time, error) {
	var acknowledgedAt time.Time
	err := s.pool.QueryRow(ctx, `UPDATE alerts SET
		acknowledged_at=COALESCE(acknowledged_at,now()),
		acknowledged_by=COALESCE(acknowledged_by,$2)
		WHERE id=$1 RETURNING acknowledged_at`, alertID, actorID).Scan(&acknowledgedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	return acknowledgedAt, err
}

// RetryDeadWebhookOutbox is an explicit operator action. It creates a fresh
// retry window on the existing immutable delivery record and preserves its
// event identity for receiver-side deduplication.
func (s *Store) RetryDeadWebhookOutbox(ctx context.Context, projectID, outboxID string) (domain.WebhookOutbox, error) {
	var endpointID, eventID string
	err := s.pool.QueryRow(ctx, `UPDATE webhook_outbox SET status='PENDING',attempts=0,available_at=now(),
		locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,last_error=NULL,updated_at=now()
		WHERE id=$1 AND project_id=$2 AND status='DEAD' RETURNING endpoint_id,event_id`, outboxID, projectID).
		Scan(&endpointID, &eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WebhookOutbox{}, ErrNotFound
	}
	if err != nil {
		return domain.WebhookOutbox{}, err
	}
	return s.WebhookOutboxByEvent(ctx, endpointID, eventID)
}
