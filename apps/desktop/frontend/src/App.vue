<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { Events } from '@wailsio/runtime';
import { TransferService } from '../bindings/github.com/chironjit/serversend/apps/desktop';
import type {
  Account,
  File,
  Item,
} from '../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import type { ServerProbe } from '../bindings/github.com/chironjit/serversend/shared/client/models';

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
    await TransferService.ChooseAndUploadFolder(parent.value);
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });
const transfers = ref<Item[]>([]);
const theme = ref<Theme>('system');
let stopEvents: (() => void) | undefined;

const fingerprintGroups = computed(
  () => probe.value?.fingerprint.match(/.{1,4}/g)?.join(' ') ?? '',
);
const recentTransfers = computed(() => transfers.value.toReversed());

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
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });

const refresh = () =>
  attempt(async () => {
    files.value = (await TransferService.Files(parent.value)) ?? [];
  });

const upload = () =>
  attempt(async () => {
    uploadMenu.value = false;
    await TransferService.ChooseAndUpload(parent.value);
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
  if (item.state === 'failed') return item.error;
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
  stopEvents = Events.On('transfer', (event: { data: Item }) => onTransfer(event.data));
});
onUnmounted(() => {
  stopEvents?.();
  clearInterval(rateTimer);
});
</script>

<template>
  <div class="shell" :class="{ workspace: view === 'files' }">
    <header class="bar">
      <h1><span class="brand-mark">S</span> ServerSend<span class="brand-dot">.</span></h1>
      <span v-if="account" class="who">{{ account.username }} · {{ account.origin }}</span>
      <label class="theme">
        <span>Theme</span>
        <select
          :value="theme"
          @change="applyTheme(($event.target as HTMLSelectElement).value as Theme)"
        >
          <option value="system">System</option>
          <option value="light">Light</option>
          <option value="dark">Dark</option>
        </select>
      </label>
      <button v-if="account" type="button" class="quiet" :disabled="busy" @click="signOut">
        Sign out
      </button>
    </header>

    <aside v-if="view === 'files'" class="sidebar" aria-label="Workspace navigation">
      <p class="eyebrow">YOUR WORKSPACE</p>
      <a href="#library" class="nav-active">▱ <span>File library</span></a>
      <a href="#transfers"
        >⇅ <span>Transfers</span
        ><span class="count">{{
          transfers.filter((t) => t.state === 'active' || t.state === 'queued').length
        }}</span></a
      >
      <div class="sidebar-note">
        <span class="connection">● Private connection</span>
        <p>Your files stay on your server.</p>
      </div>
    </aside>
    <div v-if="view === 'files'" class="workspace-heading">
      <div>
        <p class="eyebrow">WORKSPACE / FILES</p>
        <h2>Your files, in one place.</h2>
        <p class="hint">Browse your library and keep an eye on your transfers.</p>
      </div>
      <span class="connection">● Connected</span>
    </div>
    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <form v-if="view === 'connect'" class="card" @submit.prevent="check">
      <h2>Connect to a server</h2>
      <label>
        Server address
        <input
          v-model="address"
          required
          autofocus
          placeholder="https://192.168.1.10:58443"
          spellcheck="false"
        />
      </label>
      <button type="submit" :disabled="busy">{{ busy ? 'Checking…' : 'Check server' }}</button>
    </form>

    <section v-else-if="view === 'approve' && probe" class="card">
      <h2>Is this your server?</h2>
      <p v-if="probe.publicly_trusted">
        This computer already trusts the certificate for <strong>{{ probe.origin }}</strong>.
      </p>
      <p v-else>
        <strong>{{ probe.origin }}</strong> uses a private certificate. Compare this fingerprint
        with the one your server operator gives you. Only continue if they match exactly.
      </p>
      <p class="fingerprint" data-test="fingerprint">{{ fingerprintGroups }}</p>
      <p class="hint">Certificate: {{ probe.subject }}</p>
      <div class="row">
        <button type="button" :disabled="busy" @click="approve">They match — continue</button>
        <button type="button" class="quiet" :disabled="busy" @click="view = 'connect'">Back</button>
      </div>
    </section>

    <form v-else-if="view === 'password'" class="card" @submit.prevent="signIn">
      <h2>Sign in</h2>
      <label
        >Username
        <input v-model="username" required autofocus autocomplete="username" spellcheck="false"
      /></label>
      <label>
        Password
        <input v-model="password" type="password" required autocomplete="current-password" />
      </label>
      <p class="hint">New account? Enter the temporary password from your administrator.</p>
      <div class="row">
        <button type="submit" :disabled="busy">{{ busy ? 'Signing in…' : 'Continue' }}</button>
        <button type="button" class="quiet" :disabled="busy" @click="view = 'connect'">
          Change server
        </button>
      </div>
    </form>

    <form v-else-if="view === 'replace'" class="card" @submit.prevent="replacePassword">
      <h2>Choose your password</h2>
      <p>Your temporary password must be replaced before you continue.</p>
      <label>
        New password (at least 12 characters)
        <input
          v-model="newPassword"
          type="password"
          required
          minlength="12"
          autofocus
          autocomplete="new-password"
        />
      </label>
      <button type="submit" :disabled="busy">{{ busy ? 'Saving…' : 'Set password' }}</button>
    </form>

    <form
      v-else-if="view === 'enroll' && enrollment"
      class="card"
      @submit.prevent="confirmEnrollment"
    >
      <h2>Set up two-factor sign-in</h2>
      <p>Scan this code with an authenticator app, then enter the six-digit code it shows.</p>
      <img
        class="qr"
        :src="enrollment.qr"
        alt="QR code for your authenticator app"
        width="220"
        height="220"
      />
      <p class="hint">Can't scan? Enter this key manually: <code>{{ enrollment.secret }}</code></p>
      <label>
        Six-digit code
        <input
          v-model="code"
          required
          inputmode="numeric"
          pattern="[0-9]{6}"
          maxlength="6"
          autocomplete="one-time-code"
        />
      </label>
      <button type="submit" :disabled="busy">{{ busy ? 'Checking…' : 'Confirm' }}</button>
    </form>

    <section v-else-if="view === 'recovery'" class="card">
      <h2>Save your recovery codes</h2>
      <p>
        Each code signs you in once if you lose your authenticator. They are shown only now — store
        them somewhere safe before continuing.
      </p>
      <ul class="codes">
        <li v-for="recovery in recoveryCodes" :key="recovery">
          <code>{{ recovery }}</code>
        </li>
      </ul>
      <button type="button" @click="view = 'code'">I saved them — continue</button>
    </section>

    <form v-else-if="view === 'code'" class="card" @submit.prevent="completeSignIn">
      <h2>Enter your code</h2>
      <p>
        Enter the current code from your authenticator app, or a recovery code. If you have just set
        up your authenticator, wait for it to show a new code.
      </p>
      <label>
        Code
        <input
          v-model="code"
          required
          autofocus
          maxlength="64"
          autocomplete="one-time-code"
          spellcheck="false"
        />
      </label>
      <div class="row">
        <button type="submit" :disabled="busy">{{ busy ? 'Signing in…' : 'Sign in' }}</button>
        <button type="button" class="quiet" :disabled="busy" @click="view = 'password'">
          Start again
        </button>
      </div>
    </form>

    <main v-else-if="view === 'files'" class="columns">
      <section id="library" class="card">
        <div class="row spread">
          <h2>File library <span class="count">{{ files.length }}</span></h2>
          <div class="row">
            <button type="button" class="quiet" :disabled="busy" @click="refresh">Refresh</button>
            <div class="upload-menu" @keydown.esc="uploadMenu = false">
              <button
                type="button"
                :disabled="busy"
                :aria-expanded="uploadMenu"
                @click="uploadMenu = !uploadMenu"
              >
                Upload… ▾
              </button>
              <div v-if="uploadMenu" class="upload-options">
                <button @click="upload">Files…</button
                ><button @click="uploadFolder">Folder…</button>
              </div>
            </div>
          </div>
        </div>
        <nav class="breadcrumbs" aria-label="Folder path">
          <button class="quiet" :disabled="busy" @click="navigate(0)">All files</button>
          <template v-for="(folder, index) in folders" :key="folder.id"
            ><span aria-hidden="true">/</span
            ><button
              class="quiet"
              :disabled="busy"
              :aria-current="index === folders.length - 1 ? 'page' : undefined"
              @click="navigate(index + 1)"
            >
              {{ folder.name }}
            </button></template
          >
        </nav>
        <section
          v-if="pendingDelete"
          class="delete-confirm"
          role="alertdialog"
          aria-label="Confirm deletion"
        >
          <h3>Delete “{{ pendingDelete.name }}”?</h3>
          <p>
            {{
              pendingDelete.directory
                ? 'This folder and everything inside it will be permanently deleted.'
                : 'This file and all its versions will be permanently deleted.'
            }}
            This cannot be undone.
          </p>
          <div class="row">
            <button class="danger" :disabled="busy" @click="deleteEntry">Delete permanently</button
            ><button class="quiet" :disabled="busy" @click="pendingDelete = undefined">
              Cancel
            </button>
          </div>
        </section>
        <form class="row" @submit.prevent="createFolder">
          <label>New folder name<input v-model="folderName" required maxlength="255" /></label
          ><button :disabled="busy">Create folder</button>
        </form>
        <p v-if="files.length === 1000" class="hint">
          Showing the first 1,000 entries. This test build does not yet support catalog pagination.
        </p>
        <p v-if="files.length === 0" class="hint">
          Nothing uploaded yet. Choose “Upload…” to send your first file.
        </p>
        <ul v-else class="list file-list" aria-label="Files">
          <li v-for="file in files" :key="file.id">
            <button
              v-if="file.directory"
              class="folder-link name"
              :disabled="busy"
              @click="openFolder(file)"
            >
              <span class="file-icon" aria-hidden="true">▱</span>{{ file.name }}
            </button>
            <span v-else class="name"
              ><span class="file-icon" aria-hidden="true">▤</span>{{ file.name }}</span
            >
            <button
              v-if="!file.directory"
              type="button"
              class="quiet"
              :disabled="busy"
              @click="download(file)"
            >
              Download…
            </button>
            <button
              class="quiet delete-action"
              :aria-label="'Delete ' + file.name"
              :disabled="busy"
              @click="pendingDelete = file"
            >
              Delete
            </button>
          </li>
        </ul>
      </section>
      <section id="transfers" class="card">
        <h2>Transfers <span class="count">{{ transfers.length }}</span></h2>
        <p>↑ {{ size(speed.up) }}/s · ↓ {{ size(speed.down) }}/s</p>
        <svg
          viewBox="0 0 300 64"
          role="img"
          aria-label="Upload and download throughput over the last 60 seconds"
        >
          <polyline :points="chart('up')" fill="none" stroke="var(--accent)" stroke-width="2" />
          <polyline :points="chart('down')" fill="none" stroke="var(--ok)" stroke-width="2" />
        </svg>
        <p class="hint">
          Upload: violet · Download: green. Acknowledged/verified bytes per second; includes disk
          and verification time.
        </p>
        <p v-if="transfers.length === 0" class="hint">
          Uploads and downloads from this session appear here.
        </p>
        <ul v-else class="list transfer-list" tabindex="0" aria-label="Transfer queue">
          <li v-for="item in recentTransfers" :key="item.id" class="transfer">
            <div class="row spread">
              <span class="name"
                >{{ item.direction === 'upload' ? '↑' : '↓' }} {{ item.name }}</span
              >
              <span :class="['state', item.state]">{{ status(item) }}</span>
            </div>
            <progress v-if="item.state === 'active'" :value="percent(item)" max="100">
              {{ percent(item) }}%
            </progress>
          </li>
        </ul>
      </section>
    </main>
  </div>
