<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router';
import { api, ApiError } from './api/client';
import { invalidateAuth } from './router';
import { startTrafficPolling } from './stores/traffic';

const route = useRoute();
const router = useRouter();

const kernelMode = ref<string | null>(null);
const agentVersion = ref<string | null>(null);
const user = ref<string | null>(null);

const isLogin = computed(() => route.name === 'login');

async function refreshStatus() {
  try {
    const s = await api.status();
    kernelMode.value = s.kernel_mode;
    agentVersion.value = s.kernel_mode;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      kernelMode.value = null;
    }
  }
}

async function refreshWhoami() {
  try {
    const w = await api.whoami();
    user.value = w.user;
  } catch {
    user.value = null;
  }
}

async function logout() {
  try {
    await api.logout();
  } finally {
    invalidateAuth();
    user.value = null;
    router.push({ name: 'login' });
  }
}

onMounted(() => {
  refreshWhoami();
  refreshStatus();
  // один глобальный пуллинг трафика, живёт пока SPA открыта
  startTrafficPolling();
});
</script>

<template>
  <div v-if="isLogin" class="min-h-full flex items-center justify-center">
    <RouterView />
  </div>
  <div v-else class="min-h-full flex flex-col">
    <header class="border-b border-neutral-800 bg-neutral-900/60">
      <div class="max-w-6xl mx-auto px-4 h-14 flex items-center gap-6">
        <RouterLink to="/" class="font-semibold tracking-tight text-lg">wg-admin</RouterLink>
        <nav class="flex gap-4 text-sm text-neutral-300">
          <RouterLink
            to="/"
            class="hover:text-white"
            :class="{ 'text-white': route.name === 'overview' || route.name === 'interface' }"
          >Интерфейсы</RouterLink>
          <RouterLink
            to="/map"
            class="hover:text-white"
            :class="{ 'text-white': route.name === 'map' }"
          >Карта</RouterLink>
          <RouterLink
            to="/plans"
            class="hover:text-white"
            :class="{ 'text-white': route.name === 'plans' || route.name === 'plan' }"
          >Планы</RouterLink>
          <RouterLink
            to="/audit"
            class="hover:text-white"
            :class="{ 'text-white': route.name === 'audit' }"
          >Журнал</RouterLink>
        </nav>
        <div class="ml-auto flex items-center gap-3 text-sm text-neutral-400">
          <span v-if="user">{{ user }}</span>
          <button
            v-if="user"
            @click="logout"
            class="px-2 py-1 rounded border border-neutral-700 hover:bg-neutral-800"
          >Logout</button>
        </div>
      </div>
    </header>

    <main class="flex-1">
      <div class="max-w-6xl mx-auto px-4 py-6">
        <RouterView />
      </div>
    </main>

    <footer class="border-t border-neutral-800 bg-neutral-900/60 text-xs text-neutral-500">
      <div class="max-w-6xl mx-auto px-4 h-10 flex items-center justify-between">
        <div>
          kernel:
          <span
            class="font-mono"
            :class="kernelMode === 'mock' ? 'text-amber-400' : 'text-emerald-400'"
          >{{ kernelMode ?? '—' }}</span>
        </div>
        <div>wg-admin v0.1</div>
      </div>
    </footer>
  </div>
</template>
