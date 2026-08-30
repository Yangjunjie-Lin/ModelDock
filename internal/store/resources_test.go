package store

import (
	"testing"

	"github.com/relayedock/relayedock/internal/domain"
)

func TestNormalizeModelPriceDefaultsCachedInputToZero(t *testing.T) {
	price := normalizeModelPriceDefaults(domain.ModelPrice{
		InputPrice:  domain.MustDecimal("1"),
		OutputPrice: domain.MustDecimal("2"),
	})
	if price.CachedInputPrice.String() != "0" {
		t.Fatalf("expected omitted cached-input price to default to zero, got %q", price.CachedInputPrice)
	}
}

func TestNormalizeModelPriceDefaultsPreservesCachedInputPrice(t *testing.T) {
	price := normalizeModelPriceDefaults(domain.ModelPrice{
		InputPrice:       domain.MustDecimal("1"),
		CachedInputPrice: domain.MustDecimal("0.25"),
		OutputPrice:      domain.MustDecimal("2"),
	})
	if price.CachedInputPrice.String() != "0.25" {
		t.Fatalf("expected explicit cached-input price to be preserved, got %q", price.CachedInputPrice)
	}
}
