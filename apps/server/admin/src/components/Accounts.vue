<script setup lang="ts">
import { onUnmounted, ref } from 'vue';
import { Loader2, LogOut } from 'lucide-vue-next';
import Logs from './Logs.vue';
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
  disabled: boolean;
  temporary_password?: string;
  expires_at?: string;
};
const users = ref<User[]>([]);
let refreshTimer: ReturnType<typeof setInterval> | undefined;
let generation = 0;
async function loadUsers() {
  const current = generation;
  const response = await fetch('/api/v1/admin/users', {
    headers: { Authorization: 'Bearer ' + token },
    cache: 'no-store',
  });
  if (!response.ok)
    throw new Error('Could not refresh accounts. Sign in again if your session expired.');
  const result = await response.json();
  if (current === generation) users.value = result;
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
  clearInterval(refreshTimer);
  token = '';
  users.value = [];
});
let challenge = '';
let token = '';
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
  clearInterval(refreshTimer);
  users.value = [];
  password.value = '';
  challenge = '';
  step.value = 'password';
  if (old)
    await fetch('/api/v1/sessions/current', {
      method: 'DELETE',
      headers: { Authorization: 'Bearer ' + old },
    }).catch(() => {});
}
</script>
<template>
  <UiCard as="section" class="p-5 sm:p-6">
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
            <th>Temporary password</th>
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
          </tr>
          <tr v-if="!users.length">
            <td colspan="3" class="text-muted-foreground">
              No users yet. Create an account above to get started.
            </td>
          </tr>
        </tbody>
      </UiTable>
    </div>
    <UiButton v-if="step === 'users'" variant="outline" class="mt-4" @click="signOut">
      <LogOut aria-hidden="true" />
      Sign out
    </UiButton>
  </UiCard>
  <Logs v-if="step === 'users'" :token="token" />
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
