<script setup lang="ts">
import { ref } from 'vue';
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
const issued = ref<{ username: string; temporary_password: string; expires_at: string }>();
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
    } else if (step.value === 'users') {
      issued.value = undefined;
      issued.value = (await request('/admin/users', { username: newUser.value }, token)) as {
        username: string;
        temporary_password: string;
        expires_at: string;
      };
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
  issued.value = undefined;
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
  <section class="mt-6 rounded-2xl border border-slate-300 p-6 dark:border-slate-700">
    <h2 class="text-lg font-semibold">
      {{ step === 'users' ? 'Create user account' : 'Administrator sign-in' }}
    </h2>
    <p class="my-3 text-sm">
      Administrators manage accounts here. Users upload and download in the desktop app.
    </p>
    <p v-if="error" role="alert" class="my-3 text-red-700 dark:text-red-300">{{ error }}</p>
    <div v-if="step === 'recovery'">
      <h3>Save your recovery codes</h3>
      <ul>
        <li v-for="value in codes" :key="value"><code>{{ value }}</code></li>
      </ul>
      <button class="action-button mt-4" @click="step = 'second_factor'">
        I saved them — continue
      </button>
    </div>
    <form v-else class="grid max-w-md gap-4" @submit.prevent="submit">
      <template v-if="step === 'password'">
        <label>Username<input v-model="username" required autocomplete="username" /></label>
        <label
          >Password<input
            v-model="password"
            type="password"
            required
            autocomplete="current-password"
        /></label>
      </template>
      <label v-if="step === 'replace_password'"
        >New password (12–128 characters)<input
          v-model="newPassword"
          type="password"
          minlength="12"
          maxlength="128"
          required
          autocomplete="new-password"
      /></label>
      <template v-if="step === 'enroll_totp'">
        <p>Scan with your authenticator.</p>
        <img :src="qr" alt="Authenticator QR code" width="220" height="220" /><code>{{
          secret
        }}</code>
      </template>
      <label v-if="step === 'enroll_totp' || step === 'second_factor'"
        >Authenticator or recovery code<input
          v-model="code"
          required
          autocomplete="one-time-code"
          maxlength="64"
      /></label>
      <template v-if="step === 'users'">
        <p class="text-sm">
          Creating users requires a sign-in within the last five minutes. Sign out and back in if it
          expires.
        </p>
        <label>New username<input v-model="newUser" required autocomplete="off" /></label>
      </template>
      <button class="action-button" :disabled="busy">
        {{ busy ? 'Working…' : step === 'users' ? 'Create user' : 'Continue' }}
      </button>
    </form>
    <div v-if="issued" class="mt-4">
      <p>
        Give {{ issued.username }} this temporary password securely. It is displayed only now and
        expires at {{ issued.expires_at }}.
      </p>
      <code>{{ issued.temporary_password }}</code>
    </div>
    <button v-if="step === 'users'" class="action-button mt-4" @click="signOut">Sign out</button>
  </section>
</template>
<style scoped>
input {
  display: block;
  width: 100%;
  padding: 0.5rem;
  border: 1px solid #64748b;
  border-radius: 0.375rem;
  background: transparent;
  color: inherit;
}
</style>
