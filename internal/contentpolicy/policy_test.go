package contentpolicy

import (
	"context"
	"testing"
)

func TestNoopProviderAllowsWithoutInspectingContent(t *testing.T) {
	decision, err := (NoopProvider{}).Evaluate(context.Background(), Request{Phase: PreRequest, Body: []byte("sensitive prompt")})
	if err != nil || !decision.Allowed || decision.Action != "ALLOW" || decision.FailureMode != "FAIL_OPEN" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
