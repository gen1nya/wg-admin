import http from 'node:http';
import type { RequestHandler } from 'express';
import type { AppConfig } from './config.js';

// unixProxy strips `/api` from the URL and forwards everything else to the
// agent over the unix socket. The agent's routes are `/interfaces`, `/peers`,
// etc. — no /api prefix. X-Actor header carries the logged-in user for audit.
export function unixProxy(cfg: AppConfig): RequestHandler {
  return (req, res) => {
    // req.url here is relative to the mount point /api (express strips the mount).
    // We preserve method, query string, headers (minus hop-by-hop), body.
    const targetPath = req.url || '/';

    const headers: Record<string, string | string[]> = {};
    for (const [k, v] of Object.entries(req.headers)) {
      if (v === undefined) continue;
      const lk = k.toLowerCase();
      if (HOP_BY_HOP.has(lk)) continue;
      if (lk === 'cookie') continue; // cookie is ours, not agent's business
      headers[k] = v as string | string[];
    }
    headers['host'] = 'unix';
    if (req.session?.sub) headers['x-actor'] = req.session.sub;

    const upstream = http.request(
      {
        socketPath: cfg.socket,
        method: req.method,
        path: targetPath,
        headers,
      },
      (up) => {
        res.status(up.statusCode ?? 502);
        for (const [k, v] of Object.entries(up.headers)) {
          if (v === undefined) continue;
          if (HOP_BY_HOP.has(k.toLowerCase())) continue;
          res.setHeader(k, v as string | string[]);
        }
        up.pipe(res);
      },
    );
    upstream.on('error', (err) => {
      if (res.headersSent) {
        res.end();
        return;
      }
      res.status(502).json({ error: `agent unreachable: ${err.message}` });
    });
    req.pipe(upstream);
  };
}

const HOP_BY_HOP = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
]);
