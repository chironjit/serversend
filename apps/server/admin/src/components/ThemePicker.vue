<script setup lang="ts">
import { Monitor, Moon, Sun } from 'lucide-vue-next';
import { useTheme } from '../theme';

const { preference, setPreference, storageUnavailable } = useTheme();
const choices = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'Match system', icon: Monitor },
] as const;
</script>

<template>
  <fieldset class="relative m-0 min-w-0 border-0 p-0">
    <legend class="sr-only">Appearance</legend>
    <div class="bg-muted inline-flex items-center gap-0.5 rounded-lg p-0.5">
      <label v-for="choice in choices" :key="choice.value" :title="choice.label" class="relative">
        <input
          type="radio"
          name="theme"
          :value="choice.value"
          :checked="preference === choice.value"
          class="peer sr-only"
          @change="setPreference(choice.value)"
        />
        <span
          class="text-muted-foreground hover:text-foreground peer-checked:bg-card peer-checked:text-foreground peer-focus-visible:outline-ring grid size-7 place-items-center rounded-md transition-colors peer-checked:shadow-xs peer-focus-visible:outline-2"
        >
          <component :is="choice.icon" class="size-4" aria-hidden="true" />
          <span class="sr-only">{{ choice.label }}</span>
        </span>
      </label>
    </div>
    <p
      v-if="storageUnavailable"
      role="status"
      class="text-warning absolute top-full right-0 mt-2 w-64 text-xs leading-5"
    >
      This browser could not save your preference. It applies until this page is closed.
    </p>
  </fieldset>
</template>
