# wg-admin

Self-hostable web UI and host-side agent for managing native WireGuard
interfaces and their routing. Built to replace `wg-easy` plus a pile of
hand-written split-routing shell scripts with one declarative system.

## Goals

1. Manage peers from the web UI and CLI (create, delete, download `.conf`).
2. Model "where traffic goes" as a first-class entity (`exit`), so clients
   can be switched between exits declaratively (direct / EU tunnel / etc).
3. Keep the source of truth in **SQLite on the agent** — WireGuard configs,
   `iptables`/`nft` rules, and `ipset` entries are derived artifacts.
4. Make kernel changes safe via a two-phase **`plan → apply (+watchdog) →
   confirm`** flow: a misapplied change is automatically rolled back if you
   don't confirm it in time.
5. Drop the `wg-easy` Docker overhead and reduce script entropy.

## Architecture

```
   browser / CLI
        │
        ▼
   ┌─────────────────────┐
   │   wg-admin app      │  Node + Vue 3 SPA, stateless
   │   (HTTPS, auth)     │
   └──────────┬──────────┘
              │ unix socket /run/wg-agent.sock (X-Actor from session)
              ▼
   ┌─────────────────────┐
   │   wg-agent          │  Go daemon, runs on the host
   │   (SQLite + kernel) │  owns wg / ip / ipset / nft
   └──────────┬──────────┘
              ▼
        kernel state
```

- **`app/`** — Node 22 + Express + Vue 3 + Vite SPA. Pure frontend; no
  business logic. Proxies `/api/*` to the agent's unix socket and attaches
  the authenticated user as `X-Actor` so the agent can audit per-user.
- **`agent/`** — Go daemon. Owns `/var/lib/wg-admin/state.db` (SQLite) and
  exposes an HTTP API over `unix:/run/wg-agent.sock`. Has a mock kernel
  mode for local development without root.

Per-host state: each agent has its own local `state.db`. Mesh links
between hosts have to be registered on **both** endpoints (each agent
keeps its own peer database).

## Status

Skeleton, working end-to-end against the mock kernel. Real-kernel mode
(`wg`/`ip`/`ipset`/`nft` actually applied) is being stood up.

- Frontend: all 8 spec pages closed, `npm run lint` clean, `npm test` 31
  green, `npm audit` 0 vulnerabilities, SPA ~155 KB gzipped.
- Agent: HTTP API, store, plan/apply/confirm engine, importer for
  existing `/etc/wireguard` backups, renderer for client `.conf` output.

## Quick start (dev, mock kernel)

```sh
# Terminal 1: agent
cd agent
go mod tidy
go run ./cmd/wg-agent daemon -mock \
  -socket /tmp/wg-agent.sock \
  -db     /tmp/wg-admin-state.db

# Terminal 2: app
cd app
cp dev-app.conf.example dev-app.conf
# generate password hash and put it into dev-app.conf
npx tsx server/hash-cli.ts 'your-dev-password'
npm install
npm run dev
# open http://127.0.0.1:5173/, login with admin / your-dev-password
```

In another terminal you can also hit the agent directly:

```sh
curl --unix-socket /tmp/wg-agent.sock http://unix/status
curl --unix-socket /tmp/wg-agent.sock http://unix/interfaces
```

## Running tests

```sh
# Go agent
cd agent && go test ./...

# Node app (unit tests)
cd app && npm test

# Importer end-to-end against a real /etc/wireguard backup
# (skipped if env var is not set; set it to point at the dir)
export WG_ADMIN_IMPORTER_FIXTURE=/path/to/etc-wireguard-backup
cd agent && go test ./internal/importer/...
```

## Layout

```
agent/
  cmd/wg-agent/     entrypoint and subcommand dispatch
  internal/
    api/            HTTP handlers
    auth/           token auth, X-Actor extraction
    devseed/        dev-mode DB seed (mock interfaces and exits)
    importer/       /etc/wireguard backup → DB
    kernel/         kernel abstraction (real + mock impls)
    model/          record types
    plan/           plan/apply/confirm engine
    reconcile/      post-boot heal: kernel ← DB
    renderer/       DB row → client .conf text
    server/         unix socket listener
    store/          SQLite + migrations
    wgconf/         WireGuard config file parsing
    wgkey/          curve25519 keypair generation
  systemd/          wg-agent.service unit
app/
  server/           Node/Express: auth, /api proxy, hash CLI
  src/              Vue 3 SPA (pages, components, API client)
  systemd/          wg-admin-app.service unit
```

## Design notes

- **API first, never direct SQL.** Peers, interfaces, and plans go through
  the HTTP API only (`POST /interfaces/{name}/peers`, `GET /peers/{id}/config`,
  the `/plan` flow). Mutating the SQLite file by hand desynchronizes DB ↔
  kernel ↔ on-disk WireGuard configs.
- **Plan → apply → confirm.** Any state change that touches the kernel
  produces a plan diff (`ipsets`/`routes`/`rules`/`nft`). Apply sets a
  watchdog timer; if you don't `POST /plans/{id}/confirm` within the
  timeout, the change is auto-reverted. This prevents one-click outages.
- **Per-host state, no central DB.** Each agent owns its own `state.db`.
  Mesh links must be registered on both ends. There is intentionally no
  master / cluster.

## License

MIT — see `LICENSE`.
