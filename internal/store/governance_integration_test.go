package store

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestGovernanceRiskFreezeAndLifecycleIntegration(t *testing.T) {
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
	email := "governance-" + userID + "@example.invalid"
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES($1,$2,'integration-hash','Governance Integration','USER','ACTIVE',now())`, userID, email); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, domain.LegacyOrganizationID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status) VALUES($1,$2,$3,'DEVELOPER','ACTIVE')`, domain.LegacyOrganizationID, domain.LegacyProjectID, userID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RecordRiskEvent(ctx, domain.RiskEvent{IdempotencyKey: "risk-" + userID, UserID: userID, OrganizationID: domain.LegacyOrganizationID, EventType: "DEVICE_ANOMALY", ScoreDelta: 10}, []byte("fixture-ip-hmac"), []byte("fixture-device-hmac")); err != nil {
		t.Fatal(err)
	}
	var score int
	if err = s.pool.QueryRow(ctx, `SELECT risk_score FROM users WHERE id=$1`, userID).Scan(&score); err != nil || score != 10 {
		t.Fatalf("score=%d err=%v", score, err)
	}
	keyID := id.UUID()
	hash := []byte("governance-hash-32-bytes-value!!")
	if _, err = s.pool.Exec(ctx, `INSERT INTO api_keys(id,user_id,organization_id,project_id,name,environment,key_prefix,key_hash,status) VALUES($1,$2,$3,$4,'Integration','test','rdk_test_fixture',$5,'ACTIVE')`, keyID, userID, domain.LegacyOrganizationID, domain.LegacyProjectID, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertPrivacySettings(ctx, domain.PrivacySettings{SubjectType: "ORGANIZATION", SubjectID: domain.LegacyOrganizationID, RetentionDays: 30, CrossBorderRoute: "DOMESTIC"}, userID); err != nil {
		t.Fatal(err)
	}
	requestID := "governance-policy-log-" + userID
	if err = s.InsertScopedRequestLog(ctx, domain.RequestLog{RequestID: requestID, UserID: userID, APIKeyID: keyID, OrganizationID: domain.LegacyOrganizationID, ProjectID: domain.LegacyProjectID, RequestedModel: "governance-fixture", ResolvedModel: "governance-fixture", Endpoint: "/v1/responses", StatusCode: 403}); err != nil {
		t.Fatal(err)
	}
	var saveContent bool
	var classification, crossBorderRoute string
	if err = s.pool.QueryRow(ctx, `SELECT content_stored,data_classification,cross_border_route FROM request_logs WHERE request_id=$1`, requestID).Scan(&saveContent, &classification, &crossBorderRoute); err != nil {
		t.Fatal(err)
	}
	if saveContent || classification != "CONFIDENTIAL" || crossBorderRoute != "DOMESTIC" {
		t.Fatalf("content_stored=%v classification=%s cross_border_route=%s", saveContent, classification, crossBorderRoute)
	}
	var contentColumns int64
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='request_logs' AND column_name IN ('prompt','request_body','response','response_body')`).Scan(&contentColumns); err != nil || contentColumns != 0 {
		t.Fatalf("request log content columns=%d err=%v", contentColumns, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.FreezeAPIKeyByHash(ctx, hash, "concurrent leak fixture", userID, "192.0.2.10")
		}()
	}
	wg.Wait()
	close(errs)
	for freezeErr := range errs {
		if freezeErr != nil {
			t.Fatal(freezeErr)
		}
	}
	var status string
	if err = s.pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, keyID).Scan(&status); err != nil || status != "DISABLED" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	riskyOrgID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,abuse_status) VALUES($1,'Risky organization',$2,'ACTIVE','FROZEN')`, riskyOrgID, "risky-"+riskyOrgID); err != nil {
		t.Fatal(err)
	}
	if allowed, _, riskErr := s.GatewayRiskCheck(ctx, userID, riskyOrgID); allowed || riskErr != ErrRiskFrozen {
		t.Fatalf("risky organization allowed=%v err=%v", allowed, riskErr)
	}
	if allowed, reason, riskErr := s.GatewayRiskCheck(ctx, userID, domain.LegacyOrganizationID); !allowed || riskErr != nil {
		t.Fatalf("normal organization allowed=%v reason=%s err=%v", allowed, reason, riskErr)
	}
	if _, err = s.UpsertPrivacySettings(ctx, domain.PrivacySettings{SubjectType: "USER", SubjectID: userID, RetentionDays: 30, LegalHold: true}, userID); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateLifecycleJob(ctx, domain.DataLifecycleJob{SubjectType: "USER", SubjectID: userID, JobType: "DELETE", IdempotencyKey: "delete-" + userID}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProcessNextLifecycleJob(ctx); err != nil {
		t.Fatal(err)
	}
	job, err = s.LifecycleJobByID(ctx, job.ID)
	if err != nil || job.Status != "BLOCKED" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if _, err = s.UpsertPrivacySettings(ctx, domain.PrivacySettings{SubjectType: "USER", SubjectID: userID, RetentionDays: 30, LegalHold: false}, userID); err != nil {
		t.Fatal(err)
	}
	deleteJob, _, err := s.CreateLifecycleJob(ctx, domain.DataLifecycleJob{SubjectType: "USER", SubjectID: userID, JobType: "DELETE", IdempotencyKey: "delete-allowed-" + userID}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProcessNextLifecycleJob(ctx); err != nil {
		t.Fatal(err)
	}
	deleteJob, err = s.LifecycleJobByID(ctx, deleteJob.ID)
	if err != nil || deleteJob.Status != "COMPLETED" {
		t.Fatalf("delete job=%+v err=%v", deleteJob, err)
	}
	var deletedEmail, deletedStatus string
	if err = s.pool.QueryRow(ctx, `SELECT email,status FROM users WHERE id=$1`, userID).Scan(&deletedEmail, &deletedStatus); err != nil {
		t.Fatal(err)
	}
	if deletedStatus != "CLOSED" || deletedEmail == email {
		t.Fatalf("email=%s status=%s", deletedEmail, deletedStatus)
	}
	var audits int64
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='data_lifecycle_job' AND resource_id=$1`, deleteJob.ID).Scan(&audits); err != nil || audits == 0 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}
