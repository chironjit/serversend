<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
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
} from '../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import type { ServerProbe } from '../bindings/github.com/chironjit/serversend/shared/client/models';
import BrandMark from './components/ui/BrandMark.vue';
import ThemeSwitch from './components/ui/ThemeSwitch.vue';
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
  | 'files';
type Theme = 'system' | 'light' | 'dark';

const view = ref<View>('connect');
const signingIn = computed(() => view.value !== 'files');
const busy = ref(false);
const error = ref('');

const address = ref('');
const probe = ref<ServerProbe>();
const username = ref('');
const password = ref('');
const newPassword = ref('');
const code = ref('');
const enrollment = ref<{ secret: string; qr: string }>();
const recoveryCodes = ref<string[]>([]);
const account = ref<Account>();
const files = ref<File[]>([]);
const folders = ref<{ id: string; name: string }[]>([]);
const parent = computed(() => folders.value.at(-1)?.id ?? '');
const folderName = ref('');
const rates = ref<{ up: number; down: number }[]>([]);
let rateTimer: ReturnType<typeof setInterval> | undefined;
const speed = ref({ up: 0, down: 0 });
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
  rates.value = [...rates.value, speed.value].slice(-60);
}
function chart(direction: 'up' | 'down') {
  const max = Math.max(1, ...rates.value.flatMap((r) => [r.up, r.down]));
  return rates.value.map((r, i) => `${(i * 300) / 59},${60 - (r[direction] / max) * 56}`).join(' ');
}
const openFolder = (file: File) =>
  attempt(async () => {
    const list = await TransferService.Files(file.id);
    folders.value.push({ id: file.id, name: file.name });
    files.value = list ?? [];
  });
const navigate = (depth: number) =>
  attempt(async () => {
    const list = await TransferService.Files(folders.value[depth - 1]?.id ?? '');
    folders.value = folders.value.slice(0, depth);
    files.value = list ?? [];
  });
