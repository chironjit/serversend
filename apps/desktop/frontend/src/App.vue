<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef } from 'vue';
import { Events } from '@wailsio/runtime';
import {
  ArrowDownToLine,
  ArrowLeftRight,
  ArrowUpFromLine,
  ChevronDown,
  ChevronRight,
  Download,
  FileText,
  Folder,
  FolderOpen,
  FolderPlus,
  Home,
  Loader2,
  LogOut,
  RefreshCw,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  Upload,
  UserRound,
} from 'lucide-vue-next';
import { TransferService } from '../bindings/github.com/chironjit/serversend/apps/desktop';
import type {
  Account,
  Batch,
  File,
  Item,
  Profile,
} from '../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import type { DeviceInfo } from '../bindings/github.com/chironjit/serversend/shared/api/models';
import type { ServerProbe } from '../bindings/github.com/chironjit/serversend/shared/client/models';
import BrandMark from './components/ui/BrandMark.vue';
import ThemeSwitch from './components/ui/ThemeSwitch.vue';
import StorageDashboard from './components/StorageDashboard.vue';
import RecoveryPanel from './components/RecoveryPanel.vue';
import VaultPanel from './components/VaultPanel.vue';
const recovery = ref<{ id?: string; name?: string }>();
import UiAlert from './components/ui/UiAlert.vue';
import UiBadge from './components/ui/UiBadge.vue';
import UiButton from './components/ui/UiButton.vue';
import UiCard from './components/ui/UiCard.vue';
import UiCardHeader from './components/ui/UiCardHeader.vue';
import UiCode from './components/ui/UiCode.vue';
import UiEmpty from './components/ui/UiEmpty.vue';
import UiField from './components/ui/UiField.vue';
import UiInput from './components/ui/UiInput.vue';
import UiProgress from './components/ui/UiProgress.vue';

type View =
  | 'connect'
  | 'approve'
  | 'password'
  | 'replace'
  | 'enroll'
  | 'recovery'
  | 'code'
  | 'reauth'
  | 'files';
type Theme = 'system' | 'light' | 'dark';

const view = ref<View>('connect');
const signingIn = computed(() => view.value !== 'files');
const busy = ref(false);
const error = ref('');

const profiles = ref<Profile[]>([]);
const remember = ref(false);
const combinedQueue = ref(false);
const editingProfile = ref('');
const profileName = ref('');
const profileRoutes = ref('');
const repairProfile = ref('');
function editProfile(saved: Profile) {
  editingProfile.value = saved.origin;
  profileName.value = saved.name ?? '';
  profileRoutes.value = (saved.endpoints ?? []).join('\n');
}
const saveProfile = () =>
  attempt(async () => {
    await TransferService.EditProfile(
      editingProfile.value,
      profileName.value,
      profileRoutes.value.split(/\s+/).filter(Boolean),
    );
    editingProfile.value = '';
    await loadProfiles();
  });
const repairTrust = () =>
  attempt(async () => {
    const origin = repairProfile.value;
    await TransferService.RemoveProfile(origin);
    repairProfile.value = '';
    address.value = origin;
    await loadProfiles();
  });
function scopeName(scope: string) {
  const saved = profiles.value.find(
    (p) => p.trust?.identity?.server_id + '/' + p.account_id === scope,
  );
  return saved?.name || saved?.origin || scope;
}
async function loadProfiles() {
  profiles.value = (await TransferService.Profiles()) ?? [];
}
const openProfile = (origin: string) =>
  attempt(async () => {
    address.value = origin;
    const saved = profiles.value.find((p) => p.origin === origin);
    remember.value = saved?.remember ?? false;
    username.value = saved?.username ?? '';
    account.value = undefined;
    transfers.value = [];
    batches.value = [];
    samples.clear();
    devices.value = [];
    showDevices.value = false;
    revokeDeviceID.value = '';
    files.value = [];
    selectedIDs.value = [];
    catalogRevision.value = '';
    libraryRequest++;
    folders.value = [];
    view.value = 'password';
    const restored = await TransferService.OpenProfile(origin);
    if (restored.username) {
      account.value = restored;
      view.value = restored.reauthenticationRequired ? 'reauth' : 'files';
      if (view.value === 'reauth') return;
      await loadFilePage('');
      await Promise.all([refreshBatches(), loadBandwidth()]);
    }
  });
const removeProfile = (origin: string) =>
  attempt(async () => {
    await TransferService.RemoveProfile(origin);
    await loadProfiles();
  });
const address = ref('');
const probe = ref<ServerProbe>();
const username = ref('');
const password = ref('');
const newPassword = ref('');
const code = ref('');
const enrollment = ref<{ secret: string; qr: string }>();
const recoveryCodes = ref<string[]>([]);
const account = ref<Account>();
const deviceCursor = ref('');
const previousDevicePages = ref<string[]>([]);
const showDevices = ref(false);
const devices = ref<DeviceInfo[]>([]);
const revokeDeviceID = ref('');
const loadDevices = () =>
  attempt(async () => {
    devices.value = (await TransferService.Devices('')) ?? [];
    showDevices.value = true;
    deviceCursor.value = '';
    previousDevicePages.value = [];
    revokeDeviceID.value = '';
  });
const changeDevicePage = (back = false) =>
  attempt(async () => {
    const cursor = back
      ? (previousDevicePages.value.at(-1) ?? '')
      : (devices.value.at(-1)?.id ?? '');
    const result = (await TransferService.Devices(cursor)) ?? [];
    if (back) previousDevicePages.value.pop();
    else previousDevicePages.value.push(deviceCursor.value);
    deviceCursor.value = cursor;
    devices.value = result;
    revokeDeviceID.value = '';
  });
const revokeDevice = () =>
  attempt(async () => {
    await TransferService.RevokeDevice(revokeDeviceID.value);
    revokeDeviceID.value = '';
    devices.value = (await TransferService.Devices(deviceCursor.value)) ?? [];
  });
function deviceDate(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Unknown';
}

const files = ref<File[]>([]);
const fileCursor = ref('');
const nextFileCursor = ref('');
const previousFilePages = ref<string[]>([]);
const filePageNumber = ref(1);
const catalogRevision = ref('');
const catalogSnapshot = ref('');
const catalogNotice = ref('');
const selectedIDs = ref<string[]>([]);
const restorePolicy = ref('fail');
let libraryRequest = 0;

async function loadFilePage(
  folder: string,
  cursor = '',
  direction: 'reset' | 'next' | 'back' = 'reset',
) {
  const request = ++libraryRequest;
  let effectiveCursor = cursor;
  let page;
  let resetNotice = '';
  try {
    page = await TransferService.FilesPage(folder, cursor);
  } catch (reason) {
    if (!cursor) throw reason;
    // A continuation cannot be trusted after a catalog mutation or reconnect.
    // Restart once; the fresh request also surfaces authentication/network errors.
    page = await TransferService.FilesPage(folder, '');
    effectiveCursor = '';
    resetNotice = 'The folder changed or its page expired. Showing the first page.';
  }
  if (
    effectiveCursor &&
    (page.revision !== catalogRevision.value || page.snapshot !== catalogSnapshot.value)
  ) {
    page = await TransferService.FilesPage(folder, '');
    effectiveCursor = '';
    resetNotice = 'The folder changed. Showing the latest first page.';
  }
  if (request !== libraryRequest || view.value !== 'files') return false;
  if (!effectiveCursor || direction === 'reset') {
    previousFilePages.value = [];
    filePageNumber.value = 1;
  } else if (direction === 'next') {
    previousFilePages.value = [...previousFilePages.value, fileCursor.value].slice(-100);
    filePageNumber.value++;
  } else {
    previousFilePages.value.pop();
    filePageNumber.value--;
  }
  fileCursor.value = effectiveCursor;
  nextFileCursor.value = page.nextCursor;
  catalogRevision.value = page.revision;
  catalogSnapshot.value = page.snapshot;
  files.value = page.files ?? [];
  selectedIDs.value = [];
  catalogNotice.value = resetNotice;
  return true;
}
const changeFilePage = (back = false) =>
  attempt(async () => {
    const cursor = back ? (previousFilePages.value.at(-1) ?? '') : nextFileCursor.value;
    await loadFilePage(parent.value, cursor, back ? 'back' : 'next');
  });
