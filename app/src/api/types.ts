// Mirror of agent/internal/model/model.go. Sync by hand; see docs/TODO.md.

export type Role = 'clients' | 'mesh';

export interface Interface {
  id: number;
  name: string;
  address: string;
  subnet: string;
  listen_port: number;
  mtu?: number;
  private_key: string;
  public_endpoint: string;
  public_port: number;
  dns: string;
  keepalive: number;
  default_exit_id?: number;
  client_allowed_ips: string;
  custom_postup: string;
  custom_postdown: string;
  enabled: boolean;
  role: Role;
  created_at: number;
}

export interface Peer {
  id: number;
  interface_id: number;
  name: string;
  public_key: string;
  private_key: string;
  address: string;
  default_exit_id?: number;
  enabled: boolean;
  notes: string;
  tags: string;
  created_at: number;
}

export interface PeerStatus {
  public_key: string;
  endpoint: string;
  allowed_ips: string;
  latest_handshake: number;
  rx_bytes: number;
  tx_bytes: number;
}

export interface InterfaceStatus {
  name: string;
  public_key: string;
  listen_port: number;
  fwmark: number;
  peers: PeerStatus[];
}

export interface InterfaceDetail {
  interface: Interface;
  status?: InterfaceStatus;
  status_error?: string;
}

export interface Exit {
  id: number;
  name: string;
  kind: 'direct' | 'wg' | 'xray' | 'custom';
  out_interface: string;
  mark_id: number;
  masquerade: boolean;
  description: string;
  enabled: boolean;
}

export interface Mark {
  id: number;
  fwmark: number;
  name: string;
  routing_table: string;
  description: string;
}

export interface TrafficEntry {
  peer_id?: number;
  interface: string;
  peer_name?: string;
  public_key: string;
  allowed_ips: string;
  rx_bytes: number;
  tx_bytes: number;
  latest_handshake: number;
  unknown?: boolean;
}

// One connected peer located by its endpoint IP. Secrets are never sent.
// has_location=false (and lat/lon absent) when geo is disabled or the endpoint
// couldn't be resolved — the entry is still listed, just not placed on the map.
export interface GeoEntry {
  peer_id?: number;
  peer_name?: string;
  interface: string;
  role: Role;
  public_key: string;
  endpoint: string;
  endpoint_ip: string;
  country?: string;
  country_code?: string;
  city?: string;
  lat?: number;
  lon?: number;
  has_location: boolean;
  accuracy_km?: number;
  latest_handshake: number;
  rx_bytes: number;
  tx_bytes: number;
  unknown?: boolean;
}

export interface GeoResponse {
  enabled: boolean;       // a geo DB is loaded on the agent
  database?: string;      // mmdb type, e.g. "GeoLite2-City"
  db_path?: string;       // where to drop the DB when absent
  entries: GeoEntry[];
}

export interface AuditEntry {
  id: number;
  ts: number;
  actor: string;
  action: string;
  entity_type: string;
  entity_id?: number;
  payload: string;
}

export type PlanState = 'pending' | 'applied' | 'confirmed' | 'reverted' | 'expired';

export interface Plan {
  id: number;
  created_at: number;
  created_by: string;
  description: string;
  desired: string;
  diff: string;
  state: PlanState;
  applied_at?: number;
  confirmed_at?: number;
  reverted_at?: number;
  timeout_sec: number;
  snapshot_pre?: string;
}

export interface PlanCreateResponse {
  plan: Plan;
  diff: Diff;
}

export interface Diff {
  ipsets?: IPSetDiff[];
  routes?: RouteDiff[];
  rules?: RuleDiff[];
  nft?: NFTDiff | null;
}

export interface IPSetDiff {
  name: string;
  created?: boolean;
  add?: string[];
  remove?: string[];
}
export interface RouteDiff {
  table: string;
  dest: string;
  op: 'create' | 'update' | 'noop';
}
export interface RuleDiff {
  priority: number;
  op: string;
}
export interface NFTDiff {
  op: 'create' | 'replace' | 'noop';
}

export interface Status {
  ok: boolean;
  kernel_mode: string;
}

export interface PeerConfigResponse {
  peer_id: number;
  config: string;
}
