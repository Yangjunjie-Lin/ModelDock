package server

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/scheduler"
)

// mergeCredentialConstraints returns a stable union. Keeping a tag in both
// lists is intentional: contradictory route configuration must select no
// credential instead of silently weakening either constraint.
func mergeCredentialConstraints(values ...scheduler.CredentialConstraints) scheduler.CredentialConstraints {
	required := make(map[string]struct{})
	excluded := make(map[string]struct{})
	add := func(target map[string]struct{}, tags []string) {
		for _, tag := range tags {
			tag = strings.ToLower(strings.TrimSpace(tag))
			if tag != "" {
				target[tag] = struct{}{}
			}
		}
	}
	for _, value := range values {
		add(required, value.RequiredTags)
		add(excluded, value.ExcludedTags)
	}
	out := scheduler.CredentialConstraints{
		RequiredTags: make([]string, 0, len(required)),
		ExcludedTags: make([]string, 0, len(excluded)),
	}
	for tag := range required {
		out.RequiredTags = append(out.RequiredTags, tag)
	}
	for tag := range excluded {
		out.ExcludedTags = append(out.ExcludedTags, tag)
	}
	sort.Strings(out.RequiredTags)
	sort.Strings(out.ExcludedTags)
	return out
}

// admitProjectBudgets performs a conservative pre-flight check. Token limits
// include a request-size estimate; cost limits use committed spend because the
// provider price is only known after the route and response usage are known.
func admitProjectBudgets(ctx context.Context, d Dependencies, key domain.APIKey, requestID string, estimatedTokens int64) (bool, error) {
	policies, err := d.Store.ListProjectBudgetPolicies(ctx, key.ProjectID)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	for _, policy := range policies {
		if policy.Status != "ACTIVE" || !policy.EnforceHardLimit {
			continue
		}
		usage, err := d.Store.ProjectBudgetUsage(ctx, key.ProjectID, policy.Period, now)
		if err != nil {
			return false, err
		}
		tokenBlocked := policy.TokenLimit != nil && usage.TotalTokens+estimatedTokens > *policy.TokenLimit
		costBlocked := false
		if policy.CostLimit != nil {
			comparison, compareErr := usage.Cost.Compare(*policy.CostLimit)
			if compareErr != nil {
				return false, compareErr
			}
			costBlocked = comparison >= 0
		}
		if !tokenBlocked && !costBlocked {
			continue
		}
		metadata := budgetMetadata(policy, usage)
		metadata["level"] = "exceeded"
		metadata["estimated_tokens"] = estimatedTokens
		if _, err := emitBudgetEvent(ctx, d, key, policy, usage, requestID, "REJECT", "budget.exceeded", metadata); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// commitProjectBudget is called after the scoped request ledger and usage
// aggregates commit. It emits one warning/exceeded event per policy period.
func commitProjectBudget(ctx context.Context, d Dependencies, key domain.APIKey, logEntry domain.RequestLog) error {
	policies, err := d.Store.ListProjectBudgetPolicies(ctx, key.ProjectID)
	if err != nil {
		return err
	}
	at := logEntry.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	for _, policy := range policies {
		if policy.Status != "ACTIVE" {
			continue
		}
		usage, err := d.Store.ProjectBudgetUsage(ctx, key.ProjectID, policy.Period, at)
		if err != nil {
			return err
		}
		level := ""
		webhookType := ""
		limitReached, err := budgetLimitReached(policy, usage)
		if err != nil {
			return err
		}
		thresholdReached, err := budgetThresholdReached(policy, usage)
		if err != nil {
			return err
		}
		if policy.EnforceHardLimit && limitReached {
			level = "exceeded"
			webhookType = "budget.exceeded"
		} else if thresholdReached {
			level = "warning"
			webhookType = "budget.warning"
		}
		if level == "" {
			continue
		}
		metadata := budgetMetadata(policy, usage)
		metadata["level"] = level
		if _, err := emitBudgetEvent(ctx, d, key, policy, usage, logEntry.RequestID, "THRESHOLD", webhookType, metadata); err != nil {
			return err
		}
	}
	return nil
}

func budgetLimitReached(policy domain.ProjectBudgetPolicy, usage domain.ProjectBudgetUsage) (bool, error) {
	if policy.TokenLimit != nil && usage.TotalTokens >= *policy.TokenLimit {
		return true, nil
	}
	if policy.CostLimit == nil {
		return false, nil
	}
	comparison, err := usage.Cost.Compare(*policy.CostLimit)
	return comparison >= 0, err
}

func budgetThresholdReached(policy domain.ProjectBudgetPolicy, usage domain.ProjectBudgetUsage) (bool, error) {
	threshold := policy.AlertThreshold
	negative, err := threshold.IsNegative()
	if err != nil {
		return false, err
	}
	if negative {
		threshold = domain.MustDecimal("0")
	}
	comparison, err := threshold.Compare(domain.MustDecimal("1"))
	if err != nil {
		return false, err
	}
	if comparison > 0 {
		threshold = domain.MustDecimal("1")
	}
	if policy.TokenLimit != nil {
		tokenLimit, parseErr := domain.ParseDecimal(strconv.FormatInt(*policy.TokenLimit, 10))
		if parseErr != nil {
			return false, parseErr
		}
		limit, multiplyErr := tokenLimit.Multiply(threshold)
		if multiplyErr != nil {
			return false, multiplyErr
		}
		totalTokens, parseErr := domain.ParseDecimal(strconv.FormatInt(usage.TotalTokens, 10))
		if parseErr != nil {
			return false, parseErr
		}
		tokenComparison, compareErr := totalTokens.Compare(limit)
		if compareErr != nil {
			return false, compareErr
		}
		if tokenComparison >= 0 {
			return true, nil
		}
	}
	if policy.CostLimit != nil {
		limit, multiplyErr := policy.CostLimit.Multiply(threshold)
		if multiplyErr != nil {
			return false, multiplyErr
		}
		costComparison, compareErr := usage.Cost.Compare(limit)
		if compareErr != nil {
			return false, compareErr
		}
		if costComparison >= 0 {
			return true, nil
		}
	}
	return false, nil
}

func budgetMetadata(policy domain.ProjectBudgetPolicy, usage domain.ProjectBudgetUsage) map[string]any {
	return map[string]any{
		"policy_id":       policy.ID,
		"policy_name":     policy.Name,
		"period":          policy.Period,
		"period_from":     usage.From,
		"period_to":       usage.To,
		"token_limit":     policy.TokenLimit,
		"cost_limit":      policy.CostLimit,
		"alert_threshold": policy.AlertThreshold,
		"total_tokens":    usage.TotalTokens,
		"cost":            usage.Cost,
	}
}

func emitBudgetEvent(
	ctx context.Context,
	d Dependencies,
	key domain.APIKey,
	policy domain.ProjectBudgetPolicy,
	usage domain.ProjectBudgetUsage,
	requestID string,
	eventType string,
	webhookType string,
	metadata map[string]any,
) (domain.BudgetEvent, error) {
	level, _ := metadata["level"].(string)
	idempotencyKey := fmt.Sprintf("budget:%s:%s:%s:%s", policy.ID, strings.ToLower(eventType), level, usage.From.UTC().Format("20060102"))
	policyID := policy.ID
	userID := key.UserID
	apiKeyID := key.ID
	event, err := d.Store.CreateBudgetEvent(ctx, domain.BudgetEvent{
		OrganizationID: key.OrganizationID,
		ProjectID:      key.ProjectID,
		PolicyID:       &policyID,
		UserID:         &userID,
		APIKeyID:       &apiKeyID,
		RequestID:      requestID,
		EventType:      eventType,
		Tokens:         usage.TotalTokens,
		Cost:           usage.Cost,
		IdempotencyKey: idempotencyKey,
		Metadata:       metadata,
	})
	if err != nil {
		return domain.BudgetEvent{}, err
	}
	payload := map[string]any{
		"id":         event.ID,
		"type":       webhookType,
		"created_at": event.CreatedAt,
		"project_id": event.ProjectID,
		"data": map[string]any{
			"policy_id":  policy.ID,
			"request_id": requestID,
			"usage":      usage,
			"metadata":   metadata,
		},
	}
	_, err = d.Store.EnqueueWebhookEvent(ctx, key.ProjectID, event.ID, webhookType, payload, d.Config.WebhookMaxAttempts)
	return event, err
}
