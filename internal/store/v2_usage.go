package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

// InsertScopedRequestLog commits the request ledger, daily/hourly aggregates,
// budget event, and webhook outbox rows atomically.  V1 InsertRequestLog is
// retained for compatibility; all V2 data-plane writes should use this method.
func (s *Store) InsertScopedRequestLog(ctx context.Context, logEntry domain.RequestLog) error {
	createdAt := logEntry.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var organizationID, projectID string
	var routeID *string
	err = tx.QueryRow(ctx, `INSERT INTO request_logs(id,request_id,trace_id,user_id,api_key_id,organization_id,project_id,route_id,
		provider_id,credential_id,requested_model,resolved_model,endpoint,status_code,streaming,input_tokens,
		cached_input_tokens,output_tokens,total_tokens,estimated_cost,reference_cost,savings_amount,estimated_cost_exact,reference_cost_exact,savings_amount_exact,latency_ms,ttft_ms,upstream_request_id,error_code,
		scheduler_reason,created_at,funding_operation_id,usage_source)
		VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,round($20::numeric,8),round($21::numeric,8),round($22::numeric,8),$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
		RETURNING organization_id,project_id,route_id`, id.UUID(), logEntry.RequestID, logEntry.TraceID,
		nullString(logEntry.UserID), nullString(logEntry.APIKeyID), nullString(logEntry.OrganizationID), nullString(logEntry.ProjectID), nullString(logEntry.RouteID),
		nullString(logEntry.ProviderID), nullString(logEntry.CredentialID), logEntry.RequestedModel, logEntry.ResolvedModel,
		logEntry.Endpoint, logEntry.StatusCode, logEntry.Streaming, logEntry.InputTokens, logEntry.CachedInputTokens,
		logEntry.OutputTokens, logEntry.TotalTokens, logEntry.EstimatedCost.String(), logEntry.ReferenceCost.String(), logEntry.SavingsAmount.String(),
		logEntry.LatencyMS, logEntry.TTFTMS, nullString(logEntry.UpstreamRequestID), nullString(logEntry.ErrorCode),
		jsonBytes(logEntry.SchedulerReason), createdAt, nullString(logEntry.FundingOperationID), nullString(logEntry.UsageSource)).
		Scan(&organizationID, &projectID, &routeID)
	if err != nil {
		return err
	}

	errorCount := boolInt(logEntry.StatusCode >= 400)
	_, err = tx.Exec(ctx, `INSERT INTO usage_daily(organization_id,project_id,route_id,date,user_id,api_key_id,model,
		requests,input_tokens,cached_input_tokens,output_tokens,cost,cost_exact,errors)
		VALUES($1,$2,$3,($4::timestamptz AT TIME ZONE 'UTC')::date,$5,$6,$7,1,$8,$9,$10,round($11::numeric,8),$11,$12)
		ON CONFLICT ON CONSTRAINT usage_daily_scope_pkey DO UPDATE SET
		requests=usage_daily.requests+1,input_tokens=usage_daily.input_tokens+EXCLUDED.input_tokens,
		cached_input_tokens=usage_daily.cached_input_tokens+EXCLUDED.cached_input_tokens,
		output_tokens=usage_daily.output_tokens+EXCLUDED.output_tokens,cost=round(usage_daily.cost+EXCLUDED.cost,8),
		cost_exact=usage_daily.cost_exact+EXCLUDED.cost_exact,
		errors=usage_daily.errors+EXCLUDED.errors`, organizationID, projectID, routeID, createdAt, logEntry.UserID,
		logEntry.APIKeyID, logEntry.ResolvedModel, logEntry.InputTokens, logEntry.CachedInputTokens,
		logEntry.OutputTokens, logEntry.EstimatedCost.String(), errorCount)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO usage_hourly(organization_id,project_id,route_id,hour,user_id,api_key_id,model,
		requests,input_tokens,cached_input_tokens,output_tokens,cost,cost_exact,errors)
		VALUES($1,$2,$3,date_trunc('hour',$4::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',$5,$6,$7,1,$8,$9,$10,round($11::numeric,8),$11,$12)
		ON CONFLICT ON CONSTRAINT usage_hourly_scope_pkey DO UPDATE SET
		requests=usage_hourly.requests+1,input_tokens=usage_hourly.input_tokens+EXCLUDED.input_tokens,
		cached_input_tokens=usage_hourly.cached_input_tokens+EXCLUDED.cached_input_tokens,
		output_tokens=usage_hourly.output_tokens+EXCLUDED.output_tokens,cost=round(usage_hourly.cost+EXCLUDED.cost,8),
		cost_exact=usage_hourly.cost_exact+EXCLUDED.cost_exact,
		errors=usage_hourly.errors+EXCLUDED.errors`, organizationID, projectID, routeID, createdAt, logEntry.UserID,
		logEntry.APIKeyID, logEntry.ResolvedModel, logEntry.InputTokens, logEntry.CachedInputTokens,
		logEntry.OutputTokens, logEntry.EstimatedCost.String(), errorCount)
	if err != nil {
		return err
	}

	eventType := "request.completed"
	if logEntry.StatusCode >= 400 {
		eventType = "request.failed"
	}
	budgetMetadata := map[string]any{"status_code": logEntry.StatusCode, "model": logEntry.RequestedModel}
	_, err = tx.Exec(ctx, `INSERT INTO budget_events(id,organization_id,project_id,user_id,api_key_id,request_id,event_type,tokens,cost,cost_exact,idempotency_key,metadata)
		VALUES($1,$2,$3,$4,$5,$6,'COMMIT',$7,round($8::numeric,8),$8,$6,$9) ON CONFLICT DO NOTHING`, id.UUID(), organizationID,
		projectID, logEntry.UserID, logEntry.APIKeyID, logEntry.RequestID, logEntry.InputTokens+logEntry.OutputTokens,
		logEntry.EstimatedCost.String(), jsonBytes(budgetMetadata))
	if err != nil {
		return err
	}
	if err = settleBillingUsage(ctx, tx, logEntry, organizationID, projectID); err != nil {
		return err
	}
	webhookPayload := map[string]any{
		"id": logEntry.RequestID, "type": eventType, "created_at": createdAt, "project_id": projectID,
		"data": map[string]any{"model": logEntry.RequestedModel, "status_code": logEntry.StatusCode,
			"input_tokens": logEntry.InputTokens, "cached_input_tokens": logEntry.CachedInputTokens,
			"output_tokens": logEntry.OutputTokens, "total_tokens": logEntry.TotalTokens,
			"estimated_cost": logEntry.EstimatedCost, "latency_ms": logEntry.LatencyMS},
	}
	_, err = tx.Exec(ctx, `INSERT INTO webhook_outbox(id,endpoint_id,organization_id,project_id,event_id,event_type,payload,max_attempts)
		SELECT gen_random_uuid(),e.id,e.organization_id,e.project_id,$2,$3,$4,6 FROM webhook_endpoints e
		WHERE e.project_id=$1 AND e.enabled AND (e.event_types='[]'::jsonb OR e.event_types ? $3)
		ON CONFLICT(endpoint_id,event_id) DO NOTHING`, projectID, logEntry.RequestID, eventType, jsonBytes(webhookPayload))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ExportUsageRows returns stable, server-filtered source rows for CSV or JSON
// serialization.  The caller owns CSV formula neutralization because that is
// an output-encoding concern rather than a database concern.
func (s *Store) ExportUsageRows(ctx context.Context, filter domain.UsageExportFilter) ([]domain.UsageExportRow, error) {
	query := `SELECT request_id,organization_id,project_id,COALESCE(user_id::text,''),COALESCE(api_key_id::text,''),
		COALESCE(route_id::text,''),COALESCE(requested_model,''),endpoint,status_code,input_tokens,cached_input_tokens,
		output_tokens,total_tokens,estimated_cost_exact::text,latency_ms,created_at FROM request_logs WHERE true`
	args := make([]any, 0, 10)
	add := func(clause string, value any) {
		args = append(args, value)
		query += " AND " + clause + "$" + strconv.Itoa(len(args))
	}
	if filter.OrganizationID != "" {
		add("organization_id=", filter.OrganizationID)
	}
	if filter.ProjectID != "" {
		add("project_id=", filter.ProjectID)
	}
	if filter.UserID != "" {
		add("user_id=", filter.UserID)
	}
	if filter.APIKeyID != "" {
		add("api_key_id=", filter.APIKeyID)
	}
	if filter.RouteID != "" {
		add("route_id=", filter.RouteID)
	}
	if filter.Model != "" {
		args = append(args, filter.Model)
		query += ` AND (requested_model=$` + strconv.Itoa(len(args)) + ` OR resolved_model=$` + strconv.Itoa(len(args)) + `)`
	}
	if filter.StatusCode != nil {
		add("status_code=", *filter.StatusCode)
	}
	if !filter.From.IsZero() {
		add("created_at>=", filter.From.UTC())
	}
	if !filter.To.IsZero() {
		add("created_at<", filter.To.UTC())
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 10_000
	}
	if limit > 100_000 {
		limit = 100_000
	}
	args = append(args, limit)
	query += ` ORDER BY created_at,request_id LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.UsageExportRow, 0)
	for rows.Next() {
		var row domain.UsageExportRow
		var estimatedCost string
		if err := rows.Scan(&row.RequestID, &row.OrganizationID, &row.ProjectID, &row.UserID, &row.APIKeyID,
			&row.RouteID, &row.Model, &row.Endpoint, &row.StatusCode, &row.InputTokens, &row.CachedTokens,
			&row.OutputTokens, &row.TotalTokens, &estimatedCost, &row.LatencyMS, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.EstimatedCost = domain.Decimal(estimatedCost)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) UsageExportRows(ctx context.Context, filter domain.UsageExportFilter) ([]domain.UsageExportRow, error) {
	return s.ExportUsageRows(ctx, filter)
}

func (s *Store) ProjectUsageSeries(ctx context.Context, projectID string, from, to time.Time) ([]map[string]any, error) {
	if !to.After(from) {
		return nil, errors.New("usage range end must be after start")
	}
	rows, err := s.pool.Query(ctx, `SELECT date::text,sum(requests),sum(input_tokens),sum(cached_input_tokens),
		sum(output_tokens),sum(cost_exact)::text,sum(errors) FROM usage_daily
		WHERE project_id=$1 AND date >= ($2::timestamptz AT TIME ZONE 'UTC')::date
		AND date < ($3::timestamptz AT TIME ZONE 'UTC')::date GROUP BY date ORDER BY date`, projectID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var date string
		var requests, input, cached, output, failures int64
		var cost string
		if err := rows.Scan(&date, &requests, &input, &cached, &output, &cost, &failures); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"date": date, "requests": requests, "input_tokens": input,
			"cached_input_tokens": cached, "output_tokens": output, "cost": domain.Decimal(cost), "errors": failures})
	}
	return out, rows.Err()
}

// ProjectUsageHourlySeries returns a gap-free sequence of UTC hour buckets.
// Callers provide aligned half-open bounds; generate_series supplies zero rows
// so charts do not collapse quiet hours or misrepresent their spacing.
func (s *Store) ProjectUsageHourlySeries(ctx context.Context, projectID string, from, to time.Time) ([]map[string]any, error) {
	if !to.After(from) {
		return nil, errors.New("usage range end must be after start")
	}
	rows, err := s.pool.Query(ctx, `WITH buckets AS (
		SELECT generate_series($2::timestamptz,$3::timestamptz - interval '1 hour',interval '1 hour') AS hour
	), aggregate AS (
		SELECT date_trunc('hour',hour) AS hour,sum(requests) AS requests,
			sum(input_tokens) AS input_tokens,sum(cached_input_tokens) AS cached_input_tokens,
			sum(output_tokens) AS output_tokens,sum(cost_exact)::text AS cost,sum(errors) AS errors
		FROM usage_hourly WHERE project_id=$1 AND hour >= $2 AND hour < $3 GROUP BY date_trunc('hour',hour)
	)
	SELECT buckets.hour,COALESCE(aggregate.requests,0),COALESCE(aggregate.input_tokens,0),
		COALESCE(aggregate.cached_input_tokens,0),COALESCE(aggregate.output_tokens,0),
		COALESCE(aggregate.cost,'0'),COALESCE(aggregate.errors,0)
	FROM buckets LEFT JOIN aggregate USING(hour) ORDER BY buckets.hour`, projectID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0, 24)
	for rows.Next() {
		var hour time.Time
		var requests, input, cached, output, failures int64
		var cost string
		if err := rows.Scan(&hour, &requests, &input, &cached, &output, &cost, &failures); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"time": hour.UTC().Format(time.RFC3339), "requests": requests,
			"input_tokens": input, "cached_input_tokens": cached, "output_tokens": output,
			"cost": domain.Decimal(cost), "errors": failures})
	}
	return out, rows.Err()
}

// CSVSafeCell neutralizes spreadsheet formulas while preserving the original
// visible value.  Export handlers should apply it to every untrusted text cell.
func CSVSafeCell(value string) string {
	trimmed := strings.TrimLeft(value, "\t\r\n ")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}
