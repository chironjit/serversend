<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { Loader2, LogOut } from 'lucide-vue-next';
import Logs from './Logs.vue';
import Dashboard from './Dashboard.vue';
const props = withDefaults(defineProps<{ page?: string; restore?: boolean }>(), {
  page: 'accounts',
  restore: false,
});
import UiAlert from './ui/UiAlert.vue';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiCode from './ui/UiCode.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';
import UiTable from './ui/UiTable.vue';
const step = ref('password');
const username = ref('');
const password = ref('');
const newPassword = ref('');
const code = ref('');
const newUser = ref('');
const error = ref('');
const busy = ref(false);
const secret = ref('');
const qr = ref('');
const codes = ref<string[]>([]);
type User = {
  id: string;
  username: string;
  state: string;
  storage_bytes?: number;
  recovery_kind?: string;
  disabled: boolean;
  temporary_password?: string;
  expires_at?: string;
};
const users = ref<User[]>([]);
type Device = {
  id: string;
  name: string;
  platform: string;
  app_version: string;
  signed_in_at: string;
  last_seen_at: string;
  last_refresh_at?: string;
  idle_expires_at?: string;
  reauthenticate_at?: string;
  revoked_at?: string;
  status: string;
  recently_active: boolean;
};
const selectedUser = ref('');
const devices = ref<Device[]>([]);
const deviceLoading = ref(false);
const confirmation = ref<{ kind: 'reset-password' | 'reset-totp' | 'revoke'; device?: string }>();
const resetPassword = ref<{ password: string; expires: string }>();
function date(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Unknown';
}
async function loadDevices() {
  if (!selectedUser.value || deviceLoading.value) return;
  const selected = selectedUser.value,
    current = generation;
  deviceLoading.value = true;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 5000);
  try {
    const response = await fetch('/api/v1/admin/users/' + selected + '/devices', {
      headers: token ? { Authorization: 'Bearer ' + token } : {},
      cache: 'no-store',
      signal: controller.signal,
    });
    if (!response.ok)
      throw new Error('Could not load devices. Sign in again if the session expired.');
    const result = await response.json();
    if (current === generation && selected === selectedUser.value) devices.value = result;
  } finally {
    deviceLoading.value = false;
    clearTimeout(timeout);
  }
}
async function manage(user: User) {
  selectedUser.value = user.id;
  devices.value = [];
  confirmation.value = undefined;
  resetPassword.value = undefined;
  error.value = '';
  try {
    await loadDevices();
  } catch {
    error.value = 'Could not load devices. Try again.';
  }
}
async function confirmAction() {
  const action = confirmation.value;
  if (!action || !selectedUser.value) return;
  busy.value = true;
  error.value = '';
  const current = generation;
  try {
    const path =
      '/admin/users/' +
      selectedUser.value +
      '/' +
      (action.kind === 'revoke' ? 'devices/' + action.device + '/revoke' : action.kind);
    const result = await request(path, {}, token);
    if (current !== generation) return;
    confirmation.value = undefined;
    if (action.kind === 'reset-password')
      resetPassword.value = { password: result.temporary_password, expires: result.expires_at };
    await loadUsers();
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : 'Action failed';
  } finally {
    busy.value = false;
  }
}
let refreshTimer: ReturnType<typeof setInterval> | undefined;
let generation = 0;
let restoreRequest: AbortController | undefined;
async function loadUsers() {
  const current = generation;
  const response = await fetch('/api/v1/admin/users', {
    headers: token ? { Authorization: 'Bearer ' + token } : {},
    cache: 'no-store',
  });
  if (!response.ok)
    throw new Error('Could not refresh accounts. Sign in again if your session expired.');
  const result = await response.json();
  if (current === generation) {
    users.value = result;
    await loadDevices();
  }
}
function startRefresh() {
  clearInterval(refreshTimer);
  refreshTimer = setInterval(() => {
    void loadUsers().catch(() => {
      users.value = [];
      error.value = 'Account refresh failed. Refresh or sign in again.';
    });
  }, 5000);
}
onUnmounted(() => {
  generation++;
  restoreRequest?.abort();
  clearInterval(refreshTimer);
  token = '';
  users.value = [];
});
let challenge = '';
let token = '';
onMounted(async () => {
  if (!props.restore) return;
  const current = generation;
  restoreRequest = new AbortController();
  const timeout = setTimeout(() => restoreRequest?.abort(), 5000);
  try {
    const response = await fetch('/api/v1/sessions/current', {
      cache: 'no-store',
      signal: restoreRequest.signal,
    });
    if (!response.ok) return;
    const session = await response.json();
    if (current !== generation || session.role !== 'admin') return;
    step.value = 'users';
    await loadUsers();
    if (current === generation) startRefresh();
  } catch {
    if (current === generation) error.value = 'Could not restore session. Sign in again.';
  } finally {
    clearTimeout(timeout);
    restoreRequest = undefined;
  }
});
async function request(path: string, body: unknown, credential = ''): Promise<Record<string, any>> {
  const response = await fetch('/api/v1' + path, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(credential ? { Authorization: 'Bearer ' + credential } : {}),
    },
    body: JSON.stringify(body),
  });
  if (response.status === 204) return {};
  const result = await response.json();
  if (!response.ok) throw new Error(result.error?.message ?? 'Request failed');
  return result;
}
async function follow() {
  const result = await request('/auth/password', {
    username: username.value,
    password: password.value,
  });
  challenge = result.challenge;
  step.value = result.next_step;
  if (step.value === 'enroll_totp') {
    const enrollment = await request('/auth/totp/enroll', {}, challenge);
    secret.value = enrollment.secret;
    qr.value = 'data:image/png;base64,' + enrollment.qr_png;
  }
  if (step.value === 'second_factor') password.value = '';
}
async function submit() {
  busy.value = true;
  error.value = '';
  try {
    if (step.value === 'password') await follow();
    else if (step.value === 'replace_password') {
      await request('/auth/password/replace', { password: newPassword.value }, challenge);
      password.value = newPassword.value;
      newPassword.value = '';
      await follow();
    } else if (step.value === 'enroll_totp') {
      const result = await request('/auth/totp/confirm', { code: code.value }, challenge);
      codes.value = result.recovery_codes;
      secret.value = '';
      qr.value = '';
      await follow();
      step.value = 'recovery';
      code.value = '';
    } else if (step.value === 'second_factor') {
      const result = await request(
        '/sessions',
        { code: code.value, device_name: 'Web administration' },
        challenge,
      );
      code.value = '';
      challenge = '';
      if (result.role !== 'admin') {
        await fetch('/api/v1/sessions/current', {
          method: 'DELETE',
          headers: { Authorization: 'Bearer ' + result.access_token },
        });
        step.value = 'password';
        throw new Error('Use the desktop app with your user account.');
      }
      token = result.access_token;
      password.value = '';
      codes.value = [];
      step.value = 'users';
      await loadUsers();
      startRefresh();
    } else if (step.value === 'users') {
      await request('/admin/users', { username: newUser.value }, token);
      await loadUsers();
      newUser.value = '';
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : 'Request failed';
  } finally {
    busy.value = false;
  }
}
async function signOut() {
  const old = token;
  token = '';
  generation++;
  restoreRequest?.abort();
  clearInterval(refreshTimer);
  users.value = [];
  selectedUser.value = '';
  devices.value = [];
  resetPassword.value = undefined;
  confirmation.value = undefined;
  password.value = '';
  challenge = '';
  step.value = 'password';
  await fetch('/api/v1/sessions/current', {
    method: 'DELETE',
    headers: old ? { Authorization: 'Bearer ' + old } : {},
  }).catch(() => {});
}
</script>
<template>
  <UiCard v-show="step !== 'users' || page === 'accounts'" as="section" class="p-5 sm:p-6">
    <UiCardHeader
      :title="step === 'users' ? 'User accounts' : 'Administrator sign-in'"
      :description="
        step === 'users'
          ? undefined
          : 'Administrators manage accounts here. Users upload and download in the desktop app.'
      "
    />
    <UiAlert v-if="error" class="mt-4">{{ error }}</UiAlert>
    <div v-if="step === 'recovery'" class="mt-5 grid gap-4">
      <h3 class="m-0 text-sm font-semibold">Save your recovery codes</h3>
      <p class="text-muted-foreground m-0 text-sm leading-6">
        Each code signs you in once if you lose your authenticator. They are shown only now — store
        them somewhere safe before continuing.
      </p>
      <ul class="m-0 grid list-none grid-cols-1 gap-2 p-0 sm:grid-cols-2">
        <li v-for="value in codes" :key="value">
          <UiCode block class="text-center">{{ value }}</UiCode>
        </li>
      </ul>
      <UiButton class="w-full sm:w-auto" @click="step = 'second_factor'">
        I saved them — continue
      </UiButton>
    </div>
    <form
      v-else
      class="mt-5 grid gap-4"
      :class="step === 'users' ? 'create-form' : 'max-w-md'"
      @submit.prevent="submit"
    >
      <template v-if="step === 'password'">
        <UiField label="Username">
          <UiInput v-model="username" required autocomplete="username" />
        </UiField>
        <UiField label="Password">
          <UiInput v-model="password" type="password" required autocomplete="current-password" />
        </UiField>
      </template>
      <UiField v-if="step === 'replace_password'" label="New password" hint="12–128 characters.">
        <UiInput
          v-model="newPassword"
          type="password"
          minlength="12"
          maxlength="128"
          required
          autocomplete="new-password"
        />
      </UiField>
      <template v-if="step === 'enroll_totp'">
        <p class="text-muted-foreground m-0 text-sm">Scan with your authenticator.</p>
        <img
          class="rounded-lg border bg-white p-2"
          :src="qr"
          alt="Authenticator QR code"
          width="220"
          height="220"
        />
        <p class="text-muted-foreground m-0 text-xs leading-6">
          Can't scan? Enter this key manually:
          <UiCode class="wrap-anywhere">{{ secret }}</UiCode>
        </p>
      </template>
      <UiField
        v-if="step === 'enroll_totp' || step === 'second_factor'"
        label="Authenticator or recovery code"
      >
        <UiInput v-model="code" required autocomplete="one-time-code" maxlength="64" />
      </UiField>
      <template v-if="step === 'users'">
        <p class="create-note text-muted-foreground m-0 text-xs leading-5">
          Creating users requires a sign-in within the last five minutes. Sign out and back in if it
          expires.
        </p>
        <UiField label="New username">
          <UiInput v-model="newUser" required autocomplete="off" />
        </UiField>
      </template>
      <UiButton type="submit" :disabled="busy" class="w-full sm:w-auto">
        <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
        {{ busy ? 'Working…' : step === 'users' ? 'Create user' : 'Continue' }}
      </UiButton>
    </form>
    <div v-if="step === 'users'" class="mt-6">
      <div class="flex items-center gap-2">
        <h3 class="m-0 text-sm font-semibold">All users</h3>
        <UiBadge>{{ users.length }}</UiBadge>
      </div>
      <p class="text-muted-foreground mt-1.5 text-xs leading-5">
        Temporary passwords remain available until replaced or expired. Account status refreshes
        every five seconds.
      </p>
      <UiTable class="mt-3">
        <thead>
          <tr>
            <th>User</th>
            <th>Status</th>
            <th>Stored file bytes</th>
            <th>Temporary password</th>
            <th>Access and recovery</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="user in users" :key="user.id">
            <td class="font-medium">{{ user.username }}</td>
            <td>
              <UiBadge
                :tone="
                  user.disabled
                    ? 'neutral'
                    : user.state === 'password_required'
                      ? 'warning'
                      : user.state === 'totp_required'
                        ? 'primary'
                        : 'success'
                "
                dot
              >
                {{
                  user.disabled
                    ? 'Disabled'
                    : user.state === 'password_required'
                      ? 'Awaiting password'
                      : user.state === 'totp_required'
                        ? 'Setting up 2FA'
                        : 'Active'
                }}
              </UiBadge>
            </td>
            <td>{{ (user.storage_bytes ?? 0).toLocaleString() }} B</td>
            <td>
              <template v-if="user.temporary_password">
                <UiCode>{{ user.temporary_password }}</UiCode>
                <small class="text-muted-foreground mt-1 block text-xs">
                  Expires {{ new Date(user.expires_at!).toLocaleString() }}
                </small>
              </template>
              <span v-else class="text-muted-foreground">{{
                user.state === 'password_required' ? 'Expired or unavailable' : 'Password set'
              }}</span>
            </td>
            <td>
              <UiButton variant="outline" size="sm" :disabled="busy" @click="manage(user)"
                >Manage access</UiButton
              >
            </td>
          </tr>
          <tr v-if="!users.length">
            <td colspan="5" class="text-muted-foreground">
              No users yet. Create an account above to get started.
            </td>
          </tr>
        </tbody>
      </UiTable>
    </div>
    <section
      v-if="step === 'users' && selectedUser"
      class="mt-6 border-t pt-5"
      aria-label="User access and recovery"
    >
      <h3>Access for {{ users.find((user) => user.id === selectedUser)?.username }}</h3>
      <p class="text-muted-foreground text-sm">
        Device revocation and recovery require administrator sign-in within five minutes. Either
        reset signs out every device. Complete one recovery before starting the other.
      </p>
      <div class="flex flex-wrap gap-2">
        <UiButton
          variant="outline"
          :disabled="
            busy ||
            users.find((user) => user.id === selectedUser)?.recovery_kind === 'totp' ||
            users.find((user) => user.id === selectedUser)?.state === 'totp_required'
          "
          @click="confirmation = { kind: 'reset-password' }"
          >Reset password</UiButton
        >
        <UiButton
          variant="outline"
          :disabled="
            busy ||
            users.find((user) => user.id === selectedUser)?.recovery_kind === 'password' ||
            users.find((user) => user.id === selectedUser)?.state === 'password_required'
          "
          @click="confirmation = { kind: 'reset-totp' }"
          >Reset TOTP</UiButton
        >
      </div>
      <div v-if="confirmation" class="mt-4 rounded border p-4" role="alert">
        <p>
          {{
            confirmation.kind === 'revoke'
              ? 'Revoke this device? It will need a full login to access the server again.'
              : confirmation.kind === 'reset-password'
                ? 'Reset the password and sign out all devices? The existing authenticator will still be required.'
                : 'Reset the authenticator and sign out all devices? The existing password will still be required.'
          }}
        </p>
        <UiButton :disabled="busy" @click="confirmAction">Confirm</UiButton>
        <UiButton variant="ghost" :disabled="busy" @click="confirmation = undefined"
          >Cancel</UiButton
        >
      </div>
      <div v-if="resetPassword" class="mt-4 rounded border p-4">
        <p>
          Save this temporary password now. The user must replace it and verify their existing
          authenticator.
        </p>
        <UiCode>{{ resetPassword.password }}</UiCode>
        <p>Expires {{ date(resetPassword.expires) }}</p>
        <UiButton variant="ghost" @click="resetPassword = undefined">Dismiss password</UiButton>
      </div>
      <h4 class="mt-6">Devices</h4>
      <p class="text-muted-foreground text-xs">
        Recent activity means contact within 90 seconds. Offline devices remain listed. Device names
        are provided by the client.
      </p>
      <p v-if="deviceLoading">Refreshing devices…</p>
      <div v-for="device in devices" :key="device.id" class="mt-3 rounded border p-4">
        <div class="flex flex-wrap justify-between gap-2">
          <strong>{{ device.name }}</strong
          ><UiBadge
            >{{ device.status }} ·
            {{ device.recently_active ? 'Recently active' : 'Not recently seen' }}</UiBadge
          >
        </div>
        <p class="text-xs">
          {{ device.platform || 'Unknown platform' }} ·
          {{ device.app_version || 'Unknown version' }} · {{ device.id }}
        </p>
        <dl class="grid gap-2 text-sm sm:grid-cols-2">
          <div>
            <dt>Signed in</dt>
            <dd>{{ date(device.signed_in_at) }}</dd>
          </div>
          <div>
            <dt>Last activity</dt>
            <dd>{{ date(device.last_seen_at) }}</dd>
          </div>
          <div>
            <dt>Last refresh</dt>
            <dd>{{ date(device.last_refresh_at) }}</dd>
          </div>
          <div>
            <dt>Inactivity expiry</dt>
            <dd>{{ date(device.idle_expires_at) }}</dd>
          </div>
          <div>
            <dt>TOTP due</dt>
            <dd>{{ date(device.reauthenticate_at) }}</dd>
          </div>
        </dl>
        <UiButton
          variant="outline"
          class="mt-3"
          :disabled="busy || device.status === 'revoked'"
          @click="confirmation = { kind: 'revoke', device: device.id }"
          >Revoke access</UiButton
        >
      </div>
      <p v-if="!deviceLoading && !devices.length">No device authorizations recorded.</p>
    </section>
    <UiButton v-if="step === 'users'" variant="outline" class="mt-4" @click="signOut">
      <LogOut aria-hidden="true" />
      Sign out
    </UiButton>
  </UiCard>
  <Logs v-if="step === 'users' && page === 'logs'" :token="token" />
  <Dashboard v-if="step === 'users' && page === 'dashboard'" />
  <UiCard v-if="step === 'users' && page === 'settings'" class="p-6">
    <h2>Server settings</h2>
    <p>
      Administration uses HTTPS. Sessions last seven days; sensitive account changes require a
      sign-in within five minutes.
    </p>
    <p>
      Storage paths, quotas, listening addresses and TLS certificates are configured through
      SERVER_SEND_* deployment settings. Restart the server after changing these settings.
    </p>
    <UiButton variant="outline" @click="signOut">Sign out</UiButton>
  </UiCard>
</template>
<style scoped>
.create-form {
  grid-template-columns: minmax(180px, 1fr) auto;
  align-items: end;
}
.create-note {
  grid-column: 1/-1;
}
@media (max-width: 600px) {
  .create-form {
    grid-template-columns: 1fr;
  }
}
</style>
