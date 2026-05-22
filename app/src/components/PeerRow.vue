<script setup lang="ts">
import { computed } from 'vue';
import type { Peer, PeerStatus } from '../api/types';
import { formatBytes, formatRate, isHandshakeLive, relativeTime } from '../api/format';
import { trafficStore, peerHasRecentData } from '../stores/traffic';
import Sparkline from './Sparkline.vue';

const props = defineProps<{
  peer: Peer;
  status?: PeerStatus;
  readOnly?: boolean;
}>();
const emit = defineEmits<{
  (e: 'config', peer: Peer): void;
  (e: 'edit', peer: Peer): void;
  (e: 'delete', peer: Peer): void;
}>();

const live = computed(() => isHandshakeLive(props.status?.latest_handshake));
const rx = computed(() => formatBytes(props.status?.rx_bytes ?? 0));
const tx = computed(() => formatBytes(props.status?.tx_bytes ?? 0));
const handshake = computed(() => relativeTime(props.status?.latest_handshake ?? null));

// История из глобального trafficStore — индексируется по public_key
const history = computed(() => trafficStore.peers[props.peer.public_key]);
const rxSeries = computed(() => history.value?.rxRate ?? []);
const txSeries = computed(() => history.value?.txRate ?? []);
const hasData = computed(() => peerHasRecentData(props.peer.public_key));
const lastRx = computed(() => {
  const h = history.value;
  return h ? (h.rxRate[h.rxRate.length - 1] ?? 0) : 0;
});
const lastTx = computed(() => {
  const h = history.value;
  return h ? (h.txRate[h.txRate.length - 1] ?? 0) : 0;
});
</script>

<template>
  <tr class="border-b border-neutral-800 hover:bg-neutral-900/50">
    <td class="px-2 py-2">
      <div class="flex items-center gap-2">
        <span
          class="inline-block w-2 h-2 rounded-full"
          :class="live ? 'bg-emerald-500' : 'bg-neutral-600'"
          title="handshake"
        ></span>
        <span
          class="inline-block w-2 h-2 rounded-full"
          :class="hasData ? 'bg-emerald-400' : 'bg-neutral-700'"
          title="трафик сейчас"
        ></span>
        <span>{{ peer.name }}</span>
      </div>
    </td>
    <td class="px-2 py-2 font-mono text-sm text-neutral-300">{{ peer.address }}</td>
    <td class="px-2 py-2 text-sm text-neutral-400">{{ handshake }}</td>
    <td class="px-2 py-2 text-sm font-mono">
      <div class="text-neutral-400">↓ {{ rx }}</div>
      <div class="text-[10px] text-emerald-400">{{ formatRate(lastRx) }}</div>
    </td>
    <td class="px-2 py-2 text-sm font-mono">
      <div class="text-neutral-400">↑ {{ tx }}</div>
      <div class="text-[10px] text-sky-400">{{ formatRate(lastTx) }}</div>
    </td>
    <td class="px-2 py-2">
      <div class="flex flex-col gap-0.5 w-20">
        <Sparkline :values="rxSeries" color="#10b981" :height="12" :width="80" :opacity="0.8" />
        <Sparkline :values="txSeries" color="#60a5fa" :height="12" :width="80" :opacity="0.8" mirror />
      </div>
    </td>
    <td class="px-2 py-2 text-right">
      <div v-if="!readOnly" class="flex justify-end gap-1">
        <button
          @click="emit('config', peer)"
          class="px-2 py-0.5 text-xs border border-neutral-700 hover:bg-neutral-800 rounded"
        >conf</button>
        <button
          @click="emit('edit', peer)"
          class="px-2 py-0.5 text-xs border border-neutral-700 hover:bg-neutral-800 rounded"
        >edit</button>
        <button
          @click="emit('delete', peer)"
          class="px-2 py-0.5 text-xs border border-rose-800 text-rose-300 hover:bg-rose-900/40 rounded"
        >delete</button>
      </div>
    </td>
  </tr>
</template>
