<script setup lang="ts">
import { onMounted, ref } from 'vue';
import Modal from './Modal.vue';
import { api, ApiError } from '../api/client';
import type { Exit, Peer } from '../api/types';

const props = defineProps<{ peer: Peer }>();
const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'updated', peer: Peer): void;
}>();

const name = ref(props.peer.name);
const notes = ref(props.peer.notes);
const enabled = ref(props.peer.enabled);
const exitId = ref<number | null>(props.peer.default_exit_id ?? null);
const initialExitId = props.peer.default_exit_id ?? null;
const exits = ref<Exit[]>([]);
const busy = ref(false);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    exits.value = (await api.exits()).filter((e) => e.enabled);
  } catch {
    /* non-fatal */
  }
});

async function submit() {
  busy.value = true;
  error.value = null;
  try {
    let updated = await api.updatePeer(props.peer.id, {
      name: name.value,
      notes: notes.value,
      enabled: enabled.value,
    });
    // exit is a separate endpoint (tier-1 semantic is different)
    if (exitId.value !== initialExitId) {
      updated = await api.setPeerExit(props.peer.id, exitId.value);
    }
    emit('updated', updated);
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'update failed';
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal :title="`Изменить: ${peer.name}`" @close="emit('close')">
    <form @submit.prevent="submit" class="space-y-3">
      <label class="block">
        <span class="text-xs text-neutral-400">Имя</span>
        <input
          v-model="name"
          class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
        />
      </label>
      <label class="block">
        <span class="text-xs text-neutral-400">Заметки</span>
        <textarea
          v-model="notes"
          rows="3"
          class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1 text-sm"
        ></textarea>
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

      <label class="flex items-center gap-2 text-sm">
        <input v-model="enabled" type="checkbox" class="accent-emerald-600" />
        <span>Активен</span>
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
        {{ busy ? '…' : 'Сохранить' }}
      </button>
    </template>
  </Modal>
</template>
