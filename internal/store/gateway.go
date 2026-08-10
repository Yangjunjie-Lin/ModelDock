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

const apiKeyColumns = `id,user_id,name,environment,key_prefix,key_hash,status,expires_at,rate_limit_rpm,rate_limit_tpm,monthly_token_limit,monthly_cost_limit,allowed_models,created_at,updated_at,last_used_at`

func scanAPIKey(row pgx.Row) (domain.APIKey, error) {
	var k domain.APIKey
	var allowed []byte
	err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.Environment, &k.KeyPrefix, &k.KeyHash, &k.Status, &k.ExpiresAt, &k.RateLimitRPM, &k.RateLimitTPM, &k.MonthlyTokenLimit, &k.MonthlyCostLimit, &allowed, &k.CreatedAt, &k.UpdatedAt, &k.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrNotFound
	}
	if err != nil {
		return k, err
	}
	_ = json.Unmarshal(allowed, &k.AllowedModels)
	if k.AllowedModels == nil {
		k.AllowedModels = []string{}
	}
	return k, nil
}
func (s *Store) CreateAPIKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error) {
	k.ID = id.UUID()
	_, err := s.pool.Exec(ctx, `INSERT INTO api_keys(id,user_id,name,environment,key_prefix,key_hash,status,expires_at,rate_limit_rpm,rate_limit_tpm,monthly_token_limit,monthly_cost_limit,allowed_models) VALUES($1,$2,$3,$4,$5,$6,'ACTIVE',$7,$8,$9,$10,$11,$12)`, k.ID, k.UserID, k.Name, k.Environment, k.KeyPrefix, k.KeyHash, k.ExpiresAt, k.RateLimitRPM, k.RateLimitTPM, k.MonthlyTokenLimit, k.MonthlyCostLimit, jsonBytes(k.AllowedModels))
	if err != nil {
		return k, err
	}
	return scanAPIKey(s.pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE id=$1`, k.ID))
}
func (s *Store) ListAPIKeys(ctx context.Context, userID *string, limit, offset int) ([]domain.APIKey, error) {
	query := `SELECT ` + apiKeyColumns + ` FROM api_keys`
	args := []any{}
	if userID != nil {
		query += ` WHERE user_id=$1`
		args = append(args, *userID)
	}
	n := len(args)
	query += ` ORDER BY created_at DESC LIMIT $` + itoa(n+1) + ` OFFSET $` + itoa(n+2)
	args = append(args, clamp(limit), max(offset, 0))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Store) APIKeyByHash(ctx context.Context, hash []byte) (domain.APIKey, error) {
	k, err := scanAPIKey(s.pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE key_hash=$1`, hash))
	if err == nil {
		_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, k.ID)
	}
	return k, err
}
func (s *Store) MonthlyTokens(ctx context.Context, keyID string) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(input_tokens+output_tokens),0) FROM usage_daily WHERE api_key_id=$1 AND date>=date_trunc('month',current_date)::date`, keyID).Scan(&n)
	return n, err
}
func (s *Store) MonthlyCost(ctx context.Context, keyID string) (float64, error) {
	var n float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(cost),0)::float8 FROM usage_daily WHERE api_key_id=$1 AND date>=date_trunc('month',current_date)::date`, keyID).Scan(&n)
	return n, err
}
func (s *Store) MonthlyUserUsage(ctx context.Context, userID string) (int64, float64, error) {
	var tokens int64
	var cost float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(input_tokens+output_tokens),0),COALESCE(sum(cost),0)::float8 FROM usage_daily WHERE user_id=$1 AND date>=date_trunc('month',current_date)::date`, userID).Scan(&tokens, &cost)
	return tokens, cost, err
}
func (s *Store) RevokeAPIKey(ctx context.Context, keyID, userID string, admin bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := `UPDATE api_keys SET status='REVOKED',updated_at=now() WHERE id=$1`
	args := []any{keyID}
	if !admin {
		q += ` AND user_id=$2`
		args = append(args, userID)
	}
	tag, err := tx.Exec(ctx, q, args...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE api_key_versions SET status='REVOKED',grace_expires_at=NULL WHERE api_key_id=$1`, keyID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) UpdateAPIKeyStatus(ctx context.Context, keyID, status string) error {
	if status != "ACTIVE" && status != "DISABLED" && status != "REVOKED" {
		return errors.New("invalid API key status")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET status=$2,updated_at=now() WHERE id=$1`, keyID, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) InsertRequestLog(ctx context.Context, l domain.RequestLog) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,user_id,api_key_id,provider_id,credential_id,requested_model,resolved_model,endpoint,status_code,streaming,input_tokens,cached_input_tokens,output_tokens,total_tokens,estimated_cost,reference_cost,savings_amount,latency_ms,ttft_ms,upstream_request_id,error_code,scheduler_reason,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,COALESCE($24,now()))`, id.UUID(), l.RequestID, nullString(l.UserID), nullString(l.APIKeyID), nullString(l.ProviderID), nullString(l.CredentialID), l.RequestedModel, l.ResolvedModel, l.Endpoint, l.StatusCode, l.Streaming, l.InputTokens, l.CachedInputTokens, l.OutputTokens, l.TotalTokens, l.EstimatedCost, l.ReferenceCost, l.SavingsAmount, l.LatencyMS, l.TTFTMS, nullString(l.UpstreamRequestID), nullString(l.ErrorCode), jsonBytes(l.SchedulerReason), nullableTime(l.CreatedAt))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO usage_daily(date,user_id,api_key_id,model,requests,input_tokens,cached_input_tokens,output_tokens,cost,errors) VALUES(current_date,$1,$2,$3,1,$4,$5,$6,$7,$8) ON CONFLICT(date,user_id,api_key_id,model) DO UPDATE SET requests=usage_daily.requests+1,input_tokens=usage_daily.input_tokens+EXCLUDED.input_tokens,cached_input_tokens=usage_daily.cached_input_tokens+EXCLUDED.cached_input_tokens,output_tokens=usage_daily.output_tokens+EXCLUDED.output_tokens,cost=usage_daily.cost+EXCLUDED.cost,errors=usage_daily.errors+EXCLUDED.errors`, l.UserID, l.APIKeyID, l.ResolvedModel, l.InputTokens, l.CachedInputTokens, l.OutputTokens, l.EstimatedCost, boolInt(l.StatusCode >= 400))
	return err
}

func (s *Store) ListRequestLogs(ctx context.Context, userID *string, limit, offset int) ([]domain.RequestLog, error) {
	q := `SELECT request_id,COALESCE(user_id::text,''),COALESCE(api_key_id::text,''),organization_id::text,project_id::text,
		COALESCE(route_id::text,''),COALESCE(provider_id::text,''),COALESCE(credential_id::text,''),
		COALESCE(requested_model,''),COALESCE(resolved_model,''),endpoint,status_code,streaming,input_tokens,
		cached_input_tokens,output_tokens,total_tokens,estimated_cost::float8,reference_cost::float8,savings_amount::float8,latency_ms,ttft_ms,
		COALESCE(upstream_request_id,''),COALESCE(error_code,''),scheduler_reason,created_at FROM request_logs`
	args := []any{}
	if userID != nil {
		q += ` WHERE user_id=$1`
		args = append(args, *userID)
	}
	n := len(args)
	q += ` ORDER BY created_at DESC LIMIT $` + itoa(n+1) + ` OFFSET $` + itoa(n+2)
	args = append(args, clamp(limit), max(offset, 0))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RequestLog
	for rows.Next() {
		var l domain.RequestLog
		var reason []byte
		if err := rows.Scan(&l.RequestID, &l.UserID, &l.APIKeyID, &l.OrganizationID, &l.ProjectID, &l.RouteID,
			&l.ProviderID, &l.CredentialID, &l.RequestedModel, &l.ResolvedModel, &l.Endpoint, &l.StatusCode,
			&l.Streaming, &l.InputTokens, &l.CachedInputTokens, &l.OutputTokens, &l.TotalTokens,
			&l.EstimatedCost, &l.ReferenceCost, &l.SavingsAmount, &l.LatencyMS, &l.TTFTMS, &l.UpstreamRequestID, &l.ErrorCode, &reason, &l.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(reason, &l.SchedulerReason)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Dashboard(ctx context.Context, userID *string) (map[string]any, error) {
	where := ""
	args := []any{}
	if userID != nil {
		where = " WHERE user_id=$1"
		args = append(args, *userID)
	}
	var requests, input, cached, output, errorsCount int64
	var cost, avgLatency, p95Latency float64
	err := s.pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(input_tokens),0),COALESCE(sum(cached_input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(estimated_cost),0)::float8,COALESCE(sum(CASE WHEN status_code>=400 THEN 1 ELSE 0 END),0),COALESCE(avg(latency_ms),0)::float8,COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms),0)::float8 FROM request_logs`+where, args...).Scan(&requests, &input, &cached, &output, &cost, &errorsCount, &avgLatency, &p95Latency)
	if err != nil {
		return nil, err
	}
	var active, healthy, rateLimited int64
	if userID == nil {
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='ACTIVE'),count(*) FILTER(WHERE current_health='HEALTHY'),count(*) FILTER(WHERE status IN ('RATE_LIMITED','COOLDOWN')) FROM provider_credentials`).Scan(&active, &healthy, &rateLimited)
	}
	success := 100.0
	if requests > 0 {
		success = float64(requests-errorsCount) * 100 / float64(requests)
	}
	var today int64
	q := `SELECT count(*) FROM request_logs WHERE created_at>=date_trunc('day',now())`
	qargs := []any{}
	if userID != nil {
		q += ` AND user_id=$1`
		qargs = append(qargs, *userID)
	}
	_ = s.pool.QueryRow(ctx, q, qargs...).Scan(&today)
	requestTrend, tokenTrend, err := s.dashboardHourly(ctx, userID)
	if err != nil {
		return nil, err
	}
	modelDistribution, err := s.dashboardModels(ctx, userID)
	if err != nil {
		return nil, err
	}
	statusDistribution, err := s.dashboardStatuses(ctx, userID)
	if err != nil {
		return nil, err
	}
	alerts := []map[string]any{}
	if userID == nil {
		if alerts, err = s.ListAlerts(ctx, 4, 0); err != nil {
			return nil, err
		}
	}
	var todayTokens int64
	var todayCost, savings float64
	todayQuery := `SELECT COALESCE(sum(input_tokens+output_tokens),0),COALESCE(sum(estimated_cost),0)::float8,
		COALESCE(sum(savings_amount),0)::float8 FROM request_logs WHERE created_at>=date_trunc('day',now())`
	todayArgs := []any{}
	if userID != nil {
		todayQuery += ` AND user_id=$1`
		todayArgs = append(todayArgs, *userID)
	}
	if err := s.pool.QueryRow(ctx, todayQuery, todayArgs...).Scan(&todayTokens, &todayCost, &savings); err != nil {
		return nil, err
	}
	return map[string]any{"total_requests": requests, "requests_today": today, "today_tokens": todayTokens,
		"today_cost": todayCost, "savings_amount": savings, "input_tokens": input, "total_input_tokens": input,
		"cached_input_tokens": cached, "total_cached_tokens": cached, "output_tokens": output, "total_output_tokens": output,
		"estimated_cost": cost, "errors": errorsCount, "success_rate": success, "average_latency_ms": avgLatency,
		"p95_latency_ms": p95Latency, "active_credentials": active, "healthy_credentials": healthy,
		"rate_limited_credentials": rateLimited, "request_trend": requestTrend, "token_trend": tokenTrend,
		"model_distribution": modelDistribution, "status_code_distribution": statusDistribution, "alerts": alerts}, nil
}

func (s *Store) dashboardHourly(ctx context.Context, userID *string) ([]map[string]any, []map[string]any, error) {
	query := `WITH hours AS (
		SELECT generate_series(date_trunc('hour',now())-interval '23 hours',date_trunc('hour',now()),interval '1 hour') AS bucket
	) SELECT to_char(h.bucket,'HH24:MI'),count(r.id),COALESCE(sum(r.input_tokens),0),COALESCE(sum(r.cached_input_tokens),0),COALESCE(sum(r.output_tokens),0)
	FROM hours h LEFT JOIN request_logs r ON r.created_at>=h.bucket AND r.created_at<h.bucket+interval '1 hour'`
	args := []any{}
	if userID != nil {
		query += ` AND r.user_id=$1`
		args = append(args, *userID)
	}
	query += ` GROUP BY h.bucket ORDER BY h.bucket`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	requests := make([]map[string]any, 0, 24)
	tokens := make([]map[string]any, 0, 24)
	for rows.Next() {
		var label string
		var count, input, cached, output int64
		if err := rows.Scan(&label, &count, &input, &cached, &output); err != nil {
			return nil, nil, err
		}
		requests = append(requests, map[string]any{"label": label, "value": count, "requests": count})
		tokens = append(tokens, map[string]any{"label": label, "input": input, "cached": cached, "output": output})
	}
	return requests, tokens, rows.Err()
}

func (s *Store) dashboardModels(ctx context.Context, userID *string) ([]map[string]any, error) {
	query := `SELECT requested_model,count(*) FROM request_logs`
	args := []any{}
	if userID != nil {
		query += ` WHERE user_id=$1`
		args = append(args, *userID)
	}
	query += ` GROUP BY requested_model ORDER BY count(*) DESC,requested_model LIMIT 8`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var model string
		var count int64
		if err := rows.Scan(&model, &count); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"model": model, "value": count, "requests": count})
	}
	return result, rows.Err()
}

func (s *Store) dashboardStatuses(ctx context.Context, userID *string) ([]map[string]any, error) {
	query := `SELECT status_code,count(*) FROM request_logs`
	args := []any{}
	if userID != nil {
		query += ` WHERE user_id=$1`
		args = append(args, *userID)
	}
	query += ` GROUP BY status_code ORDER BY status_code`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var status int
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"status_code": status, "value": count, "requests": count})
	}
	return result, rows.Err()
}

func (s *Store) UsageSeries(ctx context.Context, userID *string, days int) ([]map[string]any, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	q := `SELECT date::text,sum(requests),sum(input_tokens),sum(cached_input_tokens),sum(output_tokens),sum(cost)::float8,sum(errors) FROM usage_daily WHERE date>=current_date-$1::int`
	args := []any{days}
	if userID != nil {
		q += ` AND user_id=$2`
		args = append(args, *userID)
	}
	q += ` GROUP BY date ORDER BY date`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var date string
		var req, in, cached, outTok, errs int64
		var cost float64
		if err := rows.Scan(&date, &req, &in, &cached, &outTok, &cost, &errs); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"date": date, "requests": req, "input_tokens": in, "cached_input_tokens": cached, "output_tokens": outTok, "cost": cost, "errors": errs})
	}
	return out, rows.Err()
}

func (s *Store) UsageSummary(ctx context.Context, userID string, days int) (map[string]any, error) {
	daily, err := s.UsageSeries(ctx, &userID, days)
	if err != nil {
		return nil, err
	}
	var requests, input, cached, output, errs int64
	var cost float64
	for _, row := range daily {
		requests += asInt64(row["requests"])
		input += asInt64(row["input_tokens"])
		cached += asInt64(row["cached_input_tokens"])
		output += asInt64(row["output_tokens"])
		errs += asInt64(row["errors"])
		cost += asFloat(row["cost"])
	}
	rows, err := s.pool.Query(ctx, `SELECT model,sum(requests),sum(input_tokens+output_tokens),sum(cost)::float8 FROM usage_daily WHERE user_id=$1 AND date>=current_date-$2::int GROUP BY model ORDER BY sum(requests) DESC`, userID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var byModel []map[string]any
	for rows.Next() {
		var model string
		var req, tokens int64
		var modelCost float64
		if err := rows.Scan(&model, &req, &tokens, &modelCost); err != nil {
			return nil, err
		}
		byModel = append(byModel, map[string]any{"model": model, "requests": req, "tokens": tokens, "cost": modelCost})
	}
	return map[string]any{"requests": requests, "input_tokens": input, "cached_input_tokens": cached, "output_tokens": output, "estimated_cost": cost, "errors": errs, "daily": daily, "by_model": byModel}, rows.Err()
}

func (s *Store) ConsoleOverview(ctx context.Context, userID string) (map[string]any, error) {
	var requests, tokens, errorsToday int64
	var cost float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(requests),0),COALESCE(sum(input_tokens+output_tokens),0),COALESCE(sum(cost),0)::float8,COALESCE(sum(errors),0) FROM usage_daily WHERE user_id=$1 AND date=current_date`, userID).Scan(&requests, &tokens, &cost, &errorsToday)
	if err != nil {
		return nil, err
	}
	var monthlyTokens int64
	var monthlyCost float64
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(input_tokens+output_tokens),0),COALESCE(sum(cost),0)::float8 FROM usage_daily WHERE user_id=$1 AND date>=date_trunc('month',current_date)::date`, userID).Scan(&monthlyTokens, &monthlyCost)
	var activeKeys, models int64
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE user_id=$1 AND status='ACTIVE' AND (expires_at IS NULL OR expires_at>now())`, userID).Scan(&activeKeys)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM model_routes WHERE enabled=true`).Scan(&models)
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	logs, err := s.ListRequestLogs(ctx, &userID, 5, 0)
	if err != nil {
		return nil, err
	}
	recent := make([]map[string]any, 0, len(logs))
	for _, l := range logs {
		recent = append(recent, map[string]any{"request_id": l.RequestID, "model": l.RequestedModel, "status_code": l.StatusCode, "total_tokens": l.TotalTokens, "latency_ms": l.LatencyMS, "created_at": l.CreatedAt})
	}
	trend, err := s.UsageSeries(ctx, &userID, 1)
	if err != nil {
		return nil, err
	}
	errorRate := 0.0
	if requests > 0 {
		errorRate = float64(errorsToday) * 100 / float64(requests)
	}
	return map[string]any{"requests_today": requests, "tokens_today": tokens, "estimated_cost_today": cost, "error_rate": errorRate, "monthly_tokens_used": monthlyTokens, "monthly_cost_used": monthlyCost, "monthly_token_limit": u.MonthlyTokenLimit, "monthly_cost_limit": u.MonthlyCostLimit, "active_api_keys": activeKeys, "available_models": models, "recent_requests": recent, "request_trend": trend}, nil
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

func (s *Store) Audit(ctx context.Context, actor, action, resourceType, resourceID, ip string, after any) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,action,resource_type,resource_id,after_state,ip) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::inet)`, id.UUID(), nullString(actor), action, resourceType, nullString(resourceID), jsonBytes(after), ip)
}

