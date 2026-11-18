<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import UiCard from './ui/UiCard.vue';
interface Storage {
  updated_at: string;
  logical_bytes: number;
  available_bytes: number;
  committed_bytes: number;
  retained_bytes: number;
  staged_bytes: number;
  trash_bytes: number;
  other_bytes: number;
  reserved_bytes: number;
  unwritten_bytes: number;
  active_uploads: number;
  filesystems: { id: string; roles: string[]; available_bytes: number; total_bytes: number }[];
  transfer: { useful_rate: number; wire_rate: number; retry_rate: number };
}
const storage = ref<Storage>();
let events: EventSource | undefined;
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
      storage.value = result.storage;
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
function connectEvents() {
  if (disposed || events || typeof EventSource === 'undefined') return;
  events = new EventSource('/api/v1/admin/events');
  events.addEventListener('statistics_changed', () => void load());
  events.onerror = () => {
    error.value = 'Live connection interrupted; polling for updates.';
    if (events?.readyState === EventSource.CLOSED) {
      events.close();
      events = undefined;
      void load();
    }
  };
}
onMounted(() => {
  void load();
  connectEvents();
  timer = setInterval(() => {
    void load();
    connectEvents();
  }, 5000);
});
onUnmounted(() => {
  disposed = true;
  events?.close();
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
    <p v-if="storage">Available for new uploads (estimate): {{ bytes(storage.available_bytes) }}</p>
    <dl v-if="storage" class="grid gap-6 sm:grid-cols-2">
      <div>
        <dt>Allocated current / retained</dt>
        <dd>{{ bytes(storage.committed_bytes) }} / {{ bytes(storage.retained_bytes) }}</dd>
      </div>
      <div>
        <dt>Allocated staged / removal / other</dt>
        <dd>
          {{ bytes(storage.staged_bytes) }} / {{ bytes(storage.trash_bytes) }} /
          {{ bytes(storage.other_bytes) }}
        </dd>
      </div>
      <div>
        <dt>Reserved / unwritten</dt>
        <dd>{{ bytes(storage.reserved_bytes) }} / {{ bytes(storage.unwritten_bytes) }}</dd>
      </div>
      <div>
        <dt>Open uploads</dt>
        <dd>{{ storage.active_uploads }}</dd>
      </div>
      <div>
        <dt>Useful uploads / payload / upload retries and rejections</dt>
        <dd>
          {{ bytes(storage.transfer.useful_rate) }}/s / {{ bytes(storage.transfer.wire_rate) }}/s /
          {{ bytes(storage.transfer.retry_rate) }}/s
        </dd>
      </div>
      <div v-for="fs in storage.filesystems" :key="fs.id">
        <dt>{{ fs.roles.join(' / ') }} filesystem available / total</dt>
        <dd>{{ bytes(fs.available_bytes) }} / {{ bytes(fs.total_bytes) }}</dd>
      </div>
    </dl>
    <p v-if="storage" class="text-muted-foreground text-xs">
      Measured {{ storage.updated_at }}. Reserved bytes are promises, separate from allocated bytes.
      Rates average ten seconds and exclude transport framing. Scans converge after concurrent
      writes; file allocations exclude directory and database overhead.
    </p>
    <p class="text-muted-foreground text-xs">
      Updates every five seconds. CPU and memory describe the host visible to the server (Linux).
      Stored file bytes include committed versions, excluding staging, database and filesystem
      overhead.
    </p>
  </UiCard>
</template>
