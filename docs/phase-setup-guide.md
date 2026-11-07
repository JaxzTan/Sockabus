# Phase Setup Guide — Sockabus Secrets

| Field | Value |
|---|---|
| Purpose | Store and share the Sockabus `.env` securely with Phase instead of Bitwarden / chat |
| Owner | Jaxz |
| Last updated | 1 Nov 2026 |
| Docs | [Phase Quickstart](https://docs.phase.dev/quickstart) · [CLI usage](https://docs.phase.dev/cli/usage) · [CLI commands](https://docs.phase.dev/cli/commands) |

> **Read this first if you're new.** Real secrets (LLM API key, database password, operator token) live in Phase, not in git, chat or Bitwarden. Only the people listed in §2 can see Production. Everyone else works with test values and never needs Production access.

---

## 1. Why Phase

- **Free for our team:** up to 5 users or service accounts, 3 apps, Development / Staging / Production environments.
- **Per-person, per-environment access:** Production is visible only to the people we allow.
- **Audit log:** shows who changed which secret and when.
- **No code changes:** the CLI injects secrets as normal environment variables at runtime.
- **No lock-in:** open source, can be self-hosted, and secrets export back to a `.env` at any time.
- **No size problem:** each `.env` line is stored as its own secret, so the Bitwarden ~700 / ~7,500 character limits don't apply.

---

## 2. Who has access

| Person | Phase role | Development | Production | Notes |
|---|---|---|---|---|
| Jaxz | Owner | ✅ | ✅ | Billing + full control |
| Gilbert | Admin | ✅ | ✅ | Backup admin so we're never locked out |
| Jayci, Andy, jianjia (only if needed) | Developer | ✅ | ❌ | Test values only |
| VPS | — | — | — | **Not in Phase.** Jaxz or Gilbert copies the `.env` manually (§6) |
| GitHub Actions (CI) | — | — | — | **Not in Phase.** Uses GitHub repository secrets with test values |

This uses 2 of the 5 free slots, leaving 3 for teammates.

**Rules**

- Owner and Admin automatically see **every** environment, including Production. Never make someone an Admin just to save time.
- Everyone else gets the **Developer** role and is added to Development only.
- Only Jaxz (Owner) and Gilbert (Admin) may change Production secrets.

---

## 3. Part A — Create the Phase app (Jaxz, once, ~10 min)

### A1. Sign up

1. Go to [phase.dev](https://phase.dev) and sign up for the Phase Console.
2. Create an organisation called `Sockabus`.
3. If Phase shows a recovery kit or recovery phrase, save it in the Owner's personal password manager. **Do not** put it in the team chat.
4. Turn on two-factor login in account settings if it is available.

### A2. Create the app

1. In the console, click **Create App**.
2. Name it `sockabus`.
3. Confirm it has three environments: **Development**, **Staging**, **Production**.

### A3. Import the Production secrets

1. Open the app → **Production** environment.
2. Drag and drop the real VPS `.env` file onto the page, or open **Import secrets** and paste the contents.
3. Review the preview, then **Deploy** / save.
4. Check the number of secrets matches the number of variables in the file.

### A4. Fill Development with safe values

Development is what teammates use locally. It must contain **no real production keys**.

> Variable names must match `.env.example` exactly (`SOCKABUS_*`), or the app won't find them. Quickest start: import `.env.example` into Development, then change the values below.

| Variable | Development value |
|---|---|
| `SOCKABUS_LLM_PROVIDER` | `fake` |
| `SOCKABUS_REPLY_DELAY_MIN` | `0s` |
| `SOCKABUS_OPERATOR_TOKEN` | any random test string |
| `SOCKABUS_MESSAGE_KEY` | a test key (not the production one) |
| `SOCKABUS_POSTGRES_URL` | `postgres://buru:buru@localhost:5432/buru?sslmode=disable` |
| `SOCKABUS_CONSOLE_USER` / `_PASSWORD` | `dev` / `dev` |
| `DATABASE_URL` (Supabase owner) | leave empty |
| `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | leave empty, or use a separate low-limit test key |

Tip: Phase can import one `.env` into several environments at once. Use that to create the same **keys** in every environment, then change the values.

### A5. Invite the team

1. Go to **Access → Members** → **Invite**.
2. Invite Gilbert with role **Admin**.
3. Invite other teammates only if they need it, with role **Developer**.
4. For each Developer: open the app → environment settings (⚙️) → **Manage access** → add them to **Development only**.

---

## 4. Part B — Set up your laptop (everyone with access, ~5 min)

### B1. Install the CLI

macOS (Homebrew):

```bash
brew install phasehq/cli/phase
```

macOS / Linux / WSL (script):

```bash
curl -fsSL https://pkg.phase.dev/install.sh | bash
```

Check it installed:

```bash
phase --version
```

### B2. Log in

```bash
phase auth
```

- Choose **Phase Cloud**.
- Finish the login in the browser that opens.
- You should see `Authentication successful`.

### B3. Link the repo to the app

```bash
cd path/to/sockabus
phase init
```

- Select app **sockabus**.
- Select default environment **Development**.

This creates `.phase.json` in the repo root. It stores the app and environment choice, **not secrets**, and is committed to git (see §7).

### B4. Check it works

```bash
phase secrets list            # names only
phase secrets list --show     # names + values (only on your own screen)
```

---

## 5. Part C — Run the app with Phase

### C1. Run Go directly

```bash
make up                              # Postgres, Redis, Temporal in Docker
phase run 'go run ./cmd/sockabus'
```

Secrets are injected as environment variables for that process only. Nothing is written to disk.

### C2. Run with `make run` (our normal setup)

The Makefile reads `.env` (`-include .env`), so export one first:

```bash
phase secrets export --env Development > .env
make run
```

> Keep `.env` while you use `make`; every target re-reads it. Export again after a secret changes in Phase.

### C3. Use a different environment

```bash
phase run --env Production 'printenv | grep SOCKABUS_LLM_PROVIDER'
```

Only Jaxz and Gilbert can do this for Production.

---

## 6. Part D — Deploy secrets to the VPS

We copy the `.env` to the VPS **manually**. The VPS has no Phase account and uses no slot.

Run on Jaxz's or Gilbert's laptop **whenever a Production secret changes**:

```bash
# 1. Export Production secrets to a temporary file
phase secrets export --env Production > prod.env

# 2. Copy to the VPS
scp prod.env <user>@<vps-host>:/opt/sockabus/.env

# 3. Lock the file and redeploy
ssh <user>@<vps-host> "chmod 600 /opt/sockabus/.env && cd /opt/sockabus && make deploy"

# 4. Delete the local copy
rm prod.env
```

Replace `<user>`, `<vps-host>` and `/opt/sockabus` with our real values.

`make deploy` is unchanged (TRD TR-23): `git pull` → `docker compose build` → migrations → `docker compose up -d`.

> Later, if we deploy often, the VPS can fetch secrets itself through a Phase service account. That uses 1 free slot. Not needed now.

---

## 7. Repo changes (do once)

### `.gitignore`

Make sure these lines exist:

```gitignore
.env
prod.env
secrets.env
*.env
!.env.example
```

### `.env.example`

Keep **every** variable name here with no real values. When you add a secret in Phase, add its name here in the same pull request.

```bash
SOCKABUS_LLM_PROVIDER=fake
SOCKABUS_OPERATOR_TOKEN=
SOCKABUS_MESSAGE_KEY=
DATABASE_URL=
# ...
```

### `.phase.json`

Commit it so everyone links to the same app. It contains no secrets.

### Docs to update

- **Blueprint §10 (Secrets):** "Production secrets live in Phase. Only Jaxz and Gilbert have access."
- **Blueprint §12 Day-1 onboarding:** add "If you need real secrets, ask Jaxz for Phase access; otherwise use `.env.example` with `SOCKABUS_LLM_PROVIDER=fake`."

---

## 8. Clean-up checklist (do right after setup)

- [ ] Rotate every key that was ever pasted into WhatsApp, Telegram, email or Bitwarden. At minimum:
  - [ ] LLM API key (Gemini / Anthropic)
  - [ ] `SOCKABUS_OPERATOR_TOKEN`
  - [ ] `SOCKABUS_MESSAGE_KEY` (re-encrypt or plan for existing messages first — check with Gilbert)
  - [ ] Telegram `api_id` / `api_hash` session if shared
- [ ] Update the rotated values in Phase **Production**, then redeploy the VPS (§6).
- [ ] Delete old plaintext `.env` files from laptops, Downloads folders and chat history.
- [ ] Delete the old Bitwarden notes / Sends that held the `.env`.
- [ ] Confirm `.gitignore` (§7) and run `git status` to make sure no `.env` is tracked.

---

## 9. Day-to-day rules

1. **Change secrets in Phase first, then export to the VPS.** Never edit the VPS `.env` directly, or Phase and the server drift apart.
2. **Never paste a secret** in chat, issues, pull requests or screenshots.
3. **New variable?** Add it in Phase (every environment) **and** its name in `.env.example`.
4. **Someone leaves or no longer needs access:** remove them in **Access → Members**, then rotate any Production key they could see.
5. **Free plan limit:** 5 users + service accounts in total. Ask Jaxz before inviting anyone.

---

## 10. Troubleshooting

| Problem | Fix |
|---|---|
| `phase: command not found` | Re-open the terminal, or reinstall with the script in §B1 |
| Login stuck in WSL / SSH (no browser) | Use a personal access token from the console, or run `phase auth` on a machine with a browser |
| `phase secrets list` shows nothing | Run `phase init` again in the repo root and pick `sockabus`; check you have access to that environment |
| Can't see Production | Expected unless you're Jaxz or Gilbert |
| App can't find a variable | Check the name in Phase matches `.env.example` exactly (case-sensitive) |
| Docker containers use old values | Export again (§C2 / §6), then `docker compose up -d` to recreate containers |
| Secret count after import is wrong | Check the `.env` for lines without `=` or multi-line values; base64-encode long values into one line |

---

## 11. If we outgrow the free plan

- **Upgrade to Phase Pro** ($10/user/month): billing change only.
- **Self-host Phase** on our own server: free, a few hours of setup.
- **Move to another tool** (Infisical, SOPS + age, dotenvx): `phase secrets export --env <env> > .env`, import into the new tool, change the run/deploy commands. App code stays the same.

---

## Sources

- [Phase Quickstart](https://docs.phase.dev/quickstart)
- [Phase CLI usage](https://docs.phase.dev/cli/usage)
- [Phase CLI commands](https://docs.phase.dev/cli/commands)
- [Phase Console: importing secrets](https://docs.phase.dev/console/secrets)
- [Phase Console: environments and access](https://docs.phase.dev/console/environments)
- [Phase access control](https://docs.phase.dev/access-control)
- [Phase + Docker Compose](https://docs.phase.dev/integrations/platforms/docker-compose)
- [Phase pricing (via Toolradar)](https://toolradar.com/tools/phase/pricing)
