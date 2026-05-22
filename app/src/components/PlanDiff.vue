<script setup lang="ts">
import { computed } from 'vue';
import type { Diff } from '../api/types';

const props = defineProps<{ diff: Diff | null }>();

const empty = computed(() => {
  const d = props.diff;
  if (!d) return true;
  const hasIp = (d.ipsets ?? []).some(s => s.created || (s.add?.length ?? 0) > 0 || (s.remove?.length ?? 0) > 0);
  const hasR = (d.routes ?? []).some(r => r.op !== 'noop');
  const hasRu = (d.rules ?? []).some(r => r.op !== 'noop');
  const hasN = d.nft && d.nft.op !== 'noop';
  return !hasIp && !hasR && !hasRu && !hasN;
});
</script>

<template>
  <div v-if="!diff" class="text-neutral-500 text-sm">diff отсутствует</div>
  <div v-else-if="empty" class="text-neutral-400 text-sm">no-op (ничего не изменится)</div>
  <div v-else class="space-y-3 text-sm">
    <div v-if="diff.ipsets?.length">
      <div class="text-xs uppercase text-neutral-500 mb-1">ipsets</div>
      <ul class="space-y-1">
        <li
          v-for="s in diff.ipsets"
          :key="s.name"
          class="flex flex-wrap items-center gap-2"
        >
          <span class="font-mono text-neutral-200">{{ s.name }}</span>
          <span v-if="s.created" class="text-emerald-400 text-xs">create</span>
          <span v-if="(s.add?.length ?? 0) > 0" class="text-emerald-400 text-xs">+{{ s.add!.length }}</span>
          <span v-if="(s.remove?.length ?? 0) > 0" class="text-rose-400 text-xs">-{{ s.remove!.length }}</span>
        </li>
      </ul>
    </div>

    <div v-if="diff.routes?.length">
      <div class="text-xs uppercase text-neutral-500 mb-1">routes</div>
      <ul class="space-y-1">
        <li v-for="r in diff.routes" :key="`${r.table}:${r.dest}`" class="flex items-center gap-2">
          <span class="font-mono text-neutral-200">{{ r.table }}:{{ r.dest }}</span>
          <span class="text-xs" :class="r.op === 'create' ? 'text-emerald-400' : r.op === 'update' ? 'text-amber-400' : 'text-neutral-500'">{{ r.op }}</span>
        </li>
      </ul>
    </div>

    <div v-if="diff.rules?.length">
      <div class="text-xs uppercase text-neutral-500 mb-1">ip rules</div>
      <ul class="space-y-1">
        <li v-for="r in diff.rules" :key="r.priority" class="flex items-center gap-2">
          <span class="font-mono text-neutral-200">pri {{ r.priority }}</span>
          <span class="text-xs">{{ r.op }}</span>
        </li>
      </ul>
    </div>

    <div v-if="diff.nft">
      <div class="text-xs uppercase text-neutral-500 mb-1">nftables</div>
      <div class="text-xs" :class="diff.nft.op === 'create' ? 'text-emerald-400' : diff.nft.op === 'replace' ? 'text-amber-400' : 'text-neutral-500'">
        {{ diff.nft.op }}
      </div>
    </div>
  </div>
</template>
