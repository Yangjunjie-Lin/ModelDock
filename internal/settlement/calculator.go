// Package settlement contains exact-decimal supplier settlement rules.
package settlement

import (
	"errors"
	"math/big"
	"strings"

	"github.com/relayedock/relayedock/internal/domain"
)

var ErrInvalidAmount = errors.New("invalid supplier settlement amount")

type SplitResult struct {
	Gross      domain.Decimal
	Commission domain.Decimal
	Reserve    domain.Decimal
	Payable    domain.Decimal
}

func Split(gross domain.Decimal, commissionBPS, reserveBPS int) (SplitResult, error) {
	amount, ok := new(big.Rat).SetString(strings.TrimSpace(gross.String()))
	if !ok || amount.Sign() < 0 || commissionBPS < 0 || commissionBPS > 10000 || reserveBPS < 0 || reserveBPS > 10000 {
		return SplitResult{}, ErrInvalidAmount
	}
	commission := new(big.Rat).Mul(amount, big.NewRat(int64(commissionBPS), 10000))
	commission, _ = new(big.Rat).SetString(commission.FloatString(12))
	afterCommission := new(big.Rat).Sub(amount, commission)
	reserve := new(big.Rat).Mul(afterCommission, big.NewRat(int64(reserveBPS), 10000))
	reserve, _ = new(big.Rat).SetString(reserve.FloatString(12))
	payable := new(big.Rat).Sub(afterCommission, reserve)
	return SplitResult{
		Gross: gross, Commission: domain.Decimal(commission.FloatString(12)),
		Reserve: domain.Decimal(reserve.FloatString(12)), Payable: domain.Decimal(payable.FloatString(12)),
	}, nil
}
