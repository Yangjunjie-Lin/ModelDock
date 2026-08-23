package store

import (
	"context"
	"strings"
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestCSVSafeCellNeutralizesSpreadsheetFormulas(t *testing.T) {
	for _, value := range []string{"=HYPERLINK(\"https://example.invalid\")", "+cmd", "-1+2", "@SUM(A1:A2)", "  =1+1"} {
		if got := CSVSafeCell(value); len(got) == 0 || got[0] != '\'' {
			t.Fatalf("CSVSafeCell(%q) = %q", value, got)
		}
	}
	if got := CSVSafeCell("gpt-default"); got != "gpt-default" {
		t.Fatalf("ordinary value changed: %q", got)
	}
}

func TestInsertScopedRequestLogRejectsInvalidCommercialDecimalsBeforeDatabaseWrite(t *testing.T) {
	tests := []struct {
		name  string
		field string
		entry domain.RequestLog
	}{
		{name: "estimated cost", field: "estimated_cost", entry: domain.RequestLog{EstimatedCost: "invalid", ReferenceCost: "0", SavingsAmount: "0"}},
		{name: "reference cost", field: "reference_cost", entry: domain.RequestLog{EstimatedCost: "0", ReferenceCost: "", SavingsAmount: "0"}},
		{name: "savings amount", field: "savings_amount", entry: domain.RequestLog{EstimatedCost: "0", ReferenceCost: "0", SavingsAmount: "1000000000000000000"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&Store{}).InsertScopedRequestLog(context.Background(), test.entry)
			if err == nil || !strings.Contains(err.Error(), "invalid request log "+test.field) {
				t.Fatalf("error = %v, want explicit %s validation error", err, test.field)
			}
		})
	}
}
