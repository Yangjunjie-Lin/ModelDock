package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/cockpit"
	"github.com/relayedock/relayedock/internal/config"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/email"
	"github.com/relayedock/relayedock/internal/funding"
	"github.com/relayedock/relayedock/internal/governance"
	"github.com/relayedock/relayedock/internal/observability"
	"github.com/relayedock/relayedock/internal/payment"
	"github.com/relayedock/relayedock/internal/payout"
	"github.com/relayedock/relayedock/internal/providerquality"
	"github.com/relayedock/relayedock/internal/providers"
	provideranthropic "github.com/relayedock/relayedock/internal/providers/anthropic"
	providerdeepseek "github.com/relayedock/relayedock/internal/providers/deepseek"
	providergemini "github.com/relayedock/relayedock/internal/providers/gemini"
	"github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/provisioning"
	"github.com/relayedock/relayedock/internal/ratelimit"
	"github.com/relayedock/relayedock/internal/reconciliation"
	"github.com/relayedock/relayedock/internal/scheduler"
	"github.com/relayedock/relayedock/internal/secrets"
	"github.com/relayedock/relayedock/internal/server"
	"github.com/relayedock/relayedock/internal/settlementworker"
	"github.com/relayedock/relayedock/internal/store"
	"github.com/relayedock/relayedock/internal/subscription"
	"github.com/relayedock/relayedock/internal/version"
	"github.com/relayedock/relayedock/internal/webhook"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(version.String())
		return
	}
	if err := run(); err != nil {
		slog.Error("relayedock_stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "migrate" {
		migrationConfig, configErr := config.LoadMigration()
		if configErr != nil {
			return configErr
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		db, openErr := store.OpenWithPoolConfig(ctx, migrationConfig.DatabaseURL, store.PoolConfig{
			MaxConns: migrationConfig.PostgresMaxConns, MinConns: migrationConfig.PostgresMinConns,
			MaxConnIdleTime: migrationConfig.PostgresMaxIdleTime, MaxConnLifetime: migrationConfig.PostgresMaxLifetime,
		})
		if openErr != nil {
			return openErr
		}
		defer db.Close()
		return db.Migrate(ctx)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	logWriter := io.Writer(os.Stdout)
	var logFile *os.File
	if cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0o750); err != nil {
			return fmt.Errorf("create LOG_DIR: %w", err)
		}
		logFile, err = os.OpenFile(filepath.Join(cfg.LogDir, "relaydock.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open structured log file: %w", err)
		}
		defer logFile.Close()
		logWriter = io.MultiWriter(os.Stdout, logFile)
	}
	logger := slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: level})).With(
		"service.name", "modeldock",
		"service.version", version.Current,
	)
	slog.SetDefault(logger)
	shutdownTracing, err := observability.ConfigureTracing(context.Background(), cfg.OTELExporterEndpoint, version.Current, cfg.OTELExporterInsecure)
	if err != nil {
		return fmt.Errorf("configure OpenTelemetry tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := shutdownTracing(shutdownCtx); shutdownErr != nil {
			logger.Error("otel_trace_shutdown_failed", "error", shutdownErr)
		}
	}()
	gin.SetMode(gin.ReleaseMode)

	startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.OpenWithPoolConfig(startupCtx, cfg.DatabaseURL, store.PoolConfig{
		MaxConns:        cfg.PostgresMaxConns,
		MinConns:        cfg.PostgresMinConns,
		MaxConnIdleTime: cfg.PostgresMaxIdleTime,
		MaxConnLifetime: cfg.PostgresMaxLifetime,
	})
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.MigrationMode == "startup" {
		if err = db.Migrate(startupCtx); err != nil {
			return err
		}
	} else if cfg.MigrationMode == "external" {
		if err = db.VerifySchemaCurrent(startupCtx); err != nil {
			return err
		}
	} else if cfg.MigrationMode == "disabled" {
		logger.Warn("database_migrations_disabled", "behavior", "startup continues only for an already-migrated compatible schema")
	}
	if err = db.BootstrapAdmin(startupCtx, cfg.AdminEmail, cfg.AdminPassword, cfg.AdminDisplayName); err != nil {
		return err
	}

	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	redisOptions.PoolSize = cfg.RedisPoolSize
	redisOptions.MinIdleConns = cfg.RedisMinIdleConns
	redisOptions.DialTimeout = cfg.RedisDialTimeout
	redisOptions.ReadTimeout = cfg.RedisReadTimeout
	redisOptions.WriteTimeout = cfg.RedisWriteTimeout
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()
	if err := redisClient.Ping(startupCtx).Err(); err != nil {
		logger.Warn("redis_initially_unavailable", "error", err, "behavior", "gateway fails closed and readiness remains false")
	}

	vault, err := secretcrypto.NewVault(cfg.MasterKey)
	if err != nil {
		return err
	}
	keys, err := apikey.NewManager(cfg.APIKeyHMACSecret)
	if err != nil {
		return err
	}
	authManager, err := auth.NewManagerWithRefreshAndPrevious(cfg.JWTSecret, cfg.JWTPreviousSecret, cfg.JWTLifetime, cfg.JWTRefreshLifetime)
	if err != nil {
		return err
	}
	metrics := &observability.Metrics{}
	providerPolicy := openai.EndpointPolicy{AllowedHosts: cfg.ProviderAllowedHosts, AllowPrivateNetwork: cfg.ProviderAllowPrivate, AllowHTTP: cfg.ProviderAllowHTTP}
	openAI := openai.NewWithPolicy(nil, providerPolicy)
	providerRegistry := providers.NewRegistry()
	providerRegistry.Register("openai", openAI)
	providerRegistry.Register("openai_compatible", openAI)
	providerRegistry.Register("openrouter", openAI)
	providerRegistry.Register("qwen", openAI)
	providerRegistry.Register("kimi", openAI)
	providerRegistry.Register("glm", openAI)
	providerRegistry.Register("grok", openAI)
	providerRegistry.Register("xai", openAI)
	providerRegistry.Register("mock_enterprise", openAI)
	providerRegistry.Register("deepseek", providerdeepseek.NewWithPolicy(nil, providerPolicy))
	providerRegistry.Register("anthropic", provideranthropic.NewWithPolicy(nil, providerPolicy))
	providerRegistry.Register("gemini", providergemini.NewWithPolicy(nil, providerPolicy))
	paymentRegistry := payment.NewRegistry()
	paymentRegistry.Register(payment.NewSandbox(payment.SandboxConfig{Enabled: cfg.PaymentSandboxEnabled,
		Secret: cfg.PaymentSandboxSecret, AllowedRegions: cfg.PaymentAllowedRegions, TimestampSkew: cfg.PaymentTimestampSkew}))
	paymentRegistry.Register(payment.NewManualTransfer(cfg.PaymentManualEnabled, cfg.PaymentAllowedRegions))
	payoutRegistry := payout.NewRegistry()
	payoutRegistry.Register(payout.NewSandbox(payout.SandboxConfig{Enabled: cfg.PayoutSandboxEnabled,
		Secret: cfg.PayoutSandboxSecret, AllowedRegions: cfg.PayoutAllowedRegions}))
	webhookDispatcher := webhook.New(webhook.Config{Timeout: cfg.WebhookTimeout, AllowHTTP: cfg.WebhookAllowHTTP, AllowPrivateNetwork: cfg.WebhookAllowPrivate})
	cockpitClient := cockpit.New(cockpit.Config{SnapshotPath: cfg.CockpitSnapshotPath, BaseURL: cfg.CockpitBaseURL, APIKey: cfg.CockpitAPIKey, TestModel: cfg.CockpitTestModel})
	provisioningRegistry := provisioning.NewRegistry()
	provisioningRegistry.Register("openai", provisioning.NewOpenAIAdmin(nil, cfg.OpenAIAdminKey, cfg.OpenAIProvisioningEnabled))
	for _, providerType := range []string{"openai_compatible", "openrouter", "deepseek", "anthropic", "gemini", "qwen", "kimi", "glm", "grok", "xai"} {
		provisioningRegistry.Register(providerType, provisioning.NewStatic(provisioning.Capability{ProviderType: providerType,
			Mode: "BYOK", Enabled: true, Reason: "No configured official project-credit API; use BYOK or an operator-reviewed enterprise binding."}))
	}
	provisioningRegistry.Register("mock_enterprise", provisioning.NewMockEnterprise(cfg.ProviderProvisioningMock))
	workerHost, _ := os.Hostname()
	webhookWorker := webhook.NewWorker(db, vault, webhookDispatcher, webhook.WorkerConfig{
		WorkerID:     workerHost + "-" + fmt.Sprint(os.Getpid()),
		PollInterval: cfg.WebhookPollInterval,
		Logger:       logger,
	})
	var mailProvider email.Provider
	if cfg.MailProvider == "smtp" {
		mailProvider, err = email.NewSMTPProvider(email.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLSMode: cfg.SMTPTLSMode})
		if err != nil {
			return err
		}
	} else {
		mailProvider = &email.CaptureProvider{Directory: cfg.MailCaptureDir}
	}
	mailWorker := email.NewWorker(db, vault, mailProvider, email.WorkerConfig{
		WorkerID: workerHost + "-mail-" + fmt.Sprint(os.Getpid()), PollInterval: cfg.MailPollInterval,
		MaxBackoff: 15 * time.Minute, Logger: logger,
	})
	fundingWorker := funding.NewWorker(db, cfg.FundingRecoveryPoll, cfg.FundingStaleAfter, logger)
	paymentWorker := payment.NewWorker(db, paymentRegistry, cfg.PaymentPollInterval, logger)
	subscriptionWorker := subscription.NewWorker(db, cfg.SubscriptionPoll, logger)
	reconciliationWorker := reconciliation.NewWorkerWithPayments(db, paymentRegistry, cfg.ReconciliationInterval, cfg.ReconciliationRunAt, logger)
	governanceWorker := governance.NewWorker(db, cfg.GovernanceCleanupInterval, logger)
	providerQualityWorker := providerquality.NewWorker(db, vault, providerRegistry, providerquality.Config{
		Region: cfg.ProviderQualityProbeRegion, PollInterval: cfg.ProviderQualityPoll,
		Lease: cfg.ProviderQualityLease, BatchSize: cfg.ProviderQualityBatchSize, Logger: logger,
	})
	provisioningWorker := provisioning.NewWorker(db, vault, provisioningRegistry, provisioning.WorkerConfig{
		WorkerID: workerHost + "-provisioning-" + fmt.Sprint(os.Getpid()), PollInterval: cfg.ProviderProvisioningPoll,
		Lease: cfg.ProviderProvisioningLease, BatchSize: cfg.ProviderProvisioningBatch, Logger: logger,
	})
	supplierSettlementWorker := settlementworker.NewWorker(db, vault, payoutRegistry, cfg.SupplierSettlementPoll,
		cfg.SupplierSettlementBatchSize, logger)
	counter := scheduler.NewRedisCounter(redisClient)
	startupComplete := &atomic.Bool{}
	draining := &atomic.Bool{}
	deps := server.Dependencies{
		Config: cfg, Store: db, Redis: redisClient, Vault: vault, APIKeys: keys, Auth: authManager,
		OpenAI: openAI, Providers: providerRegistry, Provisioners: provisioningRegistry, Scheduler: scheduler.New(db, counter), Limiter: ratelimit.New(redisClient), Metrics: metrics, Logger: logger, Webhooks: webhookDispatcher, Cockpit: cockpitClient, Payments: paymentRegistry,
		StartupComplete: startupComplete, Draining: draining, SecretManager: secrets.NewEnvManager(), Payouts: payoutRegistry,
	}
	gateway := &http.Server{Addr: cfg.GatewayAddr, Handler: server.GatewayEngine(deps), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	control := &http.Server{Addr: cfg.ControlAddr, Handler: server.ControlEngine(deps), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	errCh := make(chan error, 2)
	go func() {
		logger.Info("server_started", "component", "gateway", "address", cfg.GatewayAddr)
		errCh <- gateway.ListenAndServe()
	}()
	startupComplete.Store(true)
	go func() {
		logger.Info("server_started", "component", "control_plane", "address", cfg.ControlAddr)
		errCh <- control.ListenAndServe()
	}()
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	mailWorkerDone := make(chan struct{})
	fundingWorkerDone := make(chan struct{})
	paymentWorkerDone := make(chan struct{})
	subscriptionWorkerDone := make(chan struct{})
	reconciliationWorkerDone := make(chan struct{})
	governanceWorkerDone := make(chan struct{})
	providerQualityWorkerDone := make(chan struct{})
	provisioningWorkerDone := make(chan struct{})
	supplierSettlementWorkerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		webhookWorker.Run(workerCtx)
	}()
	go func() {
		defer close(mailWorkerDone)
		mailWorker.Run(workerCtx)
	}()
	go func() {
		defer close(fundingWorkerDone)
		fundingWorker.Run(workerCtx)
	}()
	go func() {
		defer close(paymentWorkerDone)
		paymentWorker.Run(workerCtx)
	}()
	go func() {
		defer close(subscriptionWorkerDone)
		subscriptionWorker.Run(workerCtx)
	}()
	go func() {
		defer close(reconciliationWorkerDone)
		reconciliationWorker.Run(workerCtx)
	}()
	go func() {
		defer close(governanceWorkerDone)
		governanceWorker.Run(workerCtx)
	}()
	go func() {
		defer close(providerQualityWorkerDone)
		providerQualityWorker.Run(workerCtx)
	}()
	go func() {
		defer close(provisioningWorkerDone)
		provisioningWorker.Run(workerCtx)
	}()
	go func() {
		defer close(supplierSettlementWorkerDone)
		supplierSettlementWorker.Run(workerCtx)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	var cause error
	select {
	case sig := <-signals:
		logger.Info("shutdown_requested", "signal", sig.String())
	case serveErr := <-errCh:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			cause = serveErr
		}
	}
	draining.Store(true)
	logger.Info("readiness_disabled", "drain_delay", cfg.DrainDelay.String())
	time.Sleep(cfg.DrainDelay)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	stopWorker()
	gatewayErr := gateway.Shutdown(shutdownCtx)
	controlErr := control.Shutdown(shutdownCtx)
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-mailWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-fundingWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-paymentWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-subscriptionWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-reconciliationWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-governanceWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-providerQualityWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-provisioningWorkerDone:
	case <-shutdownCtx.Done():
	}
	select {
	case <-supplierSettlementWorkerDone:
	case <-shutdownCtx.Done():
	}
	if cause != nil {
		return cause
	}
	if gatewayErr != nil {
		return gatewayErr
	}
	return controlErr
}