const restoreSelection = (ids = selectedIDs.value) =>
  attempt(async () => {
    if (!ids.length) return;
    await TransferService.ChooseAndRestore(ids, restorePolicy.value);
    await refreshBatches();
  });
const bandwidth = ref('0');
async function loadBandwidth() {
  bandwidth.value = String((await TransferService.Bandwidth()) / 1048576);
}
const setBandwidth = () =>
  attempt(async () => {
    const limit = Number(bandwidth.value);
    if (!Number.isFinite(limit) || limit < 0 || limit > Number.MAX_SAFE_INTEGER / 1048576) {
      throw new Error('Enter a nonnegative bandwidth limit. Use 0 for unlimited.');
    }
    await TransferService.SetBandwidth(Math.round(limit * 1048576));
  });
const folders = ref<{ id: string; name: string }[]>([]);
const parent = computed(() => folders.value.at(-1)?.id ?? '');
const folderName = ref('');
const rates = ref<{ up: number; down: number }[]>([]);
let rateTimer: ReturnType<typeof setInterval> | undefined;
const speed = ref({ up: 0, down: 0 });
const clock = ref(Date.now());
const samples = new Map<number, { done: number; time: number; direction: string }>();
function sampleRates() {
  const now = performance.now();
  let up = 0,
    down = 0;
  for (const item of transfers.value) {
    const old = samples.get(item.id);
    if (old) {
      const rate = Math.max(0, item.done - old.done) / ((now - old.time) / 1000);
      if (item.direction === 'upload') up += rate;
      else down += rate;
    }
    samples.set(item.id, { done: item.done, time: now, direction: item.direction });
  }
  speed.value = { up, down };
  clock.value = Date.now();
  rates.value = [...rates.value, speed.value].slice(-60);
}
function chart(direction: 'up' | 'down') {
  const max = Math.max(1, ...rates.value.flatMap((r) => [r.up, r.down]));
  return rates.value.map((r, i) => `${(i * 300) / 59},${60 - (r[direction] / max) * 56}`).join(' ');
}
const openFolder = (file: File) =>
  attempt(async () => {
    if (await loadFilePage(file.id)) folders.value.push({ id: file.id, name: file.name });
  });
const navigate = (depth: number) =>
  attempt(async () => {
    if (await loadFilePage(folders.value[depth - 1]?.id ?? ''))
      folders.value = folders.value.slice(0, depth);
  });
const pendingDelete = ref<File>();
const deleteEntry = () =>
  attempt(async () => {
    if (!pendingDelete.value) return;
    await TransferService.DeleteEntry(pendingDelete.value.id);
    pendingDelete.value = undefined;
    await loadFilePage(parent.value);
  });
const createFolder = () =>
  attempt(async () => {
    await TransferService.CreateDirectory(parent.value, folderName.value);
    folderName.value = '';
    await loadFilePage(parent.value);
  });
const uploadMenu = ref(false);
const uploadFolder = () =>
  attempt(async () => {
    uploadMenu.value = false;
    try {
      await TransferService.ChooseAndUploadFolder(parent.value);
    } finally {
      await refreshBatches();
    }
    await loadFilePage(parent.value);
  });
