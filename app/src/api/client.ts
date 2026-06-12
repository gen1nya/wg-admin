import type {
  AuditEntry,
  Exit,
  GeoResponse,
  Interface,
  InterfaceDetail,
  Peer,
  PeerConfigResponse,
  Plan,
  PlanCreateResponse,
  Status,
  TrafficEntry,
} from './types';

export class ApiError extends Error {
  status: number;
  body: unknown;
  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

// list() нормализует ответ: Go маршалит пустой slice как `null`, а фронт
// ожидает массив. Применяется ко всем list-эндпоинтам.
async function list<T>(path: string): Promise<T[]> {
  const r = await request<T[] | null>(path);
  return r ?? [];
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: {
      'content-type': 'application/json',
      accept: 'application/json',
      ...(init.headers || {}),
    },
    ...init,
  });
  const ct = res.headers.get('content-type') || '';
  const isJson = ct.includes('application/json');
  const body = isJson ? await res.json().catch(() => null) : await res.text();
  if (!res.ok) {
    const msg = isJson && body && typeof body === 'object' && 'error' in (body as Record<string, unknown>)
      ? String((body as Record<string, unknown>).error)
      : `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, body);
  }
  return body as T;
}

export const api = {
  // auth
  login: (username: string, password: string) =>
    request<{ ok: boolean; user: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<{ ok: boolean }>('/auth/logout', { method: 'POST' }),
  whoami: () => request<{ user: string; exp: number }>('/auth/whoami'),

  // status
  status: () => request<Status>('/api/status'),

  // interfaces
  interfaces: () => list<Interface>('/api/interfaces'),
  /**
   * Bulk-вариант: каждый элемент — Interface + опциональные status/status_error.
   * Эквивалент N+1 запросов interfaceByName — для дашбордов, обновляющихся поллингом.
   */
  interfacesWithStatus: async () => {
    const arr = await list<Interface & { status?: InterfaceDetail['status']; status_error?: string }>(
      '/api/interfaces?include=status',
    );
    for (const i of arr) {
      if (i.status && i.status.peers == null) i.status.peers = [];
    }
    return arr;
  },
  interfaceByName: async (name: string) => {
    const d = await request<InterfaceDetail>(`/api/interfaces/${encodeURIComponent(name)}`);
    if (d.status && d.status.peers == null) d.status.peers = [];
    return d;
  },
  peersOnInterface: (name: string) =>
    list<Peer>(`/api/interfaces/${encodeURIComponent(name)}/peers`),
  createPeer: (
    ifaceName: string,
    body: { name: string; address?: string; default_exit_id?: number | null; notes?: string },
  ) =>
    request<Peer>(`/api/interfaces/${encodeURIComponent(ifaceName)}/peers`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  // peers
  getPeer: (id: number) => request<Peer>(`/api/peers/${id}`),
  updatePeer: (id: number, patch: Partial<Pick<Peer, 'name' | 'notes' | 'enabled' | 'tags'>>) =>
    request<Peer>(`/api/peers/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  deletePeer: (id: number) => request<{ ok: boolean }>(`/api/peers/${id}`, { method: 'DELETE' }),
  peerConfig: (id: number) => request<PeerConfigResponse>(`/api/peers/${id}/config`),
  peerConfigDownloadURL: (id: number) => `/api/peers/${id}/config?format=raw`,
  setPeerExit: (id: number, exit_id: number | null) =>
    request<Peer>(`/api/peers/${id}/exit`, {
      method: 'PATCH',
      body: JSON.stringify(exit_id === null ? { clear: true } : { exit_id }),
    }),

  // exits / marks
  exits: () => list<Exit>('/api/exits'),

  // traffic
  traffic: () => list<TrafficEntry>('/api/traffic'),

  // geo — peers located by endpoint IP for the map view
  geo: () => request<GeoResponse>('/api/geo'),

  // audit
  audit: (limit = 200) => list<AuditEntry>(`/api/audit?limit=${limit}`),

  // plans
  plans: (limit = 50) => list<Plan>(`/api/plans?limit=${limit}`),
  getPlan: (id: number) => request<Plan>(`/api/plans/${id}`),
  createPlan: (description: string, desired: unknown) =>
    request<PlanCreateResponse>('/api/plan', {
      method: 'POST',
      body: JSON.stringify({ description, desired }),
    }),
  applyPlan: (id: number, timeoutSec: number) =>
    request<Plan>(`/api/plans/${id}/apply?timeout=${timeoutSec}`, { method: 'POST' }),
  confirmPlan: (id: number) => request<Plan>(`/api/plans/${id}/confirm`, { method: 'POST' }),
  revertPlan: (id: number) => request<Plan>(`/api/plans/${id}/revert`, { method: 'POST' }),
};
