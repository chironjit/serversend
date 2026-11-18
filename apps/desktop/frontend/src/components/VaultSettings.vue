<script setup lang="ts">
import { ref } from 'vue';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import { failure } from '../format';
import UiAlert from './ui/UiAlert.vue';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';

// Storage mode and vault policy for the signed-in server.
const props = defineProps<{
  privateMode: boolean;
  unlocked: boolean;
  minutes?: number;
  remember?: boolean;
}>();
const emit = defineEmits<{ changed: []; choose: [] }>();
const minutes = ref(String(props.minutes || 15));
const remember = ref(props.remember ?? false);
const passphrase = ref('');
const confirmStandard = ref(false);
const busy = ref(false);
const error = ref('');
const saved = ref('');

async function action(run: () => Promise<unknown>, done = '') {
  busy.value = true;
  error.value = '';
  saved.value = '';
  try {
    await run();
    saved.value = done;
    emit('changed');
  } catch (e) {
    error.value = failure(e);
  } finally {
    passphrase.value = '';
    busy.value = false;
  }
}
</script>

<template>
  <div class="grid gap-4">
    <UiAlert v-if="error">{{ error }}</UiAlert>
    <p v-if="saved" class="text-success m-0 text-sm" role="status">{{ saved }}</p>

    <div class="flex flex-wrap items-center gap-3">
      <div class="grid min-w-0 flex-1 gap-1">
        <p class="m-0 flex items-center gap-2 text-sm font-medium">
          Storage mode
          <UiBadge :tone="privateMode ? 'primary' : 'neutral'">{{
            privateMode ? 'Private vault' : 'Standard'
          }}</UiBadge>
          <UiBadge v-if="privateMode" :tone="unlocked ? 'success' : 'warning'" dot>{{
            unlocked ? 'Unlocked' : 'Locked'
          }}</UiBadge>
        </p>
        <p class="text-muted-foreground m-0 text-sm">
          {{
            privateMode
              ? 'New uploads are encrypted on this computer. Only your vault passphrase or recovery file can open them. Private files are limited to 16 GiB each; you can choose to upload larger files unencrypted.'
              : 'New uploads are stored in original form on the server’s encrypted drive.'
          }}
        </p>
      </div>
      <UiButton
        v-if="privateMode"
        variant="outline"
        size="sm"
        :disabled="busy"
        @click="confirmStandard = true"
        >Switch to standard</UiButton
      >
      <UiButton v-else variant="outline" size="sm" :disabled="busy" @click="emit('choose')"
        >Set up private vault</UiButton
      >
    </div>
    <div
      v-if="confirmStandard"
      role="alert"
      class="bg-muted flex flex-wrap items-center gap-2 rounded-lg p-3 text-sm"
    >
      <span class="flex-1"
        >New uploads will be stored unencrypted on the server. Existing private files stay
        encrypted.</span
      >
      <UiButton
        size="sm"
        :disabled="busy"
        @click="
          action(() => TransferService.SetPrivateMode(false));
          confirmStandard = false;
        "
        >Switch</UiButton
      >
      <UiButton variant="ghost" size="sm" @click="confirmStandard = false">Cancel</UiButton>
    </div>

    <template v-if="privateMode && unlocked">
      <form
        class="grid gap-3 border-t pt-4 sm:grid-cols-[1fr_auto] sm:items-end"
        @submit.prevent="
          action(
            () => TransferService.ConfigureVault(Number(minutes), remember),
            'Unlock policy saved.',
          )
        "
      >
        <div class="grid gap-3">
          <UiField
            label="Lock automatically after (minutes)"
            hint="1–1440. Applies even while transfers run."
          >
            <UiInput v-model="minutes" type="number" min="1" max="1440" required class="max-w-32" />
          </UiField>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="remember" type="checkbox" class="accent-primary" />
            Remember the vault key in this computer’s credential store
          </label>
        </div>
        <UiButton type="submit" variant="outline" size="sm" :disabled="busy">Save policy</UiButton>
      </form>
      <form
        class="grid gap-3 border-t pt-4 sm:grid-cols-[1fr_auto] sm:items-end"
        @submit.prevent="
          action(() => TransferService.RewrapVault(passphrase), 'Vault passphrase changed.')
        "
      >
        <UiField label="New vault passphrase">
          <UiInput
            v-model="passphrase"
            type="password"
            required
            minlength="12"
            autocomplete="new-password"
          />
        </UiField>
        <UiButton type="submit" variant="outline" size="sm" :disabled="busy"
          >Change passphrase</UiButton
        >
      </form>
      <div class="flex items-center justify-between gap-3 border-t pt-4">
        <p class="text-muted-foreground m-0 text-sm">
          Locking hides private files until you unlock again.
        </p>
        <UiButton
          variant="outline"
          size="sm"
          :disabled="busy"
          @click="action(() => TransferService.LockVault())"
          >Lock now</UiButton
        >
      </div>
    </template>
  </div>
</template>
