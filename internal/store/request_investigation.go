package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// InvestigateRequest returns the operational and financial evidence for one
// request. Monetary NUMERIC values are always scanned as decimal strings.
func (s *Store) InvestigateRequest(ctx context.Context, requestID string) (map[string]any, error) {
	request := map[string]any{}
	var traceID, routeID, routeAlias, providerID, providerSlug, credentialID string
	var requestedModel, resolvedModel, endpoint, upstreamRequestID, errorCode string
	var statusCode int
	var streaming bool
	var inputTokens, cachedInputTokens, outputTokens, totalTokens, latencyMS int64
	var ttftMS *int64
	var estimatedCost, referenceCost, savingsAmount, fundingOperationID string
	var schedulerReason []byte
	var createdAt any
	err := s.pool.QueryRow(ctx, `SELECT r.request_id,COALESCE(r.trace_id,''),COALESCE(r.route_id::text,''),COALESCE(route.alias,''),
		COALESCE(r.provider_id::text,''),COALESCE(provider.slug,''),COALESCE(r.credential_id::text,''),
		COALESCE(r.requested_model,''),COALESCE(r.resolved_model,''),r.endpoint,r.status_code,r.streaming,
		r.input_tokens,r.cached_input_tokens,r.output_tokens,r.total_tokens,r.estimated_cost::text,r.reference_cost::text,
		r.savings_amount::text,r.latency_ms,r.ttft_ms,COALESCE(r.upstream_request_id,''),COALESCE(r.error_code,''),
		r.scheduler_reason,COALESCE(r.funding_operation_id::text,''),r.created_at
		FROM request_logs r LEFT JOIN model_routes route ON route.id=r.route_id
		LEFT JOIN providers provider ON provider.id=r.provider_id WHERE r.request_id=$1`, requestID).Scan(
		&requestID, &traceID, &routeID, &routeAlias, &providerID, &providerSlug, &credentialID,
		&requestedModel, &resolvedModel, &endpoint, &statusCode, &streaming, &inputTokens, &cachedInputTokens,
		&outputTokens, &totalTokens, &estimatedCost, &referenceCost, &savingsAmount, &latencyMS, &ttftMS,
		&upstreamRequestID, &errorCode, &schedulerReason, &fundingOperationID, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var reason map[string]any
	_ = unmarshalSupportJSON(schedulerReason, &reason)
	request = map[string]any{
		"request_id": requestID, "trace_id": traceID, "route_id": routeID, "route_alias": routeAlias,
		"provider_id": providerID, "provider": providerSlug, "credential_id": credentialID,
		"requested_model": requestedModel, "resolved_model": resolvedModel, "endpoint": endpoint,
		"status_code": statusCode, "streaming": streaming, "input_tokens": inputTokens,
		"cached_input_tokens": cachedInputTokens, "output_tokens": outputTokens, "total_tokens": totalTokens,
		"estimated_cost": estimatedCost, "reference_cost": referenceCost, "savings_amount": savingsAmount,
		"latency_ms": latencyMS, "ttft_ms": ttftMS, "upstream_request_id": upstreamRequestID,
		"error_code": errorCode, "scheduler_reason": reason, "funding_operation_id": fundingOperationID,
		"created_at": createdAt,
	}

	funding, err := s.requestFundingEvidence(ctx, requestID)
	if err != nil {
		return nil, err
	}
	usage, err := s.requestUsageEvidence(ctx, requestID)
	if err != nil {
		return nil, err
	}
	transactions, err := s.requestTransactionEvidence(ctx, requestID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"request": request, "funding": funding, "usage": usage, "wallet_transactions": transactions}, nil
}

func (s *Store) requestFundingEvidence(ctx context.Context, requestID string) (map[string]any, error) {
	var operationID, status, currency, maximum, settled, released, failureCode string
	var actualInput, actualCached, actualOutput *int64
	err := s.pool.QueryRow(ctx, `SELECT id::text,status,currency,maximum_amount::text,settled_amount::text,released_amount::text,
		actual_input_tokens,actual_cached_input_tokens,actual_output_tokens,COALESCE(failure_code,'')
		FROM funding_operation WHERE request_id=$1`, requestID).Scan(&operationID, &status, &currency, &maximum, &settled, &released, &actualInput, &actualCached, &actualOutput, &failureCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	attemptRows, err := s.pool.Query(ctx, `SELECT attempt.attempt_no,provider.slug,attempt.is_fallback,attempt.status,
		COALESCE(attempt.http_status,0),COALESCE(attempt.upstream_request_id,''),COALESCE(attempt.error_code,'')
		FROM funding_provider_attempt attempt JOIN providers provider ON provider.id=attempt.provider_id
		WHERE attempt.operation_id=$1 ORDER BY attempt.attempt_no`, operationID)
	if err != nil {
		return nil, err
	}
	defer attemptRows.Close()
	attempts := make([]map[string]any, 0)
	for attemptRows.Next() {
		var number, httpStatus int
		var provider, attemptStatus, upstreamID, errorCode string
		var fallback bool
		if err = attemptRows.Scan(&number, &provider, &fallback, &attemptStatus, &httpStatus, &upstreamID, &errorCode); err != nil {
			return nil, err
		}
		attempts = append(attempts, map[string]any{"attempt": number, "provider": provider, "fallback": fallback, "status": attemptStatus, "http_status": httpStatus, "upstream_request_id": upstreamID, "error_code": errorCode})
	}
	if err = attemptRows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"operation_id": operationID, "status": status, "currency": currency, "maximum_amount": maximum,
		"settled_amount": settled, "released_amount": released, "actual_input_tokens": actualInput,
		"actual_cached_input_tokens": actualCached, "actual_output_tokens": actualOutput, "failure_code": failureCode,
		"provider_attempts": attempts}, nil
}

func (s *Store) requestUsageEvidence(ctx context.Context, requestID string) (map[string]any, error) {
	var usageID, status, model, currency, amount, providerCost, customerSale, grossMargin, promotion, tax, finalAmount string
	err := s.pool.QueryRow(ctx, `SELECT usage.id::text,usage.status,usage.model,usage.currency,usage.amount::text,
		COALESCE(usage.provider_cost_amount,0)::text,COALESCE(usage.customer_sale_amount,usage.amount)::text,
		COALESCE(snapshot.platform_gross_margin,0)::text,usage.promotion_amount::text,usage.tax_amount::text,
		COALESCE(usage.final_user_amount,usage.amount)::text
		FROM billing_usage_records usage LEFT JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
		WHERE usage.request_id=$1`, requestID).Scan(&usageID, &status, &model, &currency, &amount, &providerCost, &customerSale, &grossMargin, &promotion, &tax, &finalAmount)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"usage_record_id": usageID, "status": status, "model": model, "currency": currency,
		"amount": amount, "provider_cost_amount": providerCost, "customer_sale_amount": customerSale,
		"gross_margin": grossMargin, "promotion_amount": promotion, "tax_amount": tax, "final_user_amount": finalAmount}, nil
}

func (s *Store) requestTransactionEvidence(ctx context.Context, requestID string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT transaction.id::text,transaction.transaction_type,transaction.amount::text,
		transaction.balance_after::text,COALESCE(transaction.journal_id::text,''),COALESCE(journal.journal_type,''),
		COALESCE(journal.status,''),transaction.created_at
		FROM wallet_transactions transaction
		LEFT JOIN billing_usage_records usage ON usage.id=transaction.usage_record_id
		LEFT JOIN funding_operation operation ON operation.id=transaction.funding_operation_id
		LEFT JOIN ledger_journal journal ON journal.id=transaction.journal_id
		WHERE usage.request_id=$1 OR operation.request_id=$1 ORDER BY transaction.created_at,transaction.id`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var transactionID, transactionType, amount, balanceAfter, journalID, journalType, journalStatus string
		var createdAt any
		if err = rows.Scan(&transactionID, &transactionType, &amount, &balanceAfter, &journalID, &journalType, &journalStatus, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"transaction_id": transactionID, "type": transactionType, "amount": amount,
			"balance_after": balanceAfter, "journal_id": journalID, "journal_type": journalType, "journal_status": journalStatus,
			"created_at": createdAt})
	}
	return out, rows.Err()
}
