<script setup lang="ts">
withDefaults(defineProps<{ as?: string }>(), { as: 'div' });
</script>

<template>
  <component :is="as" class="bg-card rounded-xl border shadow-xs">
    <slot />
  </component>
</template>
