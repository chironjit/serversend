<script setup lang="ts">
import { onMounted, ref } from 'vue';
import UiAlert from './ui/UiAlert.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiTable from './ui/UiTable.vue';

type AuditEvent = {
  id: string;
  actor: string;
  target: string;
  action: string;
  device_id?: string;
  created_at: string;
};
const events = ref<AuditEvent[]>([]);
const next = ref('');
const current = ref('');
const previous = ref<string[]>([]);
const loading = ref(false);
const error = ref('');
async function load(direction: 'latest' | 'older' | 'newer' = 'latest') {
  if (loading.value) return;
  const before =
    direction === 'older' ? next.value : direction === 'newer' ? (previous.value.at(-1) ?? '') : '';
  loading.value = true;
  error.value = '';
  try {
    const response = await fetch(
      '/api/v1/admin/audit' + (before ? '?before=' + encodeURIComponent(before) : ''),
      {
        cache: 'no-store',
        signal: AbortSignal.timeout(5000),
      },
    );
    if (!response.ok) throw new Error('Audit history unavailable');
    const page = await response.json();
    if (direction === 'older') previous.value.push(current.value);
    else if (direction === 'newer') previous.value.pop();
    else previous.value = [];
    current.value = before;
    events.value = page.events;
    next.value = page.next_before ?? '';
  } catch {
    error.value =
      'Could not load account audit history. Try again or sign in again if your session expired.';
  } finally {
    loading.value = false;
  }
}
onMounted(() => void load());
</script>

<template>
  <UiCard as="section" class="p-5">
    <UiCardHeader title="Account audit history" />
    <div class="my-3 flex flex-wrap gap-2">
      <UiButton variant="outline" :disabled="loading" @click="load()">Latest</UiButton>
      <UiButton variant="outline" :disabled="loading || !previous.length" @click="load('newer')"
        >Newer</UiButton
      >
      <UiButton variant="outline" :disabled="loading || !next" @click="load('older')"
        >Older</UiButton
      >
    </div>
    <UiAlert v-if="error">{{ error }}</UiAlert>
    <p v-if="loading" role="status">Loading audit history…</p>
    <p v-else-if="!error && !events.length">No account events recorded.</p>
    <UiTable v-if="events.length">
      <thead>
        <tr>
          <th>Time</th>
          <th>Actor</th>
          <th>Action</th>
          <th>Account</th>
          <th>Device ID</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="event in events" :key="event.id">
          <td>{{ new Date(event.created_at).toLocaleString() }}</td>
          <td>{{ event.actor || 'Local operator' }}</td>
          <td class="capitalize">{{ event.action.replaceAll('_', ' ') }}</td>
          <td>{{ event.target }}</td>
          <td>{{ event.device_id || '—' }}</td>
        </tr>
      </tbody>
    </UiTable>
  </UiCard>
</template>
