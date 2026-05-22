# wg-admin-app

Web UI для `wg-agent`. Node/Express фронтит unix-сокет, Vue 3 SPA рендерит
интерфейс. См. `../docs/frontend-spec.md`.

## Зависимости

- Node.js ≥ 22 LTS
- Запущенный `wg-agent` (unix socket)

## Dev

Запустить агента в мок-режиме на `/tmp/wg-agent.sock`:

```sh
cd ../agent
go run ./cmd/wg-agent daemon -mock -socket /tmp/wg-agent.sock -db /tmp/wg-admin-state.db
```

В `app/`:

```sh
cp dev-app.conf.example dev-app.conf
npm run hash -- secret123          # подставить output в dev-app.conf.password_hash
npm ci
npm run dev                        # vite (5173) + express (3000) параллельно
```

Открыть http://localhost:5173. Vite проксит `/api/*` и `/auth/*` на `http://127.0.0.1:3000`.

Логин: `admin` / `secret123` (или что задали в `hash`).

## Build + prod

```sh
npm ci
npm run build                      # dist/web/ — SPA; dist/server/ — Node entry
NODE_ENV=production node dist/server/index.js
```

### Конфиг (`/etc/wg-admin/app.conf`)

```json
{
  "user": "admin",
  "password_hash": "scrypt:<salt_hex>:<hash_hex>",
  "socket": "/run/wg-agent.sock",
  "listen_host": "10.99.0.5",
  "listen_port": 8080
}
```

Сгенерировать хеш:

```sh
node dist/server/hash-cli.js 'password'
```

Сессионный секрет (`/etc/wg-admin/session.secret`) автогенерируется при первом старте (32 байта, 0600).

## Безопасность

- App слушает только на WG-mesh IP, не на 0.0.0.0 (задать `listen_host`)
- Traefik фронтит TLS, публичного DNS на поддомен нет
- Auth: HMAC-cookie (node:crypto scrypt), `HttpOnly; Secure; SameSite=Strict`
- CSRF: `SameSite=Strict` + проверка `Origin`/`Referer` на writes
- CSP, X-Frame-Options DENY, HSTS (в prod)
- Зависимости минимизированы: `express`, `vue`, `vue-router`, `qrcode` на рантайме — всё

**Про хеш:** в спеке указан bcrypt, но мы используем `node:crypto` scrypt —
stdlib, нулевая транзитивка, модернее. Формат: `scrypt:<saltHex>:<hashHex>`.

## Раскладка

```
app/
├── server/              Node/Express entry + auth + proxy + headers + config
│   ├── index.ts
│   ├── config.ts
│   ├── auth.ts
│   ├── proxy.ts
│   ├── headers.ts
│   └── hash-cli.ts      tsx server/hash-cli.ts <pw>
├── src/                 Vue SPA
│   ├── main.ts
│   ├── App.vue
│   ├── router.ts
│   ├── style.css
│   ├── api/             client, types (mirror of Go model), format helpers
│   ├── pages/           Login / Overview / Interface / Plans / PlanDetail / Audit
│   └── components/      PeerRow / *Modal / PlanDiff / PlanStateBadge / Countdown
├── systemd/wg-admin.service
├── index.html
├── vite.config.ts
├── tsconfig.json
└── package.json
```

## API мэппинг

Browser hits `/api/<anything>` → express strips `/api`, forwards to
`/run/wg-agent.sock` with `X-Actor: <user>`. См. `../docs/frontend-spec.md`
для полной таблицы путей.

## Деплой (набросок)

1. Скопировать репо → `/opt/wg-admin-app`
2. `npm ci --omit=dev && npm run build` (или собирать локально и раскатать `dist/`)
3. Создать user/group: `groupadd wg-admin; usermod -aG wg-admin www-data`
4. `chown :wg-admin /run/wg-agent.sock && chmod 0660 /run/wg-agent.sock`
   (либо через `SocketMode=0660 SocketGroup=wg-admin` в `wg-agent.service`)
5. Положить `app.conf`, сгенерить хеш
6. `cp systemd/wg-admin.service /etc/systemd/system/ && systemctl enable --now wg-admin`
7. Настроить Traefik на WG-mesh IP + TLS
