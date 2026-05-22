# wg-admin app — handoff

Сводка того, что сделано, чтобы другой инстанс / другой сеанс мог сразу продолжить. Дополняет, не заменяет `../docs/frontend-spec.md`.

Последнее состояние: **2026-04-17, скелет + тесты**. Всё работает на мок-агенте end-to-end.

## TL;DR

- `wg-admin/app/` — Node/Express + Vue 3 + Vite 8 SPA. Заменяет `wg-admin/legacy/app/`.
- 8 этапов плана из `frontend-spec.md` закрыты (скелет, auth, proxy, headers, Overview, Interface, Plans, Audit + systemd).
- `npm run lint` чистый, `npm test` 31 зелёный, `npm audit` 0 уязвимостей, SPA ~155KB gzip (< 300KB лимит).
- Через мок-агент проверено: логин → создание пира → список интерфейсов. Всё через vite dev-proxy тоже.

## Что лежит в репе

```
app/
├── server/                     Node/Express (prod-entry и dev-entry одно и то же)
│   ├── index.ts                mount points: /health /auth/* /api/*
│   ├── config.ts               /etc/wg-admin/app.conf loader, scrypt hashPassword
│   ├── auth.ts                 HMAC cookie (node:crypto), requireAuth, requireSameOrigin (CSRF), login/logout/whoami
│   ├── proxy.ts                /api/* → unix socket, X-Actor из сессии, cookie стрипается
│   ├── headers.ts              CSP, X-Frame-Options, HSTS (prod)
│   ├── hash-cli.ts             `npx tsx server/hash-cli.ts <pw>` → scrypt line для config
│   ├── auth.test.ts            21 тест (round-trip, tamper, CSRF, cookie flags)
│   └── proxy.test.ts           8 тестов (forward, X-Actor, cookie strip, 502, content-type)
├── src/                        Vue SPA
│   ├── main.ts                 entrypoint
│   ├── App.vue                 layout, header nav, footer с kernel_mode (mock/real цветом)
│   ├── router.ts               vue-router, auth-guard через /auth/whoami
│   ├── style.css               Tailwind v4 через @import
│   ├── api/
│   │   ├── types.ts            ручное зеркало agent/internal/model/model.go + plan/types.go
│   │   ├── client.ts           типизированные fetch-обёртки, ApiError
│   │   └── format.ts           bytes, relativeTime, isHandshakeLive
│   ├── pages/
│   │   ├── Login.vue           форма + редирект на ?next
│   │   ├── Overview.vue        карточки интерфейсов со статус-индикатором (live handshake)
│   │   ├── Interface.vue       шапка + peer list + модалки add/edit/conf/delete; role=mesh → read-only
│   │   ├── Plans.vue           таблица планов с PlanStateBadge
│   │   ├── PlanDetail.vue      diff/snapshot/desired, countdown, apply/confirm/revert, pending→applied→terminal
│   │   └── Audit.vue           список с группировкой по дню
│   └── components/
│       ├── Modal.vue           базовая модалка с overlay/close
│       ├── AddPeerModal.vue    name+address+exit dropdown+notes
│       ├── PeerConfigModal.vue QR + .conf текст + копи + download
│       ├── EditPeerModal.vue   name/notes/enabled
│       ├── PeerRow.vue         строка пира с trafficом, кнопки conf/edit/delete (скрыты на mesh)
│       ├── PlanStateBadge.vue  цветная плашка статуса
│       ├── PlanDiff.vue        рендер ipsets/routes/rules/nft
│       └── Countdown.vue       live-отсчёт, красным при <10s, анимация pulse
├── systemd/wg-admin.service    prod-юнит с ProtectSystem/NoNewPrivileges
├── index.html                  shell с dark bg, referrer=no-referrer
├── vite.config.ts              плагин vue + tailwind, proxy /api+/auth → :3000
├── vitest.config.ts            test runner config
├── tsconfig.json               корневой, references
├── tsconfig.web.json           strict + DOM, для Vue
├── tsconfig.server.json        strict + Node, outDir=dist/server
├── dev-app.conf.example        шаблон dev-конфига
├── .gitignore
├── package.json                deps: express, vue, vue-router, qrcode; dev: vite, tailwind, tsx, vitest, vue-tsc
├── README.md                   dev + build + deploy
└── HANDOFF.md                  этот файл
```

