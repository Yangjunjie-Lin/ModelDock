package store

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestObservabilitySupportIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	userID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'integration-hash','Support Integration','ADMIN','ACTIVE',now())`, userID, "support-"+userID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}

	const concurrency = 16
	dedupeKey := "integration-provider-failure-" + userID
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.RecordOperationalAlert(ctx, "PROVIDER_FLEET_FAILURE", "CRITICAL", "integration fixture", dedupeKey, map[string]any{"provider": "fixture"})
		}()
	}
	wg.Wait()
	close(errs)
	for alertErr := range errs {
		if alertErr != nil {
			t.Fatal(alertErr)
		}
	}
	var alertCount int64
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE dedupe_key=$1 AND resolved_at IS NULL`, dedupeKey).Scan(&alertCount); err != nil || alertCount != 1 {
		t.Fatalf("open alerts=%d err=%v", alertCount, err)
	}
	if err = s.ResolveOperationalAlert(ctx, dedupeKey); err != nil {
		t.Fatal(err)
	}

	event, err := s.CreateStatusEvent(ctx, domain.StatusEvent{Component: "provider", Status: "partial_outage", Summary: "Fixture event", PublicMessage: "Requests may be delayed", Metadata: map[string]any{"internal_case": "fixture-only"}}, userID)
	if err != nil {
		t.Fatal(err)
	}
	publicStatus, err := s.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events := publicStatus["events"].([]domain.StatusEvent)
	if len(events) == 0 || len(events[0].Metadata) != 0 {
		t.Fatalf("public events leaked metadata: %#v", events)
	}
	if err = s.ResolveStatusEvent(ctx, event.ID, userID); err != nil {
		t.Fatal(err)
	}

	ticket, err := s.CreateSupportTicket(ctx, SupportTicketCreateRequest{
		Subject: "Request investigation", Body: "Redacted fixture body", Priority: "HIGH", UserID: userID,
		OrganizationID: domain.LegacyOrganizationID, RequestID: "req_fixture_123", CreatedBy: userID,
		Context: map[string]any{"request_id": "req_fixture_123", "trace_id": "trace_fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddSupportTicketMessage(ctx, ticket.ID, userID, "INTERNAL", "internal fixture note"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddSupportTicketMessage(ctx, ticket.ID, userID, "PUBLIC", "public fixture reply"); err != nil {
		t.Fatal(err)
	}
	userView, err := s.SupportTicketByID(ctx, ticket.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(userView.Messages) != 2 {
		t.Fatalf("public messages=%d want=2", len(userView.Messages))
	}
	for _, message := range userView.Messages {
		if message.Visibility != "PUBLIC" || message.Body == "internal fixture note" {
			t.Fatalf("internal note leaked: %#v", message)
		}
	}
	if _, err = s.UpdateSupportTicket(ctx, ticket.ID, "RESOLVED", "HIGH", userID, userID); err != nil {
		t.Fatal(err)
	}
	var audits int64
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='support_ticket' AND resource_id=$1`, ticket.ID).Scan(&audits); err != nil || audits < 4 {
		t.Fatalf("support audits=%d err=%v", audits, err)
	}
	gauges, err := s.ObservabilityGauges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gauges["relaydock_postgres_pool_max_connections"]; !ok {
		t.Fatalf("pool gauge missing: %v", gauges)
	}
}
