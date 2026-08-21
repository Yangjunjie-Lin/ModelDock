package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

type traceContextKey struct{}

type TraceContext struct {
	TraceID string
	SpanID  string
	Flags   string
}

func NewTraceContext(parent string) TraceContext {
	if parsed, ok := ParseTraceparent(parent); ok {
		return TraceContext{TraceID: parsed.TraceID, SpanID: randomHex(8), Flags: parsed.Flags}
	}
	return TraceContext{TraceID: randomHex(16), SpanID: randomHex(8), Flags: "01"}
}

func ParseTraceparent(value string) (TraceContext, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return TraceContext{}, false
	}
	if _, err := hex.DecodeString(parts[1] + parts[2] + parts[3]); err != nil || strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return TraceContext{}, false
	}
	return TraceContext{TraceID: parts[1], SpanID: parts[2], Flags: parts[3]}, true
}

func (t TraceContext) Traceparent() string {
	if t.TraceID == "" || t.SpanID == "" {
		return ""
	}
	flags := t.Flags
	if flags == "" {
		flags = "01"
	}
	return "00-" + t.TraceID + "-" + t.SpanID + "-" + flags
}

func WithTrace(ctx context.Context, value TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, value)
}

func FromContext(ctx context.Context) (TraceContext, bool) {
	value, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return value, ok && value.TraceID != "" && value.SpanID != ""
}

func randomHex(size int) string {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic("cryptographic random source unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
