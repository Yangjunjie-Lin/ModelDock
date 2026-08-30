package server

import (
	"log/slog"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/cockpit"
	"github.com/relayedock/relayedock/internal/config"
	"github.com/relayedock/relayedock/internal/contentpolicy"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/observability"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/payout"
	"github.com/relayedock/relayedock/internal/providers"
	"github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/provisioning"
	"github.com/relayedock/relayedock/internal/ratelimit"
	"github.com/relayedock/relayedock/internal/scheduler"
	"github.com/relayedock/relayedock/internal/secrets"
	"github.com/relayedock/relayedock/internal/store"
	"github.com/relayedock/relayedock/internal/webhook"
)

type Dependencies struct {
	Config          config.Config
	Store           *store.Store
	Redis           *redis.Client
	Vault           *secretcrypto.Vault
	APIKeys         *apikey.Manager
	Auth            *auth.Manager
	OpenAI          *openai.Adapter
	Providers       *providers.Registry
	Provisioners    *provisioning.Registry
	Scheduler       *scheduler.Scheduler
	Limiter         *ratelimit.Limiter
	Metrics         *observability.Metrics
	Logger          *slog.Logger
	Webhooks        *webhook.Dispatcher
	Cockpit         *cockpit.Client
	Payments        *payment.Registry
	Payouts         *payout.Registry
	ContentPolicy   contentpolicy.Provider
	StartupComplete *atomic.Bool
	Draining        *atomic.Bool
	SecretManager   secrets.Manager
}

func providerAdapter(d Dependencies, providerType string) (providers.Provider, error) {
	if d.Providers != nil {
		return d.Providers.Resolve(providerType)
	}
	if d.OpenAI != nil {
		return d.OpenAI, nil
	}
	return nil, providers.ErrProviderNotRegistered
}
