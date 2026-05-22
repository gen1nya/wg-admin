CREATE TABLE marks (
  id INTEGER PRIMARY KEY,
  fwmark INTEGER NOT NULL UNIQUE,
  name TEXT NOT NULL UNIQUE,
  routing_table TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE exits (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL CHECK (kind IN ('direct','wg','xray','custom')),
  out_interface TEXT NOT NULL,
  mark_id INTEGER NOT NULL REFERENCES marks(id),
  masquerade INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE interfaces (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  address TEXT NOT NULL,
  subnet TEXT NOT NULL,
  listen_port INTEGER NOT NULL,
  mtu INTEGER,
  private_key TEXT NOT NULL,
  public_endpoint TEXT NOT NULL,
  public_port INTEGER NOT NULL,
  dns TEXT NOT NULL DEFAULT '',
  keepalive INTEGER NOT NULL DEFAULT 25,
  default_exit_id INTEGER REFERENCES exits(id),
  custom_postup TEXT NOT NULL DEFAULT '',
  custom_postdown TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

CREATE TABLE peers (
  id INTEGER PRIMARY KEY,
  interface_id INTEGER NOT NULL REFERENCES interfaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  public_key TEXT NOT NULL UNIQUE,
  private_key TEXT NOT NULL,
  address TEXT NOT NULL,
  default_exit_id INTEGER REFERENCES exits(id),
  enabled INTEGER NOT NULL DEFAULT 1,
  notes TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_peers_interface ON peers(interface_id);

CREATE TABLE ipsets (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE ipset_entries (
  id INTEGER PRIMARY KEY,
  ipset_id INTEGER NOT NULL REFERENCES ipsets(id) ON DELETE CASCADE,
  cidr TEXT NOT NULL,
  comment TEXT NOT NULL DEFAULT '',
  UNIQUE(ipset_id, cidr)
);

CREATE TABLE routing_rules (
  id INTEGER PRIMARY KEY,
  scope TEXT NOT NULL CHECK (scope IN ('global','interface','peer')),
  scope_id INTEGER,
  match_type TEXT NOT NULL CHECK (match_type IN ('cidr','ipset','domain','all')),
  match_value TEXT NOT NULL,
  exit_id INTEGER NOT NULL REFERENCES exits(id),
  priority INTEGER NOT NULL DEFAULT 100,
  enabled INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX idx_rules_scope ON routing_rules(scope, scope_id);

CREATE TABLE plans (
  id INTEGER PRIMARY KEY,
  created_at INTEGER NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  diff TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending','applied','confirmed','reverted','expired')),
  applied_at INTEGER,
  confirmed_at INTEGER,
  reverted_at INTEGER,
  timeout_sec INTEGER NOT NULL DEFAULT 30,
  snapshot_pre TEXT
);

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  actor TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id INTEGER,
  payload TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_ts ON audit_log(ts);