## Критические решения и отличия от спеки

1. **scrypt, не bcrypt.** Спека `frontend-spec.md` просит bcrypt, но мы используем `node:crypto` scrypt. Причина: stdlib, ноль транзитивных зависимостей (bcrypt — либо native с gyp, либо bcryptjs — дополнительный риск). Формат: `scrypt:<saltHex>:<hashHex>`. Задокументировано в `README.md`.

2. **Сессии в HMAC-cookie, а не JWT.** Как спека и просит. `node:crypto` HMAC-SHA256. Формат: `base64url(JSON).base64url(sig)`. TTL 7 дней. Никакого `jsonwebtoken`.

3. **CSRF = SameSite=Strict + Origin check.** Проверяем `Origin` (и как fallback `Referer`) на всех методах кроме GET/HEAD/OPTIONS. Без второго токена.

4. **Auth cookie стрипается от прокси.** Cookie только для app-сервера, агент её не видит — он получает `X-Actor: <username>` для audit_log.

5. **Vite 8 — биндит IPv6 по умолчанию.** При ручном тестировании надо `http://localhost:5173/`, а не `http://127.0.0.1:5173/`. В проде express слушает IPv4 из `listen_host`, поэтому неактуально.

6. **Два процесса в dev (vite + express), один в prod.** `npm run dev` = `concurrently` на оба. В проде express сам раздаёт `dist/web/` + SPA fallback.

## Контракт с агентом

- Сокет: `/run/wg-agent.sock` (dev: `/tmp/wg-agent.sock`)
- Пути `/api/<anything>` → agent `/<anything>` (snape `/api` префикс). Никакой трансформации тел.
- `X-Actor: <user>` добавляется из сессии — агент кладёт в `audit_log.actor`.
- Таблица путей — в `docs/frontend-spec.md` "API мэппинг".

Типы в `src/api/types.ts` — ручное зеркало Go-моделей (`agent/internal/model/model.go`, `agent/internal/plan/types.go`, `agent/internal/kernel/kernel.go`). **При изменении Go-моделей — синхронизация руками.** Автоген в TODO.

## Как это запустить (dev)

```sh
# 1. Агент в мок-режиме
cd agent
go run ./cmd/wg-agent daemon -mock -socket /tmp/wg-agent.sock -db /tmp/wg-admin-state.db

# 2. App dev (два терминала либо один npm run dev)
cd app
cp dev-app.conf.example dev-app.conf
npx tsx server/hash-cli.ts dev123
# ...вставить scrypt:... в dev-app.conf.password_hash (поле уже есть)
npm ci
npm run dev                              # concurrently vite:5173 + express:3000

# Открыть http://localhost:5173/
# Логин: admin / dev123
```

## Тесты

```sh
npm test          # vitest, 31 тест
npm run lint      # vue-tsc + tsc --noEmit
npm run build     # vue-tsc + vite build + tsc сервера
```

Покрытие тестами:

**`server/auth.test.ts` (21):**
- HMAC cookie round-trip, отказ при undefined / malformed / tampered body / tampered sig / cross-secret / expired
- `loginHandler` — missing body (400), no Origin (403), cross-origin (403), wrong user (401), wrong password (401), valid → cookie с `HttpOnly; SameSite=Strict`, dev без `Secure`, prod с `Secure`
- `requireAuth` — 401 без cookie, 401 на bogus, 200 и `req.session.sub` на валидной
- `requireSameOrigin` — GET ok, POST без Origin → 403, cross-origin → 403
- `logoutHandler` — `Max-Age=0`
- `whoamiHandler` — 401 без сессии, `{user, exp}` с

**`server/proxy.test.ts` (8):**
- GET с query и `/api` префикс снимается
- POST body пробрасывается целиком (важно: **не ставить `express.json()` перед прокси** — сжирает стрим)
- `X-Actor` инжектится / отсутствует без сессии
- `cookie` header не доходит до агента
- Статус/тело агента возвращаются
- `Content-Type: text/plain` (для `.conf`) пробрасывается
- Мёртвый сокет → 502 `agent unreachable`

Тестов на Vue-компоненты пока нет — smoke через Playwright планируется отдельным шагом (см. ниже).

## Что НЕ сделано

