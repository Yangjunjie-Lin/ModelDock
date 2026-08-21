package server

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/domain"
)

func consoleProjectOverview(c *gin.Context, d Dependencies, project domain.Project, userID string) (map[string]any, error) {
	now := time.Now().UTC()
	todayFrom, todayTo := consoleUsageDateRange(now, 1)
	monthFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	today, err := d.Store.ProjectUsage(c.Request.Context(), project.ID, todayFrom, todayTo)
	if err != nil {
		return nil, err
	}
	monthly, err := d.Store.ProjectUsage(c.Request.Context(), project.ID, monthFrom, todayTo)
	if err != nil {
		return nil, err
	}
	trendFrom, trendTo := consoleUsageHourlyRange(now)
	trend, err := d.Store.ProjectUsageHourlySeries(c.Request.Context(), project.ID, trendFrom, trendTo)
	if err != nil {
		return nil, err
	}
	routes, err := d.Store.ListProjectRoutes(c.Request.Context(), project.ID)
	if err != nil {
		return nil, err
	}
	keys, err := d.Store.ListProjectAPIKeys(c.Request.Context(), project.ID, &userID, 1000, 0)
	if err != nil {
		return nil, err
	}
	logs, err := d.Store.ListProjectRequestLogs(c.Request.Context(), project.ID, &userID, 5, 0)
	if err != nil {
		return nil, err
	}
	sanitizeConsoleRequestLogs(logs)
	policies, err := d.Store.ListProjectBudgetPolicies(c.Request.Context(), project.ID)
	if err != nil {
		return nil, err
	}
	var tokenLimit int64
	costLimit := domain.Decimal("0")
	for _, policy := range policies {
		if policy.Status != "ACTIVE" || policy.Period != "MONTHLY" {
			continue
		}
		if policy.TokenLimit != nil && *policy.TokenLimit > tokenLimit {
			tokenLimit = *policy.TokenLimit
		}
		if policy.CostLimit != nil && policy.CostLimit.Compare(costLimit) > 0 {
			costLimit = *policy.CostLimit
		}
	}
	activeKeys := 0
	for _, key := range keys {
		if key.Status == "ACTIVE" {
			activeKeys++
		}
	}
	availableModels := 0
	defaultModel := ""
	for _, route := range routes {
		if route.Enabled {
			availableModels++
			if defaultModel == "" {
				defaultModel = route.Alias
			}
		}
	}
	errorRate := 0.0
	if today.Requests > 0 {
		errorRate = float64(today.Errors) / float64(today.Requests) * 100
	}
	return map[string]any{
		"project_id": project.ID, "total_requests": monthly.Requests, "requests_today": today.Requests,
		"tokens_today": today.TotalTokens, "estimated_cost_today": today.Cost, "error_rate": errorRate,
		"monthly_tokens_used": monthly.TotalTokens, "monthly_token_limit": tokenLimit,
		"monthly_cost_used": monthly.Cost, "monthly_cost_limit": costLimit,
		"active_api_keys": activeKeys, "available_models": availableModels, "default_model": defaultModel,
		"request_trend": trend, "recent_requests": logs,
	}, nil
}

func sanitizeConsoleRequestLogs(logs []domain.RequestLog) {
	for i := range logs {
		logs[i].CredentialID = ""
		logs[i].UpstreamRequestID = ""
		logs[i].SchedulerReason = nil
	}
}

// consoleUsageHourlyRange returns the current UTC hour plus the preceding 23
// complete hour buckets. The end is exclusive so the store always emits
// exactly 24 points, including zero-valued gaps.
func consoleUsageHourlyRange(now time.Time) (time.Time, time.Time) {
	to := now.UTC().Truncate(time.Hour).Add(time.Hour)
	return to.Add(-24 * time.Hour), to
}

func consoleProjectUsage(ctx context.Context, d Dependencies, projectID string, days int) (map[string]any, error) {
	from, to := consoleUsageDateRange(time.Now(), days)
	daily, err := d.Store.ProjectUsageSeries(ctx, projectID, from, to)
	if err != nil {
		return nil, err
	}
	byModel, err := d.Store.ProjectUsageByModel(ctx, projectID, from, to)
	if err != nil {
		return nil, err
	}
	return map[string]any{"daily": daily, "by_model": byModel}, nil
}

// consoleUsageDateRange returns complete UTC calendar days as a half-open
// interval. Usage aggregates are keyed by UTC date, so an upper bound within
// the current day would otherwise exclude that entire day after conversion to
// a date in the store query.
func consoleUsageDateRange(now time.Time, days int) (time.Time, time.Time) {
	if days <= 0 {
		days = 30
	}
	if days > 366 {
		days = 366
	}
	today := now.UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	to := today.AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -days)
	return from, to
}
