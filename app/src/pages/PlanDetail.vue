<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { api, ApiError } from '../api/client';
import { formatTimestamp } from '../api/format';
import PlanStateBadge from '../components/PlanStateBadge.vue';
import PlanDiff from '../components/PlanDiff.vue';
import Countdown from '../components/Countdown.vue';
import type { Diff, Plan } from '../api/types';

const props = defineProps<{ id: number }>();

const plan = ref<Plan | null>(null);
const loading = ref(true);
const error = ref<string | null>(null);
const actionError = ref<string | null>(null);
const busy = ref(false);

const selectedTimeout = ref(60);
const showSnapshot = ref(false);
const showDesired = ref(false);

let pollTimer: ReturnType<typeof setInterval> | null = null;

const state = computed(() => plan.value?.state);
const deadline = computed(() => {
  if (!plan.value?.applied_at || !plan.value?.timeout_sec) return 0;
  return plan.value.applied_at + plan.value.timeout_sec;
});

const diff = computed<Diff | null>(() => {
  if (!plan.value?.diff) return null;
  try {
    return JSON.parse(plan.value.diff) as Diff;
  } catch {
    return null;
  }
});

const prettyDesired = computed(() => pretty(plan.value?.desired));
const prettySnapshot = computed(() => pretty(plan.value?.snapshot_pre));

function pretty(s: string | undefined | null): string {
  if (!s) return '';
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

async function load() {
  try {
    plan.value = await api.getPlan(props.id);
    error.value = null;
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'load failed';
  } finally {
    loading.value = false;
  }
}

function startPolling() {
  stopPolling();
  pollTimer = setInterval(load, 1000);
}
function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

watch(state, (s) => {
  if (s === 'applied') startPolling();
  else stopPolling();
}, { immediate: false });

async function doApply() {
  if (!plan.value) return;
  busy.value = true;
  actionError.value = null;
  try {
    plan.value = await api.applyPlan(plan.value.id, selectedTimeout.value);
  } catch (e) {
    actionError.value = e instanceof ApiError ? e.message : 'apply failed';
  } finally {
    busy.value = false;
  }
}

async function doConfirm() {
  if (!plan.value) return;
  busy.value = true;
  actionError.value = null;
  try {
    plan.value = await api.confirmPlan(plan.value.id);
  } catch (e) {
    actionError.value = e instanceof ApiError ? e.message : 'confirm failed';
  } finally {
    busy.value = false;
  }
}

async function doRevert() {
  if (!plan.value) return;
  if (!confirm('Откатить изменения?')) return;
  busy.value = true;
  actionError.value = null;
  try {
    plan.value = await api.revertPlan(plan.value.id);
  } catch (e) {
    actionError.value = e instanceof ApiError ? e.message : 'revert failed';
  } finally {
    busy.value = false;
  }
}

onMounted(async () => {
  await load();
  if (state.value === 'applied') startPolling();
});
onUnmounted(stopPolling);
</script>

<template>
  <div class="space-y-6">
    <div v-if="loading" class="text-neutral-400">Загрузка…</div>
    <div v-else-if="error" class="text-rose-400">{{ error }}</div>
    <template v-else-if="plan">
      <header class="flex items-center gap-3 border-b border-neutral-800 pb-4">
        <h1 class="text-xl font-semibold">План #{{ plan.id }}</h1>
        <PlanStateBadge :state="plan.state" />
        <div class="ml-auto text-xs text-neutral-500">
          создан {{ formatTimestamp(plan.created_at) }} автором {{ plan.created_by }}
        </div>
      </header>

      <section v-if="plan.description">
        <p class="text-neutral-300">{{ plan.description }}</p>
      </section>

      <section>
        <h2 class="font-semibold mb-2">Diff</h2>
        <PlanDiff :diff="diff" />
      </section>

      <section>
        <details :open="showSnapshot" @toggle="(e: Event) => (showSnapshot = (e.target as HTMLDetailsElement).open)">
          <summary class="cursor-pointer text-neutral-400 hover:text-neutral-200 text-sm">Snapshot (pre-apply)</summary>
          <pre class="mt-2 bg-neutral-950 border border-neutral-800 rounded p-3 overflow-x-auto text-xs font-mono">{{ prettySnapshot || '—' }}</pre>
        </details>
      </section>

      <section>
        <details :open="showDesired" @toggle="(e: Event) => (showDesired = (e.target as HTMLDetailsElement).open)">
          <summary class="cursor-pointer text-neutral-400 hover:text-neutral-200 text-sm">Desired state</summary>
          <pre class="mt-2 bg-neutral-950 border border-neutral-800 rounded p-3 overflow-x-auto text-xs font-mono">{{ prettyDesired || '—' }}</pre>
        </details>
      </section>

      <section v-if="actionError" class="text-rose-400">{{ actionError }}</section>

      <!-- Pending → apply -->
      <section
        v-if="plan.state === 'pending'"
        class="border border-neutral-800 rounded-lg p-4 bg-neutral-900/40 space-y-3"
      >
        <div class="flex items-center gap-3">
          <label class="text-sm text-neutral-400">Таймаут автоотката:</label>
          <select
            v-model.number="selectedTimeout"
            class="bg-neutral-950 border border-neutral-700 rounded px-2 py-1 text-sm"
          >
            <option :value="30">30 сек</option>
            <option :value="60">60 сек</option>
            <option :value="120">120 сек</option>
          </select>
        </div>
        <button
          @click="doApply"
          :disabled="busy"
          class="w-full py-3 bg-amber-600 hover:bg-amber-500 disabled:opacity-50 rounded font-semibold"
        >
          {{ busy ? '…' : 'Apply' }}
        </button>
      </section>

      <!-- Applied → big confirm + revert + countdown -->
      <section
        v-else-if="plan.state === 'applied'"
        class="border-2 border-amber-600 rounded-lg p-4 bg-amber-900/20 space-y-4"
      >
        <div class="flex items-center gap-3">
          <Countdown v-if="deadline" :deadline="deadline" class="text-lg" />
          <div class="ml-auto text-xs text-neutral-400">
            applied {{ formatTimestamp(plan.applied_at) }}
          </div>
        </div>
        <button
          @click="doConfirm"
          :disabled="busy"
          class="w-full py-8 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 rounded text-xl font-bold uppercase tracking-wider"
        >
          {{ busy ? '…' : '✓ Confirm' }}
        </button>
        <button
          @click="doRevert"
          :disabled="busy"
          class="w-full py-2 bg-rose-700 hover:bg-rose-600 disabled:opacity-50 rounded text-sm"
        >
          Откатить сейчас
        </button>
      </section>

      <!-- Terminal states -->
      <section
        v-else
        class="border border-neutral-800 rounded p-3 text-sm text-neutral-400"
      >
        <div v-if="plan.state === 'confirmed'">
          Подтверждён {{ formatTimestamp(plan.confirmed_at) }}.
        </div>
        <div v-else-if="plan.state === 'reverted'">
          Откачен {{ formatTimestamp(plan.reverted_at) }}.
        </div>
        <div v-else-if="plan.state === 'expired'">
          Откачен watchdog'ом — причина: нет confirm в срок.
        </div>
      </section>
    </template>
  </div>
</template>
