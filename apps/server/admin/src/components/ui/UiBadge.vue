<script setup lang="ts">
type Tone = 'neutral' | 'primary' | 'success' | 'warning' | 'danger';

withDefaults(defineProps<{ tone?: Tone; dot?: boolean }>(), { tone: 'neutral' });

const tones: Record<Tone, string> = {
  neutral: 'bg-muted text-muted-foreground',
  primary: 'bg-accent text-accent-foreground',
  success: 'bg-success/12 text-success',
  warning: 'bg-warning/15 text-warning',
  danger: 'bg-destructive/10 text-destructive',
};
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium whitespace-nowrap tabular-nums"
    :class="tones[tone]"
  >
    <span v-if="dot" class="size-1.5 rounded-full bg-current" aria-hidden="true" />
    <slot />
  </span>
</template>
