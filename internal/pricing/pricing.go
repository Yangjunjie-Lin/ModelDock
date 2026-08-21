// Package pricing contains currency-safe pricing arithmetic. Values cross the
// package boundary as decimal strings so JSON and PostgreSQL NUMERIC values do
// not pass through binary floating point.
package pricing

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var (
	ErrInvalidAmount  = errors.New("invalid decimal amount")
	ErrNegativeAmount = errors.New("amount must not be negative")
	ErrInvalidUnit    = errors.New("pricing unit must be positive")
)

var storedDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,12})?$`)

// ValidateStoredDecimal validates the exact representation accepted by the
// commercial NUMERIC(30,12) columns. Exponents, fractions, signs, NaN and
// binary floating point representations are deliberately rejected.
func ValidateStoredDecimal(value string) error {
	value = strings.TrimSpace(value)
	if !storedDecimalPattern.MatchString(value) {
		return ErrInvalidAmount
	}
	return nil
}

type Rate struct {
	Input    string
	Cached   string
	Output   string
	Fixed    string
	Unit     int64
	Currency string
}

type Tokens struct {
	Input  int64
	Cached int64
	Output int64
}

type Result struct {
	ProviderCost    string
	RetailAmount    string
	ExchangeRate    string
	GrossMargin     string
	PromotionAmount string
	PreTaxAmount    string
	TaxRate         string
	TaxAmount       string
	FinalAmount     string
}

func Calculate(cost, retail Rate, tokens Tokens, promotion, taxRate, exchangeRate string) (Result, error) {
	if tokens.Input < 0 || tokens.Cached < 0 || tokens.Output < 0 || tokens.Cached > tokens.Input {
		return Result{}, fmt.Errorf("invalid token counts")
	}
	if cost.Unit <= 0 || retail.Unit <= 0 {
		return Result{}, ErrInvalidUnit
	}
	if strings.TrimSpace(exchangeRate) == "" {
		exchangeRate = "1"
	}
	if strings.TrimSpace(promotion) == "" {
		promotion = "0"
	}
	if strings.TrimSpace(taxRate) == "" {
		taxRate = "0"
	}
	if err := validateRate(cost); err != nil {
		return Result{}, fmt.Errorf("cost rate: %w", err)
	}
	if err := validateRate(retail); err != nil {
		return Result{}, fmt.Errorf("retail rate: %w", err)
	}
	promo, err := nonNegative(promotion)
	if err != nil {
		return Result{}, fmt.Errorf("promotion: %w", err)
	}
	tax, err := nonNegative(taxRate)
	if err != nil {
		return Result{}, fmt.Errorf("tax rate: %w", err)
	}
	rate, err := nonNegative(exchangeRate)
	if err != nil {
		return Result{}, fmt.Errorf("exchange rate: %w", err)
	}
	if rate.Sign() == 0 {
		return Result{}, errors.New("exchange rate must be positive")
	}

	providerCost := calculateRate(cost, tokens)
	retailAmount := calculateRate(retail, tokens)
	convertedCost := new(big.Rat).Mul(providerCost, rate)
	margin := new(big.Rat).Sub(retailAmount, convertedCost)
	if promo.Cmp(retailAmount) > 0 {
		promo.Set(retailAmount)
	}
	preTax := new(big.Rat).Sub(retailAmount, promo)
	if preTax.Sign() < 0 {
		preTax.SetInt64(0)
	}
	taxAmount := new(big.Rat).Mul(preTax, tax)
	finalAmount := new(big.Rat).Add(preTax, taxAmount)
	return Result{
		ProviderCost: format(providerCost), RetailAmount: format(retailAmount),
		ExchangeRate: format(rate), GrossMargin: format(margin),
		PromotionAmount: format(promo), PreTaxAmount: format(preTax),
		TaxRate: format(tax), TaxAmount: format(taxAmount), FinalAmount: format(finalAmount),
	}, nil
}

// MeetsMinimumMargin applies both an absolute amount and a percentage (basis
// points) guard. The check is conservative: it validates a one-unit request
// plus the fixed fee, so every larger request has at least the same per-unit
// floor when rates are non-negative.
func MeetsMinimumMargin(cost, retail Rate, minimumAmount string, minimumBPS int64, exchangeRate string) (bool, string, error) {
	if strings.TrimSpace(exchangeRate) == "" {
		exchangeRate = "1"
	}
	if cost.Unit <= 0 || retail.Unit <= 0 {
		return false, "", ErrInvalidUnit
	}
	if err := validateRate(cost); err != nil {
		return false, "", err
	}
	if err := validateRate(retail); err != nil {
		return false, "", err
	}
	minAmount, err := nonNegative(minimumAmount)
	if err != nil {
		return false, "", err
	}
	if minimumBPS < 0 {
		return false, "", errors.New("minimum margin bps must not be negative")
	}
	exchange, err := nonNegative(exchangeRate)
	if err != nil {
		return false, "", err
	}
	if exchange.Sign() == 0 {
		return false, "", errors.New("exchange rate must be positive")
	}
	factor := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).SetFrac(big.NewInt(minimumBPS), big.NewInt(10000)))
	costValues := []*big.Rat{mustRat(cost.Input), mustRat(cost.Cached), mustRat(cost.Output), mustRat(cost.Fixed)}
	retailValues := []*big.Rat{mustRat(retail.Input), mustRat(retail.Cached), mustRat(retail.Output), mustRat(retail.Fixed)}
	lowest := new(big.Rat)
	initialized := false
	ok := true
	for index := range costValues {
		c := new(big.Rat).Set(costValues[index])
		r := new(big.Rat).Set(retailValues[index])
		if index < 3 {
			c.Quo(c, new(big.Rat).SetInt64(cost.Unit))
			r.Quo(r, new(big.Rat).SetInt64(retail.Unit))
		}
		required := new(big.Rat).Mul(c, exchange)
		required.Mul(required, factor)
		if index == 3 {
			required.Add(required, minAmount)
		}
		margin := new(big.Rat).Sub(r, required)
		if !initialized || margin.Cmp(lowest) < 0 {
			lowest.Set(margin)
			initialized = true
		}
		if margin.Sign() < 0 {
			ok = false
		}
	}
	return ok, format(lowest), nil
}

func validateRate(rate Rate) error {
	for _, value := range []string{rate.Input, rate.Cached, rate.Output, rate.Fixed} {
		if _, err := nonNegative(value); err != nil {
			return err
		}
	}
	return nil
}

func calculateRate(rate Rate, tokens Tokens) *big.Rat {
	input := mustRat(rate.Input)
	cached := mustRat(rate.Cached)
	output := mustRat(rate.Output)
	fixed := mustRat(rate.Fixed)
	nonCached := tokens.Input - tokens.Cached
	total := new(big.Rat).Set(fixed)
	tokensAmount := new(big.Rat).Mul(new(big.Rat).SetInt64(nonCached), input)
	tokensAmount.Add(tokensAmount, new(big.Rat).Mul(new(big.Rat).SetInt64(tokens.Cached), cached))
	tokensAmount.Add(tokensAmount, new(big.Rat).Mul(new(big.Rat).SetInt64(tokens.Output), output))
	total.Add(total, tokensAmount.Quo(tokensAmount, new(big.Rat).SetInt64(rate.Unit)))
	return total
}

func mustRat(value string) *big.Rat {
	r, _ := new(big.Rat).SetString(strings.TrimSpace(value))
	return r
}

func nonNegative(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "0"
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, ErrInvalidAmount
	}
	if r.Sign() < 0 {
		return nil, ErrNegativeAmount
	}
	return r, nil
}

func format(r *big.Rat) string {
	if r == nil {
		return "0"
	}
	value := r.FloatString(12)
	value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	if value == "-0" || value == "" {
		return "0"
	}
	return value
}
