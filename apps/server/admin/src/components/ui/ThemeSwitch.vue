<script setup lang="ts">
import { Monitor, Moon, Sun } from 'lucide-vue-next';

type Theme = 'light' | 'dark' | 'system';

defineProps<{ modelValue: Theme }>();
const emit = defineEmits<{ 'update:modelValue': [value: Theme] }>();

const choices = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'Match system', icon: Monitor },
] as const;
</script>

<template>
  <fieldset class="m-0 min-w-0 border-0 p-0">
    <legend class="sr-only">Appearance</legend>
    <div class="bg-muted inline-flex items-center gap-0.5 rounded-lg p-0.5">
      <label v-for="choice in choices" :key="choice.value" :title="choice.label" class="relative">
        <input
          type="radio"
          name="theme"
          :value="choice.value"
          :checked="modelValue === choice.value"
          class="peer sr-only"
          @change="emit('update:modelValue', choice.value)"
        />
        <span
          class="text-muted-foreground hover:text-foreground peer-checked:bg-card peer-checked:text-foreground peer-focus-visible:outline-ring grid size-7 place-items-center rounded-md transition-colors peer-checked:shadow-xs peer-focus-visible:outline-2"
        >
          <component :is="choice.icon" class="size-4" aria-hidden="true" />
          <span class="sr-only">{{ choice.label }}</span>
        </span>
      </label>
    </div>
  </fieldset>
</template>
