<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import UiAlert from './ui/UiAlert.vue';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiInput from './ui/UiInput.vue';
import UiTable from './ui/UiTable.vue';
const props = defineProps<{ token: string }>();
type Entry = { id: number; time: string; level: 'info' | 'warn' | 'error' | 'ok'; message: string };
const entries = ref<Entry[]>([]);
const level = ref('');
const query = ref('');
const busy = ref(false);
const error = ref('');
const more = ref(false);
let active: AbortController | undefined;
let disposed = false;
async function load(older = false) {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  active = new AbortController();
  const timer = setTimeout(() => active?.abort(), 10000);
  try {
    const params = new URLSearchParams({ level: level.value, q: query.value });
    if (older && entries.value.length) params.set('before', String(entries.value.at(-1)!.id));
    const response = await fetch('/api/v1/admin/logs?' + params, {
      headers: props.token ? { Authorization: 'Bearer ' + props.token } : {},
      cache: 'no-store',
      redirect: 'error',
      signal: active.signal,
    });
    if (!response.ok)
      throw new Error(
        response.status === 401 || response.status === 403
          ? 'Sign in as an administrator again to view logs.'
          : 'Could not load logs. Try again.',
      );
    const page: Entry[] = await response.json();
    if (!disposed) {
      entries.value = older ? [...entries.value, ...page] : page;
      more.value = page.length === 100;
    }
  } catch (reason) {
    if (!disposed) {
      entries.value = [];
      more.value = false;
      error.value = reason instanceof Error ? reason.message : 'Could not load logs.';
    }
  } finally {
    clearTimeout(timer);
    busy.value = false;
    active = undefined;
  }
}
function search() {
  entries.value = [];
  void load();
}
onMounted(() => {
  void load();
});
onUnmounted(() => {
  disposed = true;
  active?.abort();
  entries.value = [];
});
</script>
<template>
  <UiCard as="section" id="logs" class="mt-5 p-5 sm:p-6" aria-labelledby="logs-heading">
    <h2 id="logs-heading" class="m-0 text-base font-semibold">Application logs</h2>
    <p class="text-muted-foreground text-sm">
      Recent server events, newest first. Refresh to see new events. History is bounded and retained
      across restarts.
    </p>
    <form class="my-4 flex flex-wrap items-end gap-3" @submit.prevent="search">
      <label class="grid gap-1 text-sm"
        >Search logs<UiInput
          v-model="query"
          maxlength="256"
          :disabled="busy"
          placeholder="Search messages…"
      /></label>
      <label class="grid gap-1 text-sm"
        >Level
        <select
          v-model="level"
          :disabled="busy"
          class="border-border bg-card rounded-md border p-2"
        >
          <option value="">All levels</option>
          <option value="info">Info</option>
          <option value="ok">OK</option>
          <option value="warn">Warning</option>
          <option value="error">Error</option>
        </select>
      </label>
      <UiButton type="submit" :disabled="busy">{{
        busy ? 'Loading…' : 'Search / refresh'
      }}</UiButton>
    </form>
    <UiAlert v-if="error" role="alert">{{ error }}</UiAlert>
    <div :aria-busy="busy" aria-live="polite">
      <UiTable>
        <thead>
          <tr>
            <th>Time</th>
            <th>Level</th>
            <th>Event</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in entries" :key="entry.id">
            <td class="whitespace-nowrap text-xs">{{ new Date(entry.time).toLocaleString() }}</td>
            <td>
              <UiBadge
                :tone="
                  entry.level === 'error'
                    ? 'danger'
                    : entry.level === 'warn'
                      ? 'warning'
                      : entry.level === 'ok'
                        ? 'success'
                        : 'primary'
                "
                dot
                >{{ entry.level === 'ok' ? 'OK' : entry.level }}</UiBadge
              >
            </td>
            <td class="wrap-anywhere whitespace-pre-wrap text-xs">{{ entry.message }}</td>
          </tr>
          <tr v-if="!entries.length">
            <td colspan="3">{{ busy ? 'Loading logs…' : 'No matching log entries.' }}</td>
          </tr>
        </tbody>
      </UiTable>
    </div>
    <UiButton v-if="more" variant="outline" class="mt-4" :disabled="busy" @click="load(true)"
      >Load older entries</UiButton
    >
  </UiCard>
</template>
