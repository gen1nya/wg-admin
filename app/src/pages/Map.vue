<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { api, ApiError } from '../api/client';
import { isHandshakeLive, relativeTime, formatBytes } from '../api/format';
import type { GeoEntry, GeoResponse } from '../api/types';

const REFRESH_MS = 30_000;

const data = ref<GeoResponse | null>(null);
const error = ref<string | null>(null);
const loading = ref(true);

const mapEl = ref<HTMLDivElement | null>(null);
const map = shallowRef<L.Map | null>(null);
const markerLayer = shallowRef<L.LayerGroup | null>(null);
let refreshTimer: ReturnType<typeof setInterval> | null = null;
let fitted = false;

const located = computed(() => (data.value?.entries ?? []).filter((e) => e.has_location));
const unlocated = computed(() => (data.value?.entries ?? []).filter((e) => !e.has_location));
const enabled = computed(() => data.value?.enabled ?? false);

// Один маркер на координату: GeoLite2 отдаёт центроид города, поэтому все пиры
// одного города ложатся в одну точку — группируем и показываем списком в попапе.
interface LocGroup {
  lat: number;
  lon: number;
  city: string;
  country: string;
  peers: GeoEntry[];
  live: number;
}

function groupByLocation(entries: GeoEntry[]): LocGroup[] {
  const byKey = new Map<string, LocGroup>();
  for (const e of entries) {
    if (e.lat == null || e.lon == null) continue;
    const key = `${e.lat.toFixed(4)},${e.lon.toFixed(4)}`;
    let g = byKey.get(key);
    if (!g) {
      g = { lat: e.lat, lon: e.lon, city: e.city ?? '', country: e.country ?? '', peers: [], live: 0 };
      byKey.set(key, g);
    }
    g.peers.push(e);
    if (isHandshakeLive(e.latest_handshake)) g.live += 1;
  }
  return [...byKey.values()];
}

function esc(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string,
  );
}

function popupHtml(g: LocGroup): string {
  const place = [g.city, g.country].filter(Boolean).map(esc).join(', ') || 'неизвестно';
  const rows = g.peers
    .map((p) => {
      const live = isHandshakeLive(p.latest_handshake);
      const dot = live ? '#10b981' : '#737373';
      const name = esc(p.peer_name || p.public_key.slice(0, 10) + '…');
      const meta = `${esc(p.interface)} · ${relativeTime(p.latest_handshake)} · ↓${formatBytes(
        p.rx_bytes,
      )} ↑${formatBytes(p.tx_bytes)}`;
      return `<div style="margin:2px 0"><span style="display:inline-block;width:8px;height:8px;border-radius:9999px;background:${dot};margin-right:6px"></span><b>${name}</b><br><span style="color:#9ca3af;font-size:11px;margin-left:14px">${meta}</span></div>`;
    })
    .join('');
  return `<div style="min-width:180px"><div style="font-weight:600;margin-bottom:4px">${place} <span style="color:#9ca3af;font-weight:400">· ${g.peers.length} пир(ов)</span></div>${rows}</div>`;
}

function renderMarkers(): void {
  const m = map.value;
  const layer = markerLayer.value;
  if (!m || !layer) return;
  layer.clearLayers();
  const groups = groupByLocation(located.value);
  const pts: L.LatLngExpression[] = [];
  for (const g of groups) {
    const color = g.live > 0 ? '#10b981' : '#9ca3af';
    const radius = 6 + Math.min(g.peers.length, 8) * 1.5;
    const marker = L.circleMarker([g.lat, g.lon], {
      radius,
      color,
      weight: 1.5,
      fillColor: color,
      fillOpacity: 0.5,
    });
    marker.bindPopup(popupHtml(g));
    marker.bindTooltip(`${g.peers.length}`, {
      permanent: true,
      direction: 'center',
      className: 'wg-marker-count',
    });
    marker.addTo(layer);
    pts.push([g.lat, g.lon]);
  }
  if (!fitted && pts.length > 0) {
    m.fitBounds(L.latLngBounds(pts).pad(0.3), { maxZoom: 6 });
    fitted = true;
  }
}

async function load(): Promise<void> {
  try {
    data.value = await api.geo();
    error.value = null;
    renderMarkers();
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  } finally {
    loading.value = false;
  }
}

watch(located, renderMarkers);

