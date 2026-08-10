package server

import (
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestConsoleUsageDateRangeIncludesCurrentUTCDay(t *testing.T) {
	// 04:47 in Asia/Shanghai is still the previous UTC calendar day. The
	// aggregate writer therefore stores these requests under 2026-08-09.
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, time.August, 10, 4, 47, 5, 0, shanghai)

	from, to := consoleUsageDateRange(now, 1)
	assertUsageRange(t, from, to, "2026-08-09T00:00:00Z", "2026-08-10T00:00:00Z")
}

func TestConsoleUsageDateRangeUsesInclusiveCalendarDayCounts(t *testing.T) {
	now := time.Date(2026, time.August, 9, 20, 47, 5, 0, time.UTC)

	from, to := consoleUsageDateRange(now, 7)
	assertUsageRange(t, from, to, "2026-08-03T00:00:00Z", "2026-08-10T00:00:00Z")

	from, to = consoleUsageDateRange(now, 30)
	assertUsageRange(t, from, to, "2026-07-11T00:00:00Z", "2026-08-10T00:00:00Z")
}

func TestConsoleUsageDateRangeDefaultsAndClamps(t *testing.T) {
	now := time.Date(2026, time.August, 9, 20, 47, 5, 0, time.UTC)

	from, to := consoleUsageDateRange(now, 0)
	assertUsageRange(t, from, to, "2026-07-11T00:00:00Z", "2026-08-10T00:00:00Z")

	from, to = consoleUsageDateRange(now, 1000)
	if got := int(to.Sub(from).Hours() / 24); got != 366 {
		t.Fatalf("clamped range contains %d days, want 366", got)
	}
}

func TestConsoleUsageHourlyRangeReturnsTwentyFourUTCBuckets(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, time.August, 10, 4, 47, 5, 0, shanghai)

	from, to := consoleUsageHourlyRange(now)
	assertUsageRange(t, from, to, "2026-08-08T21:00:00Z", "2026-08-09T21:00:00Z")
	if got := int(to.Sub(from).Hours()); got != 24 {
		t.Fatalf("hourly range contains %d hours, want 24", got)
	}
}

func TestSanitizeConsoleRequestLogsRemovesUpstreamIdentifiers(t *testing.T) {
	logs := []domain.RequestLog{{
		CredentialID:      "credential-internal",
		UpstreamRequestID: "provider-request-internal",
		SchedulerReason:   map[string]any{"selected": "credential-internal"},
		RequestID:         "rd_req_visible",
		RequestedModel:    "project-alias",
	}}
	sanitizeConsoleRequestLogs(logs)
	if logs[0].CredentialID != "" || logs[0].UpstreamRequestID != "" || logs[0].SchedulerReason != nil {
		t.Fatalf("console log still exposes upstream-only fields: %#v", logs[0])
	}
	if logs[0].RequestID != "rd_req_visible" || logs[0].RequestedModel != "project-alias" {
		t.Fatalf("console-visible fields changed: %#v", logs[0])
	}
}

func assertUsageRange(t *testing.T, from, to time.Time, wantFrom, wantTo string) {
	t.Helper()
	if got := from.Format(time.RFC3339); got != wantFrom {
		t.Fatalf("from = %s, want %s", got, wantFrom)
	}
	if got := to.Format(time.RFC3339); got != wantTo {
		t.Fatalf("to = %s, want %s", got, wantTo)
	}
}
