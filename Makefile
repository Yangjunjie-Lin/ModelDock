.DEFAULT_GOAL := help

COMPOSE := docker compose
PYTHON ?= python

.PHONY: help env dirs config build up up-mock down restart logs ps test vet frontend-install frontend-lint frontend-typecheck frontend-build frontend-audit frontend-ci test-sdk test-funding fmt format-check

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

env: ## Create .env from .env.example when it does not exist
	@test -f .env || cp .env.example .env
	@echo "Review .env and replace every CHANGE_ME value before starting RelayDock."

dirs: ## Create project-local runtime directories
	mkdir -p data/postgres data/redis logs

config: ## Validate the Compose model using .env
	$(COMPOSE) --env-file .env config --quiet

build: ## Build all application images
	$(COMPOSE) --env-file .env build

up: dirs ## Start RelayDock
	$(COMPOSE) --env-file .env up -d --build

up-mock: dirs ## Start RelayDock with the offline mock OpenAI profile
	$(COMPOSE) --env-file .env --profile mock-openai up -d --build

down: ## Stop services without deleting project data
	$(COMPOSE) --env-file .env down --remove-orphans

restart: ## Restart application services
	$(COMPOSE) --env-file .env restart relaydock admin-web console-web

logs: ## Follow service logs
	$(COMPOSE) --env-file .env logs -f --tail=200

ps: ## Show service and health status
	$(COMPOSE) --env-file .env ps

test: ## Run backend tests and all frontend checks
	go test ./...
	$(MAKE) frontend-ci

vet: ## Run Go static analysis
	go vet ./...

frontend-install: ## Install both locked frontend dependency graphs
	npm --prefix apps/admin-web ci
	npm --prefix apps/console-web ci

frontend-lint: ## Lint both web applications
	npm --prefix apps/admin-web run lint
	npm --prefix apps/console-web run lint

frontend-typecheck: ## Typecheck both web applications
	npm --prefix apps/admin-web run typecheck
	npm --prefix apps/console-web run typecheck

frontend-build: ## Build both web applications
	npm --prefix apps/admin-web run build
	npm --prefix apps/console-web run build

frontend-audit: ## Fail for high or critical npm advisories
	npm --prefix apps/admin-web audit --audit-level=high
	npm --prefix apps/console-web audit --audit-level=high

frontend-ci: frontend-lint frontend-typecheck frontend-build frontend-audit ## Run frontend CI without reinstalling dependencies

test-sdk: ## Run official Python SDK compatibility tests against a running gateway
	$(PYTHON) -m pytest -q tests/sdk/python

test-funding: ## Run isolated concurrent reservation and immutable-ledger integration tests
	powershell -File tests/integration/verify-funding.ps1 -ConfirmIsolatedTestDatabase

test-payments: ## Run isolated payment replay, recovery, and traceability integration tests
	powershell -File tests/integration/verify-payments.ps1

test-marketplace: ## Run isolated Marketplace launch, payout-gate, and lifecycle integration tests
	powershell -File tests/integration/verify-marketplace-launch.ps1 -ConfirmIsolatedTestDatabase

fmt: ## Format Go sources
	gofmt -w cmd internal migrations tests/backend

format-check: ## Fail when Go sources are not gofmt-clean
	@test -z "$$(gofmt -l cmd internal migrations tests/backend)"