onMounted(() => {
  if (!mapEl.value) return;
  const m = L.map(mapEl.value, { worldCopyJump: true }).setView([55, 50], 3);
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 18,
    attribution: '© OpenStreetMap',
  }).addTo(m);
  markerLayer.value = L.layerGroup().addTo(m);
  map.value = m;
  void load();
  refreshTimer = setInterval(() => void load(), REFRESH_MS);
});

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer);
  map.value?.remove();
  map.value = null;
});
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center gap-3">
      <h1 class="text-xl font-semibold">Карта клиентов</h1>
      <span v-if="data" class="text-sm text-neutral-500">
        {{ located.length }} на карте<template v-if="unlocated.length">, {{ unlocated.length }} без геолокации</template>
      </span>
      <button
        @click="load"
        class="ml-auto px-3 py-1 text-sm border border-neutral-700 hover:bg-neutral-800 rounded"
      >Обновить</button>
    </div>

    <div v-if="error" class="text-rose-400 text-sm">{{ error }}</div>

    <div
      v-if="data && !enabled"
      class="rounded-lg border border-amber-800 bg-amber-950/40 px-4 py-3 text-sm text-amber-200"
    >
      GeoIP-база не загружена — клиентов не на чем разместить.
      Положи <span class="font-mono">GeoLite2-City.mmdb</span> в
      <span class="font-mono">{{ data.db_path || '/var/lib/wg-admin/GeoLite2-City.mmdb' }}</span>
      на хосте и перезапусти агента (<span class="font-mono">systemctl restart wg-agent</span>).
      Бесплатная база — MaxMind GeoLite2 (City). Ниже — список подключённых пиров с их IP.
    </div>

    <div
      ref="mapEl"
      class="w-full h-[520px] rounded-lg border border-neutral-800 overflow-hidden bg-neutral-900/40 z-0"
    ></div>

    <div class="flex items-center gap-4 text-xs text-neutral-400">
      <span class="flex items-center gap-1.5">
        <span class="inline-block w-2.5 h-2.5 rounded-full" style="background:#10b981"></span>
        есть live-пир (handshake &lt; 3 мин)
      </span>
      <span class="flex items-center gap-1.5">
        <span class="inline-block w-2.5 h-2.5 rounded-full" style="background:#9ca3af"></span>
        без свежего handshake
      </span>
      <span v-if="enabled && data?.database" class="ml-auto font-mono text-neutral-600">
        {{ data.database }}
      </span>
    </div>

    <section v-if="unlocated.length" class="space-y-2">
      <h2 class="text-sm font-semibold text-neutral-300">
        Без геолокации ({{ unlocated.length }})
        <span class="font-normal text-neutral-500">— приватный/нерезолвимый endpoint или база без координат</span>
      </h2>
      <table class="w-full text-sm">
        <thead class="text-left text-xs uppercase text-neutral-500 border-b border-neutral-800">
          <tr>
            <th class="px-2 py-1 font-medium">Имя</th>
            <th class="px-2 py-1 font-medium">Интерфейс</th>
            <th class="px-2 py-1 font-medium">Endpoint IP</th>
            <th class="px-2 py-1 font-medium">Страна</th>
            <th class="px-2 py-1 font-medium">Handshake</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="e in unlocated" :key="e.public_key" class="border-b border-neutral-800/60">
            <td class="px-2 py-1">
              <span
                class="inline-block w-2 h-2 rounded-full mr-1.5 align-middle"
                :class="isHandshakeLive(e.latest_handshake) ? 'bg-emerald-500' : 'bg-neutral-600'"
              ></span>
              {{ e.peer_name || (e.unknown ? '(не в БД)' : e.public_key.slice(0, 10) + '…') }}
            </td>
            <td class="px-2 py-1 font-mono text-neutral-400">{{ e.interface }}</td>
            <td class="px-2 py-1 font-mono text-neutral-300">{{ e.endpoint_ip || '—' }}</td>
            <td class="px-2 py-1 text-neutral-400">
              <template v-if="e.country_code">{{ e.country_code }}</template>
              <template v-else>—</template>
            </td>
            <td class="px-2 py-1 text-neutral-400">{{ relativeTime(e.latest_handshake) }}</td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<style>
/* Маркеры-счётчики поверх кружков. Leaflet рисует tooltip отдельным слоем,
   поэтому стилизуем глобально (не scoped). */
.wg-marker-count {
  background: transparent;
  border: none;
  box-shadow: none;
  color: #fff;
  font-size: 10px;
  font-weight: 700;
  text-shadow: 0 0 2px rgba(0, 0, 0, 0.9);
}
.wg-marker-count::before {
  display: none;
}
.leaflet-popup-content-wrapper,
.leaflet-popup-tip {
  background: #1c1c1f;
  color: #e5e5e5;
}
.leaflet-popup-content {
  margin: 10px 12px;
}
</style>
