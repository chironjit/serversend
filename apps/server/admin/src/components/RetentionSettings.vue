<script setup lang="ts">
import { ref, onMounted } from 'vue';
import UiButton from './ui/UiButton.vue';
import UiInput from './ui/UiInput.vue';
const props = defineProps<{ csrf: string }>();
const trashDays = ref('30'),
  versionDays = ref('0'),
  busy = ref(false),
  error = ref(''),
  message = ref('');
async function request(save = false) {
  busy.value = true;
  error.value = '';
  message.value = '';
  try {
    const response = await fetch('/api/v1/admin/retention', {
      method: save ? 'PUT' : 'GET',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': props.csrf },
      body: save
        ? JSON.stringify({
            trash_days: Number(trashDays.value),
            version_days: Number(versionDays.value),
          })
        : undefined,
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error?.message ?? 'Request failed');
    trashDays.value = String(result.trash_days);
    versionDays.value = String(result.version_days);
    if (save) message.value = 'Retention saved.';
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
onMounted(() => request());
</script>
<template>
  <form class="my-4 grid max-w-md gap-3" @submit.prevent="request(true)" :aria-busy="busy">
    <h3>Versions and trash retention</h3>
    <p>
      Changes apply at the next cleanup, including existing items. Expired data is permanently
      removed. Current and unresolved conflict versions are kept.
    </p>
    <label
      >Trash retention (days, 1–36500)<UiInput
        v-model="trashDays"
        type="number"
        min="1"
        max="36500"
        required
        :disabled="busy"
    /></label>
    <label
      >Version retention (days; 0 keeps all)<UiInput
        v-model="versionDays"
        type="number"
        min="0"
        max="36500"
        required
        :disabled="busy"
    /></label>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="message" role="status">{{ message }}</p>
    <UiButton type="submit" :disabled="busy">Save retention</UiButton>
  </form>
</template>
