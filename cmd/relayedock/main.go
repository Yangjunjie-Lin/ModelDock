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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/cockpit"
	"github.com/relayedock/relayedock/internal/config"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/observability"
	"github.com/relayedock/relayedock/internal/providers/openai"
	"github.com/relayedock/relayedock/internal/ratelimit"
	"github.com/relayedock/relayedock/internal/scheduler"
	"github.com/relayedock/relayedock/internal/server"
	"github.com/relayedock/relayedock/internal/store"
	"github.com/relayedock/relayedock/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		slog.Error("relayedock_stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
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
		logFile, err = os.OpenFile(filepath.Join(cfg.LogDir, "relaydock.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return fmt.Errorf("open structured log file: %w", err)
		}
		defer logFile.Close()
		logWriter = io.MultiWriter(os.Stdout, logFile)
	}
	logger := slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	gin.SetMode(gin.ReleaseMode)

	startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.OpenWithPoolConfig(startupCtx, cfg.DatabaseURL, store.PoolConfig{
		MaxConns:        int32(cfg.PostgresMaxConns),
		MinConns:        int32(cfg.PostgresMinConns),
		MaxConnIdleTime: cfg.PostgresMaxIdleTime,
		MaxConnLifetime: cfg.PostgresMaxLifetime,
	})
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.Migrate(startupCtx); err != nil {
		return err
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
	authManager, err := auth.NewManagerWithRefresh(cfg.JWTSecret, cfg.JWTLifetime, cfg.JWTRefreshLifetime)
	if err != nil {
		return err
	}
	metrics := &observability.Metrics{}
	openAI := openai.New(nil)
	webhookDispatcher := webhook.New(webhook.Config{Timeout: cfg.WebhookTimeout, AllowHTTP: cfg.WebhookAllowHTTP, AllowPrivateNetwork: cfg.WebhookAllowPrivate})
	cockpitClient := cockpit.New(cockpit.Config{SnapshotPath: cfg.CockpitSnapshotPath, BaseURL: cfg.CockpitBaseURL, APIKey: cfg.CockpitAPIKey, TestModel: cfg.CockpitTestModel})
	workerHost, _ := os.Hostname()
	webhookWorker := webhook.NewWorker(db, vault, webhookDispatcher, webhook.WorkerConfig{
		WorkerID:     workerHost + "-" + fmt.Sprint(os.Getpid()),
		PollInterval: cfg.WebhookPollInterval,
		Logger:       logger,
	})
	counter := scheduler.NewRedisCounter(redisClient)
	deps := server.Dependencies{
		Config: cfg, Store: db, Redis: redisClient, Vault: vault, APIKeys: keys, Auth: authManager,
		OpenAI: openAI, Scheduler: scheduler.New(db, counter), Limiter: ratelimit.New(redisClient), Metrics: metrics, Logger: logger, Webhooks: webhookDispatcher, Cockpit: cockpitClient,
	}
	gateway := &http.Server{Addr: cfg.GatewayAddr, Handler: server.GatewayEngine(deps), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	control := &http.Server{Addr: cfg.ControlAddr, Handler: server.ControlEngine(deps), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	errCh := make(chan error, 2)
	go func() {
		logger.Info("server_started", "component", "gateway", "address", cfg.GatewayAddr)
		errCh <- gateway.ListenAndServe()
	}()
	go func() {
		logger.Info("server_started", "component", "control_plane", "address", cfg.ControlAddr)
		errCh <- control.ListenAndServe()
	}()
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		webhookWorker.Run(workerCtx)
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
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	stopWorker()
	gatewayErr := gateway.Shutdown(shutdownCtx)
	controlErr := control.Shutdown(shutdownCtx)
	select {
	case <-workerDone:
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
