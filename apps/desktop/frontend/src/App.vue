<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef } from 'vue';
import { Events } from '@wailsio/runtime';
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  FolderOpen,
  Laptop,
  Loader2,
  LockKeyhole,
  RefreshCw,
  Server,
} from 'lucide-vue-next';
import {
  BackgroundService,
  TransferService,
} from '../bindings/github.com/chironjit/serversend/apps/desktop';
import type {
  Account,
  Batch,
  Item,
  Profile,
} from '../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import AuthFlow, { type AuthStep } from './components/AuthFlow.vue';
import DevicesView from './components/DevicesView.vue';
import FileBrowser from './components/FileBrowser.vue';
import ServersView from './components/ServersView.vue';
import SettingsView from './components/SettingsView.vue';
import TransfersView from './components/TransfersView.vue';
import VaultSetup from './components/VaultSetup.vue';
import BrandMark from './components/ui/BrandMark.vue';
import ThemeSwitch from './components/ui/ThemeSwitch.vue';
import UiAlert from './components/ui/UiAlert.vue';
import UiBadge from './components/ui/UiBadge.vue';
import UiButton from './components/ui/UiButton.vue';
import UiCard from './components/ui/UiCard.vue';
import UiEmpty from './components/ui/UiEmpty.vue';
import { failure, size } from './format';

type Theme = 'system' | 'light' | 'dark';
type Tab = 'library' | 'transfers' | 'servers' | 'devices' | 'settings';
type Auth = {
  step: AuthStep;
  origin?: string;
  username?: string;
  remember?: boolean;
  notice?: string;
};

const tabs: { id: Tab; label: string }[] = [
  { id: 'library', label: 'Library' },
  { id: 'transfers', label: 'Transfers' },
  { id: 'servers', label: 'Servers' },
  { id: 'devices', label: 'Devices' },
  { id: 'settings', label: 'Settings' },
];
const tab = ref<Tab>('library');
const theme = ref<Theme>('system');
const loaded = ref(false);
const busy = ref(false);
const error = ref('');
const profiles = ref<Profile[]>([]);
const account = ref<Account>();
const active = ref('');
const auth = ref<Auth>();
// A sign-in to another server returns here if abandoned part-way.
let returnTo = '';
const authKey = ref(0);
// The file browser and transfer polling only run with a usable session.
const ready = computed(() => !!account.value && !auth.value);
const onboarding = computed(() => loaded.value && profiles.value.length === 0 && !account.value);
const activeProfile = computed(() => profiles.value.find((p) => p.origin === active.value));
// The open server comes first. A session whose profile is not saved yet still
// gets a card (without a storage choice, which is stored on the profile).
const library = computed(() => {
  const current =
    activeProfile.value ??
    (account.value
      ? ({ origin: account.value.origin, username: account.value.username } as Profile)
      : undefined);
  return current
    ? [current, ...profiles.value.filter((p) => p.origin !== current.origin)]
    : profiles.value;
});

const vault = ref({ privateMode: false, unlocked: false, known: false });
const choosingVault = ref(false);
const needsChoice = computed(
  () =>
    choosingVault.value ||
    (!!activeProfile.value &&
      !activeProfile.value.vault_configured &&
      !activeProfile.value.private_default),
);
const browser = ref<InstanceType<typeof FileBrowser>>();

// Development builds cannot update, so the check stays visible but disabled.
const updatesEnabled = ref(false);
const combinedQueue = ref(false);
// Replaced wholesale on each event batch; deep reactivity over thousands of
// queued files is what made large folder uploads freeze the window.
const transfers = shallowRef<Item[]>([]);
const batches = ref<Batch[]>([]);
const batchError = ref('');
const speed = ref({ up: 0, down: 0 });
const rates = ref<{ up: number; down: number }[]>([]);
const clock = ref(Date.now());
const samples = new Map<number, { done: number; time: number }>();
const maxTransferRows = 200;
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
const activeCount = computed(
  () => transfers.value.filter((t) => t.state === 'active' || t.state === 'queued').length,
);

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

function startAuth(step: AuthStep, extra: Omit<Auth, 'step'> = {}) {
  returnTo = step === 'connect' && account.value ? active.value : '';
  authKey.value++;
  auth.value = { step, ...extra };
}
function scopeName(scope: string) {
  const saved = profiles.value.find(
    (p) => p.trust?.identity?.server_id + '/' + p.account_id === scope,
  );
  return saved?.name || saved?.origin || scope;
}
async function loadProfiles() {
  profiles.value = (await TransferService.Profiles()) ?? [];
}
function clearSession() {
  account.value = undefined;
  transfers.value = [];
  batches.value = [];
  batchError.value = '';
  samples.clear();
  choosingVault.value = false;
  vault.value = { privateMode: false, unlocked: false, known: false };
}
function remember(origin: string) {
  try {
    localStorage.setItem('serversend-last-server', origin);
  } catch {
    // Startup falls back to the first saved server.
  }
}

