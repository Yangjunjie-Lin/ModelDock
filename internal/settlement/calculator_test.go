package settlement

import (
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestSplitUsesExactDecimalArithmetic(t *testing.T) {
	result, err := Split(domain.Decimal("123.456789012345"), 1250, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if result.Commission.String() != "15.432098626543" || result.Reserve.String() != "10.802469038580" || result.Payable.String() != "97.222221347222" {
		t.Fatalf("unexpected exact split: %+v", result)
	}
	if result.Gross.String() != result.Commission.Add(result.Reserve).Add(result.Payable).String() {
		t.Fatalf("split does not balance: %+v", result)
	}
}

func TestSplitRejectsInvalidBasisPoints(t *testing.T) {
	if _, err := Split(domain.Decimal("1"), 10001, 0); err == nil {
		t.Fatal("expected invalid basis points to fail")
	}
}
