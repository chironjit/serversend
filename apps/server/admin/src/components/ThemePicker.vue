<script setup lang="ts">
import { useTheme } from '../theme';

const { preference, setPreference, storageUnavailable } = useTheme();
const choices = ['light', 'dark', 'system'] as const;
</script>

<template>
  <fieldset>
    <legend class="mb-2 text-sm font-medium text-slate-600 dark:text-slate-300">Appearance</legend>
    <div class="flex flex-wrap gap-1 rounded-xl border border-slate-300 p-1 dark:border-slate-600">
      <label v-for="choice in choices" :key="choice" class="relative cursor-pointer">
        <input
          type="radio"
          name="theme"
          :value="choice"
          :checked="preference === choice"
          class="peer sr-only"
          @change="setPreference(choice)"
        />
        <span
          class="block rounded-lg px-3 py-2 text-sm font-medium capitalize peer-checked:bg-slate-900 peer-checked:text-white peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-teal-700 dark:peer-checked:bg-slate-100 dark:peer-checked:text-slate-950 dark:peer-focus-visible:outline-teal-300"
          >{{ choice }}</span
        >
      </label>
    </div>
    <p
      v-if="storageUnavailable"
      role="status"
      class="mt-2 max-w-xs text-sm text-amber-800 dark:text-amber-200"
    >
      This browser could not save your preference. It applies until this page is closed.
    </p>
  </fieldset>
</template>
