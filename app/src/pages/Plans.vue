<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { RouterLink } from 'vue-router';
import { api, ApiError } from '../api/client';
import { formatTimestamp } from '../api/format';
import PlanStateBadge from '../components/PlanStateBadge.vue';
import type { Plan } from '../api/types';

const plans = ref<Plan[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

async function load() {
  try {
    plans.value = await api.plans(50);
    error.value = null;
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  } finally {
    loading.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center gap-2">
      <h1 class="text-xl font-semibold">Планы</h1>
      <RouterLink
        :to="{ name: 'plan-new' }"
        class="ml-auto text-sm bg-emerald-600 hover:bg-emerald-500 px-3 py-1 rounded"
      >+ Новый</RouterLink>
      <button
        @click="load"
        class="text-sm border border-neutral-700 hover:bg-neutral-800 px-2 py-1 rounded"
      >Обновить</button>
    </div>

    <div v-if="loading" class="text-neutral-400">Загрузка…</div>
    <div v-else-if="error" class="text-rose-400">{{ error }}</div>
    <div v-else-if="plans.length === 0" class="text-neutral-400">Планов нет.</div>

    <table v-else class="w-full text-sm">
      <thead class="text-left text-xs uppercase text-neutral-500 border-b border-neutral-800">
        <tr>
          <th class="px-2 py-2 font-medium">#</th>
          <th class="px-2 py-2 font-medium">Создан</th>
          <th class="px-2 py-2 font-medium">Автор</th>
          <th class="px-2 py-2 font-medium">Описание</th>
          <th class="px-2 py-2 font-medium">Статус</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="p in plans"
          :key="p.id"
          class="border-b border-neutral-800 hover:bg-neutral-900/50"
        >
          <td class="px-2 py-2 font-mono text-neutral-400">{{ p.id }}</td>
          <td class="px-2 py-2 text-neutral-300">{{ formatTimestamp(p.created_at) }}</td>
          <td class="px-2 py-2 text-neutral-400">{{ p.created_by }}</td>
          <td class="px-2 py-2">{{ p.description || '—' }}</td>
          <td class="px-2 py-2"><PlanStateBadge :state="p.state" /></td>
          <td class="px-2 py-2 text-right">
            <RouterLink
              :to="{ name: 'plan', params: { id: p.id } }"
              class="text-sky-400 hover:text-sky-300"
            >детали →</RouterLink>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
