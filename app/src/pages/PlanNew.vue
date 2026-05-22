<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import { api, ApiError } from '../api/client';

const EXAMPLE_OBJ = {
  ipsets: [{ name: 'direct', entries: ['77.88.0.0/16', '87.250.0.0/16'] }],
  rules: [{ priority: 10500, fwmark: 1, table: 'wg_tunnel' }],
  routes: [{ table: 'wg_tunnel', dest: 'default', dev: 'wg-exit-a' }],
  nft: {
    body:
      'chain forward {\n' +
      '\ttype filter hook forward priority 0; policy accept;\n' +
      '}',
  },
};
const EXAMPLE = JSON.stringify(EXAMPLE_OBJ, null, 2);

const router = useRouter();

const description = ref('');
const bodyText = ref<string>(EXAMPLE);
const busy = ref(false);
const error = ref<string | null>(null);
const parseError = ref<string | null>(null);

const parsed = computed(() => {
  parseError.value = null;
  const s = bodyText.value.trim();
  if (!s) return null;
  try {
    return JSON.parse(s);
  } catch (e) {
    parseError.value = e instanceof Error ? e.message : 'invalid JSON';
    return null;
  }
});

async function submit() {
  if (!parsed.value) {
    error.value = 'DesiredState должен быть валидным JSON';
    return;
  }
  busy.value = true;
  error.value = null;
  try {
    const { plan } = await api.createPlan(description.value.trim(), parsed.value);
    router.push({ name: 'plan', params: { id: plan.id } });
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'create failed';
  } finally {
    busy.value = false;
  }
}

function insertExample() {
  bodyText.value = EXAMPLE;
}

function clearBody() {
  bodyText.value = '';
}
</script>

<template>
  <div class="space-y-4 max-w-4xl">
    <header class="flex items-center gap-3 border-b border-neutral-800 pb-3">
      <h1 class="text-xl font-semibold">Новый план</h1>
      <RouterLink
        :to="{ name: 'plans' }"
        class="ml-auto text-sm text-sky-400 hover:text-sky-300"
      >← к списку</RouterLink>
    </header>

    <p class="text-sm text-neutral-400">
      Тело — объект <code class="text-neutral-200 font-mono">DesiredState</code>. Упомянутые ресурсы агент
      подведёт к этому состоянию; неупомянутые не тронет. См.
      <code class="text-neutral-200 font-mono">docs/project.md</code> и
      <code class="text-neutral-200 font-mono">plan.DesiredState</code> в
      <code class="text-neutral-200 font-mono">internal/plan/types.go</code>.
    </p>

    <label class="block">
      <span class="text-xs text-neutral-400">Описание (необязательно)</span>
      <input
        v-model="description"
        placeholder="например, telegram-dc split to exit-a"
        class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1"
      />
    </label>

    <label class="block">
      <div class="flex items-center gap-2">
        <span class="text-xs text-neutral-400">DesiredState (JSON)</span>
        <button
          type="button"
          @click="insertExample"
          class="ml-auto text-xs text-sky-400 hover:text-sky-300"
        >подставить пример</button>
        <button
          type="button"
          @click="clearBody"
          class="text-xs text-neutral-500 hover:text-neutral-300"
        >очистить</button>
      </div>
      <textarea
        v-model="bodyText"
        spellcheck="false"
        rows="22"
        class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded p-2 font-mono text-xs"
        :class="{ 'border-rose-600': parseError }"
      ></textarea>
      <div v-if="parseError" class="text-xs text-rose-400 mt-1">JSON: {{ parseError }}</div>
    </label>

    <div v-if="error" class="text-sm text-rose-400">{{ error }}</div>

    <div class="flex gap-2">
      <button
        @click="submit"
        :disabled="busy || !parsed"
        class="px-4 py-2 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 rounded font-semibold"
      >
        {{ busy ? '…' : 'Создать план' }}
      </button>
      <p class="text-xs text-neutral-500 self-center">
        После создания план останется в состоянии <code class="text-neutral-300">pending</code>,
        apply/confirm — на детальной странице.
      </p>
    </div>
  </div>
</template>
