<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { api } from '../api/client';
import { isHandshakeLive, relativeTime, formatRate } from '../api/format';
import { usePoll } from '../api/usePoll';
import { trafficStore, interfaceRate } from '../stores/traffic';
import Sparkline from '../components/Sparkline.vue';
import type { Interface, InterfaceStatus } from '../api/types';

type Bulk = Interface & { status?: InterfaceStatus; status_error?: string };

// Статусы интерфейсов меняются редко — 30 секунд для Overview достаточно.
// Счётчики трафика живут в глобальном trafficStore и поллятся отдельно.
const { data, error } = usePoll<Bulk[]>(() => api.interfacesWithStatus(), 30_000);

interface Row {
  iface: Bulk;
  kernelUp: boolean;
  peerCount: number;
  livePeers: number;
  lastHandshake: number;
}

const rows = computed<Row[]>(() => {
  const list = data.value;
  if (!list) return [];
  return list.map((iface) => {
    const up = !iface.status_error && !!iface.status;
    const peers = iface.status?.peers ?? [];
    let last = 0;
    let live = 0;
    for (const p of peers) {
      if (p.latest_handshake > last) last = p.latest_handshake;
      if (isHandshakeLive(p.latest_handshake)) live += 1;
    }
    return { iface, kernelUp: up, peerCount: peers.length, livePeers: live, lastHandshake: last };
  });
});

const displayError = computed(() => error.value);

function kernelBadge(iface: Interface, up: boolean): string {
  if (!iface.enabled) return 'bg-neutral-500';
  return up ? 'bg-emerald-500' : 'bg-rose-500';
}

function roleBadge(role: string): string {
  return role === 'clients'
    ? 'bg-sky-900/60 text-sky-200 border-sky-700'
    : 'bg-violet-900/60 text-violet-200 border-violet-700';
}

function peersText(r: Row): string {
  if (r.peerCount === 0) return 'нет пиров';
  return `${r.livePeers} live / ${r.peerCount}`;
}

function peersClass(r: Row): string {
  if (r.peerCount === 0) return 'text-neutral-500';
  return r.livePeers > 0 ? 'text-emerald-300' : 'text-neutral-400';
}

// Каждая карточка читает из trafficStore по имени, computed реактивны — график сам обновляется
function rxSeries(name: string): number[] {
  return trafficStore.interfaces[name]?.rxRate ?? [];
}
function txSeries(name: string): number[] {
  return trafficStore.interfaces[name]?.txRate ?? [];
}
function rate(name: string): { rx: number; tx: number } {
  return interfaceRate(name);
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">Интерфейсы</h1>
      <span v-if="!data" class="text-sm text-neutral-500">загрузка…</span>
    </div>

    <div v-if="displayError" class="text-rose-400">{{ displayError }}</div>
    <div v-else-if="rows.length === 0 && data" class="text-neutral-400">Нет интерфейсов</div>

    <div v-else-if="rows.length" class="grid grid-cols-1 md:grid-cols-2 gap-3">
      <RouterLink
        v-for="r in rows"
        :key="r.iface.id"
        :to="{ name: 'interface', params: { name: r.iface.name } }"
        class="relative overflow-hidden block border border-neutral-800 hover:border-neutral-600 bg-neutral-900/40 rounded-lg p-4"
      >
        <!-- фоновый sparkline: занимает всю ширину карточки, линия тонкая, прозрачная -->
        <div class="absolute inset-x-0 bottom-0 pointer-events-none">
          <Sparkline
            :values="rxSeries(r.iface.name)"
            color="#10b981"
            :height="48"
            :width="400"
            :opacity="0.35"
          />
        </div>
        <div class="absolute inset-x-0 top-0 pointer-events-none">
          <Sparkline
            :values="txSeries(r.iface.name)"
            color="#60a5fa"
            :height="40"
            :width="400"
            :opacity="0.28"
            mirror
          />
        </div>

        <div class="relative">
          <div class="flex items-center gap-2">
            <span
              class="inline-block w-2.5 h-2.5 rounded-full"
              :class="kernelBadge(r.iface, r.kernelUp)"
              :title="r.kernelUp ? 'интерфейс поднят' : 'интерфейса нет в ядре'"
            ></span>
            <span class="font-mono font-semibold">{{ r.iface.name }}</span>
            <span
              class="ml-auto text-xs px-1.5 py-0.5 rounded border"
              :class="roleBadge(r.iface.role)"
            >{{ r.iface.role }}</span>
          </div>
          <div class="mt-2 text-sm text-neutral-400 space-y-0.5">
            <div>адрес: <span class="font-mono text-neutral-200">{{ r.iface.address }}</span></div>
            <div>пиры: <span :class="peersClass(r)">{{ peersText(r) }}</span></div>
            <div v-if="r.peerCount > 0">
              последний handshake: <span class="text-neutral-200">{{ relativeTime(r.lastHandshake) }}</span>
            </div>
            <div class="flex gap-3 pt-1 font-mono text-xs">
              <span class="text-emerald-400">↓ {{ formatRate(rate(r.iface.name).rx) }}</span>
              <span class="text-sky-400">↑ {{ formatRate(rate(r.iface.name).tx) }}</span>
            </div>
          </div>
        </div>
      </RouterLink>
    </div>
  </div>
</template>
