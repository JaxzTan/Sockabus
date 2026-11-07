# Docker

Docker runs the local development stack and, for v1.0, the production stack on one VPS ([TRD.md](TRD.md) §8, TR-23). Progress: [plan.md](plan.md).

## Files

| File | Purpose |
|---|---|
| `docker-compose.yml` | Local stack: 3 dependencies + 2 app containers + `migrate` tool |
| `docker-compose.prod.yml` | Prod overlay: Caddy, Uptime Kuma, persistent Temporal, no public database ports |
| `Caddyfile` | HTTPS reverse proxy for `SOCKABUS_DOMAIN` |
| `scripts/backup.sh`, `scripts/restore-test.sh`, `scripts/pg.sh` | Nightly `pg_dump` of Supabase, restore check in a throwaway container, PostgreSQL 17 client in Docker |
| `Dockerfile` | Go API image (`cmd/sockabus`) |
| `web/Dockerfile` | Next.js dashboard image |
| `.dockerignore`, `web/.dockerignore` | Keep `.env`, `.git`, `node_modules` and `.next` out of builds |

## Services

| Container | Image | Ports | Starts with | Notes |
|---|---|---|---|---|
| `redis` | `redis:7.4-alpine` | 6379 | `make up` | Lookup cache and rate limits (TR-10, TR-13) |
| `temporal` | `temporalio/temporal:1.5.1` | 7233 (gRPC), 8233 (web UI) | `make up` | Dev server; workflow history is in memory and resets on restart |
| `app` | built from `Dockerfile` | 8080 | `--profile app` | Go API. Uses Supabase (`SOCKABUS_POSTGRES_URL`); there is no local database container |
| `web` | built from `web/Dockerfile` | 3000 | `--profile app` | Next.js dashboard. Proxies `/api/*` to `http://app:8080` |
| `migrate` | `migrate/migrate:v4.19.1` | none | `docker compose run --rm migrate` | Applies `db/migrations` to the `postgres` container |

Container names are fixed (`container_name`), so they must be unique on your machine. If another project already has a container named `postgres` or `redis`, `docker compose up` fails: stop that container or rename these.

## Common tasks

| Task | Command |
|---|---|
| Start dependencies only (develop Go/web on the host) | `make up` |
| Start everything in containers | `docker compose --profile app up -d --build` |
| Rebuild after code changes | `docker compose --profile app up -d --build app web` |
| Stop everything | `make down` |
| Run migrations in Docker (no Go tools on the host) | `docker compose run --rm migrate` |
| Stop and delete local database data | `docker compose --profile app down -v` |
| See status | `docker compose --profile app ps` |
| Follow logs | `docker compose logs -f app web` |
| Temporal UI | open `http://localhost:8233` |

## Production (one VPS)

`docker-compose.prod.yml` is layered on top of `docker-compose.yml`. Every `make` prod target uses both files with `--profile app`.

| Container | Reachable from | Notes |
|---|---|---|
| `caddy` | Internet, ports 80 and 443 | Gets a Let's Encrypt certificate for `SOCKABUS_DOMAIN`. `/v1/*`, `/healthz`, `/readyz` go to `app`; everything else goes to `web` |
| `app`, `web`, `postgres`, `redis` | Docker network only | No host ports |
| `temporal` | `127.0.0.1:8233` (UI) | Workflow history is in a SQLite file on the `temporal-data` volume, so it survives restarts |
| `uptime-kuma` | `127.0.0.1:3001` | Monitoring UI. Data in the `uptime-kuma-data` volume |

All containers use `restart: unless-stopped`, so they come back after a crash or reboot.

**First-time server setup**
1. Install Docker and Git; open only ports 22, 80 and 443.
2. `git clone` the repo to `/opt/buru`.
3. `cp .env.example .env`, then set `SOCKABUS_MODE=live`, `SOCKABUS_DOMAIN`, `DATABASE_URL` and `SOCKABUS_POSTGRES_URL` (Supabase session pooler), `make keys` values, the LLM key and the Telegram values.
4. Copy `.secrets/telegram/` from your laptop, then `sudo chown -R 65532:65532 .secrets/telegram` (the app image runs as uid 65532).
5. Point the domain's DNS A record at the server.
6. `make deploy`, then `make seed` once.
7. Add the backup cron: `0 3 * * * cd /opt/buru && make backup >> backups/backup.log 2>&1`.

| Task | Command |
|---|---|
| Deploy `main` (pull, migrate, rebuild, restart) | `make deploy` |
| Status | `make prod-ps` |
| Logs | `make prod-logs` |
| Dump the database now | `make backup` |
| Check the newest dump restores | `make restore-test` (or `FILE=backups/<name>.sql.gz`) |
| Open Temporal UI / Uptime Kuma from your laptop | `ssh -L 8233:localhost:8233 -L 3001:localhost:3001 <server>`, then `http://localhost:8233` and `http://localhost:3001` |

Backups go to `backups/` and are kept for 14 days. Set `BACKUP_REMOTE` (an rclone destination) to also copy them off the server; without it, losing the server loses the backups.

## Images

**Go API (`Dockerfile`)**
1. `golang:1.26-alpine` builds a static binary from `./cmd/sockabus`.
2. `gcr.io/distroless/static-debian12:nonroot` runs it: no shell, non-root user, about 49 MB.

**Web (`web/Dockerfile`)**
1. `deps`: `pnpm install --frozen-lockfile`.
2. `build`: `pnpm build` with `output: "standalone"` (`web/next.config.ts`).
3. Runtime: `node:24-alpine` runs `server.js` as the `node` user, about 290 MB.

The Go API address is baked in at build time (`SOCKABUS_API_URL` build arg, default `http://app:8080`), because Next.js fixes rewrites during `next build`. Changing it means rebuilding the `web` image.

## Configuration and secrets

- The `app` container reads `.env` through `env_file`, then overrides `SOCKABUS_POSTGRES_URL`, `SOCKABUS_REDIS_ADDR` and `SOCKABUS_TEMPORAL_HOSTPORT` to point at the other containers.
- `.env` is never copied into an image (`.dockerignore`).
- To run the `app` container against Supabase, remove the `SOCKABUS_POSTGRES_URL` override and use the Supabase **Session pooler** URL. The direct host (`db.<ref>.supabase.co`) is IPv6-only, and Docker Desktop cannot reach it.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `container name "/postgres" is already in use` | Another container has that name | `docker rm -f postgres`, or rename in `docker-compose.yml` |
| `/api/*` returns 500 from `web` | `app` is not running or not healthy | `docker compose logs app` |
| `/readyz` shows a postgres error | Database not ready or wrong URL | `docker compose ps postgres`; check `SOCKABUS_POSTGRES_URL` |
| Web still shows old UI | Image not rebuilt | `docker compose --profile app up -d --build web` |
| Port already allocated | Something on the host uses 5432, 6379, 8080 or 3000 | Stop it, or change the left side of `ports:` |
| `required variable SOCKABUS_DOMAIN is missing` | Prod overlay without a domain | Set `SOCKABUS_DOMAIN` in `.env` |
| Caddy logs certificate errors | DNS does not point at the server yet, or port 80 is blocked | Fix the A record or firewall; Caddy retries on its own |
| Telegram `permission denied` on session files | Session files owned by your user, not uid 65532 | `sudo chown -R 65532:65532 .secrets/telegram` |
