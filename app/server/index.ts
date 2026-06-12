import express from 'express';
import fs from 'node:fs';
import path from 'node:path';
import { loadConfig } from './config.js';
import { securityHeaders } from './headers.js';
import {
  loginHandler,
  logoutHandler,
  requireAuth,
  requireSameOrigin,
  whoamiHandler,
} from './auth.js';
import { unixProxy } from './proxy.js';

const cfg = loadConfig();
const app = express();
app.disable('x-powered-by');
app.use(securityHeaders(cfg.dev));

// Unauthenticated liveness check. Keep it contentless — no socket path or
// dev flag — so an anonymous probe learns nothing about the deployment.
app.get('/health', (_req, res) => {
  res.json({ ok: true });
});

app.post('/auth/login', express.json({ limit: '4kb' }), requireSameOrigin(cfg), loginHandler(cfg));
app.post('/auth/logout', requireSameOrigin(cfg), logoutHandler(cfg));
app.get('/auth/whoami', whoamiHandler(cfg));

// /api/* → unix socket. Auth + CSRF for all methods.
app.use('/api', requireAuth(cfg), requireSameOrigin(cfg), unixProxy(cfg));

// Static assets (prod). In dev, vite serves SPA on its own port and proxies /api here.
if (!cfg.dev && fs.existsSync(cfg.staticDir)) {
  app.use(express.static(cfg.staticDir, { index: false, maxAge: '1h' }));
  // SPA fallback — serve index.html for anything else.
  app.get(/.*/, (_req, res) => {
    res.sendFile(path.join(cfg.staticDir, 'index.html'));
  });
}

app.listen(cfg.listenPort, cfg.listenHost, () => {
  console.log(
    `[wg-admin] listening on ${cfg.listenHost}:${cfg.listenPort}, agent=${cfg.socket}, dev=${cfg.dev}`,
  );
});
