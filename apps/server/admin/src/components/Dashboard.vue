<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import UiCard from './ui/UiCard.vue';
const stats = ref<Record<string, number | string | null>>({});
const error = ref('');
let timer: ReturnType<typeof setInterval> | undefined;
let active: AbortController | undefined;
let disposed = false;
async function load() {
  if (active) return;
  active = new AbortController();
  const timeout = setTimeout(() => active?.abort(), 5000);
  try {
    const response = await fetch('/api/v1/admin/stats', {
      cache: 'no-store',
      signal: active.signal,
    });
    if (!response.ok)
      throw new Error('Could not refresh statistics. Sign in again if your session expired.');
    const result = await response.json();
    if (!disposed) {
      stats.value = result;
      error.value = '';
    }
  } catch {
    if (!disposed) error.value = 'Statistics unavailable. Check the connection or sign in again.';
  } finally {
    clearTimeout(timeout);
    active = undefined;
  }
}
function bytes(value: unknown) {
  return typeof value === 'number' ? (value / 1073741824).toFixed(2) + ' GiB' : 'Unavailable';
}
onMounted(() => {
  void load();
  timer = setInterval(() => void load(), 5000);
});
onUnmounted(() => {
  disposed = true;
  clearInterval(timer);
  active?.abort();
});
</script>
<template>
  <UiCard class="p-6">
    <h2>Dashboard</h2>
    <p v-if="error" role="alert">{{ error }} Values may be stale.</p>
    <dl class="grid gap-6 sm:grid-cols-2">
      <div>
        <dt>Server status</dt>
        <dd>{{ error ? 'Unavailable' : (stats.status ?? 'Checking') }}</dd>
      </div>
      <div>
        <dt>Host CPU use</dt>
        <dd>
          {{
            typeof stats.cpu_percent === 'number'
              ? stats.cpu_percent.toFixed(1) + '%'
              : 'Sampling / unavailable'
          }}
        </dd>
      </div>
      <div>
        <dt>Host memory use</dt>
        <dd>{{ bytes(stats.memory_used_bytes) }} / {{ bytes(stats.memory_total_bytes) }}</dd>
      </div>
      <div>
        <dt>Stored file bytes</dt>
        <dd>{{ bytes(stats.storage_bytes) }}</dd>
      </div>
      <div>
        <dt>Storage available</dt>
        <dd>{{ bytes(stats.storage_available_bytes) }}</dd>
      </div>
    </dl>
    <p class="text-muted-foreground text-xs">
      Updates every five seconds. CPU and memory describe the host visible to the server (Linux).
      Stored file bytes include committed versions, excluding staging, database and filesystem
      overhead.
    </p>
  </UiCard>
</template>