const open = (origin: string) =>
  attempt(async () => {
    clearSession();
    auth.value = undefined;
    active.value = origin;
    remember(origin);
    const saved = profiles.value.find((p) => p.origin === origin);
    const restored = await TransferService.OpenProfile(origin);
    if (restored.username) {
      account.value = restored;
      if (restored.reauthenticationRequired) startAuth('reauth', { origin });
      else await sessionReady();
    } else startAuth('password', { origin, username: saved?.username, remember: saved?.remember });
  });
async function sessionReady() {
  tab.value = ['devices', 'transfers', 'settings'].includes(tab.value) ? tab.value : 'library';
  await Promise.all([refreshBatches(), refreshVault()]);
}
async function signedIn(next: Account) {
  account.value = next;
  active.value = next.origin;
  remember(next.origin);
  auth.value = undefined;
  await attempt(async () => {
    await loadProfiles();
    await sessionReady();
  });
}
// Leaving an unfinished sign-in reconnects the server that was open before.
function cancelAuth() {
  auth.value = undefined;
  if (returnTo) void open(returnTo);
}
const signOut = () =>
  attempt(async () => {
    try {
      await TransferService.SignOut();
    } finally {
      clearSession();
    }
  });
async function profilesChanged() {
  await attempt(loadProfiles);
  if (active.value && !profiles.value.some((p) => p.origin === active.value)) {
    clearSession();
    active.value = '';
  }
  void refreshVault();
}

async function refreshVault() {
  if (!ready.value) return;
  try {
    const [unlocked, privateMode] = await Promise.all([
      TransferService.VaultUnlocked(),
      TransferService.PrivateMode(),
    ]);
    vault.value = { unlocked, privateMode, known: true };
  } catch {
    vault.value = { ...vault.value, unlocked: false, known: true };
  }
}
async function vaultChosen() {
  choosingVault.value = false;
  await attempt(loadProfiles);
  await refreshVault();
}

let fetchingBatches = false;
let pendingBatchRefresh = false;
async function refreshBatches() {
  if (!ready.value) return;
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
    if (ready.value && account.value === scope) {
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

// Running work first, then what is next, then the newest finished rows.
function boundTransfers(list: Item[]) {
  const running = list.filter((item) => item.state === 'active' || item.state === 'running');
  const pending = list
    .filter((item) => !terminalStates.has(item.state) && !running.includes(item))
    .slice(0, 20);
  const finished = list
    .filter((item) => terminalStates.has(item.state))
    .sort((a, b) => b.id - a.id);
  const bounded = [...running, ...pending, ...finished].slice(0, maxTransferRows);
  const kept = new Set(bounded.map((item) => item.id));
  for (const id of samples.keys()) if (!kept.has(id)) samples.delete(id);
  return bounded;
}
// Events arrive as batches of changed transfers, a few times a second.
function onTransfers(items: Item[]) {
  if (!ready.value) return;
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
      browser.value?.reloadSoon();
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
      samples.set(item.id, { done: 0, time: performance.now() });
    } else list[position] = item;
    uploaded ||= item.state === 'done' && item.direction === 'upload';
  }
  transfers.value = boundTransfers(list);
  if (uploaded) browser.value?.reloadSoon();
}
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
    samples.set(item.id, { done: item.done, time: now });
  }
  speed.value = { up, down };
  clock.value = Date.now();
  rates.value = [...rates.value, speed.value].slice(-60);
}

