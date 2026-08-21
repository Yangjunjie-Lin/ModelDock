package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/supplier"
)

func TestSupplierOnboardingIntegration(t *testing.T) {
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
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES($1,$2,'synthetic-hash','Supplier Test','USER','ACTIVE',now())`, userID, "supplier-test-"+userID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateSupplier(ctx, domain.SupplierOrganization{LegalName: "Synthetic Supplier LLC", DisplayName: "Synthetic Supplier", RegistrationNumber: "TEST-" + userID, IncorporationCountry: "US", PayoutCurrency: "USD"}, userID, []byte("ciphertext"), "1234", domain.SupplierContact{FullName: "Synthetic Contact", Email: "contact-" + userID + "@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "DRAFT" || created.PayoutAccountLast4 != "1234" {
		t.Fatalf("unexpected supplier: %+v", created)
	}
	if _, err = s.SubmitSupplier(ctx, created.ID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewSupplier(ctx, created.ID, "APPROVED", "not ready", userID); !errors.Is(err, ErrSupplierApprovalRequired) {
		t.Fatalf("expected supplier approval boundary, got %v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE supplier_organizations SET status='APPROVED' WHERE id=$1`, created.ID); err == nil {
		t.Fatal("supplier self-approval trigger did not reject direct update")
	}
	endpoint, err := s.CreateSupplierEndpoint(ctx, domain.SupplierEndpoint{SupplierID: created.ID, EndpointURL: "https://public.example/api"}, userID, supplier.ChallengeHash("synthetic-challenge"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.SupplierEndpointChallengeValid(ctx, endpoint.ID, "synthetic-challenge") || s.SupplierEndpointChallengeValid(ctx, endpoint.ID, "wrong") {
		t.Fatal("endpoint challenge hash boundary failed")
	}
	if err = s.MarkSupplierEndpointVerification(ctx, endpoint.ID, userID, "203.0.113.10", true, ""); err != nil {
		t.Fatal(err)
	}
	questionnaire, err := s.CreateSupplierQuestionnaire(ctx, domain.SupplierSecurityQuestionnaire{SupplierID: created.ID, Version: "2026-01", Answers: map[string]any{"encryption_at_rest": true}}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateSupplierCompliance(ctx, created.ID, "VERIFIED", "ACTIVE", "contract-2026", nil, nil, userID); err == nil {
		t.Fatal("non-admin compliance update unexpectedly succeeded")
	}
	// The integration fixture uses a direct administrator identity only for
	// compliance/review, while supplier ownership remains bound to userID.
	adminID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES($1,$2,'synthetic-hash','Supplier Admin','ADMIN','ACTIVE',now())`, adminID, "supplier-admin-"+adminID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewSupplier(ctx, created.ID, "APPROVED", "not ready", adminID); !errors.Is(err, ErrSupplierNotReady) {
		t.Fatalf("expected fail-closed approval, got %v", err)
	}
	if err = s.ReviewSupplierEvidence(ctx, "QUESTIONNAIRE", questionnaire.ID, "APPROVED", "security controls reviewed", adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateSupplierCompliance(ctx, created.ID, "VERIFIED", "ACTIVE", "contract-2026", nil, nil, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewSupplier(ctx, created.ID, "APPROVED", "evidence reviewed", adminID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.RequestSupplierExit(ctx, created.ID, userID) }()
	}
	wg.Wait()
	final, err := s.SupplierByID(ctx, created.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "EXIT_REQUESTED" {
		t.Fatalf("concurrent exit did not converge: %s", final.Status)
	}
}
