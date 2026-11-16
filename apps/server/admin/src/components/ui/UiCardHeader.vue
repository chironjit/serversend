<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string;
    description?: string;
    titleId?: string;
    /** Heading element, so each screen keeps one h1 and a sensible outline. */
    level?: 'h1' | 'h2' | 'h3';
    size?: 'md' | 'lg';
  }>(),
  { level: 'h2', size: 'md' },
);
</script>

<template>
  <div class="flex items-start justify-between gap-4">
    <div class="grid min-w-0 gap-1.5">
      <component
        :is="level"
        :id="titleId"
        class="m-0 font-semibold tracking-tight"
        :class="size === 'lg' ? 'text-xl' : 'text-base leading-snug'"
      >
        {{ title }}
      </component>
      <p v-if="description || $slots.description" class="text-muted-foreground m-0 text-sm">
        <slot name="description">{{ description }}</slot>
      </p>
    </div>
    <slot name="action" />
  </div>
</template>
