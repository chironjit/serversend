<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
const emit = defineEmits<{ changed: []; locked: [] }>();
const unlocked = ref(false),
  privateMode = ref(false),
  passphrase = ref(''),
  recovery = ref('');
const exported = ref(''),
  acknowledged = ref(false),
  remember = ref(false),
  minutes = ref(15);
const busy = ref(false),
  error = ref('');
let timer: ReturnType<typeof setInterval> | undefined;
async function status() {
  const previous = unlocked.value;
  try {
    unlocked.value = await TransferService.VaultUnlocked();
    privateMode.value = await TransferService.PrivateMode();
  } catch (e) {
    unlocked.value = false;
    error.value = String(e);
  }
  if (previous && !unlocked.value) {
    passphrase.value = '';
    recovery.value = '';
    exported.value = '';
    emit('locked');
  }
}
async function action(run: () => Promise<unknown>) {
  busy.value = true;
  error.value = '';
  try {
    await run();
    await status();
    emit('changed');
  } catch (e) {
    error.value = String(e);
  } finally {
    passphrase.value = '';
    recovery.value = '';
    busy.value = false;
  }
}
async function setup() {
  exported.value = await TransferService.SetupVault(passphrase.value, acknowledged.value);
}
async function exportRecovery() {
  if (await TransferService.ExportVaultRecovery(exported.value)) exported.value = '';
}
async function mode() {
  await TransferService.SetPrivateMode(privateMode.value);
}
async function lock() {
  await TransferService.LockVault();
  exported.value = '';
  emit('locked');
}
onMounted(() => {
  void status();
  timer = setInterval(() => void status(), 1000);
});
onUnmounted(() => {
  clearInterval(timer);
  exported.value = '';
});
</script>
<template>
  <details class="border-border m-4 rounded-lg border p-4" aria-label="Private vault">
    <summary>Private vault — {{ unlocked ? 'unlocked' : 'locked' }}</summary>
    <p class="my-2 text-sm">
      Standard transfers work while locked. Private names and contents are encrypted on this device.
      Auto-lock uses a fixed timer, including during transfers.
    </p>
    <p v-if="error" role="alert">{{ error }}</p>
    <label
      ><input v-model="privateMode" type="checkbox" :disabled="busy" @change="action(mode)" />
      Private for new batches (remembered for this profile)</label
    >
    <div v-if="!unlocked" class="grid gap-3">
      <label
        >Vault passphrase (separate from account password)<input
          v-model="passphrase"
          type="password"
          autocomplete="off"
          class="border-border block rounded border p-2"
      /></label>
      <label
        ><input v-model="acknowledged" type="checkbox" /> I understand that if I lose my passphrase
        and all keys/recovery copies, administrators cannot recover my private files. I will store
        recovery material away from the server.</label
      >
      <div class="flex flex-wrap gap-3">
        <button :disabled="busy || !acknowledged" @click="action(setup)">Create vault</button>
        <button :disabled="busy" @click="action(() => TransferService.UnlockVault(passphrase, ''))">
          Unlock
        </button>
        <button :disabled="busy" @click="action(() => TransferService.UnlockRememberedVault())">
          Unlock from credential store
        </button>
      </div>
      <label
        >Recovery package<textarea
          v-model="recovery"
          autocomplete="off"
          class="border-border block w-full rounded border p-2"
        />
      </label>
      <button
        :disabled="busy || !recovery"
        @click="action(() => TransferService.UnlockVault('', recovery))"
      >
        Recover vault on this device
      </button>
    </div>
    <div v-else class="grid gap-3">
      <label
        >Auto-lock after minutes<input
          v-model.number="minutes"
          type="number"
          min="1"
          max="1440"
          class="border-border ml-2 rounded border p-2"
      /></label>
      <label
        ><input v-model="remember" type="checkbox" /> Remember unlock separately in the native
        credential store</label
      >
      <button
        :disabled="busy"
        @click="action(() => TransferService.ConfigureVault(minutes, remember))"
      >
        Save unlock policy
      </button>
      <label
        >New vault passphrase<input
          v-model="passphrase"
          type="password"
          autocomplete="new-password"
          class="border-border block rounded border p-2"
      /></label>
      <button
        :disabled="busy || !passphrase"
        @click="action(() => TransferService.RewrapVault(passphrase))"
      >
        Change vault passphrase
      </button>
      <button :disabled="busy" @click="action(lock)">Lock and clear private views</button>
    </div>
    <div v-if="exported" role="alert" class="my-3">
      <p>
        Save this recovery package outside the server before closing. Account password recovery does
        not recover vault keys.
      </p>
      <button :disabled="busy" @click="action(exportRecovery)">Export recovery package…</button>
    </div>
    <p class="mt-2 text-sm">
      Revocation blocks future access but cannot erase copied keys or files. A server can replay
      older valid encrypted data; encryption does not prove freshness. Private staging is limited to
      1 GiB per active file.
    </p>
  </details>
</template>
