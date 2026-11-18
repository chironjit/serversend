<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import UiAlert from './ui/UiAlert.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiCode from './ui/UiCode.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';

const props = defineProps<{ csrf: string }>();
type Device = { id: string; name: string; current: boolean; status: string; last_seen_at: string };
const devices = ref<Device[]>([]);
const cursors = ref(['']);
const revoke = ref('');
const password = ref('');
const code = ref('');
const codes = ref<string[]>([]);
const busy = ref(false);
const error = ref('');
const controller = new AbortController();

async function request(path: string, body?: object) {
  const response = await fetch('/api/v1/sessions' + path, {
    cache: 'no-store',
    signal: AbortSignal.any([controller.signal, AbortSignal.timeout(5000)]),
    ...(body && {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': props.csrf },
      body: JSON.stringify(body),
    }),
  });
  if (response.status === 204) return;
  const result = await response.json();
  if (!response.ok) throw new Error(result.error?.message ?? 'Request failed');
  return result;
}

async function run(action: () => Promise<void>) {
  busy.value = true;
  error.value = '';
  try {
    await action();
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : 'Request failed. Try again.';
  } finally {
    busy.value = false;
  }
}
async function loadDevices(before: string) {
  devices.value = await request('/devices?before=' + encodeURIComponent(before));
}
const page = (previous: boolean) =>
  run(async () => {
    const before = previous ? (cursors.value.at(-2) ?? '') : (devices.value.at(-1)?.id ?? '');
    await loadDevices(before);
    if (previous) cursors.value.pop();
    else cursors.value.push(before);
    revoke.value = '';
  });
const revokeDevice = () =>
  run(async () => {
    await request('/devices/' + revoke.value + '/revoke', {});
    revoke.value = '';
    await loadDevices(cursors.value.at(-1) ?? '');
  });
const replaceCodes = () =>
  run(async () => {
    codes.value = [];
    try {
      const result = await request('/recovery-codes', {
        password: password.value,
        code: code.value,
      });
      codes.value = result.recovery_codes;
    } finally {
      password.value = '';
      code.value = '';
    }
  });
onMounted(() => void run(() => loadDevices('')));
onUnmounted(() => controller.abort());
</script>

<template>
  <UiCard as="section" class="grid gap-4 p-5">
    <UiCardHeader title="Your account security" />
    <UiAlert v-if="error">{{ error }}</UiAlert>
    <div>
      <h3>Signed-in devices</h3>
      <p class="text-muted-foreground text-sm">
        Device names are supplied by the client. Revoked devices need a full sign-in.
      </p>
      <div class="my-3 flex flex-wrap gap-2">
        <UiButton
          variant="outline"
          :disabled="busy"
          @click="run(() => loadDevices(cursors.at(-1) ?? ''))"
          >Refresh devices</UiButton
        >
        <UiButton variant="outline" :disabled="busy" @click="revoke = 'others'"
          >Sign out other devices</UiButton
        >
        <UiButton variant="outline" :disabled="busy || cursors.length < 2" @click="page(true)"
          >Previous</UiButton
        >
        <UiButton variant="outline" :disabled="busy || devices.length < 100" @click="page(false)"
          >Next</UiButton
        >
      </div>
      <p v-if="!busy && !error && !devices.length">No devices on this page.</p>
      <ul v-if="devices.length" class="grid gap-2 list-none p-0">
        <li
          v-for="device in devices"
          :key="device.id"
          class="flex flex-wrap items-center justify-between gap-2 rounded border p-3"
        >
          <div>
            <strong>{{ device.name }}</strong>
            <p class="text-muted-foreground text-sm">
              {{ device.current ? 'This browser' : device.status }} · Last seen
              {{ new Date(device.last_seen_at).toLocaleString() }}
            </p>
          </div>
          <UiButton
            v-if="!device.current && device.status !== 'revoked'"
            variant="outline"
            :disabled="busy"
            @click="revoke = device.id"
            >Revoke access</UiButton
          >
        </li>
      </ul>
      <div v-if="revoke" class="rounded border p-3">
        <p>
          {{
            revoke === 'others' ? 'Sign out all other devices?' : 'Revoke access for this device?'
          }}
        </p>
        <UiButton :disabled="busy" @click="revokeDevice">Confirm revocation</UiButton>
        <UiButton variant="ghost" :disabled="busy" @click="revoke = ''">Cancel</UiButton>
      </div>
    </div>

    <form class="grid gap-3 border-t pt-4" @submit.prevent="replaceCodes">
      <h3>Account recovery codes</h3>
      <p>
        Generating new codes invalidates the previous set. Keep your private vault recovery key
        separately.
      </p>
      <UiField label="Account password"
        ><UiInput v-model="password" required type="password" autocomplete="current-password"
      /></UiField>
      <UiField label="Fresh authenticator code"
        ><UiInput
          v-model="code"
          required
          inputmode="numeric"
          pattern="[0-9]{6}"
          autocomplete="one-time-code"
      /></UiField>
      <UiButton type="submit" :disabled="busy" class="w-fit">Generate new codes</UiButton>
    </form>
    <div v-if="codes.length" class="rounded border p-3">
      <p>Save these codes now. This is your only opportunity to copy them.</p>
      <ul class="grid gap-2 list-none p-0 sm:grid-cols-2">
        <li v-for="value in codes" :key="value"><UiCode>{{ value }}</UiCode></li>
      </ul>
      <UiButton variant="ghost" @click="codes = []">Dismiss codes</UiButton>
    </div>
  </UiCard>
</template>