func (s *Store) ListAuditLogs(ctx context.Context, limit, offset int) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id,COALESCE(u.email,'system'),a.action,a.resource_type,COALESCE(a.resource_id,''),COALESCE(host(a.ip),''),a.after_state,a.created_at FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, actor, action, typ, resource, ip string
		var raw []byte
		var at time.Time
		if err := rows.Scan(&id, &actor, &action, &typ, &resource, &ip, &raw, &at); err != nil {
			return nil, err
		}
		var after any
		_ = json.Unmarshal(raw, &after)
		out = append(out, map[string]any{"id": id, "actor": actor, "action": action, "resource_type": typ, "resource_id": resource, "ip": ip, "after": after, "timestamp": at, "created_at": at, "result": "success"})
	}
	return out, rows.Err()
}

func (s *Store) ListAlerts(ctx context.Context, limit, offset int) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,kind,severity,message,COALESCE(resource_type,''),COALESCE(resource_id,''),acknowledged_at,created_at FROM alerts ORDER BY created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, kind, severity, message, typ, resource string
		var ack *time.Time
		var at time.Time
		if err := rows.Scan(&id, &kind, &severity, &message, &typ, &resource, &ack, &at); err != nil {
			return nil, err
		}
		status := "OPEN"
		if ack != nil {
			status = "ACKNOWLEDGED"
		}
		out = append(out, map[string]any{"id": id, "kind": kind, "title": strings.ReplaceAll(kind, "_", " "), "severity": severity, "message": message, "resource_type": typ, "resource_id": resource, "status": status, "acknowledged_at": ack, "created_at": at})
	}
	return out, rows.Err()
}

