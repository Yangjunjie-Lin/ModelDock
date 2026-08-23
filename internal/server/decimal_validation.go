package server

import "github.com/relayedock/relayedock/internal/domain"

// HTTP validation treats an invalid Decimal as a 4xx validation failure.
// Stores and workers propagate Decimal errors instead of using these helpers.
func validPositiveDecimal(value domain.Decimal) bool {
	positive, err := value.IsPositive()
	return err == nil && positive
}

func invalidOrNegativeDecimal(value domain.Decimal) bool {
	negative, err := value.IsNegative()
	return err != nil || negative
}

func invalidOrZeroDecimal(value domain.Decimal) bool {
	zero, err := value.IsZero()
	return err != nil || zero
}

func decimalAtLeast(value, limit domain.Decimal) (bool, error) {
	comparison, err := value.Compare(limit)
	return comparison >= 0, err
}
