package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func (s *Store) GatewayRiskCheck(ctx context.Context, userID, organizationID string) (bool, string, error) {
	var userAbuse, orgAbuse, userPayment, orgPayment string
	var userScore, orgScore int
	err := s.pool.QueryRow(ctx, `SELECT u.abuse_status,u.payment_risk,u.risk_score,o.abuse_status,o.payment_risk,o.risk_score
		FROM users u JOIN organizations o ON o.id=$2 WHERE u.id=$1`, userID, organizationID).
		Scan(&userAbuse, &userPayment, &userScore, &orgAbuse, &orgPayment, &orgScore)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "risk_subject_not_found", ErrNotFound
	}
	if err != nil {
		return false, "risk_state_unavailable", err
	}
	if userAbuse == "FROZEN" || orgAbuse == "FROZEN" || userPayment == "BLOCKED" || orgPayment == "BLOCKED" {
		return false, "risk_frozen", ErrRiskFrozen
	}
	if userAbuse == "RESTRICTED" || orgAbuse == "RESTRICTED" || userScore >= 90 || orgScore >= 90 {
		return false, "risk_restricted", ErrRiskRestricted
	}
	var spendExceeded bool
	if err = s.pool.QueryRow(ctx, `SELECT
		(u.created_at>=now()-interval '7 days' AND u.new_account_spend_limit IS NOT NULL AND COALESCE((SELECT sum(estimated_cost) FROM request_logs WHERE user_id=u.id),0)>=u.new_account_spend_limit)
		OR (o.created_at>=now()-interval '7 days' AND o.new_account_spend_limit IS NOT NULL AND COALESCE((SELECT sum(estimated_cost) FROM request_logs WHERE organization_id=o.id),0)>=o.new_account_spend_limit)
		FROM users u JOIN organizations o ON o.id=$2 WHERE u.id=$1`, userID, organizationID).Scan(&spendExceeded); err != nil {
		return false, "risk_state_unavailable", err
	}
	if spendExceeded {
		return false, "new_account_spend_limit", ErrRiskRestricted
	}
	return true, "clear", nil
}

