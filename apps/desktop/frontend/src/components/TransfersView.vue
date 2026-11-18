<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import {
  ArrowDownToLine,
  ArrowLeftRight,
  ArrowUpFromLine,
  CircleAlert,
  Layers,
  Pause,
  Play,
  RotateCcw,
  X,
} from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type {
  Batch,
  Item,
} from '../../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import { duration, failure, size } from '../format';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiEmpty from './ui/UiEmpty.vue';
import UiInput from './ui/UiInput.vue';
import UiProgress from './ui/UiProgress.vue';

type Tone = 'primary' | 'neutral' | 'success' | 'warning' | 'danger';
const props = defineProps<{
  transfers: Item[];
  batches: Batch[];
  speed: { up: number; down: number };
  rates: { up: number; down: number }[];
  clock: number;
  busy: boolean;
  batchError: string;
  scopeName: (scope: string) => string;
}>();
const combined = defineModel<boolean>('combined', { required: true });
const emit = defineEmits<{ action: [id: number, batch: boolean, action: string]; retry: [] }>();

const stateLabel: Record<string, string> = {
  scanning: 'Scanning',
  active: 'Active',
  running: 'Transferring',
  queued: 'Queued',
  paused: 'Paused',
  waiting_network: 'Waiting for connection',
  waiting_authentication: 'Sign-in required',
  waiting_capacity: 'Waiting for space',
  waiting_vault: 'Vault locked',
  retry_scheduled: 'Retry scheduled',
  cancel_pending: 'Cancelling',
  cancelling: 'Cancelling',
  partial: 'Completed with issues',
  complete: 'Completed',
  done: 'Done',
  done_with_skips: 'Completed with skipped items',
  all_skipped: 'Nothing uploaded',
  skipped: 'Skipped',
  cancelled: 'Cancelled',
  failed: 'Failed',
};
const stateTone: Record<string, Tone> = {
  scanning: 'primary',
  active: 'primary',
  running: 'primary',
  waiting_network: 'warning',
  waiting_authentication: 'warning',
  waiting_capacity: 'warning',
  waiting_vault: 'warning',
  retry_scheduled: 'warning',
  partial: 'warning',
  done: 'success',
  complete: 'success',
  done_with_skips: 'warning',
  all_skipped: 'warning',
  skipped: 'warning',
  failed: 'danger',
};
const terminal = new Set([
  'done',
  'complete',
  'done_with_skips',
  'all_skipped',
  'skipped',
  'cancelled',
  'failed',
  'partial',
]);
const canPause = (state: string) =>
  !terminal.has(state) && !['paused', 'cancel_pending', 'cancelling'].includes(state);
const canCancel = (state: string) =>
  !terminal.has(state) && state !== 'cancel_pending' && state !== 'cancelling';
const canResume = (state: string) =>
  state === 'paused' || state.startsWith('waiting_') || state === 'retry_scheduled';

const recentBatches = computed(() => props.batches.toReversed());
const counts = computed(() => {
  let running = 0,
    attention = 0;
  for (const t of props.transfers) {
    if (t.state === 'active' || t.state === 'queued') running++;
    else if (t.state === 'failed' || t.state.startsWith('waiting_')) attention++;
  }
  return { running, attention };
});

