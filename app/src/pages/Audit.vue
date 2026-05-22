<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { api, ApiError } from '../api/client';
import type { AuditEntry } from '../api/types';

const entries = ref<AuditEntry[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

async function load() {
  try {
    entries.value = await api.audit(200);
    error.value = null;
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  } finally {
    loading.value = false;
  }
}

interface Group {
  day: string;
  items: AuditEntry[];
}

const grouped = computed<Group[]>(() => {
  const groups: Group[] = [];
  let current: Group | null = null;
  for (const e of entries.value) {
    const day = new Date(e.ts * 1000).toLocaleDateString();
    if (!current || current.day !== day) {
      current = { day, items: [] };
      groups.push(current);
    }
    current.items.push(e);
  }
  return groups;
});

function prettyPayload(p: string): string {
  if (!p) return '';
  try {
    return JSON.stringify(JSON.parse(p));
  } catch {
    return p;
  }
}

function timeOnly(ts: number): string {
  return new Date(ts * 1000).toLocaleTimeString();
}

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center">
      <h1 class="text-xl font-semibold">Журнал</h1>
      <button
        @click="load"
        class="ml-auto text-sm border border-neutral-700 hover:bg-neutral-800 px-2 py-1 rounded"
      >Обновить</button>
    </div>

    <div v-if="loading" class="text-neutral-400">Загрузка…</div>
    <div v-else-if="error" class="text-rose-400">{{ error }}</div>
    <div v-else-if="entries.length === 0" class="text-neutral-400">Записей нет.</div>

    <div v-else class="space-y-6">
      <div v-for="g in grouped" :key="g.day">
        <h2 class="text-xs uppercase text-neutral-500 mb-2 border-b border-neutral-800 pb-1">
          {{ g.day }}
        </h2>
        <ul class="space-y-1 text-sm">
          <li
            v-for="e in g.items"
            :key="e.id"
            class="flex items-start gap-3 font-mono"
          >
            <span class="text-neutral-500 text-xs w-20 shrink-0 pt-0.5">{{ timeOnly(e.ts) }}</span>
            <span class="text-sky-400 w-24 shrink-0">{{ e.actor }}</span>
            <span class="text-emerald-400 w-28 shrink-0">{{ e.action }}</span>
            <span class="text-neutral-400 w-20 shrink-0">{{ e.entity_type }}{{ e.entity_id ? `#${e.entity_id}` : '' }}</span>
            <span class="text-neutral-300 break-all text-xs pt-0.5">{{ prettyPayload(e.payload) }}</span>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
