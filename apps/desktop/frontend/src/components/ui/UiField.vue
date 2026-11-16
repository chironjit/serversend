<script setup lang="ts">
defineProps<{ label: string; hint?: string }>();
</script>

<template>
  <label class="grid gap-1.5">
    <span class="text-sm leading-none font-medium">{{ label }}</span>
    <slot />
    <span v-if="hint" class="text-muted-foreground text-xs">{{ hint }}</span>
  </label>
</template>
