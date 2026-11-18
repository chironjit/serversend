<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import {
  ArrowDownToLine,
  ArrowLeftRight,
  ArrowUpFromLine,
  ChevronRight,
  CircleAlert,
  Pause,
  Play,
  RotateCcw,
  X,
} from 'lucide-vue-next';
import { BackgroundService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
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
import UiProgress from './ui/UiProgress.vue';

type Tone = 'primary' | 'neutral' | 'success' | 'warning' | 'danger';
type Direction = 'upload' | 'download';
// One upload or download the user started, with its files. Files whose
// selection is no longer listed are gathered under "Other files".
type Group = { key: string; name: string; batch?: Batch; items: Item[] };

const props = defineProps<{
  transfers: Item[];
  batches: Batch[];
  speed: { up: number; down: number };
  rates: { up: number; down: number }[];
  clock: number;
  busy: boolean;
  batchError: string;
  combined: boolean;
  scopeName: (scope: string) => string;
}>();
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

const sections: { direction: Direction; title: string; empty: string }[] = [
  { direction: 'upload', title: 'Uploads', empty: 'Files and folders you upload appear here.' },
  {
    direction: 'download',
    title: 'Downloads',
    empty: 'Files and folders you download appear here.',
  },
];
const groups = computed(() => {
  const byBatch = new Map<number, Item[]>();
  for (const item of props.transfers) {
    const list = byBatch.get(item.batchId);
    if (list) list.push(item);
    else byBatch.set(item.batchId, [item]);
  }
  const result: Record<Direction, Group[]> = { upload: [], download: [] };
  const listed = new Set<number>();
  // Sources disagree on order (the durable store is newest first), so sort.
  for (const batch of props.batches.toSorted((a, b) => b.id - a.id)) {
    listed.add(batch.id);
    const items = byBatch.get(batch.id) ?? [];
    const direction = batch.direction || items[0]?.direction;
    result[direction === 'download' ? 'download' : 'upload'].push({
      key: `batch-${batch.id}`,
      name: batch.name,
      batch,
      items,
    });
  }
  for (const direction of ['upload', 'download'] as const) {
    const items = props.transfers.filter(
      (item) =>
        !listed.has(item.batchId) && (item.direction === 'download') === (direction === 'download'),
    );
    if (items.length)
      result[direction].push({ key: `other-${direction}`, name: 'Other files', items });
  }
  return result;
});
// Unfinished selections start expanded; a click overrides that per selection.
const expandedOverride = ref(new Map<string, boolean>());
const expanded = (group: Group) =>
  expandedOverride.value.get(group.key) ?? !group.batch?.finishedAt;
function toggle(group: Group) {
  const next = new Map(expandedOverride.value);
  next.set(group.key, !expanded(group));
  expandedOverride.value = next;
}

// Uploading unencrypted is a privacy decision, so it asks for confirmation.
const confirmUnencrypted = ref<{ id: number; batch: boolean } | null>(null);
function oversizedChoice(id: number, batch: boolean, action: 'unencrypted' | 'keep_skipped') {
  confirmUnencrypted.value = null;
  emit('action', id, batch, action);
}
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
  if (props.combined) parts.unshift(props.scopeName(batch.scope));
  return parts.join(' · ');
}
function chart(direction: 'up' | 'down') {
  const max = Math.max(1, ...props.rates.flatMap((r) => [r.up, r.down]));
  return props.rates.map((r, i) => `${(i * 300) / 59},${60 - (r[direction] / max) * 56}`).join(' ');
}

// Pausing everything persists and mirrors the tray menu.
const paused = ref(false);
const pauseError = ref('');
onMounted(async () => {
  try {
    paused.value = await BackgroundService.Paused();
  } catch (reason) {
    pauseError.value = failure(reason);
  }
});
async function togglePaused() {
  pauseError.value = '';
  try {
    await BackgroundService.SetPaused(!paused.value);
  } catch (reason) {
    pauseError.value = failure(reason);
  }
  try {
    paused.value = await BackgroundService.Paused();
  } catch {
    // Keep the last known value; the error above explains the failure.
  }
}
</script>

