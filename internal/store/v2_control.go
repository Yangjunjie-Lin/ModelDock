package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

func (s *Store) ListProjectRequestLogs(ctx context.Context, projectID string, userID *string, limit, offset int) ([]domain.RequestLog, error) {
	query := `SELECT request_id,COALESCE(trace_id,''),COALESCE(user_id::text,''),COALESCE(api_key_id::text,''),organization_id,project_id,
		COALESCE(route_id::text,''),COALESCE(provider_id::text,''),COALESCE(credential_id::text,''),requested_model,
		resolved_model,endpoint,status_code,streaming,input_tokens,cached_input_tokens,output_tokens,total_tokens,
		estimated_cost::float8,latency_ms,ttft_ms,COALESCE(upstream_request_id,''),COALESCE(error_code,''),
		scheduler_reason,created_at FROM request_logs WHERE project_id=$1`
	args := []any{projectID}
	if userID != nil {
		args = append(args, *userID)
		query += ` AND user_id=$2`
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY created_at DESC,id DESC LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RequestLog, 0)
	for rows.Next() {
		var entry domain.RequestLog
		var schedulerReason []byte
		if err := rows.Scan(&entry.RequestID, &entry.TraceID, &entry.UserID, &entry.APIKeyID, &entry.OrganizationID, &entry.ProjectID,
			&entry.RouteID, &entry.ProviderID, &entry.CredentialID, &entry.RequestedModel, &entry.ResolvedModel,
			&entry.Endpoint, &entry.StatusCode, &entry.Streaming, &entry.InputTokens, &entry.CachedInputTokens,
			&entry.OutputTokens, &entry.TotalTokens, &entry.EstimatedCost, &entry.LatencyMS, &entry.TTFTMS,
			&entry.UpstreamRequestID, &entry.ErrorCode, &schedulerReason, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.SchedulerReason = map[string]any{}
		_ = json.Unmarshal(schedulerReason, &entry.SchedulerReason)
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *Store) ProjectUsageByModel(ctx context.Context, projectID string, from, to time.Time) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT model,sum(requests),sum(input_tokens),sum(cached_input_tokens),
		sum(output_tokens),sum(input_tokens+output_tokens),sum(cost)::float8,sum(errors)
		FROM usage_daily WHERE project_id=$1 AND date >= ($2::timestamptz AT TIME ZONE 'UTC')::date
		AND date < ($3::timestamptz AT TIME ZONE 'UTC')::date GROUP BY model ORDER BY sum(requests) DESC,model`,
		projectID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var model string
		var requests, input, cached, output, tokens, failures int64
		var cost float64
		if err := rows.Scan(&model, &requests, &input, &cached, &output, &tokens, &cost, &failures); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"model": model, "requests": requests, "input_tokens": input,
			"cached_input_tokens": cached, "output_tokens": output, "tokens": tokens, "cost": cost, "errors": failures})
	}
	return out, rows.Err()
}