function percent(done: number, total: number) {
  return total > 0 ? Math.floor((done / total) * 100) : 0;
}
function status(item: Item): string {
  if (item.state === 'failed' || item.state === 'skipped') return item.error;
  if (item.state === 'queued') return 'Waiting';
  if (canResume(item.state) || item.state === 'cancel_pending')
    return item.error || stateLabel[item.state] || item.state;
  if (item.state === 'done')
    return item.error ? `${size(item.total)} · ${item.error}` : size(item.total);
  return `${size(item.done)} of ${size(item.total)} · ${size(item.speed ?? 0)}/s`;
}
// Elapsed wall time for a selection, including scanning and waiting.
function timing(batch: Batch): string {
  if (!batch.finishedAt) return `running for ${duration(props.clock - batch.startedAt)}`;
  const ms = batch.finishedAt - batch.startedAt;
  const rate = ms > 0 ? ` · ${size((batch.bytes * 1000) / ms)}/s average` : '';
  return `took ${duration(ms)} · ${size(batch.bytes)}${rate}`;
}
function summary(batch: Batch): string {
  const parts = [`${batch.completed} of ${batch.files} files`];
  if (batch.skipped) parts.push(`${batch.skipped} skipped`);
  if (batch.failed) parts.push(`${batch.failed} failed`);
  if (batch.cancelled) parts.push(`${batch.cancelled} cancelled`);
  if (batch.startedAt) parts.push(timing(batch));
  if (combined.value) parts.unshift(props.scopeName(batch.scope));
  return parts.join(' · ');
}
function chart(direction: 'up' | 'down') {
  const max = Math.max(1, ...props.rates.flatMap((r) => [r.up, r.down]));
  return props.rates.map((r, i) => `${(i * 300) / 59},${60 - (r[direction] / max) * 56}`).join(' ');
}

const bandwidth = ref('0');
const bandwidthError = ref('');
const bandwidthSaved = ref(false);
onMounted(async () => {
  try {
    bandwidth.value = String((await TransferService.Bandwidth()) / 1048576);
  } catch (reason) {
    bandwidthError.value = failure(reason);
  }
});
async function setBandwidth() {
  bandwidthError.value = '';
  bandwidthSaved.value = false;
  const limit = Number(bandwidth.value);
  if (!Number.isFinite(limit) || limit < 0 || limit > Number.MAX_SAFE_INTEGER / 1048576) {
    bandwidthError.value = 'Enter a nonnegative bandwidth limit. Use 0 for unlimited.';
    return;
  }
  try {
    await TransferService.SetBandwidth(Math.round(limit * 1048576));
    bandwidthSaved.value = true;
  } catch (reason) {
    bandwidthError.value = failure(reason);
  }
}
</script>

