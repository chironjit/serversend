<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type { StorageStatistics } from '../../bindings/github.com/chironjit/serversend/shared/api/models';
import { size } from '../format';
import UiProgress from './ui/UiProgress.vue';
const stats = ref<StorageStatistics>();
const stale = ref(false);
let timer: ReturnType<typeof setInterval> | undefined;
let loading = false;
let disposed = false;
async function load() {
  if (loading) return;
  loading = true;
  try {
    const result = await TransferService.StorageStatistics();
    if (!disposed) {
      stats.value = result;
      stale.value = false;
    }
  } catch {
    if (!disposed) stale.value = true;
  } finally {
    loading = false;
  }
}
const bytes = (n?: number) => size(n ?? 0);
const used = computed(() =>
  stats.value?.quota_bytes
    ? Math.min(100, Math.round((stats.value.logical_bytes / stats.value.quota_bytes) * 100))
    : 0,
);
onMounted(() => {
  void load();
  timer = setInterval(() => void load(), 5000);
});
onUnmounted(() => {
  disposed = true;
  clearInterval(timer);
});
</script>
<template>
  <section class="grid gap-4" aria-label="Account storage">
    <p v-if="stale" class="text-warning m-0 text-sm" role="status">
      Offline or unavailable · values are stale
    </p>
    <template v-if="stats">
      <dl class="m-0 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div
          v-for="tile in [
            ['Stored', bytes(stats.logical_bytes + (stats.private_ciphertext_bytes ?? 0))],
            ['Quota', stats.quota_bytes ? bytes(stats.quota_bytes) : 'Unlimited'],
            ['Available', bytes(stats.available_bytes)],
            ['Open uploads', String(stats.active_uploads)],
          ]"
          :key="tile[0]"
          class="bg-muted/60 rounded-lg p-3"
        >
          <dt class="text-muted-foreground text-xs">{{ tile[0] }}</dt>
          <dd class="m-0 mt-1 truncate text-base font-semibold tabular-nums">{{ tile[1] }}</dd>
        </div>
      </dl>
      <UiProgress v-if="stats.quota_bytes" :value="used" />
      <details class="text-sm">
        <summary class="text-muted-foreground cursor-pointer">Allocation details</summary>
        <dl class="text-muted-foreground m-0 mt-2 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1">
          <dt>Standard content</dt>
          <dd class="m-0 tabular-nums">{{ bytes(stats.logical_bytes) }}</dd>
          <dt>Private ciphertext</dt>
          <dd class="m-0 tabular-nums">{{ bytes(stats.private_ciphertext_bytes) }}</dd>
          <dt>Current / retained</dt>
          <dd class="m-0 tabular-nums">
            {{ bytes(stats.committed_bytes) }} / {{ bytes(stats.retained_bytes) }}
          </dd>
          <dt>Staged</dt>
          <dd class="m-0 tabular-nums">{{ bytes(stats.staged_bytes) }}</dd>
          <dt>Reserved / unwritten</dt>
          <dd class="m-0 tabular-nums">
            {{ bytes(stats.reserved_bytes) }} / {{ bytes(stats.unwritten_bytes) }}
          </dd>
          <dt>Upload rates (10 s)</dt>
          <dd class="m-0 tabular-nums">
            {{ bytes(stats.transfer.useful_rate) }}/s useful ·
            {{ bytes(stats.transfer.wire_rate) }}/s payload ·
            {{ bytes(stats.transfer.retry_rate) }}/s retried
          </dd>
        </dl>
        <p class="m-0 mt-2 text-xs">
          Updated {{ stats.updated_at }}. Rates cover all your devices; allocations exclude
          directory and database overhead.
        </p>
      </details>
    </template>
    <p v-else class="text-muted-foreground m-0 text-sm">
      Storage {{ stale ? 'unavailable' : 'loading…' }}
    </p>
  </section>
</template>
