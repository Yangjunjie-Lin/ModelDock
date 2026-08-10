.DEFAULT_GOAL := help

COMPOSE := docker compose
PYTHON ?= python

.PHONY: help env dirs config build up up-mock down restart logs ps test frontend-build test-sdk fmt

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

test: ## Run backend tests and frontend typechecked builds
	go test ./...
	$(MAKE) frontend-build

frontend-build: ## Typecheck and build both web applications
	npm --prefix apps/admin-web run build
	npm --prefix apps/console-web run build

test-sdk: ## Run official Python SDK compatibility tests against a running gateway
	$(PYTHON) -m pytest -q tests/sdk/python

fmt: ## Format Go sources
	gofmt -w cmd internal
