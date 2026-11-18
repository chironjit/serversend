<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { KeyRound, Laptop, RefreshCw } from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type { DeviceInfo } from '../../bindings/github.com/chironjit/serversend/shared/api/models';
import { date, failure } from '../format';
import UiAlert from './ui/UiAlert.vue';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiCode from './ui/UiCode.vue';
import UiEmpty from './ui/UiEmpty.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';

// Devices signed in to the current server account, and account recovery codes.
defineProps<{ server: string }>();
const busy = ref(false);
const error = ref('');
const devices = ref<DeviceInfo[]>([]);
const cursor = ref('');
const previous = ref<string[]>([]);
const revokeID = ref('');
const replacing = ref(false);
const replacementPassword = ref('');
const replacementCode = ref('');
const replacementCodes = ref<string[]>([]);

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
const load = () =>
  attempt(async () => {
    devices.value = (await TransferService.Devices('')) ?? [];
    cursor.value = '';
    previous.value = [];
    revokeID.value = '';
  });
const changePage = (back = false) =>
  attempt(async () => {
    const next = back ? (previous.value.at(-1) ?? '') : (devices.value.at(-1)?.id ?? '');
    const result = (await TransferService.Devices(next)) ?? [];
    if (back) previous.value.pop();
    else previous.value.push(cursor.value);
    cursor.value = next;
    devices.value = result;
    revokeID.value = '';
  });
const revoke = () =>
  attempt(async () => {
    await TransferService.RevokeDevice(revokeID.value);
    revokeID.value = '';
    devices.value = (await TransferService.Devices(cursor.value)) ?? [];
  });
function clearReplacement() {
  replacing.value = false;
  replacementPassword.value = '';
  replacementCode.value = '';
  replacementCodes.value = [];
}
const replaceCodes = () =>
  attempt(async () => {
    try {
      replacementCodes.value =
        (await TransferService.ReplaceRecoveryCodes(
          replacementPassword.value,
          replacementCode.value,
        )) ?? [];
      replacing.value = false;
    } finally {
      replacementPassword.value = '';
      replacementCode.value = '';
    }
  });
onMounted(load);
</script>

<template>
  <div class="grid gap-6">
    <UiAlert v-if="error">{{ error }}</UiAlert>

    <UiCard as="section" class="min-w-0">
      <div class="p-5">
        <UiCardHeader
          title="Signed-in devices"
          :description="`Devices with access to your account on ${server}. Revoking a device also stops its transfers.`"
        >
          <template #action>
            <div class="flex gap-2">
              <UiButton
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                aria-label="Refresh devices"
                title="Refresh"
                @click="load"
              >
                <RefreshCw aria-hidden="true" />
              </UiButton>
              <UiButton variant="outline" size="sm" :disabled="busy" @click="revokeID = 'others'"
                >Sign out other devices</UiButton
              >
            </div>
          </template>
        </UiCardHeader>
      </div>

      <div
        v-if="revokeID"
        role="alert"
        class="border-destructive/30 bg-destructive/5 mx-5 mb-4 flex flex-wrap items-center gap-3 rounded-lg border p-3 text-sm"
      >
        <span class="flex-1">
          {{
            revokeID === 'others' ? 'Sign out all other devices?' : 'Revoke access for this device?'
          }}
          It will need a full sign-in to reconnect.
        </span>
        <UiButton variant="destructive" size="sm" :disabled="busy" @click="revoke"
          >Confirm revocation</UiButton
        >
        <UiButton variant="ghost" size="sm" :disabled="busy" @click="revokeID = ''"
          >Cancel</UiButton
        >
      </div>

      <UiEmpty v-if="devices.length === 0" title="No devices to show">
        <template #icon><Laptop /></template>
      </UiEmpty>
      <ul v-else class="m-0 list-none border-t p-0">
        <li
          v-for="device in devices"
          :key="device.id"
          class="flex flex-wrap items-center gap-4 border-b px-5 py-3 last:border-b-0"
        >
          <span
            class="bg-muted text-muted-foreground grid size-9 shrink-0 place-items-center rounded-lg"
            aria-hidden="true"
            ><Laptop class="size-4"
          /></span>
          <div class="grid min-w-0 flex-1 gap-0.5">
            <p class="m-0 flex flex-wrap items-center gap-2 text-sm font-medium">
              {{ device.name }}
              <UiBadge v-if="device.current" tone="primary">This device</UiBadge>
              <UiBadge v-if="device.recently_active" tone="success" dot>Active now</UiBadge>
              <UiBadge v-if="device.status === 'revoked'" tone="danger">Revoked</UiBadge>
            </p>
            <p class="text-muted-foreground m-0 text-xs">
              {{ device.platform || 'Unknown platform' }} ·
              {{ device.app_version || 'Unknown version' }} · Last seen
              {{ date(device.last_seen_at) }} · Signed in {{ date(device.signed_in_at) }}
            </p>
          </div>
          <UiButton
            v-if="!device.current && device.status !== 'revoked'"
            variant="ghost"
            size="sm"
            class="hover:text-destructive"
            :disabled="busy"
            @click="revokeID = device.id"
            >Revoke access</UiButton
          >
        </li>
      </ul>
      <nav
        v-if="previous.length || devices.length >= 100"
        class="flex justify-between border-t px-5 py-3"
        aria-label="Device pages"
      >
        <UiButton
          variant="outline"
          size="sm"
          :disabled="busy || previous.length === 0"
          @click="changePage(true)"
          >Previous devices</UiButton
        >
        <UiButton
          variant="outline"
          size="sm"
          :disabled="busy || devices.length < 100"
          @click="changePage()"
          >Next devices</UiButton
        >
      </nav>
    </UiCard>

    <UiCard as="section" class="grid gap-4 p-5">
      <UiCardHeader
        title="Account recovery codes"
        description="Recovery codes sign you in if you lose your authenticator. Replacing them invalidates every previous code. They do not unlock a private vault."
      >
        <template #action>
          <UiButton
            v-if="!replacing && !replacementCodes.length"
            variant="outline"
            size="sm"
            :disabled="busy"
            @click="replacing = true"
          >
            <KeyRound aria-hidden="true" />Replace recovery codes
          </UiButton>
        </template>
      </UiCardHeader>
      <form v-if="replacing" class="grid max-w-md gap-3" @submit.prevent="replaceCodes">
        <UiField label="Account password">
          <UiInput
            v-model="replacementPassword"
            type="password"
            required
            autocomplete="current-password"
          />
        </UiField>
        <UiField label="Current authenticator code">
          <UiInput
            v-model="replacementCode"
            required
            inputmode="numeric"
            pattern="[0-9]{6}"
            autocomplete="one-time-code"
          />
        </UiField>
        <div class="flex gap-2">
          <UiButton type="submit" :disabled="busy">Generate new codes</UiButton>
          <UiButton variant="ghost" :disabled="busy" @click="clearReplacement">Cancel</UiButton>
        </div>
      </form>
      <div v-if="replacementCodes.length" class="grid gap-3">
        <p class="m-0 text-sm font-medium">
          Save these codes now. This is your only opportunity to copy them.
        </p>
        <ul class="m-0 grid list-none grid-cols-2 gap-2 p-0 md:grid-cols-4">
          <li v-for="value in replacementCodes" :key="value">
            <UiCode block class="text-center">{{ value }}</UiCode>
          </li>
        </ul>
        <div>
          <UiButton variant="outline" size="sm" @click="clearReplacement">I saved them</UiButton>
        </div>
      </div>
    </UiCard>
  </div>
</template>
