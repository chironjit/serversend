<script setup lang="ts">
defineProps<{ title: string }>();
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-1.5 px-6 py-12 text-center">
    <div
      class="bg-muted text-muted-foreground mb-2 grid size-10 place-items-center rounded-lg [&_svg]:size-5"
      aria-hidden="true"
    >
      <slot name="icon" />
    </div>
    <p class="m-0 font-medium">{{ title }}</p>
    <p class="text-muted-foreground m-0 max-w-xs text-sm text-balance"><slot /></p>
  </div>
</template>
