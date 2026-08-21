package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

type SupportTicketCreateRequest struct {
	Subject         string
	Body            string
	Priority        string
	UserID          string
	OrganizationID  string
	RequestID       string
	OrderID         string
	LedgerJournalID string
	CreatedBy       string
	Context         map[string]any
}

func (s *Store) ObservabilityGauges(ctx context.Context) (map[string]int64, error) {
	values := map[string]int64{}
	queries := map[string]string{
		"relaydock_settlement_backlog":              `SELECT count(*) FROM funding_operation WHERE status IN ('PENDING','RESERVED') AND updated_at<now()-interval '5 minutes'`,
		"relaydock_payment_webhook_failures":        `SELECT count(*) FROM payment_webhook_event WHERE processing_status='FAILED'`,
		"relaydock_reconciliation_open_differences": `SELECT count(*) FROM payment_reconciliation_record WHERE result<>'MATCH'`,
		"relaydock_wallet_balance_anomalies":        `SELECT count(*) FROM wallets WHERE available_balance<0 OR (credit_enforced AND risk_exposure>risk_limit)`,
		"relaydock_api_key_leak_alerts":             `SELECT count(*) FROM alerts WHERE kind='API_KEY_SUSPECTED_LEAK' AND resolved_at IS NULL`,
	}
	for name, query := range queries {
		var value int64
		if err := s.pool.QueryRow(ctx, query).Scan(&value); err != nil {
			return nil, err
		}
		values[name] = value
	}
	stats := s.pool.Stat()
	values["relaydock_postgres_pool_acquired_connections"] = int64(stats.AcquiredConns())
	values["relaydock_postgres_pool_idle_connections"] = int64(stats.IdleConns())
	values["relaydock_postgres_pool_max_connections"] = int64(stats.MaxConns())
	return values, nil
}