const pendingDelete = ref<File>();
const deleteEntry = () =>
  attempt(async () => {
    if (!pendingDelete.value) return;
    await TransferService.DeleteEntry(pendingDelete.value.id);
    pendingDelete.value = undefined;
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });
const createFolder = () =>
  attempt(async () => {
    await TransferService.CreateDirectory(parent.value, folderName.value);
    folderName.value = '';
    files.value = (await TransferService.Files(parent.value)) ?? [];
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
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });
const transfers = ref<Item[]>([]);
const batches = ref<Batch[]>([]);
const batchError = ref('');
let batchTimer: ReturnType<typeof setInterval> | undefined;
let fetchingBatches = false;
let pendingBatchRefresh = false;
const recentBatches = computed(() => batches.value.toReversed());
const batchStateLabel: Record<string, string> = {
  scanning: 'Scanning',
  running: 'Uploading',
  done: 'Completed',
  done_with_skips: 'Completed with skipped items',
  all_skipped: 'Nothing uploaded',
  failed: 'Failed',
};
const batchStateTone: Record<string, 'primary' | 'neutral' | 'success' | 'warning' | 'danger'> = {
  scanning: 'primary',
  running: 'primary',
  done: 'success',
  done_with_skips: 'warning',
  all_skipped: 'warning',
  failed: 'danger',
};
async function refreshBatches() {
  if (view.value !== 'files') return;
  if (fetchingBatches) {
    pendingBatchRefresh = true;
    return;
  }
  fetchingBatches = true;
  try {
    const list = await TransferService.Batches();
    if (view.value === 'files') {
      batches.value = (list ?? []).slice(-100);
      batchError.value = '';
    }
  } catch (reason) {
    batchError.value = failure(reason);
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
const recentTransfers = computed(() => transfers.value.toReversed());
const activeCount = computed(
  () => transfers.value.filter((t) => t.state === 'active' || t.state === 'queued').length,
);
const stateTone: Record<string, 'primary' | 'neutral' | 'success' | 'warning' | 'danger'> = {
  active: 'primary',
  queued: 'neutral',
  done: 'success',
  failed: 'danger',
  skipped: 'warning',
};
const stateLabel: Record<string, string> = {
  active: 'Active',
  queued: 'Queued',
  done: 'Done',
  failed: 'Failed',
  skipped: 'Skipped',
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

const completeSignIn = () =>
  attempt(async () => {
    account.value = await TransferService.CompleteSignIn(code.value);
    code.value = '';
    recoveryCodes.value = [];
    view.value = 'files';
    await refreshBatches();
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });

const refresh = () =>
  attempt(async () => {
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });

const upload = () =>
  attempt(async () => {
    uploadMenu.value = false;
    try {
      await TransferService.ChooseAndUpload(parent.value);
    } finally {
      await refreshBatches();
    }
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });

const download = (file: File) =>
  attempt(async () => {
    await TransferService.ChooseAndDownload(file.id);
  });

const signOut = () =>
  attempt(async () => {
    await TransferService.SignOut();
    account.value = undefined;
    folders.value = [];
    files.value = [];
    batches.value = [];
    batchError.value = '';
    view.value = 'password';
  });

function percent(item: Item): number {
  if (item.state === 'done') return 100;
  return item.total > 0 ? Math.floor((item.done / item.total) * 100) : 0;
}

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

function status(item: Item): string {
  if (item.state === 'failed' || item.state === 'skipped') return item.error;
  if (item.state === 'queued') return 'Waiting';
  if (item.state === 'done') return `Done · ${size(item.total)}`;
  return `${size(item.done)} of ${size(item.total)} · ${size(item.speed ?? 0)}/s`;
}

function onTransfer(item: Item) {
  const index = transfers.value.findIndex((existing) => existing.id === item.id);
  if (index === -1) {
    transfers.value.push(item);
    samples.set(item.id, { done: 0, time: performance.now(), direction: item.direction });
  } else transfers.value[index] = item;
  // Keep all current work and only the latest 500 finished rows. Source scans
  // may contain arbitrarily many files; the window need not retain them all.
  let finished = transfers.value.filter(
    (entry) => entry.state !== 'active' && entry.state !== 'queued',
  ).length;
  if (finished > 500) {
    transfers.value = transfers.value.filter((entry) => {
      if (finished <= 500 || entry.state === 'active' || entry.state === 'queued') return true;
      finished -= 1;
      samples.delete(entry.id);
      return false;
    });
  }
  if (item.state === 'done' && item.direction === 'upload' && view.value === 'files') {
    void TransferService.Files(parent.value)
      .then((list) => {
        files.value = list ?? [];
      })
      .catch((reason) => {
        error.value = failure(reason);
      });
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

onMounted(() => {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem('serversend-theme');
  } catch {
    saved = null;
  }
  applyTheme(saved === 'light' || saved === 'dark' ? saved : 'system');
  rateTimer = setInterval(sampleRates, 1000);
  batchTimer = setInterval(() => {
    if (
      busy.value ||
      batches.value.some((batch) => batch.state === 'scanning' || batch.state === 'running')
    ) {
      void refreshBatches();
    }
  }, 1000);
  stopEvents = Events.On('transfer', (event: { data: Item }) => onTransfer(event.data));
});
onUnmounted(() => {
  stopEvents?.();
  clearInterval(rateTimer);
  clearInterval(batchTimer);
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
          certificate. Compare this fingerprint with the one your server operator gives you. Only
          continue if they match exactly.
        </p>
        <UiCode block data-test="fingerprint">{{ fingerprintGroups }}</UiCode>
        <p class="text-muted-foreground m-0 text-xs">Certificate: {{ probe.subject }}</p>
        <div class="flex flex-wrap gap-2">
          <UiButton :disabled="busy" @click="approve">
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

      <form v-else-if="view === 'code'" class="grid gap-5" @submit.prevent="completeSignIn">
        <UiCardHeader
          title="Enter your code"
          description="Enter the current code from your authenticator app, or a recovery code. If you have just set up your authenticator, wait for it to show a new code."
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
                      ? 'This folder and everything inside it will be permanently deleted.'
                      : 'This file and all its versions will be permanently deleted.'
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
                    Delete permanently
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

          <p v-if="files.length === 1000" class="text-muted-foreground m-0 px-5 pt-3 text-xs">
            Showing the first 1,000 entries. This test build does not yet support catalog
            pagination.
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
        </UiCard>

        <UiCard as="section" id="transfers" class="min-w-0">
          <div class="p-5 pb-0">
            <UiCardHeader title="Transfers">
              <template #action>
                <UiBadge>{{ transfers.length }}</UiBadge>
              </template>
            </UiCardHeader>
          </div>
          <div class="grid grid-cols-2 gap-3 p-5 pb-3">
            <div class="bg-muted/60 rounded-lg p-3">
              <p class="text-muted-foreground m-0 flex items-center gap-1.5 text-xs">
                <ArrowUpFromLine class="text-chart-up size-3.5" aria-hidden="true" />
                Upload
              </p>
              <p class="m-0 mt-1 text-lg font-semibold tabular-nums">{{ size(speed.up) }}/s</p>
            </div>
            <div class="bg-muted/60 rounded-lg p-3">
              <p class="text-muted-foreground m-0 flex items-center gap-1.5 text-xs">
                <ArrowDownToLine class="text-chart-down size-3.5" aria-hidden="true" />
                Download
              </p>
              <p class="m-0 mt-1 text-lg font-semibold tabular-nums">{{ size(speed.down) }}/s</p>
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
          <section class="border-border mt-4 border-t px-5 py-4" aria-label="Upload selections">
            <h3 class="m-0 text-sm font-semibold">Upload selections</h3>
            <p class="text-muted-foreground m-0 mt-1 text-xs leading-5">
              Links and unsupported files are skipped. Select a link's real target to upload its
              contents. The latest 100 selections and up to 100 skipped paths per selection are
              shown here.
            </p>
            <div v-if="batchError" class="mt-3" role="alert">
              <p class="text-destructive m-0 text-xs">
                Upload summaries unavailable: {{ batchError }}
              </p>
              <UiButton variant="ghost" size="sm" @click="refreshBatches">Retry summaries</UiButton>
            </div>
            <ul
              v-if="batches.length"
              class="m-0 mt-3 grid max-h-80 list-none gap-3 overflow-y-auto p-0"
              tabindex="0"
              aria-label="Recent upload selections"
            >
              <li
                v-for="batch in recentBatches"
                :key="batch.id"
                class="border-border rounded-lg border p-3"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <span class="min-w-0 flex-1 wrap-anywhere text-sm font-medium">{{
                    batch.name
                  }}</span>
                  <UiBadge :tone="batchStateTone[batch.state] ?? 'neutral'">{{
                    batchStateLabel[batch.state] ?? batch.state
                  }}</UiBadge>
                </div>
                <p class="text-muted-foreground m-0 mt-2 text-xs leading-5" role="status">
                  {{ batch.completed }} of {{ batch.files }} files uploaded ·
                  {{ batch.directories }} folders created · {{ batch.skipped }} skipped ·
                  {{ batch.failed }} failed
                </p>
                <p
                  v-if="batch.state === 'all_skipped'"
                  class="text-warning m-0 mt-1 text-xs leading-5"
                >
                  Zero files uploaded. Every selected item was skipped.
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
            Uploads and downloads from this session appear here.
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
        </UiCard>
      </main>
    </div>
  </div>
</template>