// Replaced wholesale on each event batch; deep reactivity over thousands of
// queued files is what made large folder uploads freeze the window.
const transfers = shallowRef<Item[]>([]);
const maxTransferRows = 200;
const batches = ref<Batch[]>([]);
const batchError = ref('');
let batchTimer: ReturnType<typeof setInterval> | undefined;
let fetchingBatches = false;
let pendingBatchRefresh = false;
const recentBatches = computed(() => batches.value.toReversed());
const batchStateLabel: Record<string, string> = {
  scanning: 'Scanning',
  running: 'Transferring',
  queued: 'Queued',
  paused: 'Paused',
  waiting_network: 'Waiting for connection',
  waiting_authentication: 'Sign-in required',
  waiting_capacity: 'Waiting for space',
  retry_scheduled: 'Retry scheduled',
  cancel_pending: 'Cancelling',
  cancelling: 'Cancelling',
  partial: 'Completed with issues',
  complete: 'Completed',
  done: 'Completed',
  done_with_skips: 'Completed with skipped items',
  all_skipped: 'Nothing uploaded',
  cancelled: 'Cancelled',
  failed: 'Failed',
};
const batchStateTone: Record<string, 'primary' | 'neutral' | 'success' | 'warning' | 'danger'> = {
  scanning: 'primary',
  running: 'primary',
  done: 'success',
  done_with_skips: 'warning',
  all_skipped: 'warning',
  cancelled: 'neutral',
  failed: 'danger',
};
const terminalStates = new Set([
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
  !terminalStates.has(state) &&
  state !== 'paused' &&
  state !== 'cancel_pending' &&
  state !== 'cancelling';
const canCancel = (state: string) =>
  !terminalStates.has(state) && state !== 'cancel_pending' && state !== 'cancelling';
const queueAction = (id: number, batch: boolean, action: string) =>
  attempt(async () => {
    if (combinedQueue.value) {
      const scope =
        (batch
          ? batches.value.find((b) => b.id === id)?.scope
          : transfers.value.find((t) => t.id === id)?.scope) ?? '';
      await TransferService.QueueActionScoped(scope, id, batch, action);
    } else await TransferService.QueueAction(id, batch, action);
    await refreshBatches();
  });
function cancelTransfer(id: number) {
  void queueAction(id, false, 'cancel');
}
function cancelBatch(id: number) {
  void queueAction(id, true, 'cancel');
}

async function refreshBatches() {
  if (view.value !== 'files') return;
  if (fetchingBatches) {
    pendingBatchRefresh = true;
    return;
  }
  fetchingBatches = true;
  const scope = account.value;
  try {
    const [list, items, health] = await Promise.all([
      combinedQueue.value ? TransferService.CombinedBatches() : TransferService.Batches(),
      combinedQueue.value ? TransferService.CombinedTransfers() : TransferService.Transfers(),
      TransferService.QueueHealth(),
    ]);
    if (view.value === 'files' && account.value === scope) {
      batches.value = (list ?? []).slice(-100);
      transfers.value = boundTransfers(items ?? []);
      batchError.value = health ?? '';
    }
  } catch (reason) {
    if (account.value === scope) batchError.value = failure(reason);
  } finally {
    fetchingBatches = false;
    if (pendingBatchRefresh) {
      pendingBatchRefresh = false;
      void refreshBatches();
    }
  }
}
const theme = ref<Theme>('system');
let stopEvents: (() => void) | undefined;

const fingerprintGroups = computed(
  () => probe.value?.fingerprint.match(/.{1,4}/g)?.join(' ') ?? '',
);
// Running work first, then what is next, then the newest finished rows.
const recentTransfers = computed(() => {
  const list = transfers.value;
  const active = list.filter((t) => t.state === 'active');
  const queued = list.filter((t) => t.state === 'queued');
  const finished = list.filter((t) => t.state !== 'active' && t.state !== 'queued').toReversed();
  return [...active, ...queued.slice(0, 20), ...finished].slice(0, maxTransferRows);
});
const activeCount = computed(
  () => transfers.value.filter((t) => t.state === 'active' || t.state === 'queued').length,
);
const stateTone: Record<string, 'primary' | 'neutral' | 'success' | 'warning' | 'danger'> = {
  active: 'primary',
  running: 'primary',
  paused: 'neutral',
  waiting_network: 'warning',
  waiting_authentication: 'warning',
  waiting_capacity: 'warning',
  retry_scheduled: 'warning',
  cancel_pending: 'neutral',
  queued: 'neutral',
  done: 'success',
  failed: 'danger',
  skipped: 'warning',
  cancelled: 'neutral',
};
const stateLabel: Record<string, string> = {
  active: 'Active',
  running: 'Active',
  paused: 'Paused',
  waiting_network: 'Waiting for connection',
  waiting_authentication: 'Sign-in required',
  waiting_capacity: 'Waiting for space',
  retry_scheduled: 'Retry scheduled',
  cancel_pending: 'Cancelling',
  cancelling: 'Cancelling',
  queued: 'Queued',
  done: 'Done',
  failed: 'Failed',
  skipped: 'Skipped',
  cancelled: 'Cancelled',
};

function failure(reason: unknown): string {
  if (reason instanceof Error && reason.message !== '') return reason.message;
  return typeof reason === 'string' && reason !== '' ? reason : 'Something went wrong. Try again.';
}

// Every action shares one busy flag and one error line, so a failed step
// always leaves the user on the same screen with a reason and a way to retry.
async function attempt(action: () => Promise<void>) {
  busy.value = true;
  error.value = '';
  try {
    await action();
  } catch (reason) {
    error.value = failure(reason);
  } finally {
    busy.value = false;
  }
}

async function follow(next: string) {
  code.value = '';
  if (next === 'replace_password') {
    view.value = 'replace';
  } else if (next === 'enroll_totp') {
    enrollment.value = await TransferService.BeginEnrollment();
    view.value = 'enroll';
  } else {
    view.value = 'code';
  }
}

const check = () =>
  attempt(async () => {
    probe.value = await TransferService.Probe(address.value);
    view.value = 'approve';
  });

const chooseCA = () =>
  attempt(async () => {
    probe.value = await TransferService.ChooseServerCA(probe.value?.origin ?? address.value);
  });

const approve = () =>
  attempt(async () => {
    await TransferService.Approve(probe.value?.fingerprint ?? '');
    view.value = 'password';
  });

const signIn = () =>
  attempt(async () => {
    const step = await TransferService.SignIn(username.value, password.value);
    password.value = '';
    await follow(step.next);
  });

const replacePassword = () =>
  attempt(async () => {
    const step = await TransferService.ReplacePassword(newPassword.value);
    newPassword.value = '';
    await follow(step.next);
  });

const confirmEnrollment = () =>
  attempt(async () => {
    recoveryCodes.value = (await TransferService.ConfirmEnrollment(code.value)) ?? [];
    enrollment.value = undefined;
    code.value = '';
    view.value = 'recovery';
  });

const completeReauthentication = () =>
  attempt(async () => {
    account.value = await TransferService.CompleteReauthentication(code.value);
    code.value = '';
    view.value = 'files';
    await Promise.all([refreshBatches(), loadBandwidth()]);
    await loadFilePage(parent.value);
  });
let sessionTimer: ReturnType<typeof setInterval> | undefined;
let sessionChecking = false;
async function checkSession() {
  if (view.value !== 'files' || busy.value || sessionChecking) return;
  sessionChecking = true;
  try {
    const status = await TransferService.SessionStatus();
    if (
      status.next === 'files' &&
      catalogRevision.value &&
      (await TransferService.CatalogChanged(catalogRevision.value))
    ) {
      await loadFilePage(parent.value);
      catalogNotice.value = 'The folder changed. Showing the latest first page.';
    }
    if (status.next === 'reauth') {
      code.value = '';
      view.value = 'reauth';
    } else if (status.next === 'password') {
      account.value = undefined;
      devices.value = [];
      showDevices.value = false;
      revokeDeviceID.value = '';
      files.value = [];
      selectedIDs.value = [];
      catalogRevision.value = '';
      libraryRequest++;
      folders.value = [];
      view.value = 'password';
      error.value = 'Device authorization expired or was revoked. Sign in again.';
    }
  } catch {
    error.value = 'Could not check the device connection. Retrying shortly.';
  } finally {
    sessionChecking = false;
  }
}
const completeSignIn = () =>
  attempt(async () => {
    await TransferService.SetRemember(remember.value);
    account.value = await TransferService.CompleteSignIn(code.value);
    await loadProfiles();
    code.value = '';
    recoveryCodes.value = [];
    view.value = 'files';
    await Promise.all([refreshBatches(), loadBandwidth()]);
    await loadFilePage(parent.value);
  });

const refresh = () =>
  attempt(async () => {
    await loadFilePage(parent.value);
  });

const upload = () =>
  attempt(async () => {
    uploadMenu.value = false;
    try {
      await TransferService.ChooseAndUpload(parent.value);
    } finally {
      await refreshBatches();
    }
    await loadFilePage(parent.value);
  });

const download = (file: File) =>
  attempt(async () => {
    await TransferService.ChooseAndDownload(file.id);
  });

const signOut = () =>
  attempt(async () => {
    try {
      await TransferService.SignOut();
    } finally {
      account.value = undefined;
      devices.value = [];
      showDevices.value = false;
      revokeDeviceID.value = '';
      folders.value = [];
      files.value = [];
      selectedIDs.value = [];
      catalogRevision.value = '';
      libraryRequest++;
      transfers.value = [];
      samples.clear();
      batches.value = [];
      batchError.value = '';
      view.value = 'password';
    }
  });

function percent(item: Item): number {
  if (item.state === 'done') return 100;
  return item.total > 0 ? Math.floor((item.done / item.total) * 100) : 0;
}

function size(bytes: number): string {
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

function duration(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}h ${m}m ${s}s`;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

// Elapsed wall time for a selection, including scanning and waiting.
function timing(batch: Batch): string {
  if (!batch.finishedAt) return `Running for ${duration(clock.value - batch.startedAt)}`;
  const ms = batch.finishedAt - batch.startedAt;
  const rate = ms > 0 ? ` · ${size((batch.bytes * 1000) / ms)}/s average` : '';
  return `Took ${duration(ms)} · ${size(batch.bytes)}${rate}`;
}

function status(item: Item): string {
  if (item.state === 'failed' || item.state === 'skipped') return item.error;
  if (item.state === 'queued') return 'Waiting';
  if (
    item.state === 'paused' ||
    item.state.startsWith('waiting_') ||
    item.state === 'retry_scheduled' ||
    item.state === 'cancel_pending'
  )
    return item.error || stateLabel[item.state] || item.state;
  if (item.state === 'done') return `Done · ${size(item.total)}`;
  return `${size(item.done)} of ${size(item.total)} · ${size(item.speed ?? 0)}/s`;
}

function boundTransfers(list: Item[]) {
  const active = list.filter((item) => item.state === 'active' || item.state === 'running');
  const pending = list
    .filter(
      (item) =>
        !terminalStates.has(item.state) && item.state !== 'active' && item.state !== 'running',
    )
    .slice(0, 20);
  const finished = list
    .filter((item) => terminalStates.has(item.state))
    .sort((a, b) => b.id - a.id);
  const bounded = [...active, ...pending, ...finished].slice(0, maxTransferRows);
  const kept = new Set(bounded.map((item) => item.id));
  for (const id of samples.keys()) if (!kept.has(id)) samples.delete(id);
  return bounded;
}

// Events arrive as batches of changed transfers, a few times a second.
function onTransfers(items: Item[]) {
  if (view.value !== 'files') return;
  if (combinedQueue.value) {
    // Reconcile through the backend's connected-account filter: buffered
    // events may outlive profile removal or sign-out.
    if (
      items.some(
        (item) =>
          item.scope === account.value?.scope &&
          item.state === 'done' &&
          item.direction === 'upload',
      )
    )
      refreshFilesSoon();
    void refreshBatches();
    return;
  }
  const list = [...transfers.value];
  const index = new Map(list.map((entry, position) => [entry.id, position]));
  let uploaded = false;
  for (const item of items) {
    if (item.scope && item.scope !== account.value?.scope) continue;
    const position = index.get(item.id);
    if (position === undefined) {
      index.set(item.id, list.length);
      list.push(item);
      samples.set(item.id, { done: 0, time: performance.now(), direction: item.direction });
    } else list[position] = item;
    uploaded ||= item.state === 'done' && item.direction === 'upload';
  }
  transfers.value = boundTransfers(list);
  if (uploaded && view.value === 'files') refreshFilesSoon();
}

// At most one listing request every two seconds while uploads complete.
let filesTimer: ReturnType<typeof setTimeout> | undefined;
let filesRefreshedAt = 0;
function refreshFilesSoon() {
  if (filesTimer) return;
  const run = () => {
    filesTimer = undefined;
    filesRefreshedAt = Date.now();
    void loadFilePage(parent.value).catch((reason) => {
      error.value = failure(reason);
    });
  };
  const wait = filesRefreshedAt + 2000 - Date.now();
  if (wait <= 0) run();
  else filesTimer = setTimeout(run, wait);
}

function applyTheme(next: Theme) {
  theme.value = next;
  if (next === 'system') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = next;
  try {
    localStorage.setItem('serversend-theme', next);
  } catch {
    // The choice still applies to this window when storage is unavailable.
  }
}

onMounted(() => {
  void loadProfiles().catch(() => {
    error.value = 'Could not load saved servers';
  });
  let saved: string | null = null;
  try {
    saved = localStorage.getItem('serversend-theme');
  } catch {
    saved = null;
  }
  applyTheme(saved === 'light' || saved === 'dark' ? saved : 'system');
  rateTimer = setInterval(sampleRates, 1000);
  sessionTimer = setInterval(() => void checkSession(), 30000);
  batchTimer = setInterval(() => {
    if (view.value === 'files' && !busy.value) {
      void refreshBatches();
    }
  }, 1000);
  stopEvents = Events.On('transfer', (event: { data: Item[] | null }) =>
    onTransfers(event.data ?? []),
  );
});
onUnmounted(() => {
  clearInterval(sessionTimer);
  stopEvents?.();
  clearInterval(rateTimer);
  clearInterval(batchTimer);
  clearTimeout(filesTimer);
});
</script>

<template>
  <!-- Onboarding and sign-in: one quiet, centered card per step. -->
  <div
    v-if="signingIn"
    class="relative flex min-h-screen flex-col items-center justify-center px-4 py-12"
  >
    <div class="absolute top-4 right-4">
      <ThemeSwitch :model-value="theme" @update:model-value="applyTheme" />
    </div>
    <div class="mb-8 flex items-center gap-2.5">
      <BrandMark />
      <h1 class="m-0 text-lg font-semibold tracking-tight">ServerSend</h1>
    </div>
    <UiAlert v-if="error" class="mb-4 w-full max-w-md">{{ error }}</UiAlert>
    <UiCard class="w-full max-w-md p-6 sm:p-7">
      <form v-if="view === 'connect'" class="grid gap-5" @submit.prevent="check">
        <UiCardHeader
          title="Connect to a server"
          description="Enter the address your server operator gave you."
        />
        <div v-for="saved in profiles" :key="saved.origin" class="flex gap-2">
          <UiButton
            type="button"
            variant="outline"
            :disabled="busy"
            @click="openProfile(saved.origin)"
            >{{ saved.name || saved.origin
            }}{{ saved.username ? ' · ' + saved.username : '' }}</UiButton
          >
          <UiButton
            type="button"
            variant="ghost"
            :disabled="busy"
            @click="removeProfile(saved.origin)"
            >Remove</UiButton
          >
          <UiButton type="button" variant="ghost" :disabled="busy" @click="editProfile(saved)"
            >Edit</UiButton
          >
          <UiButton
            type="button"
            variant="ghost"
            :disabled="busy"
            @click="repairProfile = saved.origin"
            >Repair trust</UiButton
          >
        </div>
        <section v-if="editingProfile" class="grid gap-3" aria-label="Edit server profile">
          <UiField label="Display name"><UiInput v-model="profileName" maxlength="100" /></UiField>
          <label class="grid gap-1 text-sm"
            >Approved alternate routes (one HTTPS origin per line)
            <textarea
              v-model="profileRoutes"
              rows="3"
              class="border-input bg-card rounded-md border p-2"
            />
          </label>
          <p class="text-muted-foreground text-xs">
            Save checkpoints this profile's transfers. Reopen it to apply routes; sign in again if
            the session was not remembered.
          </p>
          <UiButton type="button" :disabled="busy" @click="saveProfile">Save profile</UiButton>
          <UiButton type="button" variant="ghost" @click="editingProfile = ''"
            >Cancel edit</UiButton
          >
        </section>
        <section v-if="repairProfile" class="grid gap-3" aria-label="Confirm trust repair">
          <p>
            Forget trust and saved credentials for {{ repairProfile }}? Compare the new CA with your
            operator before pairing again. Local queue records and downloaded files are retained.
          </p>
          <UiButton type="button" :disabled="busy" @click="repairTrust"
            >Forget trust and pair again</UiButton
          >
          <UiButton type="button" variant="ghost" @click="repairProfile = ''"
            >Cancel repair</UiButton
          >
        </section>
        <UiField label="Server address">
          <UiInput
            v-model="address"
            required
            autofocus
            placeholder="https://192.168.1.10:58443"
            spellcheck="false"
          />
        </UiField>
        <UiButton type="submit" :disabled="busy" class="w-full">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ busy ? 'Checking…' : 'Check server' }}
        </UiButton>
      </form>

      <section v-else-if="view === 'approve' && probe" class="grid gap-5">
        <UiCardHeader title="Is this your server?" />
        <p v-if="probe.publicly_trusted" class="text-muted-foreground m-0 text-sm leading-6">
          This computer already trusts the certificate for
          <strong class="text-foreground font-medium">{{ probe.origin }}</strong>.
        </p>
        <p v-else class="text-muted-foreground m-0 text-sm leading-6">
          <strong class="text-foreground font-medium">{{ probe.origin }}</strong> uses a private
          certificate. Choose the public CA certificate supplied by your administrator, then compare
          its fingerprint through a trusted channel before continuing.
        </p>
        <UiCode block data-test="fingerprint">{{ fingerprintGroups }}</UiCode>
        <template v-if="probe.authority_fingerprint">
          <p class="m-0 text-sm">Imported CA fingerprint</p>
          <UiCode block>{{ probe.authority_fingerprint }}</UiCode>
        </template>
        <UiButton
          v-if="!probe.publicly_trusted"
          variant="outline"
          :disabled="busy"
          @click="chooseCA"
          >Choose server CA certificate</UiButton
        >
        <p class="text-muted-foreground m-0 text-xs">Certificate: {{ probe.subject }}</p>
        <div class="flex flex-wrap gap-2">
          <UiButton
            :disabled="busy || (!probe.publicly_trusted && !probe.authority_fingerprint)"
            @click="approve"
          >
            <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
            <ShieldCheck v-else aria-hidden="true" />
            They match — continue
          </UiButton>
          <UiButton variant="ghost" :disabled="busy" @click="view = 'connect'">Back</UiButton>
        </div>
      </section>

      <form v-else-if="view === 'password'" class="grid gap-5" @submit.prevent="signIn">
        <UiCardHeader
          title="Sign in"
          description="Use the account your administrator created for you."
        />
        <UiField label="Username">
          <UiInput
            v-model="username"
            required
            autofocus
            autocomplete="username"
            spellcheck="false"
          />
        </UiField>
        <UiField
          label="Password"
          hint="New account? Enter the temporary password from your administrator."
        >
          <UiInput v-model="password" type="password" required autocomplete="current-password" />
        </UiField>
        <label class="flex items-center gap-2 text-sm"
          ><input v-model="remember" type="checkbox" />Remember sign-in on this device</label
        >
        <div class="flex flex-wrap gap-2">
          <UiButton type="submit" :disabled="busy">
            <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
            {{ busy ? 'Signing in…' : 'Continue' }}
          </UiButton>
          <UiButton variant="ghost" :disabled="busy" @click="view = 'connect'">
            Change server
          </UiButton>
        </div>
      </form>

      <form v-else-if="view === 'replace'" class="grid gap-5" @submit.prevent="replacePassword">
        <UiCardHeader
          title="Choose your password"
          description="Your temporary password must be replaced before you continue."
        />
        <UiField label="New password" hint="At least 12 characters.">
          <UiInput
            v-model="newPassword"
            type="password"
            required
            minlength="12"
            autofocus
            autocomplete="new-password"
          />
        </UiField>
        <UiButton type="submit" :disabled="busy" class="w-full">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ busy ? 'Saving…' : 'Set password' }}
        </UiButton>
      </form>

      <form
        v-else-if="view === 'enroll' && enrollment"
        class="grid gap-5"
        @submit.prevent="confirmEnrollment"
      >
        <UiCardHeader
          title="Set up two-factor sign-in"
          description="Scan this code with an authenticator app, then enter the six-digit code it shows."
        />
        <img
          class="mx-auto rounded-lg border bg-white p-2"
          :src="enrollment.qr"
          alt="QR code for your authenticator app"
          width="220"
          height="220"
        />
        <p class="text-muted-foreground m-0 text-xs leading-6">
          Can't scan? Enter this key manually:
          <UiCode class="wrap-anywhere">{{ enrollment.secret }}</UiCode>
        </p>
        <UiField label="Six-digit code">
          <UiInput
            v-model="code"
            required
            inputmode="numeric"
            pattern="[0-9]{6}"
            maxlength="6"
            autocomplete="one-time-code"
            class="font-mono"
          />
        </UiField>
        <UiButton type="submit" :disabled="busy" class="w-full">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ busy ? 'Checking…' : 'Confirm' }}
        </UiButton>
      </form>

      <section v-else-if="view === 'recovery'" class="grid gap-5">
        <UiCardHeader
          title="Save your recovery codes"
          description="Each code signs you in once if you lose your authenticator. They are shown only now — store them somewhere safe before continuing."
        />
        <ul class="m-0 grid list-none grid-cols-1 gap-2 p-0 sm:grid-cols-2">
          <li v-for="recovery in recoveryCodes" :key="recovery">
            <UiCode block class="text-center">{{ recovery }}</UiCode>
          </li>
        </ul>
        <UiButton class="w-full" @click="view = 'code'">I saved them — continue</UiButton>
      </section>

      <form
        v-else-if="view === 'code' || view === 'reauth'"
        class="grid gap-5"
        @submit.prevent="view === 'reauth' ? completeReauthentication() : completeSignIn()"
      >
        <UiCardHeader
          :title="view === 'reauth' ? 'Verify this device' : 'Enter your code'"
          :description="
            view === 'reauth'
              ? 'This device needs authenticator verification every 60 days. Your password is not required.'
              : 'Enter the current code from your authenticator app, or a recovery code. If you have just set up your authenticator, wait for it to show a new code.'
          "
        />
        <UiField label="Code">
          <UiInput
            v-model="code"
            required
            autofocus
            maxlength="64"
            autocomplete="one-time-code"
            spellcheck="false"
            class="font-mono"
          />
        </UiField>
        <div class="flex flex-wrap gap-2">
          <UiButton type="submit" :disabled="busy">
            <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
            {{ busy ? 'Signing in…' : 'Sign in' }}
          </UiButton>
          <UiButton variant="ghost" :disabled="busy" @click="view = 'password'">
            Start again
          </UiButton>
        </div>
      </form>
    </UiCard>
    <p class="text-muted-foreground mt-8 text-xs">Private file transfer for your network</p>
  </div>

  <!-- Workspace: sidebar, top bar, library and transfer queue. -->
  <div v-if="view === 'files'" class="grid min-h-screen lg:grid-cols-[224px_minmax(0,1fr)]">
    <aside
      class="bg-sidebar border-border sticky top-0 hidden h-screen flex-col border-r lg:flex"
      aria-label="Workspace navigation"
    >
      <div class="flex h-14 items-center gap-2.5 px-4">
        <BrandMark />
        <span class="text-[0.9375rem] font-semibold tracking-tight">ServerSend</span>
      </div>
      <nav class="flex flex-1 flex-col gap-1 px-3 pt-4">
        <p
          class="text-muted-foreground m-0 px-3 pb-2 text-[0.6875rem] font-medium tracking-[0.08em] uppercase"
        >
          Workspace
        </p>
        <a
          href="#library"
          aria-current="page"
          class="bg-accent text-accent-foreground flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium no-underline"
        >
          <FolderOpen class="size-4" aria-hidden="true" />
          File library
        </a>
        <a
          href="#transfers"
          class="text-muted-foreground hover:bg-muted hover:text-foreground flex items-center gap-2.5 rounded-md px-3 py-2 text-sm no-underline transition-colors"
        >
          <ArrowLeftRight class="size-4" aria-hidden="true" />
          Transfers
          <UiBadge v-if="activeCount > 0" class="ml-auto">{{ activeCount }}</UiBadge>
        </a>
      </nav>
      <div class="border-border m-3 rounded-lg border p-3">
        <p class="text-success m-0 flex items-center gap-1.5 text-xs font-medium">
          <ShieldCheck class="size-3.5" aria-hidden="true" />
          Private connection
        </p>
        <p class="text-muted-foreground m-0 mt-1 text-xs leading-5">
          Your files stay on your server.
        </p>
      </div>
    </aside>

    <div class="flex min-w-0 flex-col">
      <header
        class="border-border bg-card/80 sticky top-0 z-10 flex h-14 items-center gap-3 border-b px-4 backdrop-blur sm:px-6"
      >
        <BrandMark class="lg:hidden" />
        <h1 class="m-0 text-base font-semibold tracking-tight">Files</h1>
        <div class="ml-auto flex items-center gap-3">
          <span
            v-if="account"
            class="text-muted-foreground hidden max-w-72 truncate text-xs md:inline"
            :title="account.username + ' · ' + account.origin"
          >
            <UserRound class="mr-1 inline size-3.5" aria-hidden="true" />
            {{ account.username }} · {{ account.origin }}
          </span>
          <ThemeSwitch :model-value="theme" @update:model-value="applyTheme" />
          <UiButton variant="outline" :disabled="busy" @click="loadDevices">Devices</UiButton>
          <UiButton
            variant="outline"
            :disabled="busy"
            @click="
              attempt(async () => {
                await loadProfiles();
                view = 'connect';
              })
            "
            >Switch server / add server</UiButton
          >
          <UiButton
            v-if="account"
            variant="ghost"
            size="icon-sm"
            :disabled="busy"
            aria-label="Sign out"
            title="Sign out"
            @click="signOut"
          >
            <LogOut aria-hidden="true" />
          </UiButton>
        </div>
      </header>

      <main class="grid flex-1 items-start gap-5 p-4 sm:p-6 xl:grid-cols-[minmax(0,1fr)_360px]">
        <UiAlert v-if="error" class="xl:col-span-2">{{ error }}</UiAlert>

        <UiCard v-if="showDevices" as="section" class="p-5 xl:col-span-2">
          <UiCardHeader
            title="Signed-in devices"
            description="Recent activity indicates use in the last 90 seconds. Revoking access also stops active server transfers."
          />
          <div class="my-4 flex flex-wrap gap-2">
            <UiButton variant="outline" :disabled="busy" @click="loadDevices"
              >Refresh devices</UiButton
            >
            <UiButton variant="outline" :disabled="busy" @click="revokeDeviceID = 'others'"
              >Sign out other devices</UiButton
            >
            <UiButton variant="ghost" @click="showDevices = false">Close</UiButton>
          </div>
          <div class="my-3 flex gap-2">
            <UiButton
              variant="outline"
              :disabled="busy || previousDevicePages.length === 0"
              @click="changeDevicePage(true)"
              >Previous devices</UiButton
            >
            <UiButton
              variant="outline"
              :disabled="busy || devices.length < 100"
              @click="changeDevicePage()"
              >Next devices</UiButton
            >
          </div>
          <UiEmpty v-if="devices.length === 0" title="No devices to show" />
          <ul class="grid gap-4 p-0 list-none">
            <li
              v-for="device in devices"
              :key="device.id"
              class="border-border rounded-md border p-3"
            >
              <p class="m-0 font-medium">
                {{ device.name }} <UiBadge v-if="device.current">This device</UiBadge>
              </p>
              <p class="text-muted-foreground text-xs">
                {{ device.platform || 'Unknown platform' }} ·
                {{ device.app_version || 'Unknown version' }} · {{ device.status
                }}{{ device.recently_active ? ' · Recently active' : '' }}
              </p>
              <p class="text-muted-foreground text-xs">
                Last seen: {{ deviceDate(device.last_seen_at) }} · Signed in:
                {{ deviceDate(device.signed_in_at) }}
              </p>
              <UiButton
                v-if="!device.current && device.status !== 'revoked'"
                variant="outline"
                :disabled="busy"
                @click="revokeDeviceID = device.id"
                >Revoke access</UiButton
              >
              <p v-if="device.current" class="text-muted-foreground text-xs">
                Use Sign out to end this device session.
              </p>
            </li>
          </ul>
          <div v-if="revokeDeviceID" role="alert" class="mt-4 flex flex-wrap items-center gap-3">
            <p>
              {{
                revokeDeviceID === 'others'
                  ? 'Sign out all other devices?'
                  : 'Revoke access for this device?'
              }}
              The device will need a full sign-in to reconnect.
            </p>
            <UiButton :disabled="busy" @click="revokeDevice">Confirm revocation</UiButton>
            <UiButton variant="ghost" :disabled="busy" @click="revokeDeviceID = ''"
              >Cancel</UiButton
            >
          </div>
        </UiCard>

        <UiCard as="section" id="library" class="min-w-0">
          <div class="p-5 pb-4">
            <UiCardHeader title="File library">
              <template #action>
                <div class="flex items-center gap-2">
                  <UiBadge>{{ files.length }}</UiBadge>
                  <UiButton
                    variant="ghost"
                    size="icon-sm"
                    :disabled="busy"
                    aria-label="Refresh files"
                    title="Refresh"
                    @click="refresh"
                  >
                    <RefreshCw aria-hidden="true" />
                  </UiButton>
                  <div class="upload-menu relative" @keydown.esc="uploadMenu = false">
                    <UiButton
                      :disabled="busy"
                      :aria-expanded="uploadMenu"
                      @click="uploadMenu = !uploadMenu"
                    >
                      <Upload aria-hidden="true" />
                      Upload…
                      <ChevronDown class="size-3.5" aria-hidden="true" />
                    </UiButton>
                    <div
                      v-if="uploadMenu"
                      class="upload-options bg-popover absolute top-[calc(100%+6px)] right-0 z-20 grid min-w-44 gap-1 rounded-lg border p-1.5 shadow-md"
                    >
                      <UiButton variant="ghost" class="justify-start" @click="upload">
                        <FileText aria-hidden="true" />
                        Files…
                      </UiButton>
                      <UiButton variant="ghost" class="justify-start" @click="uploadFolder">
                        <Folder aria-hidden="true" />
                        Folder…
                      </UiButton>
                    </div>
                  </div>
                </div>
              </template>
            </UiCardHeader>
          </div>

          <nav
            class="breadcrumbs border-border flex flex-wrap items-center gap-1 border-y px-4 py-2"
            aria-label="Folder path"
          >
            <UiButton variant="ghost" size="sm" :disabled="busy" @click="navigate(0)">
              <Home class="size-3.5" aria-hidden="true" />
              All files
            </UiButton>
            <template v-for="(folder, index) in folders" :key="folder.id">
              <ChevronRight class="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
              <UiButton
                variant="ghost"
                size="sm"
                :disabled="busy"
                :aria-current="index === folders.length - 1 ? 'page' : undefined"
                :class="
                  index === folders.length - 1
                    ? 'text-foreground font-medium'
                    : 'text-muted-foreground'
                "
                @click="navigate(index + 1)"
              >
                {{ folder.name }}
              </UiButton>
            </template>
          </nav>

          <UiButton variant="outline" class="m-4" :disabled="busy" @click="recovery = {}"
            >Trash</UiButton
          >
          <VaultPanel
            @changed="refresh"
            @locked="
              () => {
                files = [];
                folders = [];
                pendingDelete = undefined;
                recovery = undefined;
                refresh();
              }
            "
          />
          <RecoveryPanel
            v-if="recovery"
            :file-id="recovery.id"
            :name="recovery.name"
            @close="recovery = undefined"
            @changed="refresh"
          />
          <section
            v-if="pendingDelete"
            class="delete-confirm border-destructive/30 bg-destructive/5 m-4 rounded-lg border p-4"
            role="alertdialog"
            aria-label="Confirm deletion"
          >
            <div class="flex items-start gap-3">
              <TriangleAlert class="text-destructive mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <div class="grid min-w-0 gap-1.5">
                <h3 class="m-0 text-sm font-semibold">Delete “{{ pendingDelete.name }}”?</h3>
                <p class="text-muted-foreground m-0 text-sm leading-6">
                  {{
                    pendingDelete.directory
                      ? 'This folder and everything inside it will move to trash.'
                      : 'This file and all its versions will move to trash.'
                  }}
                  This cannot be undone.
                </p>
                <div class="mt-1.5 flex flex-wrap gap-2">
                  <UiButton
                    variant="destructive"
                    size="sm"
                    class="danger"
                    :disabled="busy"
                    @click="deleteEntry"
                  >
                    Move to trash
                  </UiButton>
                  <UiButton
                    variant="ghost"
                    size="sm"
                    class="quiet"
                    :disabled="busy"
                    @click="pendingDelete = undefined"
                  >
                    Cancel
                  </UiButton>
                </div>
              </div>
            </div>
          </section>

          <form
            class="border-border flex flex-wrap items-center gap-2 border-b px-4 py-3"
            @submit.prevent="createFolder"
          >
            <UiInput
              v-model="folderName"
              required
              maxlength="255"
              placeholder="New folder name"
              class="max-w-60 flex-1"
            />
            <UiButton variant="outline" type="submit" :disabled="busy">
              <FolderPlus aria-hidden="true" />
              Create folder
            </UiButton>
          </form>

          <div
            class="border-border flex flex-wrap items-center gap-3 border-b px-4 py-3"
            aria-label="Restore selection"
          >
            <label class="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                :disabled="busy || !files.length"
                :checked="files.length > 0 && selectedIDs.length === files.length"
                @change="
                  selectedIDs = ($event.target as HTMLInputElement).checked
                    ? files.map((file) => file.id)
                    : []
                "
              />
              Select this page
            </label>
            <label class="flex items-center gap-2 text-sm">
              Existing files
              <select
                v-model="restorePolicy"
                :disabled="busy"
                aria-label="Existing file policy"
                class="border-input bg-card h-9 rounded-md border px-2"
              >
                <option value="fail">Stop and report</option>
                <option value="skip">Skip existing</option>
                <option value="replace">Replace existing</option>
              </select>
            </label>
            <UiButton
              variant="outline"
              :disabled="busy || selectedIDs.length === 0"
              @click="restoreSelection()"
            >
              <Download aria-hidden="true" />Restore selected ({{ selectedIDs.length }})…
            </UiButton>
          </div>
          <p v-if="catalogNotice" class="text-muted-foreground m-0 px-5 py-3 text-sm" role="status">
            {{ catalogNotice }}
          </p>
          <UiEmpty v-if="files.length === 0" title="Nothing uploaded yet">
            <template #icon><FolderOpen /></template>
            Choose “Upload…” to send your first file.
          </UiEmpty>
          <ul
            v-else
            class="list file-list m-0 max-h-[55vh] list-none overflow-y-auto p-0"
            aria-label="Files"
          >
            <li
              v-for="file in files"
              :key="file.id"
              class="group border-border hover:bg-muted/50 flex items-center gap-2 border-t px-4 py-2 transition-colors first:border-t-0"
            >
              <input
                v-model="selectedIDs"
                type="checkbox"
                :value="file.id"
                :disabled="busy"
                :aria-label="'Select ' + file.name"
              />
              <button
                v-if="file.directory"
                type="button"
                class="folder-link hover:text-primary flex min-w-0 flex-1 items-center gap-3 rounded-md border-0 bg-transparent p-1 text-left transition-colors"
                :disabled="busy"
                @click="openFolder(file)"
              >
                <span
                  class="bg-accent text-accent-foreground grid size-8 shrink-0 place-items-center rounded-md"
                  aria-hidden="true"
                >
                  <Folder class="size-4" />
                </span>
                <span class="truncate text-sm font-medium">{{ file.name }}</span>
              </button>
              <span v-else class="flex min-w-0 flex-1 items-center gap-3 p-1">
                <span
                  class="bg-muted text-muted-foreground grid size-8 shrink-0 place-items-center rounded-md"
                  aria-hidden="true"
                >
                  <FileText class="size-4" />
                </span>
                <span class="truncate text-sm">{{ file.name }}</span>
              </span>
              <UiButton
                v-if="!file.directory"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="'Download ' + file.name"
                title="Download…"
                @click="download(file)"
              >
                <Download aria-hidden="true" />
              </UiButton>
              <UiButton
                v-if="file.directory"
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="'Restore folder ' + file.name"
                title="Restore folder…"
                @click="restoreSelection([file.id])"
                ><Download aria-hidden="true"
              /></UiButton>
              <UiButton
                v-if="!file.directory"
                variant="ghost"
                size="sm"
                :disabled="busy"
                @click="recovery = { id: file.id, name: file.name }"
                >Versions</UiButton
              >
              <UiButton
                variant="ghost"
                size="icon-sm"
                class="delete-action hover:text-destructive"
                :aria-label="'Delete ' + file.name"
                title="Delete"
                :disabled="busy"
                @click="pendingDelete = file"
              >
                <Trash2 aria-hidden="true" />
              </UiButton>
            </li>
          </ul>
          <nav
            aria-label="File pages"
            class="border-border flex items-center justify-between gap-2 border-t px-4 py-3"
          >
            <UiButton
              variant="outline"
              size="sm"
              :disabled="busy || !previousFilePages.length"
              @click="changeFilePage(true)"
              >Previous files</UiButton
            >
            <span class="text-muted-foreground text-xs"
              >Page {{ filePageNumber }} · {{ files.length }} entries</span
            >
            <UiButton
              variant="outline"
              size="sm"
              :disabled="busy || !nextFileCursor"
              @click="changeFilePage()"
              >Next files</UiButton
            >
          </nav>
        </UiCard>

        <UiCard as="section" id="transfers" class="min-w-0">
          <div class="p-5 pb-0">
            <UiCardHeader title="Transfers">
              <template #action>
                <UiBadge>{{ transfers.length }}</UiBadge>
              </template>
            </UiCardHeader>
          </div>
          <label class="flex items-center gap-2 px-5 pt-3 text-sm"
            ><input v-model="combinedQueue" type="checkbox" @change="refreshBatches" />All signed-in
            servers</label
          >
          <form class="flex flex-wrap items-end gap-2 px-5 pt-4" @submit.prevent="setBandwidth">
            <label for="bandwidth" class="grid gap-1 text-xs"
              >Bandwidth limit (MiB/s; 0 = unlimited)
              <UiInput
                id="bandwidth"
                v-model="bandwidth"
                type="number"
                min="0"
                step="any"
                class="max-w-44"
                required
              />
            </label>
            <UiButton type="submit" variant="outline" size="sm" :disabled="busy"
              >Apply limit</UiButton
            >
          </form>
          <StorageDashboard :key="account?.scope" />
          <div class="grid grid-cols-2 gap-3 p-5 pb-3">
            <div class="bg-muted/60 min-w-0 rounded-lg p-3">
              <p class="text-muted-foreground m-0 flex items-center gap-1.5 text-xs">
                <ArrowUpFromLine class="text-chart-up size-3.5" aria-hidden="true" />
                Upload
              </p>
              <p class="m-0 mt-1 truncate text-lg font-semibold tabular-nums">
                {{ size(speed.up) }}/s
              </p>
            </div>
            <div class="bg-muted/60 min-w-0 rounded-lg p-3">
              <p class="text-muted-foreground m-0 flex items-center gap-1.5 text-xs">
                <ArrowDownToLine class="text-chart-down size-3.5" aria-hidden="true" />
                Download
              </p>
              <p class="m-0 mt-1 truncate text-lg font-semibold tabular-nums">
                {{ size(speed.down) }}/s
              </p>
            </div>
          </div>
          <div class="px-5">
            <svg
              viewBox="0 0 300 64"
              preserveAspectRatio="none"
              class="h-16 w-full"
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
                stroke-linecap="round"
              />
              <polyline
                :points="chart('down')"
                fill="none"
                stroke="var(--chart-down)"
                stroke-width="2"
                stroke-linejoin="round"
                stroke-linecap="round"
              />
            </svg>
            <p class="text-muted-foreground m-0 mt-2 text-xs leading-5">
              Acknowledged/verified bytes per second; includes disk and verification time.
            </p>
          </div>
          <section class="border-border mt-4 border-t px-5 py-4" aria-label="Transfer selections">
            <h3 class="m-0 text-sm font-semibold">Transfer selections</h3>
            <p class="text-muted-foreground m-0 mt-1 text-xs leading-5">
              Links and unsupported files are skipped. Select a link's real target to upload its
              contents. The latest 100 selections and up to 100 skipped paths per selection are
              shown here.
            </p>
            <div v-if="batchError" class="mt-3" role="alert">
              <p class="text-destructive m-0 text-xs">
                Transfer summaries unavailable: {{ batchError }}
              </p>
              <UiButton variant="ghost" size="sm" @click="refreshBatches">Retry summaries</UiButton>
            </div>
            <ul
              v-if="batches.length"
              class="m-0 mt-3 grid max-h-80 list-none gap-3 overflow-y-auto p-0"
              tabindex="0"
              aria-label="Recent transfer selections"
            >
              <li
                v-for="batch in recentBatches"
                :key="batch.id"
                class="border-border rounded-lg border p-3"
              >
                <p v-if="combinedQueue" class="text-muted-foreground text-xs">
                  {{ scopeName(batch.scope) }}
                </p>
                <div class="flex flex-wrap items-center gap-2">
                  <span class="min-w-0 flex-1 wrap-anywhere text-sm font-medium">{{
                    batch.name
                  }}</span>
                  <UiBadge :tone="batchStateTone[batch.state] ?? 'neutral'">{{
                    batchStateLabel[batch.state] ?? batch.state
                  }}</UiBadge>
                  <UiButton
                    v-if="canPause(batch.state)"
                    variant="ghost"
                    size="sm"
                    :disabled="busy"
                    :aria-label="`Pause ${batch.name}`"
                    @click="queueAction(batch.id, true, 'pause')"
                    >Pause</UiButton
                  >
                  <UiButton
                    v-if="batch.state === 'paused'"
                    variant="ghost"
                    size="sm"
                    :disabled="busy"
                    :aria-label="`Resume ${batch.name}`"
                    @click="queueAction(batch.id, true, 'resume')"
                    >Resume</UiButton
                  >
                  <UiButton
                    v-if="['failed', 'partial', 'cancelled'].includes(batch.state)"
                    variant="ghost"
                    size="sm"
                    :disabled="busy"
                    :aria-label="`Retry ${batch.name}`"
                    @click="queueAction(batch.id, true, 'retry')"
                    >Retry unresolved</UiButton
                  >
                  <UiButton
                    v-if="canCancel(batch.state)"
                    variant="ghost"
                    size="sm"
                    :aria-label="`Cancel ${batch.name}`"
                    @click="cancelBatch(batch.id)"
                    >Cancel</UiButton
                  >
                </div>
                <p class="text-muted-foreground m-0 mt-2 text-xs leading-5" role="status">
                  {{ batch.completed }} of {{ batch.files }} files transferred ·
                  {{ batch.directories }} folders created · {{ batch.skipped }} skipped ·
                  {{ batch.failed }} failed<template v-if="batch.cancelled">
                    · {{ batch.cancelled }} cancelled</template
                  >
                </p>
                <p
                  v-if="batch.startedAt"
                  class="text-muted-foreground m-0 mt-1 text-xs leading-5 tabular-nums"
                >
                  {{ timing(batch) }}
                </p>
                <p
                  v-if="batch.state === 'all_skipped'"
                  class="text-warning m-0 mt-1 text-xs leading-5"
                >
                  Zero files transferred. Every selected item was skipped.
                </p>
                <p
                  v-if="batch.error"
                  class="text-destructive m-0 mt-1 wrap-anywhere text-xs leading-5"
                >
                  {{ batch.error }}
                </p>
                <details v-if="batch.skipped > 0" class="mt-2 text-xs">
                  <summary class="text-foreground cursor-pointer rounded-sm py-1 font-medium">
                    Review skipped items ({{ batch.skipped }})
                  </summary>
                  <ul
                    class="m-0 mt-1 grid list-none gap-2 p-0"
                    aria-label="Skipped paths and reasons"
                  >
                    <li
                      v-for="(skipped, index) in (batch.skippedItems ?? []).slice(0, 100)"
                      :key="index"
                      class="grid gap-0.5"
                    >
                      <span class="wrap-anywhere font-mono">{{ skipped.path }}</span>
                      <span class="text-muted-foreground wrap-anywhere leading-5">{{
                        skipped.reason
                      }}</span>
                    </li>
                  </ul>
                  <p
                    v-if="batch.skipped > (batch.skippedItems?.length ?? 0)"
                    class="text-muted-foreground m-0 mt-2 leading-5"
                  >
                    Showing {{ Math.min(100, batch.skippedItems?.length ?? 0) }} of
                    {{ batch.skipped }} skipped paths. The total includes every skipped item.
                  </p>
                </details>
              </li>
            </ul>
          </section>
          <UiEmpty v-if="transfers.length === 0" title="No transfers yet">
            <template #icon><ArrowLeftRight /></template>
            Saved uploads and downloads appear here.
          </UiEmpty>
          <ul
            v-else
            class="list transfer-list border-border m-0 mt-3 max-h-[min(48vh,480px)] list-none overflow-y-auto overscroll-contain border-t p-0"
            tabindex="0"
            aria-label="Transfer queue"
          >
            <li
              v-for="item in recentTransfers"
              :key="item.id"
              class="border-border grid gap-1.5 border-t px-5 py-3 first:border-t-0"
            >
              <p v-if="combinedQueue" class="text-muted-foreground text-xs">
                {{ scopeName(item.scope) }}
              </p>
              <div class="flex items-center gap-2">
                <ArrowUpFromLine
                  v-if="item.direction === 'upload'"
                  class="text-chart-up size-4 shrink-0"
                  aria-hidden="true"
                />
                <ArrowDownToLine
                  v-else
                  class="text-chart-down size-4 shrink-0"
                  aria-hidden="true"
                />
                <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ item.name }}</span>
                <UiBadge :tone="stateTone[item.state] ?? 'neutral'" dot>
                  {{ stateLabel[item.state] ?? item.state }}
                </UiBadge>
                <UiButton
                  v-if="canPause(item.state)"
                  variant="ghost"
                  size="sm"
                  :disabled="busy"
                  :aria-label="`Pause ${item.name}`"
                  @click="queueAction(item.id, false, 'pause')"
                  >Pause</UiButton
                >
                <UiButton
                  v-if="
                    item.state === 'paused' ||
                    item.state.startsWith('waiting_') ||
                    item.state === 'retry_scheduled'
                  "
                  variant="ghost"
                  size="sm"
                  :disabled="busy"
                  :aria-label="`Resume ${item.name}`"
                  @click="queueAction(item.id, false, 'resume')"
                  >Resume</UiButton
                >
                <UiButton
                  v-if="item.state === 'failed' || item.state === 'cancelled'"
                  variant="ghost"
                  size="sm"
                  :disabled="busy"
                  :aria-label="`Retry ${item.name}`"
                  @click="queueAction(item.id, false, 'retry')"
                  >Retry</UiButton
                >
                <UiButton
                  v-if="canCancel(item.state)"
                  variant="ghost"
                  size="sm"
                  :disabled="busy"
                  :aria-label="`Cancel ${item.name}`"
                  @click="cancelTransfer(item.id)"
                  >Cancel</UiButton
                >
              </div>
              <p
                class="text-muted-foreground m-0 truncate pl-6 text-xs tabular-nums"
                :class="item.state === 'failed' ? 'text-destructive' : ''"
              >
                {{ status(item) }}
              </p>
              <div v-if="item.state === 'active'" class="pl-6">
                <UiProgress :value="percent(item)" />
              </div>
            </li>
          </ul>
          <p
            v-if="transfers.length >= maxTransferRows"
            class="text-muted-foreground border-border m-0 border-t px-5 py-3 text-xs"
          >
            Showing up to 200 recent transfers. Selection summaries include all files.
          </p>
        </UiCard>
      </main>
    </div>
  </div>
</template>
