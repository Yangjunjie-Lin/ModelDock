package payout

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

type SandboxConfig struct {
	Enabled        bool
	Secret         []byte
	AllowedRegions []string
}

type Sandbox struct{ config SandboxConfig }

func NewSandbox(config SandboxConfig) *Sandbox {
	regions := make([]string, 0, len(config.AllowedRegions))
	for _, region := range config.AllowedRegions {
		region = strings.ToUpper(strings.TrimSpace(region))
		if len(region) == 2 {
			regions = append(regions, region)
		}
	}
	config.AllowedRegions = regions
	return &Sandbox{config: config}
}

func (sandbox *Sandbox) Capabilities() Capabilities {
	return Capabilities{Name: "sandbox", Enabled: sandbox != nil && sandbox.config.Enabled,
		ContractStatus: "TEST_ONLY", AllowedRegions: append([]string(nil), sandbox.config.AllowedRegions...)}
}

func (sandbox *Sandbox) Send(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if sandbox == nil || !sandbox.config.Enabled || len(sandbox.config.Secret) < 32 {
		return Result{}, ErrAdapterDisabled
	}
	amountPositive, amountErr := request.Amount.IsPositive()
	if strings.TrimSpace(request.IdempotencyKey) == "" || amountErr != nil || !amountPositive || len(request.Currency) != 3 || strings.TrimSpace(request.Destination) == "" {
		return Result{}, errors.New("invalid sandbox payout request")
	}
	mac := hmac.New(sha256.New, sandbox.config.Secret)
	_, _ = mac.Write([]byte(request.IdempotencyKey))
	reference := "sbxp_" + hex.EncodeToString(mac.Sum(nil))[:32]
	return Result{ProviderReference: reference, Status: "SUCCEEDED", Metadata: map[string]any{"mode": "sandbox"}}, nil
}