Из `frontend-spec.md` "Не-цели MVP" (осознанно отложено):
- Multi-agent UI
- Dashboards / графики (это Grafana)
- Редактор `exits` / `marks` / `routing_rules` (пока CLI/SQL)
- DNS-интеграция (AdGuard)

Из явных TODO:
- **Playwright smoke** — один сценарий `login → create peer → download conf`. Спека этого прямо просит.
- **Автоген TS типов из Go моделей** — сейчас `src/api/types.ts` синхронизируется руками. Варианты: openapi, protobuf, самописный конвертер в `agent/cmd/gen-types/`.
- **CI config** — ничего не настроено. Спека просит блокаторов сборки: eslint, tsc, `npm audit ≥ moderate`, bundle-size > 300KB warning.
- **e2e на реальном агенте** — проверено только с мок-агентом, с реальным ядром (когда `kernel/real.go` появится) нужно перепроверить.

Из UX-полировки (не в спеке, но очевидные):
- Toast-уведомления вместо inline `error.value`
- Копи-кнопка pubkey в Interface
- Поиск/фильтры в `/audit`
- Обработка expired сессии — сейчас router-guard редиректит на login только при навигации, не при onMount-запросах (пагода, на практике получит 401 → показать ошибку — ок, но можно глобально перехватывать `ApiError.status === 401` и редиректить).

## Gotchas / на что наступать

1. **`express.json()` перед `/api` прокси сломает POST.** Body-parser сжирает стрим, до прокси доходит пустой. В `server/index.ts` этого нет, в тестах тоже — если будет пересборка, не добавлять глобально.
2. **Vite 8 на IPv6.** См. выше.
3. **agent `-mock` не создаёт plan engine** (по состоянию на 2026-04-17). `POST /plan` вернёт 503 — UI покажет ошибку, это ожидаемо.
4. **Права на сокет.** В проде сокет под `wg-admin` группой (см. `wg-admin.service` в agent-репе). App запускается `User=www-data Group=wg-admin`.
5. **Origin check в dev под vite.** Vite прокидывает `Origin: http://localhost:5173`, express получает `req.headers.host = 127.0.0.1:3000` (т.к. vite proxy с `changeOrigin: false` оставляет Host оригинальный → `localhost:5173`). На практике совпадение есть. Если логин начинает 403-иться — проверить, что vite и express живут на одном хосте.
6. **Сессионный секрет** автогенерируется при первом старте в `/etc/wg-admin/session.secret` (или `dev-session.secret` в dev). Удаление файла инвалидирует все сессии.

## Куда двигаться дальше (приоритизировано)

1. **Playwright smoke** (`login → create peer → download conf`). Один сценарий. Ставится `@playwright/test` в devDep. Скрипт в `e2e/login-create.spec.ts`. CI-хук: `npm run e2e` после `npm run build` + запуск прод-сервера в фоне.
2. **Обработка 401 глобально** в `src/api/client.ts` — на 401 редиректить на `/login?next=...` вместо показа ошибки. Мелкая правка, заметно улучшает UX.
3. **CI** (github actions / local hook): `npm ci && npm run lint && npm test && npm audit --audit-level=moderate && npm run build`. Добавить check bundle-size из Vite output.
4. **Автоген типов** — когда модели агента перестанут меняться каждый день. Вариант с openapi: описать HTTP API в `agent/openapi.yaml`, генерить обеими сторонами.
5. **Реальный деплой** (live host) после stand-up `kernel/real.go` в агенте. Проверит Origin-check за Traefik (Traefik добавит `X-Forwarded-Host`, но `Origin` оставит от клиента).

## Связанная документация

- `../docs/frontend-spec.md` — исходное ТЗ на фронт (300 строк)
- `../docs/project.md` — архитектура wg-admin v2
- `../docs/kernel-spec.md` — контракт ядра (не нужен фронту напрямую)
- `../docs/TODO.md` — отложенные решения агента
- `../agent/README.md` — как запустить агент
- `../agent/internal/model/model.go` — Go-модели, mirror в `src/api/types.ts`
- `../agent/internal/api/*.go` — agent handlers, которые мы проксируем

## История работы

- 2026-04-17, сессия 1: весь скелет + 8 страниц + systemd + README. Билд, лайнт, e2e против мок-агента OK.
- 2026-04-17, сессия 2: vitest, unit-тесты на `server/auth.ts` (21) и `server/proxy.ts` (8). 31 зелёный.
