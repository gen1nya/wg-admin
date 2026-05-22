<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { api, ApiError } from '../api/client';
import { formatRate } from '../api/format';
import { trafficStore, interfaceRate } from '../stores/traffic';
import PeerRow from '../components/PeerRow.vue';
import AddPeerModal from '../components/AddPeerModal.vue';
import PeerConfigModal from '../components/PeerConfigModal.vue';
import EditPeerModal from '../components/EditPeerModal.vue';
import Modal from '../components/Modal.vue';
import Sparkline from '../components/Sparkline.vue';
import type { InterfaceDetail, Peer, PeerStatus } from '../api/types';

const props = defineProps<{ name: string }>();

const detail = ref<InterfaceDetail | null>(null);
const peers = ref<Peer[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

const addOpen = ref(false);
const configPeer = ref<Peer | null>(null);
const editPeer = ref<Peer | null>(null);
const deletePeer = ref<Peer | null>(null);
const deletingBusy = ref(false);

let detailTimer: ReturnType<typeof setInterval> | null = null;

const iface = computed(() => detail.value?.interface);
const role = computed(() => iface.value?.role);
const readOnly = computed(() => role.value !== 'clients');
const status = computed(() => detail.value?.status);
const statusByPubkey = computed(() => {
  const map = new Map<string, PeerStatus>();
  for (const p of status.value?.peers ?? []) map.set(p.public_key, p);
  return map;
});

// Трафик по интерфейсу — реактивно читаем из глобального store.
const ifaceRx = computed(() => trafficStore.interfaces[props.name]?.rxRate ?? []);
const ifaceTx = computed(() => trafficStore.interfaces[props.name]?.txRate ?? []);
const ifaceRateNow = computed(() => interfaceRate(props.name));

async function loadDetail() {
  try {
    const d = await api.interfaceByName(props.name);
    detail.value = d;
    error.value = null;
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  }
}

async function loadPeers() {
  try {
    peers.value = await api.peersOnInterface(props.name);
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  }
}

function onCreated(peer: Peer) {
  peers.value = [...peers.value, peer];
  addOpen.value = false;
  configPeer.value = peer;
  // detail обновим следующим тиком — статус из ядра подтянется
  void loadDetail();
}

function onUpdated(peer: Peer) {
  peers.value = peers.value.map((p) => (p.id === peer.id ? peer : p));
  editPeer.value = null;
}

async function confirmDelete() {
  if (!deletePeer.value) return;
  deletingBusy.value = true;
  try {
    await api.deletePeer(deletePeer.value.id);
    peers.value = peers.value.filter((p) => p.id !== deletePeer.value!.id);
    deletePeer.value = null;
    void loadDetail();
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'delete failed';
  } finally {
    deletingBusy.value = false;
  }
}

function peerStatus(peer: Peer): PeerStatus | undefined {
  return statusByPubkey.value.get(peer.public_key);
}

onMounted(async () => {
  loading.value = true;
  await Promise.all([loadDetail(), loadPeers()]);
  loading.value = false;
  // live kernel-статус обновляем сами раз в 10с: handshake'и и rx/tx от ядра
  detailTimer = setInterval(loadDetail, 10_000);
});
onUnmounted(() => {
  if (detailTimer) clearInterval(detailTimer);
});
</script>

<template>
  <div class="space-y-6">
    <div v-if="loading" class="text-neutral-400">Загрузка…</div>
    <div v-else-if="error" class="text-rose-400">{{ error }}</div>
    <template v-else-if="iface">
      <header class="border-b border-neutral-800 pb-4">
        <div class="flex items-center gap-3">
          <h1 class="text-xl font-semibold font-mono">{{ iface.name }}</h1>
          <span
            class="text-xs px-1.5 py-0.5 rounded border"
            :class="role === 'clients'
              ? 'bg-sky-900/60 text-sky-200 border-sky-700'
              : 'bg-violet-900/60 text-violet-200 border-violet-700'"
          >{{ role }}</span>
          <span v-if="!iface.enabled" class="text-xs px-1.5 py-0.5 rounded bg-neutral-800 text-neutral-300">disabled</span>
        </div>
        <dl class="mt-3 grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-1 text-sm">
          <div><dt class="text-neutral-500">address</dt><dd class="font-mono">{{ iface.address }}</dd></div>
          <div><dt class="text-neutral-500">listen_port</dt><dd class="font-mono">{{ iface.listen_port }}</dd></div>
          <div><dt class="text-neutral-500">endpoint</dt><dd class="font-mono">{{ iface.public_endpoint }}:{{ iface.public_port }}</dd></div>
          <div><dt class="text-neutral-500">subnet</dt><dd class="font-mono">{{ iface.subnet }}</dd></div>
        </dl>
        <div v-if="status" class="mt-3 text-xs text-neutral-500">
          server pubkey:
          <span class="font-mono text-neutral-300">{{ status.public_key }}</span>
          · fwmark <span class="font-mono">{{ status.fwmark }}</span>
          · пиров в ядре: {{ status.peers.length }}
        </div>
        <div v-else-if="detail?.status_error" class="mt-3 text-xs text-amber-400">
          kernel: {{ detail.status_error }}
        </div>
      </header>

      <section
        v-if="status"
        class="relative rounded-lg border border-neutral-800 bg-neutral-900/30 p-3 overflow-hidden"
      >
        <div class="flex items-center gap-4 relative">
          <div class="text-xs uppercase text-neutral-500">Трафик</div>
          <div class="font-mono text-sm flex gap-4">
            <span class="text-emerald-400">↓ {{ formatRate(ifaceRateNow.rx) }}</span>
            <span class="text-sky-400">↑ {{ formatRate(ifaceRateNow.tx) }}</span>
          </div>
          <div class="ml-auto text-[10px] uppercase text-neutral-600">
            последние ~2 минуты
          </div>
        </div>
        <div class="mt-2 flex flex-col">
          <Sparkline
            :values="ifaceRx"
            color="#10b981"
            :height="40"
            :width="800"
            :opacity="0.9"
            :fill="true"
          />
          <Sparkline
            :values="ifaceTx"
            color="#60a5fa"
            :height="40"
            :width="800"
            :opacity="0.9"
            :fill="true"
            mirror
          />
        </div>
      </section>

      <section>
        <div class="flex items-center mb-3">
          <h2 class="text-lg font-semibold">Пиры</h2>
          <span v-if="readOnly" class="ml-2 text-xs text-neutral-500">read-only (managed by tier-2 plan)</span>
          <button
            v-if="!readOnly"
            @click="addOpen = true"
            class="ml-auto px-3 py-1 bg-emerald-600 hover:bg-emerald-500 rounded text-sm"
          >+ Добавить пира</button>
        </div>

        <table v-if="peers.length" class="w-full text-sm">
          <thead class="text-left text-xs uppercase text-neutral-500 border-b border-neutral-800">
            <tr>
              <th class="px-2 py-2 font-medium">Имя</th>
              <th class="px-2 py-2 font-medium">Адрес</th>
              <th class="px-2 py-2 font-medium">Handshake</th>
              <th class="px-2 py-2 font-medium">Rx</th>
              <th class="px-2 py-2 font-medium">Tx</th>
              <th class="px-2 py-2 font-medium">График</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <PeerRow
              v-for="p in peers"
              :key="p.id"
              :peer="p"
              :status="peerStatus(p)"
              :read-only="readOnly"
              @config="(peer) => (configPeer = peer)"
              @edit="(peer) => (editPeer = peer)"
              @delete="(peer) => (deletePeer = peer)"
            />
          </tbody>
        </table>
        <div v-else class="text-neutral-500 text-sm py-6">Пиров нет.</div>
      </section>
    </template>

    <AddPeerModal
      v-if="addOpen"
      :iface-name="name"
      @close="addOpen = false"
      @created="onCreated"
    />
    <PeerConfigModal
      v-if="configPeer"
      :peer="configPeer"
      @close="configPeer = null"
    />
    <EditPeerModal
      v-if="editPeer"
      :peer="editPeer"
      @close="editPeer = null"
      @updated="onUpdated"
    />

    <Modal
      v-if="deletePeer"
      :title="`Удалить ${deletePeer.name}?`"
      @close="deletePeer = null"
    >
      <p class="text-sm text-neutral-300">
        Пир будет удалён из ядра и БД. Конфиг перестанет работать.
      </p>
      <template #footer>
        <button
          @click="deletePeer = null"
          class="px-3 py-1 border border-neutral-700 hover:bg-neutral-800 rounded"
        >Отмена</button>
        <button
          :disabled="deletingBusy"
          @click="confirmDelete"
          class="px-3 py-1 bg-rose-600 hover:bg-rose-500 disabled:opacity-50 rounded"
        >
          {{ deletingBusy ? '…' : 'Удалить' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