func (s *Store) RecordOperationalAlert(ctx context.Context, kind, severity, message, dedupeKey string, details map[string]any) error {
	if strings.TrimSpace(kind) == "" || strings.TrimSpace(message) == "" {
		return errors.New("alert kind and message are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if strings.TrimSpace(dedupeKey) != "" {
		result, insertErr := tx.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,dedupe_key,details,last_seen_at)
			VALUES($1,$2,$3,$4,$5,$6,now()) ON CONFLICT DO NOTHING`, id.UUID(), kind, severity, message, dedupeKey, jsonBytes(details))
		if insertErr != nil {
			return insertErr
		}
		if result.RowsAffected() == 0 {
			if _, err = tx.Exec(ctx, `UPDATE alerts SET severity=$2,message=$3,details=$4,last_seen_at=now() WHERE dedupe_key=$1 AND resolved_at IS NULL`, dedupeKey, severity, message, jsonBytes(details)); err != nil {
				return err
			}
		}
	} else if _, err = tx.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,details,last_seen_at) VALUES($1,$2,$3,$4,$5,now())`, id.UUID(), kind, severity, message, jsonBytes(details)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ResolveOperationalAlert(ctx context.Context, dedupeKey string) error {
	if strings.TrimSpace(dedupeKey) == "" {
		return errors.New("dedupe key is required")
	}
	_, err := s.pool.Exec(ctx, `UPDATE alerts SET resolved_at=COALESCE(resolved_at,now()),last_seen_at=now() WHERE dedupe_key=$1 AND resolved_at IS NULL`, dedupeKey)
	return err
}

func (s *Store) ListSLOs(ctx context.Context) ([]domain.ObservabilitySLO, error) {
	rows, err := s.pool.Query(ctx, `SELECT name,target_percent::text,window_minutes,description,enabled,updated_at FROM observability_slos ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ObservabilitySLO, 0)
	for rows.Next() {
		var item domain.ObservabilitySLO
		var target string
		if err := rows.Scan(&item.Name, &target, &item.WindowMinutes, &item.Description, &item.Enabled, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.TargetPercent = domain.Decimal(target)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateStatusEvent(ctx context.Context, event domain.StatusEvent, actor string) (domain.StatusEvent, error) {
	event.ID = id.UUID()
	event.Component = strings.ToUpper(strings.TrimSpace(event.Component))
	event.Status = strings.ToUpper(strings.TrimSpace(event.Status))
	if event.StartedAt.IsZero() {
		event.StartedAt = time.Now().UTC()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.StatusEvent{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO status_events(id,component,status,summary,public_message,dedupe_key,metadata,started_at,created_by)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,NULLIF($9,'')::uuid)
		RETURNING created_at`, event.ID, event.Component, event.Status, event.Summary, event.PublicMessage, event.DedupeKey, jsonBytes(event.Metadata), event.StartedAt, actor).Scan(&event.CreatedAt)
	if err != nil {
		return domain.StatusEvent{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "status.event_created", "status_event", event.ID, map[string]any{"component": event.Component, "status": event.Status, "summary": event.Summary}); err != nil {
		return domain.StatusEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StatusEvent{}, err
	}
	return event, nil
}

func (s *Store) ResolveStatusEvent(ctx context.Context, eventID, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE status_events SET resolved_at=COALESCE(resolved_at,now()) WHERE id=$1`, eventID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, &actor, "status.event_resolved", "status_event", eventID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListStatusEvents(ctx context.Context, limit int, includeInternal bool) ([]domain.StatusEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,component,status,summary,public_message,COALESCE(dedupe_key,''),metadata,started_at,resolved_at,created_at
		FROM status_events ORDER BY started_at DESC,id DESC LIMIT $1`, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.StatusEvent, 0)
	for rows.Next() {
		var event domain.StatusEvent
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.Component, &event.Status, &event.Summary, &event.PublicMessage, &event.DedupeKey, &metadata, &event.StartedAt, &event.ResolvedAt, &event.CreatedAt); err != nil {
			return nil, err
		}
		if includeInternal {
			_ = unmarshalSupportJSON(metadata, &event.Metadata)
		} else {
			// Dedupe keys are operational correlation data, not public incident content.
			event.DedupeKey = ""
			event.Metadata = map[string]any{}
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) PublicStatus(ctx context.Context) (map[string]any, error) {
	providers, err := s.pool.Query(ctx, `SELECT p.slug,p.enabled,p.emergency_kill_switch,p.commercial_status,
		p.commercial_resale_status,p.pricing_disabled,
		(SELECT count(*) FROM provider_credentials c WHERE c.provider_id=p.id AND c.status='ACTIVE'
			AND c.current_health<>'UNHEALTHY' AND (c.cooldown_until IS NULL OR c.cooldown_until<=now()))
		FROM providers p ORDER BY p.slug`)
	if err != nil {
		return nil, err
	}
	defer providers.Close()
	providerItems := make([]map[string]any, 0)
	overall := "OPERATIONAL"
	for providers.Next() {
		var slug, commercial, resale string
		var enabled, killed, pricingDisabled bool
		var schedulableCredentials int64
		if err := providers.Scan(&slug, &enabled, &killed, &commercial, &resale, &pricingDisabled, &schedulableCredentials); err != nil {
			return nil, err
		}
		status, message := publicProviderComponentStatus(enabled, killed, pricingDisabled, commercial, resale, schedulableCredentials)
		if status != "OPERATIONAL" {
			overall = "DEGRADED"
		}
		providerItems = append(providerItems, map[string]any{"name": slug, "status": status, "message": message})
	}
	if err = providers.Err(); err != nil {
		return nil, err
	}
	events, err := s.ListStatusEvents(ctx, 20, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":     overall,
		"updated_at": time.Now().UTC(),
		"components": map[string]any{
			"gateway":   map[string]any{"status": "OPERATIONAL", "message": "Operational"},
			"dashboard": map[string]any{"status": "OPERATIONAL", "message": "Operational"},
			"billing":   map[string]any{"status": "OPERATIONAL", "message": "Operational"},
			"providers": providerItems,
		},
		"events": events,
	}, nil
}

func publicProviderComponentStatus(enabled, killed, pricingDisabled bool, commercial, resale string, schedulableCredentials int64) (string, string) {
	if !enabled || killed {
		return "MAJOR_OUTAGE", "Disabled or emergency kill switch active"
	}
	if commercial != "COMMERCIAL_APPROVED" || resale != "APPROVED" {
		return "DEGRADED", "Commercial or resale admission is unavailable"
	}
	if pricingDisabled {
		return "DEGRADED", "Pricing switch is disabled"
	}
	if schedulableCredentials < 1 {
		return "DEGRADED", "No schedulable credential is currently reported"
	}
	return "OPERATIONAL", "Configured and schedulable; upstream health is not guaranteed"
}

func (s *Store) CreateSupportTicket(ctx context.Context, request SupportTicketCreateRequest) (domain.SupportTicket, error) {
	request.Subject = strings.TrimSpace(request.Subject)
	request.Body = strings.TrimSpace(request.Body)
	if request.Priority == "" {
		request.Priority = "NORMAL"
	}
	if request.Context == nil {
		request.Context = map[string]any{}
	}
	numberID := strings.ReplaceAll(id.UUID(), "-", "")
	ticket := domain.SupportTicket{ID: id.UUID(), TicketNumber: "MD-" + strings.ToUpper(numberID[:16]), Subject: request.Subject, Status: "OPEN", Priority: request.Priority,
		UserID: request.UserID, OrganizationID: request.OrganizationID, RequestID: request.RequestID, OrderID: request.OrderID, LedgerJournalID: request.LedgerJournalID,
		CreatedBy: request.CreatedBy, Context: request.Context}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupportTicket{}, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `INSERT INTO support_tickets(id,ticket_number,subject,status,priority,user_id,organization_id,request_id,order_id,ledger_journal_id,created_by,redacted_context)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,''),NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,NULLIF($11,'')::uuid,$12)
		RETURNING created_at,updated_at`, ticket.ID, ticket.TicketNumber, ticket.Subject, ticket.Status, ticket.Priority, ticket.UserID, ticket.OrganizationID, ticket.RequestID, ticket.OrderID, ticket.LedgerJournalID, ticket.CreatedBy, jsonBytes(ticket.Context)).Scan(&ticket.CreatedAt, &ticket.UpdatedAt); err != nil {
		return domain.SupportTicket{}, err
	}
	message := domain.SupportTicketMessage{ID: id.UUID(), TicketID: ticket.ID, AuthorID: request.CreatedBy, Visibility: "PUBLIC", Body: request.Body}
	if err = tx.QueryRow(ctx, `INSERT INTO support_ticket_messages(id,ticket_id,author_id,visibility,body) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5) RETURNING created_at`, message.ID, ticket.ID, message.AuthorID, message.Visibility, message.Body).Scan(&message.CreatedAt); err != nil {
		return domain.SupportTicket{}, err
	}
	ticket.Messages = []domain.SupportTicketMessage{message}
	if err = writeAuditTx(ctx, tx, &request.CreatedBy, "support.ticket_created", "support_ticket", ticket.ID, map[string]any{
		"ticket_number": ticket.TicketNumber, "priority": ticket.Priority, "user_id": ticket.UserID, "organization_id": ticket.OrganizationID,
		"request_id": ticket.RequestID, "order_id": ticket.OrderID, "ledger_journal_id": ticket.LedgerJournalID,
	}); err != nil {
		return domain.SupportTicket{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupportTicket{}, err
	}
	return ticket, nil
}

func (s *Store) ListSupportTickets(ctx context.Context, userID, organizationID *string, limit, offset int, includeInternal bool) ([]domain.SupportTicket, error) {
	_ = includeInternal
	query := `SELECT id,ticket_number,subject,status,priority,COALESCE(user_id::text,''),COALESCE(organization_id::text,''),COALESCE(request_id,''),COALESCE(order_id::text,''),COALESCE(ledger_journal_id::text,''),COALESCE(created_by::text,''),COALESCE(assigned_to::text,''),redacted_context,created_at,updated_at,resolved_at FROM support_tickets WHERE true`
	args := []any{}
	if userID != nil {
		args = append(args, *userID)
		query += ` AND user_id=$` + itoa(len(args))
	}
	if organizationID != nil {
		args = append(args, *organizationID)
		query += ` AND organization_id=$` + itoa(len(args))
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY updated_at DESC,id DESC LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupportTicket, 0)
	for rows.Next() {
		var ticket domain.SupportTicket
		var contextJSON []byte
		if err := rows.Scan(&ticket.ID, &ticket.TicketNumber, &ticket.Subject, &ticket.Status, &ticket.Priority, &ticket.UserID, &ticket.OrganizationID, &ticket.RequestID, &ticket.OrderID, &ticket.LedgerJournalID, &ticket.CreatedBy, &ticket.AssignedTo, &contextJSON, &ticket.CreatedAt, &ticket.UpdatedAt, &ticket.ResolvedAt); err != nil {
			return nil, err
		}
		_ = unmarshalSupportJSON(contextJSON, &ticket.Context)
		out = append(out, ticket)
	}
	return out, rows.Err()
}

func (s *Store) SupportTicketByID(ctx context.Context, ticketID string, includeInternal bool) (domain.SupportTicket, error) {
	var ticket domain.SupportTicket
	var contextJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT id,ticket_number,subject,status,priority,COALESCE(user_id::text,''),COALESCE(organization_id::text,''),COALESCE(request_id,''),COALESCE(order_id::text,''),COALESCE(ledger_journal_id::text,''),COALESCE(created_by::text,''),COALESCE(assigned_to::text,''),redacted_context,created_at,updated_at,resolved_at FROM support_tickets WHERE id=$1`, ticketID).
		Scan(&ticket.ID, &ticket.TicketNumber, &ticket.Subject, &ticket.Status, &ticket.Priority, &ticket.UserID, &ticket.OrganizationID, &ticket.RequestID, &ticket.OrderID, &ticket.LedgerJournalID, &ticket.CreatedBy, &ticket.AssignedTo, &contextJSON, &ticket.CreatedAt, &ticket.UpdatedAt, &ticket.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupportTicket{}, ErrNotFound
	}
	if err != nil {
		return domain.SupportTicket{}, err
	}
	_ = unmarshalSupportJSON(contextJSON, &ticket.Context)
	ticket.Messages, err = s.listTicketMessages(ctx, ticket.ID, includeInternal)
	return ticket, err
}

func (s *Store) listTicketMessages(ctx context.Context, ticketID string, includeInternal bool) ([]domain.SupportTicketMessage, error) {
	query := `SELECT id,ticket_id,COALESCE(author_id::text,''),visibility,body,created_at FROM support_ticket_messages WHERE ticket_id=$1`
	if !includeInternal {
		query += ` AND visibility='PUBLIC'`
	}
	query += ` ORDER BY created_at,id`
	rows, err := s.pool.Query(ctx, query, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupportTicketMessage, 0)
	for rows.Next() {
		var item domain.SupportTicketMessage
		if err := rows.Scan(&item.ID, &item.TicketID, &item.AuthorID, &item.Visibility, &item.Body, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) AddSupportTicketMessage(ctx context.Context, ticketID, authorID, visibility, body string) (domain.SupportTicketMessage, error) {
	message := domain.SupportTicketMessage{ID: id.UUID(), TicketID: ticketID, AuthorID: authorID, Visibility: visibility, Body: body}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupportTicketMessage{}, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `INSERT INTO support_ticket_messages(id,ticket_id,author_id,visibility,body) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5) RETURNING created_at`, message.ID, ticketID, authorID, visibility, body).Scan(&message.CreatedAt); err != nil {
		return domain.SupportTicketMessage{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE support_tickets SET status=CASE WHEN $2='INTERNAL' THEN status ELSE 'OPEN' END,updated_at=now() WHERE id=$1`, ticketID, visibility); err != nil {
		return domain.SupportTicketMessage{}, err
	}
	if err = writeAuditTx(ctx, tx, &authorID, "support.message_added", "support_ticket", ticketID, map[string]any{"message_id": message.ID, "visibility": visibility}); err != nil {
		return domain.SupportTicketMessage{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupportTicketMessage{}, err
	}
	return message, nil
}

func (s *Store) UpdateSupportTicket(ctx context.Context, ticketID, status, priority, assignedTo, actor string) (domain.SupportTicket, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupportTicket{}, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE support_tickets SET status=$2,priority=$3,assigned_to=NULLIF($4,'')::uuid,resolved_at=CASE WHEN $2 IN ('RESOLVED','CLOSED') THEN COALESCE(resolved_at,now()) ELSE NULL END,updated_at=now() WHERE id=$1`, ticketID, status, priority, assignedTo)
	if err != nil {
		return domain.SupportTicket{}, err
	}
	if result.RowsAffected() == 0 {
		return domain.SupportTicket{}, ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, &actor, "support.ticket_updated", "support_ticket", ticketID, map[string]any{"status": status, "priority": priority, "assigned_to": assignedTo}); err != nil {
		return domain.SupportTicket{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupportTicket{}, err
	}
	return s.SupportTicketByID(ctx, ticketID, true)
}

func unmarshalSupportJSON(data []byte, target any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}
