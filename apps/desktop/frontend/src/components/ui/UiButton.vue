<script setup lang="ts">
type Variant = 'primary' | 'secondary' | 'outline' | 'ghost' | 'destructive';
type Size = 'md' | 'sm' | 'icon' | 'icon-sm';

withDefaults(defineProps<{ variant?: Variant; size?: Size; type?: 'button' | 'submit' }>(), {
  variant: 'primary',
  size: 'md',
  type: 'button',
});

const variants: Record<Variant, string> = {
  primary: 'bg-primary text-primary-foreground shadow-xs hover:bg-primary/90',
  secondary: 'bg-muted text-foreground hover:bg-muted/70',
  outline: 'border border-input bg-card shadow-xs hover:bg-accent hover:text-accent-foreground',
  ghost: 'hover:bg-accent hover:text-accent-foreground',
  destructive: 'bg-destructive text-destructive-foreground shadow-xs hover:bg-destructive/90',
};
const sizes: Record<Size, string> = {
  md: 'h-9 px-4',
  sm: 'h-8 px-3',
  icon: 'size-9',
  'icon-sm': 'size-8',
};
</script>

<template>
  <button
    :type="type"
    class="inline-flex shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap transition-colors select-none disabled:cursor-default disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0"
    :class="[variants[variant], sizes[size]]"
  >
    <slot />
  </button>
</template>
