# syntax=docker/dockerfile:1

# ─── build stage ─────────────────────────────────────────────────────────────
FROM node:24-slim AS builder
WORKDIR /app

# Зависимости отдельным слоем — кэшируется пока lockfile не трогаем.
# npm install вместо npm ci: в npm 11 есть баг с optional peer deps
# (@emnapi/*), из-за которого `npm ci` падает на «rogue»-missing в свежем
# lockfile'е. `npm install --no-audit --no-fund` ставит то же самое, но не
# требует strict-sync.
COPY package.json package-lock.json ./
RUN npm install --no-audit --no-fund

# Исходники — всё остальное (см. .dockerignore, там node_modules/dist/*.conf/*.secret исключены).
COPY . .
RUN npm run build

# ─── runtime stage ───────────────────────────────────────────────────────────
FROM node:24-slim AS runtime
WORKDIR /app
ENV NODE_ENV=production

# Прод-зависимости без devDeps.
COPY package.json package-lock.json ./
RUN npm install --omit=dev --no-audit --no-fund && npm cache clean --force

# Собранные артефакты из builder.
COPY --from=builder /app/dist ./dist

# node user (uid=1000, gid=1000) уже есть в base-образе; конфиг/сокет
# читаются через supplementary group (group_add в docker-compose).
USER node

EXPOSE 3000
CMD ["node", "dist/server/index.js"]