func (s *Store) GetSettings(ctx context.Context) (map[string]any, error) {
	defaults := map[string]any{"gateway_name": "RelayDock", "log_retention_days": 30, "audit_retention_days": 365, "log_prompt_content": false, "default_rate_limit_rpm": 60, "default_rate_limit_tpm": 100000, "credential_cooldown_seconds": 30, "max_scheduler_attempts": 2, "alert_high_error_rate": 5, "alert_high_429_rate": 3, "alert_pool_healthy_min": 1}
	rows, err := s.pool.Query(ctx, `SELECT key,value FROM system_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			defaults[key] = v
		}
	}
	return defaults, rows.Err()
}
func (s *Store) SetSettings(ctx context.Context, settings map[string]any) error {
	allowed := map[string]bool{"gateway_name": true, "log_retention_days": true, "audit_retention_days": true, "log_prompt_content": true, "default_rate_limit_rpm": true, "default_rate_limit_tpm": true, "credential_cooldown_seconds": true, "max_scheduler_attempts": true, "alert_high_error_rate": true, "alert_high_429_rate": true, "alert_pool_healthy_min": true}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for key, value := range settings {
		if !allowed[key] {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO system_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, key, jsonBytes(value)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return "99"
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullableTime(v time.Time) any {
	if v.IsZero() {
		return nil
	}
	return v
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