let sessionChecking = false;
async function checkSession() {
  if (!ready.value || busy.value || sessionChecking) return;
  sessionChecking = true;
  try {
    const status = await TransferService.SessionStatus();
    if (status.next === 'files') await browser.value?.checkChanged();
    else if (status.next === 'reauth') startAuth('reauth', { origin: active.value });
    else if (status.next === 'password') {
      const saved = activeProfile.value;
      clearSession();
      startAuth('password', {
        origin: active.value,
        username: saved?.username,
        remember: saved?.remember,
        notice: 'Device authorization expired or was revoked. Sign in again.',
      });
    }
  } catch {
    error.value = 'Could not check the device connection. Retrying shortly.';
  } finally {
    sessionChecking = false;
  }
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
function stored(key: string) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

let timers: ReturnType<typeof setInterval>[] = [];
let stopEvents: (() => void) | undefined;
onMounted(() => {
  const saved = stored('serversend-theme');
  applyTheme(saved === 'light' || saved === 'dark' ? saved : 'system');
  // With a saved server, go straight to the library; otherwise onboard.
  void loadProfiles()
    .catch(() => {
      error.value = 'Could not load saved servers';
    })
    .then(() => {
      loaded.value = true;
      const last = stored('serversend-last-server');
      const first = profiles.value.find((p) => p.origin === last) ?? profiles.value[0];
      if (first) return open(first.origin);
      startAuth('connect');
    });
  timers = [
    setInterval(sampleRates, 1000),
    setInterval(() => void checkSession(), 30000),
    setInterval(() => {
      if (ready.value && !busy.value) void Promise.all([refreshBatches(), refreshVault()]);
    }, 1000),
  ];
  void BackgroundService.UpdatesEnabled()
    .then((enabled) => (updatesEnabled.value = enabled))
    .catch(() => undefined);
  stopEvents = Events.On('transfer', (event: { data: Item[] | null }) =>
    onTransfers(event.data ?? []),
  );
});
onUnmounted(() => {
  timers.forEach(clearInterval);
  stopEvents?.();
});
</script>

<template>
  <!-- First run: nothing saved yet, so pairing is the whole screen. -->
  <div
    v-if="onboarding"
    class="relative flex min-h-screen flex-col items-center justify-center px-4 py-12"
  >
    <div class="absolute top-4 right-4">
      <ThemeSwitch :model-value="theme" @update:model-value="applyTheme" />
    </div>
    <div class="mb-8 flex items-center gap-2.5">
      <BrandMark />
      <h1 class="m-0 text-lg font-semibold tracking-tight">ServerSend</h1>
    </div>
    <UiCard class="w-full max-w-md p-6 sm:p-7">
      <AuthFlow v-if="auth" :key="authKey" v-bind="auth" @done="signedIn" />
    </UiCard>
    <p class="text-muted-foreground mt-8 text-xs">Private file transfer for your network</p>
  </div>

  <div v-else-if="loaded" class="flex min-h-screen flex-col">
    <header class="bg-card/85 sticky top-0 z-30 border-b backdrop-blur">
      <div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-6">
        <div class="flex items-center gap-2.5">
          <BrandMark />
          <span class="text-[0.9375rem] font-semibold tracking-tight">ServerSend</span>
        </div>
        <nav class="flex h-full items-center gap-1" aria-label="Main">
          <button
            v-for="item in tabs"
            :key="item.id"
            type="button"
            class="relative flex h-full items-center gap-2 border-0 bg-transparent px-3 text-sm transition-colors"
            :class="
              tab === item.id
                ? 'text-foreground font-medium'
                : 'text-muted-foreground hover:text-foreground'
            "
            :aria-current="tab === item.id ? 'page' : undefined"
            @click="tab = item.id"
          >
            {{ item.label }}
            <span
              v-if="item.id === 'transfers' && activeCount > 0"
              class="bg-primary text-primary-foreground rounded-full px-1.5 text-[0.6875rem] leading-4 font-medium tabular-nums"
              :aria-label="`${activeCount} running`"
              >{{ activeCount }}</span
            >
            <span
              v-if="tab === item.id"
              class="bg-primary absolute inset-x-3 -bottom-px h-0.5 rounded-full"
              aria-hidden="true"
            />
          </button>
        </nav>
        <div class="ml-auto flex items-center gap-3">
          <button
            v-if="activeCount > 0"
            type="button"
            class="bg-accent text-accent-foreground flex items-center gap-2 rounded-full border-0 px-3 py-1 text-xs tabular-nums"
            title="Show transfers"
            @click="tab = 'transfers'"
          >
            <Loader2 class="size-3.5 animate-spin" aria-hidden="true" />
            <ArrowUpFromLine class="size-3" aria-hidden="true" />{{ size(speed.up) }}/s
            <ArrowDownToLine class="size-3" aria-hidden="true" />{{ size(speed.down) }}/s
          </button>
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="!updatesEnabled"
            :title="
              updatesEnabled ? 'Check for updates' : 'Updates are disabled in development builds'
            "
            aria-label="Check for updates"
            @click="BackgroundService.CheckForUpdates()"
          >
            <RefreshCw aria-hidden="true" /><span class="hidden lg:inline">Check for updates</span>
          </UiButton>
          <ThemeSwitch :model-value="theme" @update:model-value="applyTheme" />
        </div>
      </div>
    </header>

    <main class="mx-auto w-full max-w-6xl flex-1 px-6 py-8">
      <h1 class="m-0 mb-6 text-xl font-semibold tracking-tight">
        {{ tabs.find((t) => t.id === tab)?.label }}
      </h1>
      <UiAlert v-if="error" class="mb-6">{{ error }}</UiAlert>

      <div v-show="tab === 'library'" class="grid gap-6">
        <UiCard v-for="saved in library" :key="saved.origin" as="section" class="min-w-0">
          <div class="flex flex-wrap items-center gap-3 px-5 py-4">
            <span
              class="grid size-9 shrink-0 place-items-center rounded-lg"
              :class="
                saved.origin === active
                  ? 'bg-accent text-accent-foreground'
                  : 'bg-muted text-muted-foreground'
              "
              aria-hidden="true"
            >
              <Server class="size-4" />
            </span>
            <div class="grid min-w-0 flex-1 gap-0.5">
              <h2 class="m-0 truncate text-base font-semibold">
                {{ saved.name || saved.origin }}
              </h2>
              <p class="text-muted-foreground m-0 truncate text-xs">
                {{ saved.username ? saved.username + ' · ' : '' }}{{ saved.origin }}
              </p>
            </div>
            <template v-if="saved.origin === active && ready">
              <UiBadge v-if="vault.privateMode" :tone="vault.unlocked ? 'primary' : 'warning'">
                <LockKeyhole class="size-3" aria-hidden="true" />Private vault{{
                  vault.unlocked ? '' : ' · locked'
                }}
              </UiBadge>
              <UiBadge v-else-if="!needsChoice">Standard storage</UiBadge>
            </template>
            <UiButton
              v-else
              :variant="saved.origin === active ? 'primary' : 'outline'"
              size="sm"
              :disabled="busy"
              @click="open(saved.origin)"
            >
              <Loader2 v-if="busy && saved.origin === active" class="animate-spin" />
              {{ saved.origin === active ? 'Sign in' : 'Open' }}
            </UiButton>
          </div>
          <div v-if="saved.origin === active && ready && vault.known" class="border-t">
            <VaultSetup v-if="needsChoice" mode="choose" @done="vaultChosen" />
            <VaultSetup
              v-else-if="vault.privateMode && !vault.unlocked"
              mode="unlock"
              @done="vaultChosen"
            />
            <FileBrowser
              v-else
              :ref="(el) => (browser = el as InstanceType<typeof FileBrowser> | undefined)"
              :key="account?.scope"
              @queued="refreshBatches"
            />
          </div>
        </UiCard>
        <UiEmpty v-if="!library.length" title="No servers yet">
          <template #icon><FolderOpen /></template>
          Add a server in Servers to start backing up files.
        </UiEmpty>
      </div>

      <TransfersView
        v-if="tab === 'transfers'"
        :combined="combinedQueue"
        :transfers="transfers"
        :batches="batches"
        :speed="speed"
        :rates="rates"
        :clock="clock"
        :busy="busy"
        :batch-error="batchError"
        :scope-name="scopeName"
        @action="queueAction"
        @retry="refreshBatches"
      />

      <ServersView
        v-else-if="tab === 'servers'"
        :profiles="profiles"
        :active="active"
        :signed-in="ready"
        :busy="busy"
        :private-mode="vault.privateMode"
        :unlocked="vault.unlocked"
        @open="open"
        @add="(origin) => startAuth('connect', { origin })"
        @sign-out="signOut"
        @changed="profilesChanged"
        @choose-vault="
          choosingVault = true;
          tab = 'library';
        "
      />

      <template v-else-if="tab === 'devices'">
        <DevicesView v-if="ready" :key="account?.scope" :server="account?.origin ?? ''" />
        <UiCard v-else>
          <UiEmpty title="Not signed in">
            <template #icon><Laptop /></template>
            Sign in to a server to see the devices on your account.
          </UiEmpty>
        </UiCard>
      </template>

      <SettingsView
        v-else-if="tab === 'settings'"
        v-model:combined="combinedQueue"
        :busy="busy"
        @update:combined="refreshBatches"
      />
    </main>

    <!-- Sign-in for a saved or new server, over whichever page is open. -->
    <div
      v-if="auth"
      class="bg-overlay fixed inset-0 z-40 grid place-items-center overflow-y-auto p-4"
      @keydown.esc="cancelAuth"
    >
      <UiCard
        class="w-full max-w-md p-6 shadow-lg sm:p-7"
        role="dialog"
        aria-modal="true"
        aria-label="Sign in"
      >
        <AuthFlow :key="authKey" v-bind="auth" cancellable @done="signedIn" @cancel="cancelAuth" />
      </UiCard>
    </div>
  </div>
</template>
