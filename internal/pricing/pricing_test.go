package pricing

import "testing"

func TestValidateStoredDecimalMatchesNumericScale(t *testing.T) {
	for _, valid := range []string{"0", "1", "999999999999999999.123456789012"} {
		if err := ValidateStoredDecimal(valid); err != nil {
			t.Fatalf("valid %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"-1", "+1", "1e-3", ".5", "01", "0.1234567890123", "1000000000000000000"} {
		if err := ValidateStoredDecimal(invalid); err == nil {
			t.Fatalf("invalid %q was accepted", invalid)
		}
	}
}

func TestCalculateUsesExactDecimalArithmetic(t *testing.T) {
	cost := Rate{Input: "0.1", Cached: "0.02", Output: "0.3", Fixed: "0.005", Unit: 1000, Currency: "USD"}
	retail := Rate{Input: "0.2", Cached: "0.05", Output: "0.6", Fixed: "0.01", Unit: 1000, Currency: "USD"}
	got, err := Calculate(cost, retail, Tokens{Input: 3, Cached: 1, Output: 2}, "0.001", "0.1", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"provider_cost": "0.00582", "retail": "0.01165", "margin": "0.00583", "pre_tax": "0.01065", "tax": "0.001065", "final": "0.011715"}
	actual := map[string]string{"provider_cost": got.ProviderCost, "retail": got.RetailAmount, "margin": got.GrossMargin, "pre_tax": got.PreTaxAmount, "tax": got.TaxAmount, "final": got.FinalAmount}
	for key, expected := range want {
		if actual[key] != expected {
			t.Fatalf("%s=%s want %s", key, actual[key], expected)
		}
	}
}

func TestCachedTokensAreNotChargedTwice(t *testing.T) {
	rate := Rate{Input: "10", Cached: "1", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
	got, err := Calculate(rate, rate, Tokens{Input: 4, Cached: 3}, "0", "0", "1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderCost != "13" {
		t.Fatalf("provider cost=%s want 13", got.ProviderCost)
	}
}

func TestCalculateUsesIndependentProviderAndRetailUnits(t *testing.T) {
	cost := Rate{Input: "0.001", Cached: "0", Output: "0", Fixed: "0", Unit: 1_000, Currency: "USD"}
	retail := Rate{Input: "2", Cached: "0", Output: "0", Fixed: "0", Unit: 1_000_000, Currency: "USD"}
	got, err := Calculate(cost, retail, Tokens{Input: 1_000_000}, "0", "0", "1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderCost != "1" || got.RetailAmount != "2" || got.GrossMargin != "1" {
		t.Fatalf("provider=%s retail=%s margin=%s", got.ProviderCost, got.RetailAmount, got.GrossMargin)
	}
}

func TestCalculateRequiresExplicitExchangeRateAcrossCurrencies(t *testing.T) {
	cost := Rate{Input: "1", Cached: "0", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
	retail := Rate{Input: "7", Cached: "0", Output: "0", Fixed: "0", Unit: 1, Currency: "CNY"}
	if _, err := Calculate(cost, retail, Tokens{Input: 1}, "0", "0", ""); err == nil {
		t.Fatal("different currencies were combined without an explicit exchange rate")
	}
	if _, err := Calculate(cost, retail, Tokens{Input: 1}, "0", "0", "7"); err != nil {
		t.Fatalf("explicit exchange rate rejected: %v", err)
	}
}

func TestCalculateRejectsInvalidProviderPromotionTaxAndRange(t *testing.T) {
	baseCost := Rate{Input: "1", Cached: "0", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
	baseRetail := Rate{Input: "2", Cached: "0", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
	for _, value := range []string{"", "NaN", "Infinity", "-1", "1e2", "1000000000000000000", "0.1234567890123"} {
		cost := baseCost
		cost.Input = value
		if _, err := Calculate(cost, baseRetail, Tokens{Input: 1}, "0", "0", "1"); err == nil {
			t.Fatalf("invalid Provider price %q was accepted", value)
		}
	}
	for _, promotion := range []string{"NaN", "Infinity", "-1", "1000000000000000000"} {
		if _, err := Calculate(baseCost, baseRetail, Tokens{Input: 1}, promotion, "0", "1"); err == nil {
			t.Fatalf("invalid Promotion %q was accepted", promotion)
		}
	}
	for _, tax := range []string{"NaN", "Infinity", "-1", "1000000000000000000"} {
		if _, err := Calculate(baseCost, baseRetail, Tokens{Input: 1}, "0", tax, "1"); err == nil {
			t.Fatalf("invalid Tax %q was accepted", tax)
		}
	}
}

func TestCalculateRejectsMissingOrMalformedCurrencies(t *testing.T) {
	for _, currency := range []string{"", "US", "usd1"} {
		cost := Rate{Input: "1", Cached: "0", Output: "0", Fixed: "0", Unit: 1, Currency: currency}
		retail := Rate{Input: "2", Cached: "0", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
		if _, err := Calculate(cost, retail, Tokens{Input: 1}, "0", "0", "1"); err == nil {
			t.Fatalf("invalid currency %q was accepted", currency)
		}
	}
}

func TestMinimumMarginRejectsNegativeMargin(t *testing.T) {
	cost := Rate{Input: "1", Cached: "1", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
	retail := Rate{Input: "0.5", Cached: "0.5", Output: "0", Fixed: "0", Unit: 1, Currency: "USD"}
	ok, margin, err := MeetsMinimumMargin(cost, retail, "0", 0, "1")
	if err != nil {
		t.Fatal(err)
	}
	if ok || margin != "-0.5" {
		t.Fatalf("ok=%v margin=%s", ok, margin)
	}
}
