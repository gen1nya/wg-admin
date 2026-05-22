import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

export interface AppConfig {
  user: string;
  passwordHash: string;      // scrypt format: "scrypt:<saltHex>:<hashHex>"
  socket: string;            // agent unix socket path
  listenHost: string;        // e.g. "127.0.0.1" or "10.99.0.5"
  listenPort: number;
  sessionSecret: Buffer;     // 32+ bytes
  staticDir: string;         // dist/web
  dev: boolean;
}

interface FileConfig {
  user?: string;
  password_hash?: string;
  socket?: string;
  listen_host?: string;
  listen_port?: number;
  static_dir?: string;
}

const DEFAULT_CONFIG_PATH = '/etc/wg-admin/app.conf';
const DEFAULT_SECRET_PATH = '/etc/wg-admin/session.secret';

export function loadConfig(): AppConfig {
  const dev = process.env.NODE_ENV !== 'production';
  const configPath = process.env.WG_ADMIN_CONFIG || (dev ? 'dev-app.conf' : DEFAULT_CONFIG_PATH);
  const secretPath = process.env.WG_ADMIN_SECRET || (dev ? 'dev-session.secret' : DEFAULT_SECRET_PATH);

  let raw: FileConfig = {};
  if (fs.existsSync(configPath)) {
    raw = JSON.parse(fs.readFileSync(configPath, 'utf8')) as FileConfig;
  } else if (!dev) {
    throw new Error(`config not found: ${configPath}`);
  }

  const user = raw.user ?? process.env.WG_ADMIN_USER ?? 'admin';
  const passwordHash = raw.password_hash ?? process.env.WG_ADMIN_PASSWORD_HASH ?? '';
  if (!passwordHash) {
    if (dev) {
      console.warn('[config] no password_hash set; /auth/login will reject all attempts. Run: npm run hash -- <password>');
    } else {
      throw new Error('password_hash missing from config');
    }
  }

  const sessionSecret = loadOrCreateSecret(secretPath, dev);

  const distWeb = path.resolve(process.cwd(), 'dist/web');
  return {
    user,
    passwordHash,
    socket: raw.socket ?? process.env.WG_AGENT_SOCKET ?? '/run/wg-agent.sock',
    listenHost: raw.listen_host ?? process.env.WG_ADMIN_HOST ?? '127.0.0.1',
    listenPort: raw.listen_port ?? Number(process.env.WG_ADMIN_PORT ?? 3000),
    sessionSecret,
    staticDir: raw.static_dir ?? distWeb,
    dev,
  };
}

function loadOrCreateSecret(p: string, dev: boolean): Buffer {
  if (fs.existsSync(p)) {
    const buf = fs.readFileSync(p);
    if (buf.length < 32) throw new Error(`session secret too short: ${p}`);
    return buf;
  }
  const fresh = crypto.randomBytes(32);
  try {
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, fresh, { mode: 0o600 });
    console.warn(`[config] generated new session secret at ${p}; existing sessions invalidated`);
  } catch (e) {
    if (!dev) throw e;
    console.warn(`[config] could not persist session secret at ${p}, using in-memory only`);
  }
  return fresh;
}

// HashPassword: scrypt with random 16-byte salt, N=2^14 r=8 p=1 (Node defaults).
// Format: "scrypt:<saltHex>:<hashHex>". Consumed by auth.verifyPassword.
export function hashPassword(plain: string): string {
  const salt = crypto.randomBytes(16);
  const hash = crypto.scryptSync(plain, salt, 64);
  return `scrypt:${salt.toString('hex')}:${hash.toString('hex')}`;
}
