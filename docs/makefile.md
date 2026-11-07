# Makefile

Shortcuts for local development. Run every target from the repo root.

## Requirements

| Tool | Needed for | Install |
|---|---|---|
| Docker | `up`, `down` | Docker Desktop |
| Go 1.26+ | `run`, `test`, `test-db`, `lint` | go.dev/dl |
| golang-migrate | `migrate-up`, `migrate-down` | `brew install golang-migrate` |
| sqlc | `sqlc` | `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest` |
| pnpm | `web` | `corepack enable` |
| golangci-lint v2 | `lint` | `brew install golangci-lint` (found as `golangci-lint-v2` or `golangci-lint`) |

## Targets

| Target | What it runs | Use when |
|---|---|---|
| `make all` | infra up, `migrate` container, then `--profile app up --build` | Running the whole stack in Docker: all 5 containers (app on `:8080`, web on `:3000`) |
| `make up` | `docker compose up -d --wait postgres redis temporal` | Starting work: brings up the 3 dependencies and waits until healthy |
| `make down` | `docker compose down` | Stopping the dependencies. Does not stop `app`/`web` profile containers (see [docker.md](docker.md)) |
| `make migrate-up` | `migrate -path db/migrations ... up` on `MIGRATE_DB` | After pulling new migrations |
| `make migrate-down` | `migrate ... down 1` | Undoing the most recent migration |
| `make db-check` | read-only `scripts/check-migrations.sql` on `MIGRATE_DB` | Before `migrate-up` on Supabase: shows the applied version and rows that would break migration 000005 (all must be 0) |
| `make app-role` | `scripts/app-role.sh` on `MIGRATE_DB` | Once per database: creates the app login `buru_api` (or gives it a new password) and prints the password |
| `make seed` | `db/seed/001_demo.sql` on `MIGRATE_DB` | Once after migrating: operator 1, playbooks, demo personas (safe to rerun) |
| `make backup` / `make restore-test` | `pg_dump` of `MIGRATE_DB` into `backups/`; restore into a throwaway container | Nightly backup; proving a dump restores. The restore never touches Supabase |
| `make sqlc` | `sqlc generate` then `sqlc vet` | After changing any file in `db/migrations` or `db/queries` |
| `make run` | `go run ./cmd/sockabus` | Running the Go API on `:8080` |
| `make web` | `cd web && pnpm dev` | Running the dashboard on `:3000` (proxies `/api/*` to `:8080`) |
| `make arch` | `open docs/architecture.html` (`xdg-open` on Linux) | Viewing the architecture diagram in the browser |
| `make test` | `go test -race ./...` | Unit tests. DB tests are skipped |
| `make test-db` | `go test ./internal/db/` on `TEST_DB` | DB integration tests. Needs `make e2e-up` (scratch DB, migrated + seeded) |
| `make lint` | `go vet` + golangci-lint | Before opening a PR |

## Which database each target uses

The Makefile loads `.env` and exports every variable in it. It strips quotes from `SOCKABUS_POSTGRES_URL` and `DATABASE_URL`, because `make` keeps them and Go would reject the URL.

| Target | Database |
|---|---|
| `migrate-up`, `migrate-down`, `db-check`, `app-role`, `seed`, `backup` | `DATABASE_URL` from `.env` (Supabase owner, `sslmode=require` added if the URL has no query). They refuse to run when `DATABASE_URL` is empty |
| `run` | `SOCKABUS_POSTGRES_URL` from `.env` |
| `test-db`, `test-e2e` | Always the Playwright scratch DB (`make e2e-up`, `localhost:15432/sockabus_e2e`), never Supabase: `test-e2e` writes append-only rows that cannot be deleted |

`db-check`, `app-role`, `seed` and `backup` need no `psql` on the host: `scripts/pg.sh` runs the PostgreSQL 17 client from Docker.

`test-db` writes inside transactions that are rolled back, so nothing is kept. There is no local Docker database for the app: it always uses Supabase.

Use local Docker for one run: `make migrate-up DATABASE_URL=` (a command-line value overrides `.env`; an empty shell variable does not).

These targets do not echo their command, because the URL contains the password.

## Typical flows

**First run on a new machine**
```sh
cp .env.example .env   # fill in values
make up
make migrate-up
make run        # terminal 1
make web        # terminal 2
```

**Changing the schema**
1. Add `db/migrations/00000N_<name>.up.sql` and its `.down.sql`.
2. `make e2e-down && make e2e-up` (fresh scratch DB with the new migration), then `make test-db`
3. Update `db/queries/*.sql` if needed, then `make sqlc`.
4. `make migrate-up` (applies it to Supabase)

**Before a PR**
```sh
make lint && make test && make e2e-up && make test-db
```

## Variables

| Variable | Default | Meaning |
|---|---|---|
| `MIGRATE_DB` | `DATABASE_URL` from `.env` (empty = targets refuse) | Supabase database for `migrate-*`, `db-check`, `seed`, `backup` |
| `TEST_DB` | `postgres://buru:buru@localhost:15432/sockabus_e2e?sslmode=disable` | Playwright scratch DB for `test-db` and `test-e2e` |
| `SQLC` | `sqlc` on `PATH`, else `$(go env GOPATH)/bin/sqlc` | sqlc binary |
| `GOLANGCI` | `golangci-lint-v2` or `golangci-lint` on `PATH`, else `$(go env GOPATH)/bin/golangci-lint` | Linter binary |

Override one for a single run: `make migrate-up MIGRATE_DB='postgres://...'`.
