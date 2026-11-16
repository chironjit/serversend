<script setup lang="ts">
const model = defineModel<string>({ default: '' });
</script>

<template>
  <input
    v-model="model"
    class="border-input bg-card placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/25 h-9 w-full min-w-0 rounded-md border px-3 text-sm shadow-xs transition-[border-color,box-shadow] focus-visible:ring-3 focus-visible:outline-none disabled:opacity-50"
  />
</template>
