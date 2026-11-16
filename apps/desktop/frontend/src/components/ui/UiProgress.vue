<script setup lang="ts">
defineProps<{ value: number }>();
</script>

<template>
  <progress class="ui-progress" :value="value" max="100">{{ value }}%</progress>
</template>

<style scoped>
.ui-progress {
  display: block;
  width: 100%;
  height: 6px;
  overflow: hidden;
  appearance: none;
  background: var(--muted);
  border: 0;
  border-radius: 999px;
}
.ui-progress::-webkit-progress-bar {
  background: var(--muted);
}
.ui-progress::-webkit-progress-value {
  background: var(--primary);
  border-radius: 999px;
  transition: width 0.3s ease;
}
.ui-progress::-moz-progress-bar {
  background: var(--primary);
  border-radius: 999px;
}
</style>
