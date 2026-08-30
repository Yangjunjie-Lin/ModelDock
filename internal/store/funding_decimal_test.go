package store

import "testing"

func TestFundingRatRejectsInvalidAndAbsentDatabaseAmounts(t *testing.T) {
	for _, value := range []string{"", "invalid", "1e2", "1.0000000000001", "1000000000000000000"} {
		if _, err := fundingRat(value); err == nil {
			t.Fatalf("fundingRat accepted %q", value)
		}
	}
}
