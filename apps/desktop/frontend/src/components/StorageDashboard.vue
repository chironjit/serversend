<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type { StorageStatistics } from '../../bindings/github.com/chironjit/serversend/shared/api/models';
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
const bytes = (n: number) => (n / 1048576).toFixed(2) + ' MiB';
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
  <section class="border-border border-t p-5 text-sm" aria-label="Account storage">
    <h3>Account storage</h3>
    <p v-if="stale" role="status">Offline or unavailable · values are stale</p>
    <template v-if="stats">
      <p>
        Logical content {{ bytes(stats.logical_bytes) }} · quota
        {{ stats.quota_bytes ? bytes(stats.quota_bytes) : 'Unlimited' }}
      </p>
      <p>
        Allocated: current {{ bytes(stats.committed_bytes) }} · retained
        {{ bytes(stats.retained_bytes) }} · staged {{ bytes(stats.staged_bytes) }}
      </p>
      <p>Available for new uploads (estimate) {{ bytes(stats.available_bytes) }}</p>
      <p>
        Reserved {{ bytes(stats.reserved_bytes) }} · unwritten {{ bytes(stats.unwritten_bytes) }} ·
        {{ stats.active_uploads }} open uploads
      </p>
      <p>
        Useful uploads {{ bytes(stats.transfer.useful_rate) }}/s · payload
        {{ bytes(stats.transfer.wire_rate) }}/s · upload retries/rejections
        {{ bytes(stats.transfer.retry_rate) }}/s
      </p>
      <p class="text-muted-foreground text-xs">
        Updated {{ stats.updated_at }}. Rates average ten seconds across your devices; payload
        excludes transport framing. File allocations exclude directory and database overhead.
      </p>
    </template>
    <p v-else>Storage {{ stale ? 'unavailable' : 'loading…' }}</p>
  </section>
</template>
