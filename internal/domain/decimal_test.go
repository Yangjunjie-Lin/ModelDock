package domain

import (
	"encoding/json"
	"testing"
)

func TestDecimalJSONPreservesInputDigits(t *testing.T) {
	var value struct {
		Amount Decimal `json:"amount"`
	}
	if err := json.Unmarshal([]byte(`{"amount":"0.100000000001"}`), &value); err != nil {
		t.Fatal(err)
	}
	if value.Amount.String() != "0.100000000001" {
		t.Fatalf("amount=%s", value.Amount)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"amount":"0.100000000001"}` {
		t.Fatalf("json=%s", raw)
	}
}

func TestDecimalArithmeticIsExactAtMoneyScale(t *testing.T) {
	if got := Decimal("0.1").Add(Decimal("0.2")); got.Compare(Decimal("0.3")) != 0 {
		t.Fatalf("0.1 + 0.2 = %s", got)
	}
	if got := Decimal("999999999999999999.999999999999").Subtract(Decimal("0.000000000001")); got.String() != "999999999999999999.999999999998" {
		t.Fatalf("large-scale subtraction = %s", got)
	}
	if got := Decimal("12.345678901234").Multiply(Decimal("0.8")); got.String() != "9.876543120987" {
		t.Fatalf("12-place multiplication = %s", got)
	}
}

func TestDecimalRejectsInvalidJSONAndPreservesZero(t *testing.T) {
	for _, raw := range []string{`{"amount":"NaN"}`, `{"amount":"1e309x"}`} {
		var value struct {
			Amount Decimal `json:"amount"`
		}
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("invalid decimal accepted: %s", raw)
		}
	}
	if raw, err := json.Marshal(struct {
		Amount Decimal `json:"amount"`
	}{Amount: Decimal("0")}); err != nil || string(raw) != `{"amount":"0"}` {
		t.Fatalf("zero JSON=%s err=%v", raw, err)
	}
	if got := Decimal("invalid").Add(Decimal("1")); got.Compare(Decimal("1")) != 0 {
		t.Fatalf("invalid in-process decimal did not fail closed to zero: %s", got)
	}
}
