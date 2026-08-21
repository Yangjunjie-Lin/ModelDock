package observability

import (
	"context"
	"testing"
)

func TestTraceContextContinuesValidParent(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	trace := NewTraceContext(parent)
	if trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || trace.SpanID == "00f067aa0ba902b7" || trace.Flags != "01" {
		t.Fatalf("trace=%+v", trace)
	}
	parsed, ok := ParseTraceparent(trace.Traceparent())
	if !ok || parsed != trace {
		t.Fatalf("round trip=%+v ok=%v", parsed, ok)
	}
	ctx := WithTrace(context.Background(), trace)
	if actual, ok := FromContext(ctx); !ok || actual != trace {
		t.Fatalf("context trace=%+v ok=%v", actual, ok)
	}
}

func TestTraceContextRejectsInvalidOrZeroIDs(t *testing.T) {
	invalid := []string{"", "00-xyz-00-01", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	for _, value := range invalid {
		if _, ok := ParseTraceparent(value); ok {
			t.Fatalf("accepted invalid traceparent %q", value)
		}
	}
	generated := NewTraceContext(invalid[0])
	if _, ok := ParseTraceparent(generated.Traceparent()); !ok {
		t.Fatalf("generated invalid trace=%+v", generated)
	}
}