<template>
  <div class="grid gap-6">
    <dl class="m-0 grid grid-cols-2 gap-4 lg:grid-cols-4">
      <UiCard class="p-4">
        <dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
          <ArrowUpFromLine class="text-chart-up size-3.5" aria-hidden="true" />Upload
        </dt>
        <dd class="m-0 mt-1.5 text-xl font-semibold tabular-nums">{{ size(speed.up) }}/s</dd>
      </UiCard>
      <UiCard class="p-4">
        <dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
          <ArrowDownToLine class="text-chart-down size-3.5" aria-hidden="true" />Download
        </dt>
        <dd class="m-0 mt-1.5 text-xl font-semibold tabular-nums">{{ size(speed.down) }}/s</dd>
      </UiCard>
      <UiCard class="p-4">
        <dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
          <ArrowLeftRight class="size-3.5" aria-hidden="true" />In progress
        </dt>
        <dd class="m-0 mt-1.5 text-xl font-semibold tabular-nums">{{ counts.running }}</dd>
      </UiCard>
      <UiCard class="p-4">
        <dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
          <CircleAlert class="size-3.5" aria-hidden="true" />Needs attention
        </dt>
        <dd
          class="m-0 mt-1.5 text-xl font-semibold tabular-nums"
          :class="counts.attention ? 'text-warning' : ''"
        >
          {{ counts.attention }}
        </dd>
      </UiCard>
    </dl>

    <UiCard as="section" class="grid gap-4 p-5 lg:grid-cols-[minmax(0,1fr)_280px]">
      <div class="min-w-0">
        <div class="flex items-center justify-between gap-3">
          <h2 class="m-0 text-base font-semibold">Throughput</h2>
          <div class="text-muted-foreground flex gap-4 text-xs">
            <span class="flex items-center gap-1.5"
              ><span class="bg-chart-up size-2 rounded-full" />Upload</span
            >
            <span class="flex items-center gap-1.5"
              ><span class="bg-chart-down size-2 rounded-full" />Download</span
            >
          </div>
        </div>
        <svg
          viewBox="0 0 300 64"
          preserveAspectRatio="none"
          class="mt-3 h-24 w-full"
          role="img"
          aria-label="Upload and download throughput over the last 60 seconds"
        >
          <line x1="0" y1="63" x2="300" y2="63" stroke="var(--border)" stroke-width="1" />
          <polyline
            :points="chart('up')"
            fill="none"
            stroke="var(--chart-up)"
            stroke-width="2"
            stroke-linejoin="round"
            vector-effect="non-scaling-stroke"
          />
          <polyline
            :points="chart('down')"
            fill="none"
            stroke="var(--chart-down)"
            stroke-width="2"
            stroke-linejoin="round"
            vector-effect="non-scaling-stroke"
          />
        </svg>
        <p class="text-muted-foreground m-0 mt-2 text-xs">
          Last 60 seconds of verified bytes, including disk and verification time.
        </p>
      </div>
      <div class="grid content-start gap-4 border-t pt-4 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-5">
        <form class="grid gap-1.5" @submit.prevent="setBandwidth">
          <label for="bandwidth" class="text-sm font-medium">Bandwidth limit (MiB/s)</label>
          <div class="flex gap-2">
            <UiInput id="bandwidth" v-model="bandwidth" type="number" min="0" step="any" required />
            <UiButton type="submit" variant="outline" :disabled="busy">Apply</UiButton>
          </div>
          <span
            class="text-xs"
            :class="bandwidthError ? 'text-destructive' : 'text-muted-foreground'"
            :role="bandwidthError ? 'alert' : undefined"
          >
            {{ bandwidthError || (bandwidthSaved ? 'Limit applied.' : '0 means unlimited.') }}
          </span>
        </form>
        <label class="flex items-center gap-2 text-sm">
          <input v-model="combined" type="checkbox" class="accent-primary" />
          Show all signed-in servers
        </label>
      </div>
    </UiCard>

    <div class="grid items-start gap-6 xl:grid-cols-2">
      <UiCard as="section" class="min-w-0" aria-label="Transfer selections">
        <div class="p-5 pb-3">
          <UiCardHeader
            title="Selections"
            description="Each upload or download you start. Links and unsupported files are skipped."
          />
        </div>
        <div v-if="batchError" class="flex items-center gap-2 px-5 pb-3" role="alert">
          <p class="text-destructive m-0 flex-1 text-sm">
            Transfer summaries unavailable: {{ batchError }}
          </p>
          <UiButton variant="ghost" size="sm" @click="emit('retry')">Retry summaries</UiButton>
        </div>
        <UiEmpty v-if="!batches.length" title="No selections yet">
          <template #icon><Layers /></template>
          Uploads and downloads you start appear here.
        </UiEmpty>
        <ul v-else class="m-0 list-none border-t p-0" aria-label="Recent transfer selections">
          <li
            v-for="batch in recentBatches"
            :key="batch.id"
            class="grid gap-2 border-b px-5 py-3 last:border-b-0"
          >
            <div class="flex items-center gap-2">
              <div class="grid min-w-0 flex-1 gap-0.5">
                <span class="truncate text-sm font-medium" :title="batch.name">{{
                  batch.name
                }}</span>
                <span class="text-muted-foreground text-xs tabular-nums" role="status">{{
                  summary(batch)
                }}</span>
              </div>
              <UiBadge :tone="stateTone[batch.state] ?? 'neutral'">{{
                stateLabel[batch.state] ?? batch.state
              }}</UiBadge>
              <UiButton
                v-if="canPause(batch.state)"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Pause ${batch.name}`"
                title="Pause"
                @click="emit('action', batch.id, true, 'pause')"
                ><Pause aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="batch.state === 'paused'"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Resume ${batch.name}`"
                title="Resume"
                @click="emit('action', batch.id, true, 'resume')"
                ><Play aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="['failed', 'partial', 'cancelled'].includes(batch.state)"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Retry ${batch.name}`"
                title="Retry unresolved"
                @click="emit('action', batch.id, true, 'retry')"
                ><RotateCcw aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="canCancel(batch.state)"
                variant="ghost"
                size="icon-sm"
                :aria-label="`Cancel ${batch.name}`"
                title="Cancel"
                @click="emit('action', batch.id, true, 'cancel')"
                ><X aria-hidden="true"
              /></UiButton>
            </div>
            <UiProgress
              v-if="!batch.finishedAt && batch.files"
              :value="percent(batch.completed, batch.files)"
            />
            <p v-if="batch.state === 'all_skipped'" class="text-warning m-0 text-xs">
              Zero files transferred. Every selected item was skipped.
            </p>
            <p v-if="batch.error" class="text-destructive m-0 text-xs wrap-anywhere">
              {{ batch.error }}
            </p>
            <details v-if="batch.skipped > 0" class="text-xs">
              <summary class="text-muted-foreground hover:text-foreground cursor-pointer">
                Review skipped items ({{ batch.skipped }})
              </summary>
              <ul class="m-0 mt-2 grid list-none gap-2 p-0" aria-label="Skipped paths and reasons">
                <li
                  v-for="(skipped, index) in (batch.skippedItems ?? []).slice(0, 100)"
                  :key="index"
                  class="grid gap-0.5"
                >
                  <span class="font-mono wrap-anywhere">{{ skipped.path }}</span>
                  <span class="text-muted-foreground wrap-anywhere">{{ skipped.reason }}</span>
                </li>
              </ul>
              <p
                v-if="batch.skipped > (batch.skippedItems?.length ?? 0)"
                class="text-muted-foreground m-0 mt-2"
              >
                Showing {{ Math.min(100, batch.skippedItems?.length ?? 0) }} of
                {{ batch.skipped }} skipped paths.
              </p>
            </details>
          </li>
        </ul>
      </UiCard>

      <UiCard as="section" class="min-w-0" aria-label="Files">
        <div class="p-5 pb-3">
          <UiCardHeader
            title="Files"
            description="Running files first, then what is queued, then recently finished."
          />
        </div>
        <UiEmpty v-if="transfers.length === 0" title="No transfers yet">
          <template #icon><ArrowLeftRight /></template>
          Saved uploads and downloads appear here.
        </UiEmpty>
        <ul v-else class="m-0 list-none border-t p-0" aria-label="Transfer queue">
          <li
            v-for="item in transfers"
            :key="item.id"
            class="grid gap-1.5 border-b px-5 py-2.5 last:border-b-0"
          >
            <div class="flex items-center gap-2">
              <ArrowUpFromLine
                v-if="item.direction === 'upload'"
                class="text-chart-up size-4 shrink-0"
                aria-hidden="true"
              />
              <ArrowDownToLine v-else class="text-chart-down size-4 shrink-0" aria-hidden="true" />
              <div class="grid min-w-0 flex-1">
                <span class="truncate text-sm">{{ item.name }}</span>
                <span
                  class="truncate text-xs tabular-nums"
                  :class="item.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'"
                >
                  <template v-if="combined">{{ scopeName(item.scope) }} · </template
                  >{{ status(item) }}
                </span>
              </div>
              <UiBadge :tone="stateTone[item.state] ?? 'neutral'" dot>
                {{ stateLabel[item.state] ?? item.state }}
              </UiBadge>
              <UiButton
                v-if="canPause(item.state)"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Pause ${item.name}`"
                title="Pause"
                @click="emit('action', item.id, false, 'pause')"
                ><Pause aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="canResume(item.state)"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Resume ${item.name}`"
                title="Resume"
                @click="emit('action', item.id, false, 'resume')"
                ><Play aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="item.state === 'failed' || item.state === 'cancelled'"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Retry ${item.name}`"
                title="Retry"
                @click="emit('action', item.id, false, 'retry')"
                ><RotateCcw aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="canCancel(item.state)"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="`Cancel ${item.name}`"
                title="Cancel"
                @click="emit('action', item.id, false, 'cancel')"
                ><X aria-hidden="true"
              /></UiButton>
            </div>
            <UiProgress v-if="item.state === 'active'" :value="percent(item.done, item.total)" />
          </li>
        </ul>
        <p
          v-if="transfers.length >= 200"
          class="text-muted-foreground m-0 border-t px-5 py-3 text-xs"
        >
          Showing up to 200 recent transfers. Selection summaries include all files.
        </p>
      </UiCard>
    </div>
  </div>
</template>
