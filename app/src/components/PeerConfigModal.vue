<script setup lang="ts">
import { onMounted, ref } from 'vue';
import QRCode from 'qrcode';
import Modal from './Modal.vue';
import { api, ApiError } from '../api/client';
import type { Peer } from '../api/types';

const props = defineProps<{ peer: Peer }>();
const emit = defineEmits<{ (e: 'close'): void }>();

const config = ref<string | null>(null);
const qrDataUrl = ref<string | null>(null);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    const r = await api.peerConfig(props.peer.id);
    config.value = r.config;
    qrDataUrl.value = await QRCode.toDataURL(r.config, { margin: 1, width: 280 });
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  }
});

function copyConf() {
  if (config.value) navigator.clipboard?.writeText(config.value);
}
</script>

<template>
  <Modal :title="`Конфиг: ${peer.name}`" @close="emit('close')">
    <div v-if="error" class="text-rose-400">{{ error }}</div>
    <div v-else-if="!qrDataUrl" class="text-neutral-400">Загрузка…</div>
    <div v-else class="space-y-3">
      <div class="flex justify-center bg-white p-2 rounded">
        <img :src="qrDataUrl" alt="QR" width="280" height="280" />
      </div>
      <details class="text-xs">
        <summary class="cursor-pointer text-neutral-400 hover:text-neutral-200">показать текст .conf</summary>
        <pre class="mt-2 bg-neutral-950 border border-neutral-800 rounded p-2 overflow-x-auto text-neutral-200 font-mono">{{ config }}</pre>
      </details>
    </div>

    <template #footer>
      <button
        @click="copyConf"
        class="px-3 py-1 border border-neutral-700 hover:bg-neutral-800 rounded"
      >Копировать</button>
      <a
        :href="api.peerConfigDownloadURL(peer.id)"
        class="px-3 py-1 bg-emerald-600 hover:bg-emerald-500 rounded"
      >Скачать .conf</a>
    </template>
  </Modal>
</template>
