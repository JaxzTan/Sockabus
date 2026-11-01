-include .env
export

# make keeps quotes from .env values; strip them so Go and migrate get a valid URL
SOCKABUS_POSTGRES_URL := $(subst ",,$(SOCKABUS_POSTGRES_URL))
DATABASE_URL := $(subst ",,$(DATABASE_URL))

# migrate-*, test-db, seed, backup, db-check and app-role use DATABASE_URL from .env (Supabase owner)
# when set, else local Docker. test-e2e always uses local Docker: it writes append-only test rows.
LOCAL_DB := postgres://buru:buru@localhost:5432/buru?sslmode=disable
MIGRATE_DB := $(if $(DATABASE_URL),$(if $(findstring ?,$(DATABASE_URL)),$(DATABASE_URL),$(DATABASE_URL)?sslmode=require),$(LOCAL_DB))
# sqlc from PATH, else where `go install` puts it
SQLC := $(or $(shell command -v sqlc),$(shell go env GOPATH)/bin/sqlc)
# golangci-lint v2 under either name, else where `go install` puts it
GOLANGCI := $(or $(shell command -v golangci-lint-v2),$(shell command -v golangci-lint),$(shell go env GOPATH)/bin/golangci-lint)

# Full prod stack on the VPS (docker-compose.prod.yml adds Caddy, Uptime Kuma, persistent Temporal)
PROD := docker compose -f docker-compose.yml -f docker-compose.prod.yml --profile app

.PHONY: all up down deploy prod-ps prod-logs backup restore-test migrate-up migrate-down db-check app-role seed keys tglogin tgchat sim sqlc run web arch test test-db test-e2e lint

all: ## build and start every container: postgres, redis, temporal, migrations, app, web
	docker compose up -d --wait postgres redis temporal
	docker compose --profile tools run --rm migrate
	docker compose --profile app up -d --build --wait --remove-orphans
	docker compose --profile app ps

up: ## start PostgreSQL, Redis, Temporal
	docker compose up -d --wait postgres redis temporal

down: ## stop everything, including the app/web profile containers
	docker compose --profile app down

deploy: ## on the VPS: pull main, run migrations, rebuild and restart the full stack (TR-23)
	git pull --ff-only origin main
	$(PROD) up -d --wait redis
	$(PROD) run --rm migrate # against Supabase (DATABASE_URL in .env)
	$(PROD) up -d --build --remove-orphans
	$(PROD) ps

prod-ps:
	$(PROD) ps

prod-logs: ## follow app, web and caddy logs on the VPS
	$(PROD) logs -f --tail=100 app web caddy

backup: ## gzip pg_dump of DATABASE_URL (Supabase) into backups/, copied off-server when BACKUP_REMOTE is set
	@DATABASE_URL='$(MIGRATE_DB)' scripts/backup.sh

restore-test: ## restore the newest dump into a throwaway container, count rows, remove it (never touches Supabase)
	scripts/restore-test.sh $(FILE)

migrate-up:
	@migrate -path db/migrations -database '$(MIGRATE_DB)' up # @: never echo the URL (it holds the password)

migrate-down:
	@migrate -path db/migrations -database '$(MIGRATE_DB)' down 1

db-check: ## read-only: applied migration version + rows that would break migration 000005's constraints
	@DATABASE_URL='$(MIGRATE_DB)' scripts/pg.sh psql -q < scripts/check-migrations.sql

app-role: ## create or re-key the app login buru_api (member of buru_app); prints its new password once
	@DATABASE_URL='$(MIGRATE_DB)' scripts/app-role.sh

seed: ## demo operator, playbooks and the four console personas (idempotent)
	@DATABASE_URL='$(MIGRATE_DB)' scripts/pg.sh psql -v ON_ERROR_STOP=1 -q < db/seed/001_demo.sql

keys: ## print fresh values for SOCKABUS_OPERATOR_TOKEN and SOCKABUS_MESSAGE_KEY
	@echo "SOCKABUS_OPERATOR_TOKEN=$$(openssl rand -hex 24)"
	@echo "SOCKABUS_MESSAGE_KEY=$$(openssl rand -base64 32)"

tglogin: ## log a honeypot number into Telegram: make tglogin PHONE=+60123456789 PERSONA=1
	go run ./cmd/tglogin -phone '$(PHONE)' $(if $(PERSONA),-persona $(PERSONA),)

sim: ## chat with a persona without Telegram (needs `make run`): make sim PERSONA=1 [AS=scammer_name]
	go run ./cmd/simchat -persona $(or $(PERSONA),1) -as '$(if $(filter command line,$(origin AS)),$(AS),scammer)'

tgchat: ## play the scammer from your terminal: make tgchat PHONE=<your test account> TO=<honeypot phone or @username>
	go run ./cmd/tgchat -phone '$(PHONE)' -to '$(TO)'

sqlc: ## regenerate internal/db from db/migrations + db/queries
	$(SQLC) generate
	$(SQLC) vet

stop:
	docker compose stop

run: up
	go run ./cmd/sockabus

web: ## dashboard on :3000, proxies /api to :8080
	cd web && pnpm dev

arch: ## open docs/architecture.html in the default browser
	@$(if $(shell command -v open),open,xdg-open) docs/architecture.html

test:
	go test -race ./...

test-db: ## DB integration tests on MIGRATE_DB; writes run in rolled-back transactions
	@SOCKABUS_TEST_POSTGRES_URL='$(MIGRATE_DB)' go test -count=1 ./internal/db/

test-e2e: ## full honeypot loop on local PostgreSQL + Temporal; needs `make up migrate-up` (never Supabase)
	SOCKABUS_TEST_POSTGRES_URL='$(LOCAL_DB)' SOCKABUS_TEST_TEMPORAL=localhost:7233 go test -count=1 -v -run TestE2E ./internal/honeypot/

lint:
	go vet ./...
	$(GOLANGCI) run ./...