func (s *Store) UpdateRiskProfile(ctx context.Context, subjectType, subjectID string, riskScore int, verificationLevel, paymentRisk, abuseStatus, manualReviewStatus, spendLimit, actor, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var tag pgconn.CommandTag
	if subjectType == "USER" {
		tag, err = tx.Exec(ctx, `UPDATE users SET risk_score=$2,verification_level=$3,payment_risk=$4,abuse_status=$5,manual_review_status=$6,new_account_spend_limit=NULLIF($7,'')::numeric,updated_at=now() WHERE id=$1`, subjectID, riskScore, verificationLevel, paymentRisk, abuseStatus, manualReviewStatus, spendLimit)
	} else if subjectType == "ORGANIZATION" {
		tag, err = tx.Exec(ctx, `UPDATE organizations SET risk_score=$2,verification_level=$3,payment_risk=$4,abuse_status=$5,manual_review_status=$6,new_account_spend_limit=NULLIF($7,'')::numeric,updated_at=now() WHERE id=$1`, subjectID, riskScore, verificationLevel, paymentRisk, abuseStatus, manualReviewStatus, spendLimit)
	} else {
		return errors.New("invalid risk subject type")
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, actor, "risk.profile_updated", strings.ToLower(subjectType), subjectID, ip, map[string]any{"risk_score": riskScore, "verification_level": verificationLevel, "payment_risk": paymentRisk, "abuse_status": abuseStatus, "manual_review_status": manualReviewStatus, "new_account_spend_limit": spendLimit}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordRiskEvent(ctx context.Context, event domain.RiskEvent, ipHash, deviceHash []byte) (domain.RiskEvent, bool, error) {
	if strings.TrimSpace(event.IdempotencyKey) == "" || strings.TrimSpace(event.EventType) == "" {
		return event, false, errors.New("idempotency_key and event_type are required")
	}
	event.ID = id.UUID()
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return event, false, err
	}
	defer tx.Rollback(ctx)
	var existing string
	err = tx.QueryRow(ctx, `SELECT id FROM risk_events WHERE idempotency_key=$1`, event.IdempotencyKey).Scan(&existing)
	if err == nil {
		event.ID = existing
		_ = tx.Commit(ctx)
		return event, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return event, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO risk_events(id,idempotency_key,user_id,organization_id,event_type,ip_hash,device_hash,score_delta,metadata)
		VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9)`, event.ID, event.IdempotencyKey, event.UserID, event.OrganizationID, event.EventType, ipHash, deviceHash, event.ScoreDelta, jsonBytes(event.Metadata)); err != nil {
		return event, false, err
	}
	if event.UserID != "" {
		_, err = tx.Exec(ctx, `UPDATE users SET risk_score=LEAST(100,GREATEST(0,risk_score+$2)),updated_at=now() WHERE id=$1`, event.UserID, event.ScoreDelta)
	}
	if err == nil && event.OrganizationID != "" {
		_, err = tx.Exec(ctx, `UPDATE organizations SET risk_score=LEAST(100,GREATEST(0,risk_score+$2)),updated_at=now() WHERE id=$1`, event.OrganizationID, event.ScoreDelta)
	}
	if err == nil {
		err = insertSecurityAudit(ctx, tx, event.UserID, "risk.event", "risk_event", event.ID, "", map[string]any{"event_type": event.EventType, "organization_id": event.OrganizationID, "score_delta": event.ScoreDelta})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return event, false, err
}

func (s *Store) FreezeAPIKeyForLeak(ctx context.Context, keyID, reason, actor, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE api_keys SET status='DISABLED',frozen_reason=$2,frozen_at=COALESCE(frozen_at,now()),last_leak_detected_at=now(),updated_at=now() WHERE id=$1 AND status<>'REVOKED'`, keyID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE api_key_versions SET status='REVOKED',grace_expires_at=NULL WHERE api_key_id=$1`, keyID); err != nil {
		return err
	}
	if err = insertSecurityAudit(ctx, tx, actor, "api_key.leak_frozen", "api_key", keyID, ip, map[string]any{"reason": reason}); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,resource_type,resource_id,dedupe_key,details,last_seen_at)
		VALUES($1,'API_KEY_SUSPECTED_LEAK','CRITICAL','A RelayDock API key was automatically frozen after leak detection.','api_key',$2,$3,$4,now())
		ON CONFLICT DO NOTHING`, id.UUID(), keyID, "api-key-leak:"+keyID, jsonBytes(map[string]any{"reason": reason})); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FreezeAPIKeyByHash(ctx context.Context, hash []byte, reason, actor, ip string) error {
	var keyID string
	if err := s.pool.QueryRow(ctx, `SELECT key_id FROM (SELECT id AS key_id FROM api_keys WHERE key_hash=$1 UNION SELECT api_key_id FROM api_key_versions WHERE key_hash=$1) matched LIMIT 1`, hash).Scan(&keyID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return s.FreezeAPIKeyForLeak(ctx, keyID, reason, actor, ip)
}

func (s *Store) CheckRechargeRisk(ctx context.Context, organizationID, userID, amount string) error {
	return checkRechargeRisk(ctx, s.pool, organizationID, userID, amount, false)
}

type governanceQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func checkRechargeRisk(ctx context.Context, query governanceQueryRower, organizationID, userID, amount string, lock bool) error {
	var userPayment, userAbuse, orgPayment, orgAbuse, userLimit, orgLimit string
	var userScore, orgScore int
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF u,o"
	}
	err := query.QueryRow(ctx, `SELECT u.payment_risk,u.abuse_status,u.risk_score,COALESCE(u.new_account_spend_limit::text,''),
		o.payment_risk,o.abuse_status,o.risk_score,COALESCE(o.new_account_spend_limit::text,'')
		FROM users u JOIN organizations o ON o.id=$1 WHERE u.id=$2`+lockClause, organizationID, userID).
		Scan(&userPayment, &userAbuse, &userScore, &userLimit, &orgPayment, &orgAbuse, &orgScore, &orgLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if userPayment == "BLOCKED" || orgPayment == "BLOCKED" || userAbuse == "FROZEN" || orgAbuse == "FROZEN" {
		return ErrRiskFrozen
	}
	if userPayment == "HIGH" || orgPayment == "HIGH" || userAbuse == "RESTRICTED" || orgAbuse == "RESTRICTED" || userScore >= 90 || orgScore >= 90 {
		return ErrRiskRestricted
	}
	for _, limit := range []string{userLimit, orgLimit} {
		if limit == "" {
			continue
		}
		var exceeded bool
		if err := query.QueryRow(ctx, `SELECT $1::numeric > $2::numeric`, amount, limit).Scan(&exceeded); err != nil {
			return err
		}
		if exceeded {
			return ErrRiskRestricted
		}
	}
	return nil
}

func (s *Store) CreateContentPolicy(ctx context.Context, p domain.ContentPolicy, actor string) (domain.ContentPolicy, error) {
	p.ID = id.UUID()
	if p.ProviderName == "" {
		p.ProviderName = "builtin"
	}
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	p.LegalReviewRequired = true
	err := s.pool.QueryRow(ctx, `INSERT INTO content_policies(id,organization_id,model_id,phase,action,failure_mode,provider_name,config,enabled,legal_review_required,created_by)
		VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,true,NULLIF($10,'')::uuid) RETURNING created_at,updated_at`, p.ID, p.OrganizationID, p.ModelID, p.Phase, p.Action, p.FailureMode, p.ProviderName, jsonBytes(p.Config), p.Enabled, actor).Scan(&p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) ListContentPolicies(ctx context.Context, organizationID string, phase string) ([]domain.ContentPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,COALESCE(organization_id::text,''),COALESCE(model_id::text,''),phase,action,failure_mode,provider_name,config,enabled,legal_review_required,created_at,updated_at FROM content_policies WHERE enabled AND ($1='' OR organization_id=$1::uuid) AND ($2='' OR phase=$2) ORDER BY created_at DESC`, organizationID, phase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ContentPolicy{}
	for rows.Next() {
		var p domain.ContentPolicy
		var cfg []byte
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.ModelID, &p.Phase, &p.Action, &p.FailureMode, &p.ProviderName, &cfg, &p.Enabled, &p.LegalReviewRequired, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cfg, &p.Config)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) EffectiveContentPolicies(ctx context.Context, organizationID, model, phase string) ([]domain.ContentPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id,COALESCE(p.organization_id::text,''),COALESCE(p.model_id::text,''),p.phase,p.action,p.failure_mode,p.provider_name,p.config,p.enabled,p.legal_review_required,p.created_at,p.updated_at FROM content_policies p WHERE p.enabled AND p.phase=$3 AND (p.organization_id IS NULL OR p.organization_id=$1::uuid) AND (p.model_id IS NULL OR p.model_id IN (SELECT m.id FROM models m WHERE m.provider_model_id=$2 OR m.id::text=$2 OR EXISTS(SELECT 1 FROM model_routes r WHERE r.provider_id=m.provider_id AND r.upstream_model=m.provider_model_id AND r.alias=$2))) ORDER BY (p.organization_id IS NOT NULL) DESC,(p.model_id IS NOT NULL) DESC,p.created_at`, organizationID, model, phase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ContentPolicy{}
	for rows.Next() {
		var p domain.ContentPolicy
		var cfg []byte
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.ModelID, &p.Phase, &p.Action, &p.FailureMode, &p.ProviderName, &cfg, &p.Enabled, &p.LegalReviewRequired, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cfg, &p.Config)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DetectUsageAnomalies(ctx context.Context, logEntry domain.RequestLog) error {
	if logEntry.APIKeyID == "" || logEntry.OrganizationID == "" {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	alerts := []struct{ fingerprint, kind, severity, message, resourceType, resourceID string }{}
	if logEntry.StatusCode >= 400 {
		var failures int64
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM request_logs WHERE api_key_id=$1 AND status_code>=400 AND created_at>=now()-interval '5 minutes'`, logEntry.APIKeyID).Scan(&failures); err != nil {
			return err
		}
		if failures >= 20 {
			alerts = append(alerts, struct{ fingerprint, kind, severity, message, resourceType, resourceID string }{"high-failure:" + logEntry.APIKeyID + ":" + time.Now().UTC().Format("200601021504"), "HIGH_FREQUENCY_FAILURE", "WARNING", "High-frequency failed requests detected", "api_key", logEntry.APIKeyID})
		}
	}
	var recent, baseline string
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN created_at>=now()-interval '10 minutes' THEN estimated_cost ELSE 0 END),0)::text,COALESCE(sum(CASE WHEN created_at>=now()-interval '70 minutes' AND created_at<now()-interval '10 minutes' THEN estimated_cost ELSE 0 END)/6,0)::text FROM request_logs WHERE organization_id=$1`, logEntry.OrganizationID).Scan(&recent, &baseline)
	if err != nil {
		return err
	}
	var abnormal bool
	if err = tx.QueryRow(ctx, `SELECT $1::numeric>1 AND $1::numeric>GREATEST($2::numeric*5,0.01)`, recent, baseline).Scan(&abnormal); err != nil {
		return err
	}
	if abnormal {
		alerts = append(alerts, struct{ fingerprint, kind, severity, message, resourceType, resourceID string }{"balance-consumption:" + logEntry.OrganizationID + ":" + time.Now().UTC().Format("2006010215"), "ABNORMAL_BALANCE_CONSUMPTION", "CRITICAL", "Abnormal organization balance consumption detected", "organization", logEntry.OrganizationID})
	}
	for _, a := range alerts {
		alertID := id.UUID()
		var inserted bool
		insertErr := tx.QueryRow(ctx, `INSERT INTO anomaly_alert_dedupe(fingerprint,alert_id) VALUES($1,$2) ON CONFLICT(fingerprint) DO UPDATE SET last_seen_at=now(),count=anomaly_alert_dedupe.count+1 RETURNING (xmax=0)`, a.fingerprint, alertID).Scan(&inserted)
		if insertErr != nil {
			return insertErr
		}
		if inserted {
			if _, insertErr = tx.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,resource_type,resource_id) VALUES($1,$2,$3,$4,$5,$6)`, alertID, a.kind, a.severity, a.message, a.resourceType, a.resourceID); insertErr != nil {
				return insertErr
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateManualReview(ctx context.Context, review domain.ManualReview) (domain.ManualReview, error) {
	review.ID = id.UUID()
	if review.Status == "" {
		review.Status = "PENDING"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return review, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO manual_review_queue(id,organization_id,user_id,request_id,policy_id,reason,status,due_at) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,NULLIF($4,''),NULLIF($5,'')::uuid,$6,$7,$8) RETURNING created_at,updated_at`, review.ID, review.OrganizationID, review.UserID, review.RequestID, review.PolicyID, review.Reason, review.Status, review.DueAt).Scan(&review.CreatedAt, &review.UpdatedAt)
	if err != nil {
		return review, err
	}
	if err = insertSecurityAudit(ctx, tx, review.UserID, "manual_review.create", "manual_review", review.ID, "", map[string]any{"policy_id": review.PolicyID, "request_id": review.RequestID, "status": review.Status}); err != nil {
		return review, err
	}
	return review, tx.Commit(ctx)
}

func (s *Store) CreateUserReport(ctx context.Context, report domain.UserReport, actor string) (domain.UserReport, error) {
	report.ID = id.UUID()
	if report.SLAHours <= 0 {
		report.SLAHours = 72
	}
	if report.DueAt.IsZero() {
		report.DueAt = time.Now().UTC().Add(time.Duration(report.SLAHours) * time.Hour)
	}
	if report.Status == "" {
		report.Status = "OPEN"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO user_reports(id,reporter_user_id,organization_id,report_type,request_id,api_key_id,recharge_order_id,description,status,sla_hours,due_at) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,NULLIF($4,''),NULLIF($5,''),NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8,$9,$10,$11) RETURNING created_at,updated_at`, report.ID, report.ReporterUserID, report.OrganizationID, report.ReportType, report.RequestID, report.APIKeyID, report.RechargeOrderID, report.Description, report.Status, report.SLAHours, report.DueAt).Scan(&report.CreatedAt, &report.UpdatedAt)
	if err != nil {
		return report, err
	}
	if err = insertSecurityAudit(ctx, tx, actor, "report.create", "user_report", report.ID, "", map[string]any{"report_type": report.ReportType, "organization_id": report.OrganizationID, "status": report.Status, "due_at": report.DueAt}); err != nil {
		return report, err
	}
	return report, tx.Commit(ctx)
}

func (s *Store) ListUserReports(ctx context.Context, status string, limit, offset int) ([]domain.UserReport, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,COALESCE(reporter_user_id::text,''),COALESCE(organization_id::text,''),report_type,COALESCE(request_id,''),COALESCE(api_key_id::text,''),COALESCE(recharge_order_id::text,''),description,status,sla_hours,due_at,COALESCE(resolution,''),COALESCE(handled_by::text,''),created_at,updated_at FROM user_reports WHERE ($1='' OR status=$1) ORDER BY due_at,created_at LIMIT $2 OFFSET $3`, status, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.UserReport{}
	for rows.Next() {
		var r domain.UserReport
		if err := rows.Scan(&r.ID, &r.ReporterUserID, &r.OrganizationID, &r.ReportType, &r.RequestID, &r.APIKeyID, &r.RechargeOrderID, &r.Description, &r.Status, &r.SLAHours, &r.DueAt, &r.Resolution, &r.HandledBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListUserReportsForReporter(ctx context.Context, reporterID, status string, limit, offset int) ([]domain.UserReport, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,COALESCE(reporter_user_id::text,''),COALESCE(organization_id::text,''),report_type,COALESCE(request_id,''),COALESCE(api_key_id::text,''),COALESCE(recharge_order_id::text,''),description,status,sla_hours,due_at,COALESCE(resolution,''),COALESCE(handled_by::text,''),created_at,updated_at FROM user_reports WHERE reporter_user_id=$1 AND ($2='' OR status=$2) ORDER BY created_at DESC LIMIT $3 OFFSET $4`, reporterID, status, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.UserReport{}
	for rows.Next() {
		var report domain.UserReport
		if err := rows.Scan(&report.ID, &report.ReporterUserID, &report.OrganizationID, &report.ReportType, &report.RequestID, &report.APIKeyID, &report.RechargeOrderID, &report.Description, &report.Status, &report.SLAHours, &report.DueAt, &report.Resolution, &report.HandledBy, &report.CreatedAt, &report.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, report)
	}
	return out, rows.Err()
}

func (s *Store) ListRiskEvents(ctx context.Context, limit, offset int) ([]domain.RiskEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,idempotency_key,COALESCE(user_id::text,''),COALESCE(organization_id::text,''),event_type,score_delta,metadata,created_at FROM risk_events ORDER BY created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RiskEvent{}
	for rows.Next() {
		var e domain.RiskEvent
		var metadata []byte
		if err := rows.Scan(&e.ID, &e.IdempotencyKey, &e.UserID, &e.OrganizationID, &e.EventType, &e.ScoreDelta, &metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metadata, &e.Metadata)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListManualReviews(ctx context.Context, status string, limit, offset int) ([]domain.ManualReview, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,COALESCE(organization_id::text,''),COALESCE(user_id::text,''),COALESCE(request_id,''),COALESCE(policy_id::text,''),reason,status,COALESCE(resolution,''),COALESCE(assigned_to::text,''),due_at,created_at,updated_at FROM manual_review_queue WHERE ($1='' OR status=$1) ORDER BY due_at NULLS LAST,created_at LIMIT $2 OFFSET $3`, strings.ToUpper(status), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ManualReview{}
	for rows.Next() {
		var r domain.ManualReview
		if err := rows.Scan(&r.ID, &r.OrganizationID, &r.UserID, &r.RequestID, &r.PolicyID, &r.Reason, &r.Status, &r.Resolution, &r.AssignedTo, &r.DueAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateManualReview(ctx context.Context, reviewID, status, resolution, actor, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE manual_review_queue SET status=$2,resolution=NULLIF($3,''),assigned_to=NULLIF($4,'')::uuid,updated_at=now() WHERE id=$1`, reviewID, status, resolution, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, actor, "manual_review.update", "manual_review", reviewID, ip, map[string]any{"status": status, "resolution_recorded": strings.TrimSpace(resolution) != ""}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListLifecycleJobs(ctx context.Context, limit, offset int) ([]domain.DataLifecycleJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,subject_type,subject_id,job_type,status,legal_hold,idempotency_key,completed_at,evidence,created_at,updated_at FROM data_lifecycle_jobs ORDER BY created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.DataLifecycleJob{}
	for rows.Next() {
		var j domain.DataLifecycleJob
		var evidence []byte
		if err := rows.Scan(&j.ID, &j.SubjectType, &j.SubjectID, &j.JobType, &j.Status, &j.LegalHold, &j.IdempotencyKey, &j.CompletedAt, &evidence, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &j.Evidence)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) ListLifecycleJobsForSubject(ctx context.Context, subjectType, subjectID string, limit, offset int) ([]domain.DataLifecycleJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,subject_type,subject_id,job_type,status,legal_hold,idempotency_key,completed_at,evidence,created_at,updated_at FROM data_lifecycle_jobs WHERE subject_type=$1 AND subject_id=$2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`, subjectType, subjectID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.DataLifecycleJob{}
	for rows.Next() {
		var job domain.DataLifecycleJob
		var evidence []byte
		if err := rows.Scan(&job.ID, &job.SubjectType, &job.SubjectID, &job.JobType, &job.Status, &job.LegalHold, &job.IdempotencyKey, &job.CompletedAt, &evidence, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &job.Evidence)
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUserReport(ctx context.Context, reportID, status, resolution, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE user_reports SET status=$2,resolution=NULLIF($3,''),handled_by=NULLIF($4,'')::uuid,updated_at=now() WHERE id=$1`, reportID, status, resolution, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, actor, "report.update", "user_report", reportID, "", map[string]any{"status": status, "resolution_recorded": strings.TrimSpace(resolution) != ""}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpsertPrivacySettings(ctx context.Context, p domain.PrivacySettings, actor string) (domain.PrivacySettings, error) {
	if p.RetentionDays <= 0 {
		p.RetentionDays = 30
	}
	if strings.TrimSpace(p.CrossBorderRoute) == "" {
		p.CrossBorderRoute = "UNSPECIFIED"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO privacy_settings(subject_type,subject_id,save_content,retention_days,cross_border_route,legal_hold,updated_by) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid) ON CONFLICT(subject_type,subject_id) DO UPDATE SET save_content=EXCLUDED.save_content,retention_days=EXCLUDED.retention_days,cross_border_route=EXCLUDED.cross_border_route,legal_hold=EXCLUDED.legal_hold,updated_by=EXCLUDED.updated_by,updated_at=now()`, p.SubjectType, p.SubjectID, p.SaveContent, p.RetentionDays, p.CrossBorderRoute, p.LegalHold, actor)
	if err != nil {
		return p, err
	}
	return s.PrivacySettingsBySubject(ctx, p.SubjectType, p.SubjectID)
}
func (s *Store) PrivacySettingsBySubject(ctx context.Context, typ, subjectID string) (domain.PrivacySettings, error) {
	var p domain.PrivacySettings
	err := s.pool.QueryRow(ctx, `SELECT subject_type,subject_id,save_content,retention_days,cross_border_route,legal_hold,updated_at FROM privacy_settings WHERE subject_type=$1 AND subject_id=$2`, typ, subjectID).Scan(&p.SubjectType, &p.SubjectID, &p.SaveContent, &p.RetentionDays, &p.CrossBorderRoute, &p.LegalHold, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		p = domain.PrivacySettings{SubjectType: typ, SubjectID: subjectID, RetentionDays: 30, CrossBorderRoute: "UNSPECIFIED"}
		return p, nil
	}
	return p, err
}

func (s *Store) CreateLifecycleJob(ctx context.Context, job domain.DataLifecycleJob, actor string) (domain.DataLifecycleJob, bool, error) {
	requestedID := id.UUID()
	job.ID = requestedID
	if job.Status == "" {
		job.Status = "REQUESTED"
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO data_lifecycle_jobs(id,subject_type,subject_id,job_type,status,legal_hold,idempotency_key,requested_by) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid) ON CONFLICT(idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key WHERE data_lifecycle_jobs.subject_type=EXCLUDED.subject_type AND data_lifecycle_jobs.subject_id=EXCLUDED.subject_id AND data_lifecycle_jobs.job_type=EXCLUDED.job_type RETURNING id,status,legal_hold,created_at,updated_at`, job.ID, job.SubjectType, job.SubjectID, job.JobType, job.Status, job.LegalHold, job.IdempotencyKey, actor).Scan(&job.ID, &job.Status, &job.LegalHold, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, false, ErrIdempotencyConflict
	}
	return job, job.ID != requestedID, err
}

func (s *Store) CheckOrganizationGovernanceAccess(ctx context.Context, userID, organizationID string, minimumRank int) error {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT om.role FROM organization_memberships om JOIN organizations o ON o.id=om.organization_id WHERE om.user_id=$1 AND om.organization_id=$2 AND om.status='ACTIVE' AND o.status='ACTIVE'`, userID, organizationID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	rank := map[string]int{"VIEWER": 1, "MEMBER": 2, "ADMIN": 3, "OWNER": 4}[role]
	if rank < minimumRank {
		return ErrNotFound
	}
	return nil
}

func (s *Store) LifecycleJobByID(ctx context.Context, jobID string) (domain.DataLifecycleJob, error) {
	var j domain.DataLifecycleJob
	var evidence []byte
	err := s.pool.QueryRow(ctx, `SELECT id,subject_type,subject_id,job_type,status,legal_hold,idempotency_key,completed_at,evidence,created_at,updated_at FROM data_lifecycle_jobs WHERE id=$1`, jobID).Scan(&j.ID, &j.SubjectType, &j.SubjectID, &j.JobType, &j.Status, &j.LegalHold, &j.IdempotencyKey, &j.CompletedAt, &evidence, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	_ = json.Unmarshal(evidence, &j.Evidence)
	return j, err
}

func (s *Store) ExportSubjectData(ctx context.Context, subjectType, subjectID string) (map[string]any, error) {
	out := map[string]any{"subject_type": subjectType, "subject_id": subjectID, "generated_at": time.Now().UTC(), "content_storage_default": false}
	if subjectType == "USER" {
		u, err := s.UserByID(ctx, subjectID)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = ""
		out["user"] = u
		keys, err := s.ListAPIKeys(ctx, &subjectID, 1000, 0)
		if err != nil {
			return nil, err
		}
		out["api_keys"] = keys
		var reports int64
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM user_reports WHERE reporter_user_id=$1`, subjectID).Scan(&reports)
		out["report_count"] = reports
	} else {
		o, err := s.OrganizationByID(ctx, subjectID)
		if err != nil {
			return nil, err
		}
		out["organization"] = o
		var requests int64
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM request_logs WHERE organization_id=$1`, subjectID).Scan(&requests)
		out["request_log_count"] = requests
	}
	return out, nil
}

func (s *Store) ProcessNextLifecycleJob(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var job domain.DataLifecycleJob
	err = tx.QueryRow(ctx, `SELECT id,subject_type,subject_id,job_type,status,legal_hold,idempotency_key FROM data_lifecycle_jobs WHERE status='REQUESTED' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&job.ID, &job.SubjectType, &job.SubjectID, &job.JobType, &job.Status, &job.LegalHold, &job.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var legalHold bool
	_ = tx.QueryRow(ctx, `SELECT COALESCE((SELECT legal_hold FROM privacy_settings WHERE subject_type=$1 AND subject_id=$2),false)`, job.SubjectType, job.SubjectID).Scan(&legalHold)
	if job.SubjectType == "USER" {
		var hold bool
		_ = tx.QueryRow(ctx, `SELECT legal_hold FROM users WHERE id=$1`, job.SubjectID).Scan(&hold)
		legalHold = legalHold || hold
	} else {
		var hold bool
		_ = tx.QueryRow(ctx, `SELECT legal_hold FROM organizations WHERE id=$1`, job.SubjectID).Scan(&hold)
		legalHold = legalHold || hold
	}
	if legalHold && (job.JobType == "CLOSE" || job.JobType == "DELETE" || job.JobType == "PURGE") {
		_, err = tx.Exec(ctx, `UPDATE data_lifecycle_jobs SET status='BLOCKED',legal_hold=true,evidence='{"reason":"legal_hold"}'::jsonb,updated_at=now() WHERE id=$1`, job.ID)
		if err == nil {
			err = insertSecurityAudit(ctx, tx, "", "privacy.lifecycle_blocked", "data_lifecycle_job", job.ID, "", map[string]any{"reason": "legal_hold"})
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		return true, err
	}
	evidence := map[string]any{"audit_preserved": true, "financial_records_preserved": true}
	switch job.JobType {
	case "EXPORT":
		evidence["export_ready"] = true
	case "CLOSE":
		if job.SubjectType == "USER" {
			_, err = tx.Exec(ctx, `UPDATE users SET status='CLOSED',closed_at=COALESCE(closed_at,now()),session_version=session_version+1,updated_at=now() WHERE id=$1`, job.SubjectID)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE api_keys SET status='REVOKED',updated_at=now() WHERE user_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE api_key_versions SET status='REVOKED',grace_expires_at=NULL WHERE api_key_id IN(SELECT id FROM api_keys WHERE user_id=$1)`, job.SubjectID)
			}
		} else {
			_, err = tx.Exec(ctx, `UPDATE organizations SET status='ARCHIVED',updated_at=now() WHERE id=$1`, job.SubjectID)
		}
	case "DELETE":
		if job.SubjectType == "USER" {
			var email string
			err = tx.QueryRow(ctx, `SELECT email FROM users WHERE id=$1 FOR UPDATE`, job.SubjectID).Scan(&email)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE users SET email='deleted+'||replace(id::text,'-','')||'@example.invalid',display_name='Deleted User',password_hash='DELETED',status='CLOSED',closed_at=COALESCE(closed_at,now()),session_version=session_version+1,updated_at=now() WHERE id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM account_tokens WHERE user_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM email_outbox WHERE lower(recipient)=lower($1)`, email)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE api_keys SET status='REVOKED',updated_at=now() WHERE user_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE api_key_versions SET status='REVOKED',grace_expires_at=NULL WHERE api_key_id IN(SELECT id FROM api_keys WHERE user_id=$1)`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE user_reports SET description='[redacted by account deletion]',resolution=CASE WHEN resolution IS NULL THEN NULL ELSE '[redacted by account deletion]' END,updated_at=now() WHERE reporter_user_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE manual_review_queue SET reason='[redacted by account deletion]',resolution=CASE WHEN resolution IS NULL THEN NULL ELSE '[redacted by account deletion]' END,updated_at=now() WHERE user_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE risk_events SET ip_hash=NULL,device_hash=NULL,metadata='{}'::jsonb WHERE user_id=$1`, job.SubjectID)
			}
			evidence["profile_anonymized"] = true
		} else {
			_, err = tx.Exec(ctx, `UPDATE organizations SET name='Deleted organization',slug='deleted-'||replace(id::text,'-',''),status='ARCHIVED',metadata='{}'::jsonb,updated_at=now() WHERE id=$1`, job.SubjectID)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE user_reports SET description='[redacted by account deletion]',resolution=CASE WHEN resolution IS NULL THEN NULL ELSE '[redacted by account deletion]' END,updated_at=now() WHERE organization_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE manual_review_queue SET reason='[redacted by account deletion]',resolution=CASE WHEN resolution IS NULL THEN NULL ELSE '[redacted by account deletion]' END,updated_at=now() WHERE organization_id=$1`, job.SubjectID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE risk_events SET ip_hash=NULL,device_hash=NULL,metadata='{}'::jsonb WHERE organization_id=$1`, job.SubjectID)
			}
			evidence["organization_anonymized"] = true
		}
	case "PURGE":
		evidence["content_rows_removed"] = 0
		evidence["reason"] = "content_not_stored_by_default"
	default:
		err = errors.New("unsupported lifecycle job type")
	}
	if err != nil {
		return true, err
	}
	_, err = tx.Exec(ctx, `UPDATE data_lifecycle_jobs SET status='COMPLETED',completed_at=now(),evidence=$2,updated_at=now() WHERE id=$1`, job.ID, jsonBytes(evidence))
	if err == nil {
		err = insertSecurityAudit(ctx, tx, "", "privacy.lifecycle_completed", "data_lifecycle_job", job.ID, "", evidence)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return true, err
}

func (s *Store) CleanupExpiredGovernanceData(ctx context.Context) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var total int64
	tag, err := tx.Exec(ctx, `UPDATE user_reports r SET description='[redacted by retention policy]',resolution=CASE WHEN resolution IS NULL THEN NULL ELSE '[redacted by retention policy]' END,updated_at=now()
		WHERE r.status IN('RESOLVED','REJECTED') AND r.description<>'[redacted by retention policy]'
		AND NOT COALESCE((SELECT legal_hold FROM privacy_settings WHERE subject_type='USER' AND subject_id=r.reporter_user_id),false)
		AND NOT COALESCE((SELECT legal_hold FROM privacy_settings WHERE subject_type='ORGANIZATION' AND subject_id=r.organization_id),false)
		AND NOT COALESCE((SELECT legal_hold FROM users WHERE id=r.reporter_user_id),false)
		AND NOT COALESCE((SELECT legal_hold FROM organizations WHERE id=r.organization_id),false)
		AND r.created_at < now()-(COALESCE(LEAST(
			(SELECT retention_days FROM privacy_settings WHERE subject_type='USER' AND subject_id=r.reporter_user_id),
			(SELECT retention_days FROM privacy_settings WHERE subject_type='ORGANIZATION' AND subject_id=r.organization_id)),30)||' days')::interval`)
	if err != nil {
		return 0, err
	}
	total += tag.RowsAffected()
	tag, err = tx.Exec(ctx, `UPDATE risk_events r SET ip_hash=NULL,device_hash=NULL,metadata='{}'::jsonb
		WHERE (r.ip_hash IS NOT NULL OR r.device_hash IS NOT NULL OR r.metadata<>'{}'::jsonb)
		AND NOT COALESCE((SELECT legal_hold FROM privacy_settings WHERE subject_type='USER' AND subject_id=r.user_id),false)
		AND NOT COALESCE((SELECT legal_hold FROM privacy_settings WHERE subject_type='ORGANIZATION' AND subject_id=r.organization_id),false)
		AND NOT COALESCE((SELECT legal_hold FROM users WHERE id=r.user_id),false)
		AND NOT COALESCE((SELECT legal_hold FROM organizations WHERE id=r.organization_id),false)
		AND r.created_at < now()-(COALESCE(LEAST(
			(SELECT retention_days FROM privacy_settings WHERE subject_type='USER' AND subject_id=r.user_id),
			(SELECT retention_days FROM privacy_settings WHERE subject_type='ORGANIZATION' AND subject_id=r.organization_id)),30)||' days')::interval`)
	if err != nil {
		return 0, err
	}
	total += tag.RowsAffected()
	if total > 0 {
		if err = insertSecurityAudit(ctx, tx, "", "privacy.retention_cleanup", "governance_data", "", "", map[string]any{"rows_redacted": total}); err != nil {
			return 0, err
		}
	}
	return total, tx.Commit(ctx)
}
