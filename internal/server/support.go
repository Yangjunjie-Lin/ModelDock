package server

import (
	"regexp"
	"strings"

	"github.com/relayedock/relayedock/internal/observability"
)

var (
	supportAPIKeyPattern      = regexp.MustCompile(`(?i)rdk_(?:live|test)_[A-Za-z0-9_-]+`)
	supportProviderKeyPattern = regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{16,}\b`)
	supportBearerPattern      = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	supportSecretPattern      = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*[^\s,;]+`)
	supportEmailPattern       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
)

func redactSupportText(value string) string {
	value = supportAPIKeyPattern.ReplaceAllString(value, "[REDACTED_API_KEY]")
	value = supportProviderKeyPattern.ReplaceAllString(value, "[REDACTED_PROVIDER_KEY]")
	value = supportBearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = supportSecretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	return supportEmailPattern.ReplaceAllString(value, "[REDACTED_EMAIL]")
}

func redactSupportContext(context map[string]any) map[string]any {
	if len(context) == 0 {
		return map[string]any{}
	}
	allowed := map[string]bool{"request_id": true, "trace_id": true, "upstream_request_id": true, "provider": true, "model": true, "order_id": true, "ledger_journal_id": true, "endpoint": true}
	result := map[string]any{}
	for key, value := range context {
		key = strings.ToLower(strings.TrimSpace(key))
		if !allowed[key] {
			continue
		}
		if text, ok := value.(string); ok {
			result[key] = redactSupportText(text)
		} else {
			result[key] = value
		}
	}
	return result
}

func traceparent(c interface{ Get(string) (any, bool) }) string {
	value, ok := c.Get("trace_context")
	if !ok {
		return ""
	}
	trace, ok := value.(observability.TraceContext)
	if !ok {
		return ""
	}
	return trace.Traceparent()
}
