<script setup lang="ts">
withDefaults(defineProps<{ block?: boolean }>(), { block: false });
</script>

<template>
  <code
    class="bg-muted text-foreground rounded-md font-mono text-[0.8125rem] leading-relaxed"
    :class="block ? 'block px-3.5 py-2.5 wrap-anywhere' : 'px-1.5 py-0.5'"
  >
    <slot />
  </code>
</template>
