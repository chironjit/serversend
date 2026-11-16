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
    files.value = (await TransferService.Files()) ?? [];
  });

const refresh = () =>
  attempt(async () => {
    files.value = (await TransferService.Files()) ?? [];
  });

const upload = () =>
  attempt(async () => {
    await TransferService.ChooseAndUpload();
  });

const download = (file: File) =>
  attempt(async () => {
    await TransferService.ChooseAndDownload(file.id);
  });

const signOut = () =>
  attempt(async () => {
    account.value = undefined;
    files.value = [];
    view.value = 'password';
    await TransferService.SignOut();
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
  return `${size(item.done)} of ${size(item.total)}`;
}

function onTransfer(item: Item) {
  const index = transfers.value.findIndex((existing) => existing.id === item.id);
  if (index === -1) transfers.value.push(item);
  else transfers.value[index] = item;
  if (item.state === 'done' && item.direction === 'upload' && view.value === 'files') {
    void TransferService.Files().then((list) => {
      files.value = list ?? [];
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
  stopEvents = Events.On('transfer', (event: { data: Item }) => onTransfer(event.data));
});
onUnmounted(() => stopEvents?.());
</script>

<template>
  <div class="shell">
    <header class="bar">
      <h1>ServerSend</h1>
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

    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <form v-if="view === 'connect'" class="card" @submit.prevent="check">
      <h2>Connect to a server</h2>
      <label>
        Server address
        <input
          v-model="address"
          required
          autofocus
          placeholder="https://192.168.1.10:8443"
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
      <section class="card">
        <div class="row spread">
          <h2>Files on the server</h2>
          <div class="row">
            <button type="button" class="quiet" :disabled="busy" @click="refresh">Refresh</button>
            <button type="button" :disabled="busy" @click="upload">Upload files…</button>
          </div>
        </div>
        <p v-if="files.length === 0" class="hint">
          Nothing uploaded yet. Choose “Upload files…” to send your first file.
        </p>
        <ul v-else class="list">
          <li v-for="file in files" :key="file.id">
            <span class="name">{{ file.name }}</span>
            <button type="button" class="quiet" :disabled="busy" @click="download(file)">
              Download…
            </button>
          </li>
        </ul>
      </section>
      <section class="card">
        <h2>Transfers</h2>
        <p v-if="transfers.length === 0" class="hint">
          Uploads and downloads from this session appear here.
        </p>
        <ul v-else class="list">
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
  max-width: 960px;
  margin: 0 auto;
  padding: 16px 20px 32px;
}
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
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
  border-radius: 10px;
}
.columns {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
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
</style>
