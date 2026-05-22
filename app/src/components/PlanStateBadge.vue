<script setup lang="ts">
import { computed } from 'vue';
import type { PlanState } from '../api/types';

const props = defineProps<{ state: PlanState }>();

const classes = computed(() => {
  switch (props.state) {
    case 'pending':   return 'bg-sky-900/60 text-sky-200 border-sky-700';
    case 'applied':   return 'bg-amber-900/60 text-amber-200 border-amber-700';
    case 'confirmed': return 'bg-emerald-900/60 text-emerald-200 border-emerald-700';
    case 'reverted':  return 'bg-rose-900/60 text-rose-200 border-rose-700';
    case 'expired':   return 'bg-neutral-800 text-neutral-300 border-neutral-700';
    default:          return 'bg-neutral-800 text-neutral-300 border-neutral-700';
  }
});
</script>

<template>
  <span class="text-xs px-1.5 py-0.5 rounded border" :class="classes">{{ state }}</span>
</template>
