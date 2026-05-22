<script setup lang="ts">
import { ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { api, ApiError } from '../api/client';
import { markAuthenticated } from '../router';

const username = ref('');
const password = ref('');
const error = ref<string | null>(null);
const busy = ref(false);
const router = useRouter();
const route = useRoute();

async function submit() {
  error.value = null;
  busy.value = true;
  try {
    await api.login(username.value, password.value);
    markAuthenticated();
    const next = typeof route.query.next === 'string' ? route.query.next : '/';
    router.push(next);
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'login failed';
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <form
    @submit.prevent="submit"
    class="w-80 bg-neutral-900 border border-neutral-800 rounded-lg p-6 space-y-4"
  >
    <h1 class="text-lg font-semibold">wg-admin</h1>

    <label class="block">
      <span class="text-xs text-neutral-400">Имя</span>
      <input
        v-model="username"
        autofocus
        autocomplete="username"
        class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1 focus:border-emerald-500 focus:outline-none"
      />
    </label>

    <label class="block">
      <span class="text-xs text-neutral-400">Пароль</span>
      <input
        v-model="password"
        type="password"
        autocomplete="current-password"
        class="mt-1 w-full bg-neutral-950 border border-neutral-700 rounded px-2 py-1 focus:border-emerald-500 focus:outline-none"
      />
    </label>

    <div v-if="error" class="text-sm text-rose-400">{{ error }}</div>

    <button
      type="submit"
      :disabled="busy"
      class="w-full bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 text-white py-1.5 rounded"
    >
      {{ busy ? '…' : 'Войти' }}
    </button>
  </form>
</template>
