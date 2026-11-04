-include .env
export

# make keeps quotes from .env values; strip them so Go and migrate get a valid URL
SOCKABUS_POSTGRES_URL := $(subst ",,$(SOCKABUS_POSTGRES_URL))
DATABASE_URL := $(subst ",,$(DATABASE_URL))

# The app (make run/dev, compose app profile) uses SOCKABUS_POSTGRES_URL from .env: Supabase session pooler.
# migrate-*, seed, backup, db-check and app-role use DATABASE_URL from .env (Supabase owner, session pooler)
# when set, else local Docker. test-db and test-e2e always use the local-db Docker container (never Supabase).
LOCAL_DB := postgres://buru:buru@localhost:5432/buru?sslmode=disable
MIGRATE_DB := $(if $(DATABASE_URL),$(if $(findstring ?,$(DATABASE_URL)),$(DATABASE_URL),$(DATABASE_URL)?sslmode=require),$(LOCAL_DB))
# The direct host db.<ref>.supabase.co is IPv6-only (unreachable from this Mac and Docker Desktop):
# refuse it so a target fails fast with a hint instead of hanging. The pooler host ends in supabase.com.
DIRECT_HOST := $(or $(findstring supabase.co:,$(MIGRATE_DB)),$(findstring supabase.co/,$(MIGRATE_DB)))
NEED_POOLER = @$(if $(DIRECT_HOST),echo "DATABASE_URL uses the IPv6-only db.<ref>.supabase.co host; use the session pooler: postgresql://postgres.<ref>:<pw>@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require" >&2; exit 2,true)
# sqlc from PATH, else where `go install` puts it
SQLC := $(or $(shell command -v sqlc),$(shell go env GOPATH)/bin/sqlc)
# golangci-lint v2 under either name, else where `go install` puts it
GOLANGCI := $(or $(shell command -v golangci-lint-v2),$(shell command -v golangci-lint),$(shell go env GOPATH)/bin/golangci-lint)

# Full prod stack on the VPS (docker-compose.prod.yml adds Caddy, Uptime Kuma, persistent Temporal)
PROD := docker compose -f docker-compose.yml -f docker-compose.prod.yml --profile app

.PHONY: dev local-db retention-purge backfill-encrypt demo-customer all up down deploy prod-ps prod-logs backup restore-test migrate-up migrate-down db-check app-role seed keys tglogin tgchat sim sqlc run web arch test test-db test-e2e lint e2e-up e2e e2e-down monitor monitor-install monitor-uninstall monitor-status

all: ## build and start redis, temporal, app, web (database = Supabase from .env; no migrations run)
	docker compose up -d --wait redis temporal
	docker compose --profile app up -d --build --wait --remove-orphans
	docker compose --profile app ps

up: ## start Redis, Temporal (the database is Supabase, SOCKABUS_POSTGRES_URL in .env)
	docker compose up -d --wait redis temporal

local-db: ## start the throwaway local PostgreSQL (tests only) and migrate it
	docker compose --profile local-db up -d --wait postgres
	docker compose --profile tools run --rm migrate

down: ## stop everything, including the app/web and local-db profile containers
	docker compose --profile app --profile local-db down

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
	$(NEED_POOLER)
	@DATABASE_URL='$(MIGRATE_DB)' scripts/backup.sh

restore-test: ## restore the newest dump into a throwaway container, count rows, remove it (never touches Supabase)
	scripts/restore-test.sh $(FILE)

migrate-up:
	$(NEED_POOLER)
	@migrate -path db/migrations -database '$(MIGRATE_DB)' up # @: never echo the URL (it holds the password)

migrate-down:
	$(NEED_POOLER)
	@migrate -path db/migrations -database '$(MIGRATE_DB)' down 1

db-check: ## read-only: applied migration version + rows that would break migration 000005's constraints
	$(NEED_POOLER)
	@DATABASE_URL='$(MIGRATE_DB)' scripts/pg.sh psql -q < scripts/check-migrations.sql

app-role: ## create or re-key the app login buru_api (member of buru_app); prints its new password once
	$(NEED_POOLER)
	@DATABASE_URL='$(MIGRATE_DB)' scripts/app-role.sh

seed: ## demo operator, playbooks and the four console personas (idempotent)
	$(NEED_POOLER)
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

demo-customer: ## create a bank customer + API key on DB (required, never .env): make demo-customer DB='postgres://...' [NAME='Demo Bank']
	@test -n '$(DB)' || (echo "DB=postgres://... is required (refuses Supabase)"; exit 2)
	@go run ./cmd/customerkey -db '$(DB)' -name '$(or $(NAME),Demo Bank)' -type '$(or $(TYPE),bank)'

# DB= is required and never defaults to .env; both refuse Supabase unless ARGS=-allow-production.
retention-purge: ## TRD §7 12-month purge (000013): make retention-purge DB='postgres://...' [DRY_RUN=0] [BY=name]; dry run unless DRY_RUN=0
	@test -n '$(DB)' || (echo "DB=postgres://... is required (refuses Supabase without ARGS=-allow-production)"; exit 2)
	@go run ./cmd/retention-purge -db '$(DB)' -dry-run=$(if $(filter 0,$(DRY_RUN)),false,true) $(if $(BY),-by '$(BY)',) $(ARGS)

backfill-encrypt: ## encrypt pre-000012 scammer identifiers: make backfill-encrypt DB='postgres://...' [DRY_RUN=0] [ARGS=-verify]; needs SOCKABUS_MESSAGE_KEY
	@test -n '$(DB)' || (echo "DB=postgres://... is required (refuses Supabase without ARGS=-allow-production)"; exit 2)
	@go run ./cmd/backfill-encrypt -db '$(DB)' -dry-run=$(if $(filter 0,$(DRY_RUN)),false,true) $(ARGS)

sqlc: ## regenerate internal/db from db/migrations + db/queries
	$(SQLC) generate
	$(SQLC) vet

stop:
	docker compose stop

run: up
	go run ./cmd/sockabus

dev: run ## same as run: Redis + Temporal in Docker, Go app on the host against Supabase

web: ## dashboard on :3000, proxies /api to :8080
	cd web && pnpm dev

arch: ## open docs/architecture.html in the default browser
	@$(if $(shell command -v open),open,xdg-open) docs/architecture.html

test:
	go test -race ./...

test-db: local-db ## DB integration tests on the local-db container (never Supabase); writes are rolled back
	SOCKABUS_TEST_POSTGRES_URL='$(LOCAL_DB)' go test -count=1 ./internal/db/

test-e2e: up local-db ## full honeypot loop on the local-db PostgreSQL + Temporal (never Supabase)
	SOCKABUS_TEST_POSTGRES_URL='$(LOCAL_DB)' SOCKABUS_TEST_TEMPORAL=localhost:7233 go test -count=1 -v -run TestE2E ./internal/honeypot/

lint:
	go vet ./...
	$(GOLANGCI) run ./...

# Playwright e2e suite: self-contained in playwright/ (its own package.json, Makefile, scripts). These
# targets forward to playwright/Makefile; `make -C playwright help` lists all of them. Never Supabase.
INTERVAL ?= 60
MECH ?= cron

e2e-up: ## start the throwaway Playwright test stack (playwright/scripts/e2e-stack.sh up)
	@$(MAKE) -s -C playwright up

e2e: ## run the Playwright suite on the test stack (needs `make e2e-up`); ARGS='ui -g bank' to filter
	@$(MAKE) -s -C playwright test ARGS='$(ARGS)'

e2e-down: ## stop the Playwright test stack and delete its scratch DB
	@$(MAKE) -s -C playwright down

monitor: ## run the Playwright suite once on the test stack, archive the result, alert on failure
	@$(MAKE) -s -C playwright monitor

monitor-install: ## schedule `make monitor` every INTERVAL minutes (default 60) via MECH=cron (default) or MECH=launchd
	@$(MAKE) -s -C playwright monitor-install INTERVAL='$(INTERVAL)' MECH='$(MECH)'

monitor-uninstall: ## remove the monitor's crontab line / LaunchAgent (other crontab lines are kept)
	@$(MAKE) -s -C playwright monitor-uninstall

monitor-status: ## the installed schedule and the last monitor run
	@$(MAKE) -s -C playwright monitor-status

re: down all
