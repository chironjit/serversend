<script setup lang="ts">
import { onMounted, ref } from 'vue';
import {
  BackgroundService,
  TransferService,
} from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import { failure } from '../format';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiInput from './ui/UiInput.vue';

defineProps<{ busy: boolean }>();
const combined = defineModel<boolean>('combined', { required: true });

const bandwidth = ref('0');
const bandwidthError = ref('');
const bandwidthSaved = ref(false);
const launchAtLogin = ref(false);
const loginError = ref('');
onMounted(async () => {
  try {
    bandwidth.value = String((await TransferService.Bandwidth()) / 1048576);
  } catch (reason) {
    bandwidthError.value = failure(reason);
  }
  try {
    launchAtLogin.value = await BackgroundService.LaunchAtLogin();
  } catch (reason) {
    loginError.value = failure(reason);
  }
});

async function setBandwidth() {
  bandwidthError.value = '';
  bandwidthSaved.value = false;
  const limit = Number(bandwidth.value);
  if (!Number.isFinite(limit) || limit < 0 || limit > Number.MAX_SAFE_INTEGER / 1048576) {
    bandwidthError.value = 'Enter a nonnegative bandwidth limit. Use 0 for unlimited.';
    return;
  }
  try {
    await TransferService.SetBandwidth(Math.round(limit * 1048576));
    bandwidthSaved.value = true;
  } catch (reason) {
    bandwidthError.value = failure(reason);
  }
}
async function setLaunchAtLogin(value: boolean) {
  loginError.value = '';
  // Set optimistically so reloading a refused change re-renders the checkbox.
  launchAtLogin.value = value;
  try {
    await BackgroundService.SetLaunchAtLogin(value);
  } catch (reason) {
    loginError.value = failure(reason);
  }
  try {
    launchAtLogin.value = await BackgroundService.LaunchAtLogin();
  } catch {
    // Keep the last known value; the error above explains the failure.
  }
}
</script>

<template>
  <div class="grid max-w-2xl gap-6">
    <UiCard as="section" class="grid gap-5 p-5" aria-label="Transfer settings">
      <UiCardHeader title="Transfers" />
      <form class="grid gap-1.5" @submit.prevent="setBandwidth">
        <label for="bandwidth" class="text-sm font-medium">Bandwidth limit (MiB/s)</label>
        <div class="flex max-w-xs gap-2">
          <UiInput id="bandwidth" v-model="bandwidth" type="number" min="0" step="any" required />
          <UiButton type="submit" variant="outline" :disabled="busy">Apply</UiButton>
        </div>
        <span
          class="text-xs"
          :class="bandwidthError ? 'text-destructive' : 'text-muted-foreground'"
          :role="bandwidthError ? 'alert' : undefined"
        >
          {{ bandwidthError || (bandwidthSaved ? 'Limit applied.' : '0 means unlimited.') }}
        </span>
      </form>
      <div class="grid gap-1">
        <label class="flex items-center gap-2 text-sm">
          <input v-model="combined" type="checkbox" class="accent-primary" />
          Show all signed-in servers
        </label>
        <p class="text-muted-foreground m-0 pl-6 text-xs">
          List transfers from every signed-in server, not just the open one.
        </p>
      </div>
    </UiCard>

    <UiCard as="section" class="grid gap-5 p-5" aria-label="Background settings">
      <UiCardHeader
        title="Background"
        description="Closing the window keeps transfers running in the tray. They stop while the app is quit or the device sleeps and resume from checkpoints."
      />
      <div class="grid gap-1">
        <label class="flex items-center gap-2 text-sm">
          <input
            :checked="launchAtLogin"
            type="checkbox"
            class="accent-primary"
            @change="setLaunchAtLogin(($event.target as HTMLInputElement).checked)"
          />
          Launch at login in the tray
        </label>
        <p v-if="loginError" class="text-destructive m-0 pl-6 text-xs" role="alert">
          {{ loginError }}
        </p>
      </div>
      <div class="grid gap-2 border-t pt-4">
        <p class="text-muted-foreground m-0 text-xs">
          Quitting exits ServerSend completely instead of closing to the tray. Transfers stop at
          their checkpoints and resume the next time you open the app.
        </p>
        <div>
          <UiButton variant="outline" size="sm" @click="BackgroundService.Quit()"
            >Quit ServerSend</UiButton
          >
        </div>
      </div>
    </UiCard>
  </div>
</template>