</template>

<style scoped>
.shell {
  max-width: 1440px;
  margin: 0 auto;
  padding: 0 32px 32px;
}
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-bottom: 28px;
  min-height: 76px;
  border-bottom: 1px solid var(--border);
}
.bar h1 {
  margin: 0 auto 0 0;
  font-size: 18px;
}
.who,
.hint,
.theme span {
  color: var(--muted);
  font-size: 13px;
}
.theme {
  display: flex;
  align-items: center;
  gap: 6px;
}
.card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 520px;
  margin: 0 auto 16px;
  padding: 20px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 14px;
  box-shadow: 0 2px 8px #00000003;
}
.columns {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 340px;
  gap: 16px;
  align-items: start;
}
.columns .card {
  max-width: none;
  margin: 0;
  min-width: 0;
}
h2 {
  margin: 0;
  font-size: 17px;
}
p {
  margin: 0;
}
label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
  color: var(--muted);
}
input,
select {
  padding: 8px 10px;
  background: var(--ground);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
}
button {
  padding: 8px 14px;
  background: var(--accent);
  color: var(--accent-text);
  border: 1px solid var(--accent);
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
}
button.quiet {
  background: transparent;
  color: var(--text);
  border-color: var(--border);
}
button:disabled {
  opacity: 0.55;
  cursor: default;
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.spread {
  justify-content: space-between;
}
.error {
  max-width: 520px;
  margin: 0 auto 12px;
  padding: 10px 12px;
  color: var(--danger);
  border: 1px solid var(--danger);
  border-radius: 8px;
}
.fingerprint,
code {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
}
.fingerprint {
  padding: 10px;
  background: var(--ground);
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.qr {
  align-self: center;
  background: #ffffff;
  padding: 8px;
  border-radius: 6px;
}
.codes {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 13px;
}
.list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
}
.list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 0;
  border-top: 1px solid var(--border);
}
.list li.transfer {
  flex-direction: column;
  align-items: stretch;
  gap: 6px;
}
.name {
  min-width: 0;
  overflow-wrap: anywhere;
}
.state {
  color: var(--muted);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.state.failed {
  color: var(--danger);
}
.state.done {
  color: var(--ok);
}
progress {
  width: 100%;
  height: 6px;
  accent-color: var(--accent);
}

.workspace {
  display: grid;
  grid-template-columns: 180px minmax(0, 1fr);
  column-gap: 32px;
}
.workspace .bar {
  grid-column: 1/-1;
  margin-bottom: 0;
}
.workspace .workspace-heading,
.workspace .columns,
.workspace > .error {
  grid-column: 2;
}
.sidebar {
  grid-column: 1;
  grid-row: 2/6;
  padding-top: 36px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  border-right: 1px solid var(--border);
  padding-right: 20px;
}
.sidebar a {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--muted);
  text-decoration: none;
  border-radius: 8px;
  padding: 10px;
  font-size: 13px;
}
.sidebar .nav-active {
  background: color-mix(in srgb, var(--accent) 10%, transparent);
  color: var(--accent);
  font-weight: 600;
}
.sidebar .eyebrow {
  margin-bottom: 12px;
  font-size: 10px;
}
.sidebar-note {
  margin-top: 32px;
  font-size: 12px;
  color: var(--muted);
}
@media (max-width: 1100px) {
  .workspace {
    display: block;
  }
  .sidebar {
    display: none;
  }
}
.upload-menu {
  position: relative;
}
.upload-options {
  position: absolute;
  right: 0;
  top: calc(100% + 6px);
  z-index: 5;
  display: grid;
  min-width: 160px;
  padding: 6px;
  gap: 4px;
  border: 1px solid var(--border);
  background: var(--surface);
  border-radius: 10px;
  box-shadow: 0 8px 24px #0002;
}
.upload-options button {
  background: transparent;
  color: var(--text);
  border: 0;
  text-align: left;
}
.upload-options button:hover {
  background: var(--ground);
}
.brand-mark {
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  margin-right: 8px;
  border-radius: 9px;
  background: var(--accent);
  color: var(--accent-text);
}
.brand-dot {
  color: var(--accent);
}
.workspace-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin: 32px 0;
}
.workspace-heading h2 {
  font-size: 28px;
  letter-spacing: -0.8px;
  margin: 8px 0;
}
.eyebrow {
  color: var(--muted);
  font-size: 11px;
  letter-spacing: 1.5px;
}
.connection {
  font-size: 12px;
  color: var(--ok);
}
.count {
  font-size: 12px;
  font-weight: 500;
  padding: 3px 8px;
  border-radius: 6px;
  background: var(--ground);
  color: var(--muted);
}
.breadcrumbs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  padding: 10px 0;
  border-block: 1px solid var(--border);
}
.breadcrumbs button {
  border: 0;
  padding: 4px 8px;
  white-space: normal;
  overflow-wrap: anywhere;
}
button.folder-link {
  background: transparent;
  color: var(--text);
  border: 0;
  padding: 10px 0;
  text-align: left;
  white-space: normal;
  flex: 1;
}
button.folder-link:hover {
  color: var(--accent);
}
.file-icon {
  display: inline-block;
  color: var(--accent);
  background: var(--ground);
  padding: 4px 10px;
  margin-right: 10px;
  border-radius: 8px;
}
.file-list {
  max-height: 55vh;
  overflow-y: auto;
}
.transfer-list {
  max-height: min(48vh, 480px);
  overflow-y: auto;
  overscroll-behavior: contain;
  padding-right: 8px;
}
.list li {
  padding: 12px 0;
}
.list .name {
  flex: 1;
}
.delete-action {
  font-size: 12px;
}
button.danger {
  background: var(--danger);
  border-color: var(--danger);
  color: white;
}
.delete-confirm {
  border: 1px solid var(--danger);
  border-radius: 10px;
  padding: 16px;
  display: grid;
  gap: 12px;
}
.delete-confirm h3 {
  margin: 0;
}
@media (max-width: 900px) {
  .columns {
    grid-template-columns: minmax(0, 1fr);
  }
  .shell {
    padding: 0 16px 24px;
  }
  .who {
    width: 100%;
    order: 3;
  }
  .bar {
    padding: 12px 0;
  }
}
</style>
