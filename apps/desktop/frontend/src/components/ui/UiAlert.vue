<script setup lang="ts">
import { CircleAlert } from 'lucide-vue-next';
</script>

<template>
  <div
    role="alert"
    class="border-destructive/30 bg-destructive/8 text-destructive flex items-start gap-2.5 rounded-lg border px-3.5 py-2.5 text-sm"
  >
    <CircleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
    <p class="m-0 min-w-0 wrap-anywhere"><slot /></p>
  </div>
</template>
