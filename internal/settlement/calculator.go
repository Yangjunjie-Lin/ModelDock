// Package settlement contains exact-decimal supplier settlement rules.
package settlement

import (
	"errors"
	"math/big"

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
	validated, err := domain.ParseDecimal(gross.String())
	if err != nil {
		return SplitResult{}, ErrInvalidAmount
	}
	amount, ok := new(big.Rat).SetString(validated.String())
	if !ok || amount.Sign() < 0 || commissionBPS < 0 || commissionBPS > 10000 || reserveBPS < 0 || reserveBPS > 10000 {
		return SplitResult{}, ErrInvalidAmount
	}
	commission := new(big.Rat).Mul(amount, big.NewRat(int64(commissionBPS), 10000))
	commission, _ = new(big.Rat).SetString(commission.FloatString(12))
	afterCommission := new(big.Rat).Sub(amount, commission)
	reserve := new(big.Rat).Mul(afterCommission, big.NewRat(int64(reserveBPS), 10000))
	reserve, _ = new(big.Rat).SetString(reserve.FloatString(12))
	payable := new(big.Rat).Sub(afterCommission, reserve)
	commissionDecimal, err := domain.ParseDecimal(commission.FloatString(12))
	if err != nil {
		return SplitResult{}, err
	}
	reserveDecimal, err := domain.ParseDecimal(reserve.FloatString(12))
	if err != nil {
		return SplitResult{}, err
	}
	payableDecimal, err := domain.ParseDecimal(payable.FloatString(12))
	if err != nil {
		return SplitResult{}, err
	}
	return SplitResult{
		Gross: validated, Commission: commissionDecimal, Reserve: reserveDecimal, Payable: payableDecimal,
	}, nil
}
