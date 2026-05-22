<script setup lang="ts">
defineProps<{ title?: string }>();
defineEmits<{ (e: 'close'): void }>();
</script>

<template>
  <div
    class="fixed inset-0 z-50 bg-black/70 flex items-center justify-center p-4"
    @click.self="$emit('close')"
  >
    <div class="bg-neutral-900 border border-neutral-800 rounded-lg w-full max-w-md">
      <div v-if="title || $slots.header" class="px-4 py-3 border-b border-neutral-800 flex items-center">
        <div class="font-semibold">
          <slot name="header">{{ title }}</slot>
        </div>
        <button
          @click="$emit('close')"
          class="ml-auto text-neutral-500 hover:text-neutral-200"
          aria-label="Close"
        >✕</button>
      </div>
      <div class="p-4">
        <slot />
      </div>
      <div v-if="$slots.footer" class="px-4 py-3 border-t border-neutral-800 flex justify-end gap-2">
        <slot name="footer" />
      </div>
    </div>
  </div>
</template>
