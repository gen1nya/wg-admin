<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';

const props = defineProps<{
  // unix seconds, deadline = applied_at + timeout_sec
  deadline: number;
}>();

const now = ref(Math.floor(Date.now() / 1000));
let timer: ReturnType<typeof setInterval> | null = null;

onMounted(() => {
  timer = setInterval(() => {
    now.value = Math.floor(Date.now() / 1000);
  }, 250);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});

const remaining = computed(() => Math.max(0, props.deadline - now.value));
const critical = computed(() => remaining.value < 10);
</script>

<template>
  <div
    class="font-mono"
    :class="critical ? 'text-rose-400 animate-pulse' : 'text-amber-400'"
  >
    {{ remaining }}s до автоотката
  </div>
</template>
