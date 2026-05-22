<script setup lang="ts">
import { onMounted, ref } from 'vue';
import Modal from './Modal.vue';
import { api, ApiError } from '../api/client';
import type { Exit, Peer } from '../api/types';

const props = defineProps<{ ifaceName: string }>();
const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'created', peer: Peer): void;
}>();

const name = ref('');
const address = ref('');
const notes = ref('');
const exitId = ref<number | null>(null);
const exits = ref<Exit[]>([]);
const busy = ref(false);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    exits.value = (await api.exits()).filter((e) => e.enabled);
  } catch {
    /* non-fatal: exits list may be empty / unavailable */
  }
});

async function submit() {
  if (!name.value.trim()) {
    error.value = 'имя обязательно';
    return;
  }
  busy.value = true;
  error.value = null;
  try {
    const peer = await api.createPeer(props.ifaceName, {
      name: name.value.trim(),
      address: address.value.trim() || undefined,
      default_exit_id: exitId.value ?? undefined,
      notes: notes.value.trim() || undefined,
    });
    emit('created', peer);
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'create failed';
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal title="Добавить пира" @close="emit('close')">
    <form @submit.prevent="submit" class="space-y-3">
      <label class="block">
        <span class="text-xs text-neutral-400">Имя</span>
        <input
          v-model="name"
          autofocus
          class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
        />
      </label>

      <label class="block">
        <span class="text-xs text-neutral-400">Адрес (опционально, /32)</span>
        <input
          v-model="address"
          placeholder="авто"
          class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1 font-mono text-sm"
        />
      </label>

      <label v-if="exits.length" class="block">
        <span class="text-xs text-neutral-400">Exit</span>
        <select
          v-model="exitId"
          class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
        >
          <option :value="null">— по умолчанию (интерфейс) —</option>
          <option v-for="ex in exits" :key="ex.id" :value="ex.id">
            {{ ex.name }} ({{ ex.kind }})
          </option>
        </select>
      </label>

      <label class="block">
        <span class="text-xs text-neutral-400">Заметки</span>
        <textarea
          v-model="notes"
          rows="2"
          class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1 text-sm"
        ></textarea>
      </label>

      <div v-if="error" class="text-sm text-rose-400">{{ error }}</div>
    </form>

    <template #footer>
      <button
        @click="emit('close')"
        class="px-3 py-1 border border-neutral-700 hover:bg-neutral-800 rounded"
      >Отмена</button>
      <button
        @click="submit"
        :disabled="busy"
        class="px-3 py-1 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 rounded"
      >
        {{ busy ? '…' : 'Создать' }}
      </button>
    </template>
  </Modal>
</template>
