import crypto from 'node:crypto';
import type { RequestHandler } from 'express';
import type { AppConfig } from './config.js';

const COOKIE_NAME = 'wg_session';
const SESSION_TTL_SEC = 60 * 60 * 24 * 7; // 7 days

interface SessionPayload {
  sub: string;
  iat: number;
  exp: number;
}

declare module 'express-serve-static-core' {
  interface Request {
    session?: SessionPayload;
  }
}

function b64url(buf: Buffer): string {
  return buf.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function b64urlDecode(s: string): Buffer {
  s = s.replace(/-/g, '+').replace(/_/g, '/');
  const pad = s.length % 4 === 0 ? 0 : 4 - (s.length % 4);
  return Buffer.from(s + '='.repeat(pad), 'base64');
}

function sign(payload: Buffer, secret: Buffer): Buffer {
  return crypto.createHmac('sha256', secret).update(payload).digest();
}

export function issueSession(sub: string, secret: Buffer): string {
  const now = Math.floor(Date.now() / 1000);
  const payload: SessionPayload = { sub, iat: now, exp: now + SESSION_TTL_SEC };
  const body = Buffer.from(JSON.stringify(payload), 'utf8');
  const sig = sign(body, secret);
  return `${b64url(body)}.${b64url(sig)}`;
}

export function verifySession(token: string | undefined, secret: Buffer): SessionPayload | null {
  if (!token) return null;
  const parts = token.split('.');
  if (parts.length !== 2) return null;
  let body: Buffer, sig: Buffer;
  try {
    body = b64urlDecode(parts[0]);
    sig = b64urlDecode(parts[1]);
  } catch {
    return null;
  }
  const expected = sign(body, secret);
  if (sig.length !== expected.length) return null;
  if (!crypto.timingSafeEqual(sig, expected)) return null;

  let payload: SessionPayload;
  try {
    payload = JSON.parse(body.toString('utf8')) as SessionPayload;
  } catch {
    return null;
  }
  if (typeof payload.sub !== 'string' || typeof payload.exp !== 'number') return null;
  if (Math.floor(Date.now() / 1000) >= payload.exp) return null;
  return payload;
}

function verifyPassword(plain: string, stored: string): boolean {
  if (!stored.startsWith('scrypt:')) return false;
  const [, saltHex, hashHex] = stored.split(':');
  if (!saltHex || !hashHex) return false;
  let salt: Buffer, expected: Buffer;
  try {
    salt = Buffer.from(saltHex, 'hex');
    expected = Buffer.from(hashHex, 'hex');
  } catch {
    return false;
  }
  const got = crypto.scryptSync(plain, salt, expected.length);
  if (got.length !== expected.length) return false;
  return crypto.timingSafeEqual(got, expected);
}

// parseCookie — single-name reader; we only care about wg_session.
function readCookie(header: string | undefined, name: string): string | undefined {
  if (!header) return undefined;
  for (const part of header.split(';')) {
    const eq = part.indexOf('=');
    if (eq < 0) continue;
    const k = part.slice(0, eq).trim();
    if (k === name) return decodeURIComponent(part.slice(eq + 1).trim());
  }
  return undefined;
}

function cookieHeader(token: string, secure: boolean): string {
  const parts = [
    `${COOKIE_NAME}=${encodeURIComponent(token)}`,
    `Max-Age=${SESSION_TTL_SEC}`,
    `Path=/`,
    `HttpOnly`,
    `SameSite=Strict`,
  ];
  if (secure) parts.push('Secure');
  return parts.join('; ');
}

function clearCookieHeader(secure: boolean): string {
  const parts = [
    `${COOKIE_NAME}=`,
    `Max-Age=0`,
    `Path=/`,
    `HttpOnly`,
    `SameSite=Strict`,
  ];
  if (secure) parts.push('Secure');
  return parts.join('; ');
}

export function requireAuth(cfg: AppConfig): RequestHandler {
  return (req, res, next) => {
    const token = readCookie(req.headers.cookie, COOKIE_NAME);
    const session = verifySession(token, cfg.sessionSecret);
    if (!session) {
      res.status(401).json({ error: 'unauthorized' });
      return;
    }
    req.session = session;
    next();
  };
}

// CSRF: only the one origin is allowed to POST/PATCH/DELETE/PUT with cookies.
// SameSite=Strict already blocks cross-site, but this is belt-and-braces and
// also catches a compromised sibling origin in the same site.
export function requireSameOrigin(_cfg: AppConfig): RequestHandler {
  return (req, res, next) => {
    const m = req.method.toUpperCase();
    if (m === 'GET' || m === 'HEAD' || m === 'OPTIONS') return next();
    const origin = req.headers.origin || req.headers.referer;
    if (!origin) {
      res.status(403).json({ error: 'missing origin' });
      return;
    }
    try {
      const host = new URL(origin).host;
      const expected = req.headers.host;
      if (host !== expected) {
        res.status(403).json({ error: 'origin mismatch' });
        return;
      }
    } catch {
      res.status(403).json({ error: 'bad origin' });
      return;
    }
    // Dev-mode exception: when vite runs on 5173 and proxies to 3000, the
    // Origin header will carry 5173 while req.host is whatever. The vite
    // proxy forwards the request with preserved origin; since changeOrigin
    // is false, req.host stays 127.0.0.1:5173. So in practice host matches.
    next();
  };
}

export function loginHandler(cfg: AppConfig): RequestHandler {
  return (req, res) => {
    const body = req.body as { username?: string; password?: string } | undefined;
    const username = body?.username ?? '';
    const password = body?.password ?? '';
    if (!username || !password) {
      res.status(400).json({ error: 'username and password required' });
      return;
    }
    const userOk = crypto.timingSafeEqual(
      Buffer.from(username.padEnd(cfg.user.length, '\0')),
      Buffer.from(cfg.user.padEnd(username.length, '\0')),
    ) && username.length === cfg.user.length;
    const passOk = cfg.passwordHash && verifyPassword(password, cfg.passwordHash);
    if (!userOk || !passOk) {
      res.status(401).json({ error: 'invalid credentials' });
      return;
    }
    const token = issueSession(cfg.user, cfg.sessionSecret);
    res.setHeader('Set-Cookie', cookieHeader(token, !cfg.dev));
    res.json({ ok: true, user: cfg.user });
  };
}

export function logoutHandler(cfg: AppConfig): RequestHandler {
  return (_req, res) => {
    res.setHeader('Set-Cookie', clearCookieHeader(!cfg.dev));
    res.json({ ok: true });
  };
}

export function whoamiHandler(cfg: AppConfig): RequestHandler {
  return (req, res) => {
    const token = readCookie(req.headers.cookie, COOKIE_NAME);
    const session = verifySession(token, cfg.sessionSecret);
    if (!session) {
      res.status(401).json({ error: 'unauthorized' });
      return;
    }
    res.json({ user: session.sub, exp: session.exp });
  };
}
