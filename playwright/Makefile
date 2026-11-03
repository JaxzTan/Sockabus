# Playwright e2e suite (README.md). Run from playwright/ (`make <target>`) or from the repo root
# (`make -C playwright <target>`). The suite only ever runs on the throwaway stack in scripts/e2e-stack.sh:
# never against Supabase.
# env -u: the root Makefile exports .env, whose DATABASE_URL is Supabase; the suite must never see it.
SAFE := env -u DATABASE_URL -u SOCKABUS_POSTGRES_URL
INTERVAL ?= 60
MECH ?= cron
# Extra playwright args for `make test`, e.g. make test ARGS='ui -g bank'
ARGS ?=

.DEFAULT_GOAL := help
.PHONY: help install up down status env test test-api test-ui test-ci report clean monitor monitor-install monitor-uninstall monitor-status

help: ## list targets
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

install: ## pnpm install + the Chromium browser (once)
	pnpm install
	pnpm exec playwright install chromium

up: ## start the throwaway stack (PG, Redis, Temporal, API, console); E2E_LLM_PROVIDER=fake for offline
	@$(SAFE) scripts/e2e-stack.sh up

down: ## stop the stack and delete the scratch DB
	@scripts/e2e-stack.sh down

status: ## what is running and which LLM the API uses
	@scripts/e2e-stack.sh status

env: ## print the test variables: eval "$(make -s env)"
	@scripts/e2e-stack.sh env

test: ## full suite on the stack (needs `make up`); ARGS='ui -g bank' to filter
	@$(SAFE) scripts/e2e-stack.sh test $(ARGS)

test-api: ## API project only, on the stack
	@$(SAFE) scripts/e2e-stack.sh test --project=api $(ARGS)

test-ui: ## UI project only, on the stack
	@$(SAFE) scripts/e2e-stack.sh test --project=ui $(ARGS)

test-ci: ## what CI runs: everything except @live
	@$(SAFE) scripts/e2e-stack.sh test --grep-invert @live $(ARGS)

report: ## open the HTML report of the last run
	pnpm exec playwright show-report playwright-report

clean: ## delete the last run's report and traces (keeps results/ and monitor history)
	rm -rf playwright-report test-results blob-report

monitor: ## run the suite once on the stack, archive the result, alert on failure
	@$(SAFE) scripts/e2e-monitor.sh

monitor-install: ## schedule `make monitor` every INTERVAL minutes (default 60) via MECH=cron (default) or MECH=launchd
	@scripts/e2e-monitor-schedule.sh install '$(INTERVAL)' '$(MECH)'

monitor-uninstall: ## remove the monitor's crontab line / LaunchAgent (other crontab lines are kept)
	@scripts/e2e-monitor-schedule.sh uninstall

monitor-status: ## the installed schedule and the last monitor run
	@scripts/e2e-monitor-schedule.sh status
