package store

import "testing"

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
