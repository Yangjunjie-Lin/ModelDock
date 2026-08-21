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
	if string(raw) != `{"amount":0.100000000001}` {
		t.Fatalf("json=%s", raw)
	}
}
