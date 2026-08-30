package store

import (
	"fmt"

	"github.com/relayedock/relayedock/internal/domain"
)

// parseStoredDecimal is the only accepted boundary for database text entering
// a commercial Decimal. It returns a field-addressable error so the caller
// aborts the query/transaction instead of serializing or settling an invalid
// value as zero.
func parseStoredDecimal(value, field string) (domain.Decimal, error) {
	parsed, err := domain.ParseDecimal(value)
	if err != nil {
		return "", fmt.Errorf("invalid stored Decimal at %s: %w", field, err)
	}
	return parsed, nil
}
