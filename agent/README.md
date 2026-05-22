# wg-agent

Host-side agent for wg-admin v2. Owns SQLite at `/var/lib/wg-admin/state.db`,
exposes HTTP API over a unix socket (`/run/wg-agent.sock`).

## Status

Skeleton only. Mock kernel mode works; real kernel mode (`wg`/`ip`/`ipset`/`nft`)
is stubbed.

## Quick start (dev, mock)

```sh
cd agent
go mod tidy
go run ./cmd/wg-agent daemon -mock \
  -socket /tmp/wg-agent.sock \
  -db /tmp/wg-admin-state.db

# in another terminal
curl --unix-socket /tmp/wg-agent.sock http://unix/status
curl --unix-socket /tmp/wg-agent.sock http://unix/interfaces
curl --unix-socket /tmp/wg-agent.sock http://unix/interfaces/wg0
```

## Layout

```
cmd/wg-agent/      -- entrypoint, subcommand dispatch
internal/
  api/             -- HTTP handlers
  server/          -- unix socket listener
  store/           -- SQLite + migrations
  model/           -- record types
  kernel/          -- kernel abstraction + mock impl
systemd/           -- wg-agent.service
```
