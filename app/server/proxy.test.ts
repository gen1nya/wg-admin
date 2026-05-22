import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import express from 'express';
import type { AddressInfo } from 'node:net';
import { unixProxy } from './proxy.js';
import type { AppConfig } from './config.js';

function mkTmpSocket(): string {
  return path.join(os.tmpdir(), `wg-admin-test-${crypto.randomBytes(6).toString('hex')}.sock`);
}

interface CapturedRequest {
  method?: string;
  url?: string;
  headers: http.IncomingHttpHeaders;
  body: string;
}

async function startFakeAgent(
  socketPath: string,
  handler: (req: CapturedRequest) => { status: number; body: string | Buffer; headers?: Record<string, string> },
): Promise<http.Server> {
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => {
      const cap: CapturedRequest = {
        method: req.method,
        url: req.url,
        headers: req.headers,
        body: Buffer.concat(chunks).toString('utf8'),
      };
      const r = handler(cap);
      res.statusCode = r.status;
      if (r.headers) for (const [k, v] of Object.entries(r.headers)) res.setHeader(k, v);
      if (!res.hasHeader('content-type')) res.setHeader('content-type', 'application/json');
      res.end(r.body);
    });
  });
  await new Promise<void>((resolve) => server.listen(socketPath, resolve));
  return server;
}

function makeCfg(socket: string): AppConfig {
  return {
    user: 'admin',
    passwordHash: '',
    socket,
    listenHost: '127.0.0.1',
    listenPort: 0,
    sessionSecret: crypto.randomBytes(32),
    staticDir: '',
    dev: true,
  };
}

interface ProxyResult {
  status: number;
  body: string;
  headers: http.IncomingHttpHeaders;
}

async function runReq(opts: {
  socket: string;
  fakeAgent: http.Server;
  method: string;
  pathname: string;
  headers?: Record<string, string>;
  body?: string;
  session?: string; // sub — auto-attaches req.session
}): Promise<ProxyResult> {
  const cfg = makeCfg(opts.socket);
  const app = express();
  // Fake session middleware — the real one is requireAuth; here we inject directly.
  if (opts.session) {
    app.use((req, _res, next) => {
      req.session = {
        sub: opts.session!,
        iat: Math.floor(Date.now() / 1000),
        exp: Math.floor(Date.now() / 1000) + 3600,
      };
      next();
    });
  }
  app.use('/api', unixProxy(cfg));

  return new Promise((resolve, reject) => {
    const server = app.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as AddressInfo;
      const req = http.request(
        {
          host: '127.0.0.1',
          port,
          method: opts.method,
          path: '/api' + opts.pathname,
          headers: opts.headers ?? {},
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => {
            server.close();
            resolve({
              status: res.statusCode ?? 0,
              body: Buffer.concat(chunks).toString('utf8'),
              headers: res.headers,
            });
          });
        },
      );
      req.on('error', (e) => {
        server.close();
        reject(e);
      });
      if (opts.body) req.write(opts.body);
      req.end();
    });
  });
}

describe('unixProxy', () => {
  let socket: string;
  let agent: http.Server | null = null;

  beforeEach(() => {
    socket = mkTmpSocket();
  });

  afterEach(() => {
    agent?.close();
    agent = null;
    if (fs.existsSync(socket)) fs.unlinkSync(socket);
  });

  it('forwards GET + strips /api prefix', async () => {
    let captured: CapturedRequest | null = null;
    agent = await startFakeAgent(socket, (req) => {
      captured = req;
      return { status: 200, body: '{"ok":true}' };
    });
    const r = await runReq({
      socket,
      fakeAgent: agent,
      method: 'GET',
      pathname: '/interfaces?x=1',
    });
    expect(r.status).toBe(200);
    expect(r.body).toBe('{"ok":true}');
    expect(captured).not.toBeNull();
    expect(captured!.url).toBe('/interfaces?x=1');
    expect(captured!.method).toBe('GET');
  });

  it('forwards POST body', async () => {
    let captured: CapturedRequest | null = null;
    agent = await startFakeAgent(socket, (req) => {
      captured = req;
      return { status: 201, body: '{"id":1}' };
    });
    const r = await runReq({
      socket,
      fakeAgent: agent,
      method: 'POST',
      pathname: '/interfaces/wg0/peers',
      headers: { 'content-type': 'application/json' },
      body: '{"name":"foo"}',
    });
    expect(r.status).toBe(201);
    expect(captured!.body).toBe('{"name":"foo"}');
  });

  it('injects X-Actor from session', async () => {
    let captured: CapturedRequest | null = null;
    agent = await startFakeAgent(socket, (req) => {
      captured = req;
      return { status: 200, body: '[]' };
    });
    await runReq({
      socket,
      fakeAgent: agent,
      method: 'GET',
      pathname: '/audit',
      session: 'alice',
    });
    expect(captured!.headers['x-actor']).toBe('alice');
  });

  it('omits X-Actor when no session (fallback to agent default)', async () => {
    let captured: CapturedRequest | null = null;
    agent = await startFakeAgent(socket, (req) => {
      captured = req;
      return { status: 200, body: '[]' };
    });
    await runReq({ socket, fakeAgent: agent, method: 'GET', pathname: '/audit' });
    expect(captured!.headers['x-actor']).toBeUndefined();
  });

  it('strips cookie header from upstream', async () => {
    let captured: CapturedRequest | null = null;
    agent = await startFakeAgent(socket, (req) => {
      captured = req;
      return { status: 200, body: '[]' };
    });
    await runReq({
      socket,
      fakeAgent: agent,
      method: 'GET',
      pathname: '/status',
      headers: { cookie: 'wg_session=leaky' },
    });
    expect(captured!.headers.cookie).toBeUndefined();
  });

  it('passes through agent status code and body', async () => {
    agent = await startFakeAgent(socket, () => ({
      status: 404,
      body: '{"error":"not found"}',
    }));
    const r = await runReq({
      socket,
      fakeAgent: agent,
      method: 'GET',
      pathname: '/peers/999',
    });
    expect(r.status).toBe(404);
    expect(r.body).toBe('{"error":"not found"}');
  });

  it('forwards agent Content-Type to the client', async () => {
    agent = await startFakeAgent(socket, () => ({
      status: 200,
      body: '[Interface]\nPrivateKey=...',
      headers: { 'content-type': 'text/plain; charset=utf-8' },
    }));
    const r = await runReq({
      socket,
      fakeAgent: agent,
      method: 'GET',
      pathname: '/peers/1/config?format=raw',
      headers: { accept: 'text/plain' },
    });
    expect(r.status).toBe(200);
    expect(r.headers['content-type']).toMatch(/text\/plain/);
    expect(r.body).toContain('[Interface]');
  });

  it('returns 502 when agent socket is unreachable', async () => {
    // Do not start agent — socket path does not exist
    const r = await runReq({
      socket,
      fakeAgent: null as unknown as http.Server,
      method: 'GET',
      pathname: '/status',
    });
    expect(r.status).toBe(502);
    expect(r.body).toMatch(/agent unreachable/);
  });
});
