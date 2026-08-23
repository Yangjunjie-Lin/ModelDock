package domain

import (
	"encoding/json"
	"testing"
)

func TestDecimalJSONPreservesInputDigits(t *testing.T) {
	var value Decimal
	if err := json.Unmarshal([]byte(`"123456789012345678.123456789012"`), &value); err != nil {
		t.Fatal(err)
	}
	if value.String() != "123456789012345678.123456789012" {
		t.Fatalf("unexpected value %s", value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `"123456789012345678.123456789012"` {
		t.Fatalf("unexpected JSON %s", encoded)
	}
}

func TestDecimalArithmeticReturnsErrorsAndRoundsAtMoneyScale(t *testing.T) {
	got, err := MustDecimal("0.1").Add(MustDecimal("0.2"))
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := got.Compare(MustDecimal("0.3"))
	if err != nil || comparison != 0 {
		t.Fatalf("0.1+0.2=%s comparison=%d err=%v", got, comparison, err)
	}
	got, err = MustDecimal("999999999999999999.999999999999").Subtract(MustDecimal("0.000000000001"))
	if err != nil || got.String() != "999999999999999999.999999999998" {
		t.Fatalf("unexpected subtraction %s err=%v", got, err)
	}
	got, err = MustDecimal("12.345678901234").Multiply(MustDecimal("0.8"))
	if err != nil || got.String() != "9.876543120987" {
		t.Fatalf("unexpected product %s err=%v", got, err)
	}
	got, err = MustDecimal("0.000000000001").Multiply(MustDecimal("0.5"))
	if err != nil || got.String() != "0.000000000001" {
		t.Fatalf("rounding boundary=%s err=%v", got, err)
	}
}

func TestDecimalRejectsInvalidNullScaleAndRange(t *testing.T) {
	for _, input := range []string{`"not-a-number"`, `"1.0000000000001"`, `"1000000000000000000"`, `null`, `""`, `"1e2"`, `"1/2"`} {
		var value Decimal
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Fatalf("expected %s to fail", input)
		}
	}
	for _, value := range []Decimal{"invalid", "", "1000000000000000000"} {
		if _, err := value.Add(MustDecimal("1")); err == nil {
			t.Fatalf("Add accepted %q", value)
		}
		if _, err := value.Subtract(MustDecimal("1")); err == nil {
			t.Fatalf("Subtract accepted %q", value)
		}
		if _, err := value.Multiply(MustDecimal("1")); err == nil {
			t.Fatalf("Multiply accepted %q", value)
		}
		if _, err := value.Compare(MustDecimal("1")); err == nil {
			t.Fatalf("Compare accepted %q", value)
		}
	}
}

func TestMustDecimalPanicsOnlyForInvalidConstants(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected MustDecimal to panic")
		}
	}()
	_ = MustDecimal("invalid")
}