<template>
  <div class="grid gap-6">
    <UiCard
      class="flex flex-wrap items-center gap-3 p-4"
      :class="paused ? 'border-warning/40 bg-warning/10' : ''"
    >
      <div class="grid min-w-0 flex-1 gap-0.5">
        <p class="m-0 text-sm font-medium">
          {{ paused ? 'All transfers are paused' : 'Transfers are running' }}
        </p>
        <p class="text-muted-foreground m-0 text-xs">
          {{
            paused
              ? 'Queued work waits until you resume, even after a restart.'
              : 'Transfers continue in the tray when the window is closed.'
          }}
        </p>
        <p v-if="pauseError" class="text-destructive m-0 text-xs" role="alert">
          {{ pauseError }}
        </p>
      </div>
      <UiButton :variant="paused ? 'primary' : 'outline'" @click="togglePaused">
        <Play v-if="paused" aria-hidden="true" /><Pause v-else aria-hidden="true" />
        {{ paused ? 'Resume all transfers' : 'Pause all transfers' }}
      </UiButton>
    </UiCard>

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

    <UiCard as="section" class="p-5">
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
    </UiCard>

    <div v-if="batchError" class="flex items-center gap-2" role="alert">
      <p class="text-destructive m-0 flex-1 text-sm">
        Transfer summaries unavailable: {{ batchError }}
      </p>
      <UiButton variant="ghost" size="sm" @click="emit('retry')">Retry summaries</UiButton>
    </div>

    <div class="grid gap-6">
      <UiCard
        v-for="section in sections"
        :key="section.direction"
        as="section"
        class="min-w-0"
        :aria-label="section.title"
      >
        <div class="p-5 pb-3">
          <UiCardHeader
            :title="section.title"
            description="Newest first. Links and unsupported files are skipped."
          />
        </div>
        <UiEmpty
          v-if="!groups[section.direction].length"
          :title="`No ${section.title.toLowerCase()} yet`"
        >
          <template #icon>
            <ArrowUpFromLine v-if="section.direction === 'upload'" />
            <ArrowDownToLine v-else />
          </template>
          {{ section.empty }}
        </UiEmpty>
        <ul
          v-else
          class="m-0 max-h-[36rem] list-none overflow-y-auto border-t p-0"
          :aria-label="`${section.title} started`"
        >
          <li
            v-for="group in groups[section.direction]"
            :key="group.key"
            class="grid gap-2 border-b py-3 last:border-b-0"
          >
            <div class="flex items-center gap-2 pr-5 pl-3">
              <UiButton
                variant="ghost"
                size="icon-sm"
                :aria-expanded="expanded(group)"
                :aria-label="`${expanded(group) ? 'Hide' : 'Show'} files in ${group.name}`"
                @click="toggle(group)"
                ><ChevronRight
                  class="transition-transform"
                  :class="expanded(group) ? 'rotate-90' : ''"
                  aria-hidden="true"
              /></UiButton>
              <div class="grid min-w-0 flex-1 gap-0.5">
                <span class="truncate text-sm font-medium" :title="group.name">{{
                  group.name
                }}</span>
                <span class="text-muted-foreground text-xs tabular-nums" role="status">{{
                  group.batch
                    ? summary(group.batch)
                    : `${group.items.length} ${group.items.length === 1 ? 'file' : 'files'}`
                }}</span>
              </div>
              <template v-if="group.batch">
                <UiBadge :tone="stateTone[group.batch.state] ?? 'neutral'">{{
                  stateLabel[group.batch.state] ?? group.batch.state
                }}</UiBadge>
                <UiButton
                  v-if="canPause(group.batch.state)"
                  variant="ghost"
                  size="icon-sm"
                  :disabled="busy"
                  :aria-label="`Pause ${group.name}`"
                  title="Pause"
                  @click="emit('action', group.batch.id, true, 'pause')"
                  ><Pause aria-hidden="true"
                /></UiButton>
                <UiButton
                  v-if="group.batch.state === 'paused'"
                  variant="ghost"
                  size="icon-sm"
                  :disabled="busy"
                  :aria-label="`Resume ${group.name}`"
                  title="Resume"
                  @click="emit('action', group.batch.id, true, 'resume')"
                  ><Play aria-hidden="true"
                /></UiButton>
                <UiButton
                  v-if="['failed', 'partial', 'cancelled'].includes(group.batch.state)"
                  variant="ghost"
                  size="icon-sm"
                  :disabled="busy"
                  :aria-label="`Retry ${group.name}`"
                  title="Retry unresolved"
                  @click="emit('action', group.batch.id, true, 'retry')"
                  ><RotateCcw aria-hidden="true"
                /></UiButton>
                <UiButton
                  v-if="canCancel(group.batch.state)"
                  variant="ghost"
                  size="icon-sm"
                  :aria-label="`Cancel ${group.name}`"
                  title="Cancel"
                  @click="emit('action', group.batch.id, true, 'cancel')"
                  ><X aria-hidden="true"
                /></UiButton>
              </template>
            </div>

            <div v-if="group.batch" class="grid gap-2 px-5 empty:hidden">
              <UiProgress
                v-if="!group.batch.finishedAt && group.batch.files"
                :value="percent(group.batch.completed, group.batch.files)"
              />
              <p v-if="group.batch.state === 'all_skipped'" class="text-warning m-0 text-xs">
                Zero files transferred. Every selected item was skipped.
              </p>
              <p v-if="group.batch.error" class="text-destructive m-0 text-xs wrap-anywhere">
                {{ group.batch.error }}
              </p>
              <div
                v-if="group.batch.oversized > 0"
                class="bg-muted grid gap-2 rounded-lg p-3 text-xs"
                role="alert"
              >
                <p class="text-warning m-0">
                  {{ group.batch.oversized }}
                  {{ group.batch.oversized === 1 ? 'file is' : 'files are' }} larger than the 16 GiB
                  private file limit and {{ group.batch.oversized === 1 ? 'was' : 'were' }} skipped.
                </p>
                <p
                  v-if="confirmUnencrypted?.batch && confirmUnencrypted.id === group.batch.id"
                  class="m-0"
                >
                  Their names and contents will be stored unencrypted on the server, in the same
                  folder. Anyone with access to the server can read them.
                </p>
                <div class="flex flex-wrap gap-2">
                  <UiButton
                    v-if="confirmUnencrypted?.batch && confirmUnencrypted.id === group.batch.id"
                    size="sm"
                    :disabled="busy"
                    @click="oversizedChoice(group.batch.id, true, 'unencrypted')"
                    >Upload unencrypted</UiButton
                  >
                  <UiButton
                    v-else
                    variant="outline"
                    size="sm"
                    :disabled="busy"
                    @click="confirmUnencrypted = { id: group.batch.id, batch: true }"
                    >Upload unencrypted…</UiButton
                  >
                  <UiButton
                    variant="ghost"
                    size="sm"
                    :disabled="busy"
                    @click="oversizedChoice(group.batch.id, true, 'keep_skipped')"
                    >Keep skipped</UiButton
                  >
                </div>
              </div>
              <details v-if="group.batch.skipped > 0" class="text-xs">
                <summary class="text-muted-foreground hover:text-foreground cursor-pointer">
                  Review skipped items ({{ group.batch.skipped }})
                </summary>
                <ul
                  class="m-0 mt-2 grid list-none gap-2 p-0"
                  aria-label="Skipped paths and reasons"
                >
                  <li
                    v-for="(skipped, index) in (group.batch.skippedItems ?? []).slice(0, 100)"
                    :key="index"
                    class="grid gap-0.5"
                  >
                    <span class="font-mono wrap-anywhere">{{ skipped.path }}</span>
                    <span class="text-muted-foreground wrap-anywhere">{{ skipped.reason }}</span>
                    <div v-if="skipped.unencrypted" class="flex flex-wrap items-center gap-2">
                      <template
                        v-if="!confirmUnencrypted?.batch && confirmUnencrypted?.id === skipped.id"
                      >
                        <span>Store this file unencrypted on the server?</span>
                        <UiButton
                          size="sm"
                          :disabled="busy"
                          @click="oversizedChoice(skipped.id, false, 'unencrypted')"
                          >Upload unencrypted</UiButton
                        >
                        <UiButton variant="ghost" size="sm" @click="confirmUnencrypted = null"
                          >Cancel</UiButton
                        >
                      </template>
                      <template v-else>
                        <UiButton
                          variant="outline"
                          size="sm"
                          :disabled="busy"
                          @click="confirmUnencrypted = { id: skipped.id, batch: false }"
                          >Upload unencrypted…</UiButton
                        >
                        <UiButton
                          variant="ghost"
                          size="sm"
                          :disabled="busy"
                          @click="oversizedChoice(skipped.id, false, 'keep_skipped')"
                          >Keep skipped</UiButton
                        >
                      </template>
                    </div>
                  </li>
                </ul>
                <p
                  v-if="group.batch.skipped > (group.batch.skippedItems?.length ?? 0)"
                  class="text-muted-foreground m-0 mt-2"
                >
                  Showing {{ Math.min(100, group.batch.skippedItems?.length ?? 0) }} of
                  {{ group.batch.skipped }} skipped paths.
                </p>
              </details>
            </div>

            <template v-if="expanded(group)">
              <!-- Five one-line rows are visible; the rest scroll. -->
              <ul
                v-if="group.items.length"
                class="bg-muted/40 mx-5 my-0 max-h-[calc(5*2.25rem+4px)] list-none divide-y overflow-y-auto rounded-md border p-0"
                :aria-label="`Files: ${group.name}`"
              >
                <li
                  v-for="item in group.items"
                  :key="item.id"
                  class="flex h-9 items-center gap-2 pr-1 pl-3 text-sm"
                >
                  <span class="min-w-0 flex-1 truncate" :title="item.name">{{ item.name }}</span>
                  <span
                    class="max-w-[45%] truncate text-xs tabular-nums"
                    :class="item.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'"
                    :title="status(item)"
                  >
                    <template v-if="combined && !group.batch"
                      >{{ scopeName(item.scope) }} · </template
                    >{{ status(item) }}
                  </span>
                  <UiProgress
                    v-if="item.state === 'active'"
                    class="w-16! shrink-0"
                    :value="percent(item.done, item.total)"
                  />
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
                </li>
              </ul>
              <p v-else class="text-muted-foreground m-0 px-5 text-xs">
                No file details are listed for this {{ section.direction }}.
              </p>
            </template>
          </li>
        </ul>
      </UiCard>
    </div>
    <p v-if="transfers.length >= 200" class="text-muted-foreground m-0 text-xs">
      File lists show up to 200 recent transfers. Summaries include all files.
    </p>
  </div>
</template>
