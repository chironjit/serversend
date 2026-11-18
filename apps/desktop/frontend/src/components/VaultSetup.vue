<script setup lang="ts">
import { computed, ref } from 'vue';
import { HardDrive, KeyRound, Loader2, LockKeyhole } from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import { failure } from '../format';
import UiAlert from './ui/UiAlert.vue';
import UiButton from './ui/UiButton.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';

// "choose" asks once per server whether files are stored in standard form or
// encrypted on this computer. "unlock" opens an existing private vault.
const props = defineProps<{ mode: 'choose' | 'unlock' }>();
const emit = defineEmits<{ done: [] }>();

type Step = 'choose' | 'create' | 'unlock' | 'recover' | 'save';
const step = ref<Step>(props.mode);
const choice = ref<'standard' | 'private'>('standard');
const passphrase = ref('');
const confirm = ref('');
const recovery = ref('');
const acknowledged = ref(false);
const pkg = ref('');
const exported = ref(false);
const busy = ref(false);
const error = ref('');
const mismatch = computed(() => confirm.value !== '' && confirm.value !== passphrase.value);

async function attempt(action: () => Promise<void>) {
  busy.value = true;
  error.value = '';
  try {
    await action();
  } catch (reason) {
    error.value = failure(reason);
  } finally {
    passphrase.value = '';
    confirm.value = '';
    recovery.value = '';
    busy.value = false;
  }
}

const proceed = () =>
  choice.value === 'private'
    ? (step.value = 'create')
    : attempt(async () => {
        await TransferService.SetPrivateMode(false);
        emit('done');
      });
const create = () =>
  attempt(async () => {
    pkg.value = await TransferService.SetupVault(passphrase.value, acknowledged.value);
    await TransferService.SetPrivateMode(true);
    step.value = 'save';
  });
// Opening the vault for the first time on this device also records the choice.
const unlock = (run: () => Promise<unknown>) =>
  attempt(async () => {
    await run();
    if (props.mode === 'choose') await TransferService.SetPrivateMode(true);
    emit('done');
  });
const exportPackage = () =>
  attempt(async () => {
    exported.value = await TransferService.ExportVaultRecovery(pkg.value);
  });
function finish() {
  pkg.value = '';
  emit('done');
}
</script>

<template>
  <div class="mx-auto grid max-w-2xl gap-5 px-5 py-6">
    <UiAlert v-if="error">{{ error }}</UiAlert>

    <form v-if="step === 'choose'" class="grid gap-5" @submit.prevent="proceed">
      <UiCardHeader
        title="How should files be stored on this server?"
        description="Choose once for this server. You can change it later in Servers; files already uploaded keep the form they were stored in."
      />
      <div class="grid gap-3 sm:grid-cols-2" role="radiogroup" aria-label="Storage mode">
        <label
          class="has-checked:border-primary has-checked:ring-primary/20 grid content-start gap-2 rounded-xl border p-4 has-checked:ring-3"
        >
          <span class="flex items-center gap-2 font-medium">
            <input v-model="choice" type="radio" value="standard" class="accent-primary" />
            <HardDrive class="text-primary size-4" aria-hidden="true" />Standard
            <span class="text-muted-foreground text-xs font-normal">Recommended</span>
          </span>
          <span class="text-muted-foreground text-sm leading-6">
            Files are encrypted while they travel and kept on the server's encrypted drive in their
            original form, so they are easy to recover. A server administrator who is signed in can
            technically open them.
          </span>
        </label>
        <label
          class="has-checked:border-primary has-checked:ring-primary/20 grid content-start gap-2 rounded-xl border p-4 has-checked:ring-3"
        >
          <span class="flex items-center gap-2 font-medium">
            <input v-model="choice" type="radio" value="private" class="accent-primary" />
            <LockKeyhole class="text-primary size-4" aria-hidden="true" />Private vault
          </span>
          <span class="text-muted-foreground text-sm leading-6">
            Files and names are encrypted on this computer and stored encrypted. Only you can open
            them. You set a vault passphrase — if you lose it and its recovery file,
            <strong class="text-foreground">nobody can recover your files</strong>.
          </span>
        </label>
      </div>
      <div>
        <UiButton type="submit" :disabled="busy">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ choice === 'private' ? 'Continue' : 'Use standard storage' }}
        </UiButton>
      </div>
    </form>

    <form v-else-if="step === 'create'" class="grid gap-5" @submit.prevent="create">
      <UiCardHeader
        title="Create your vault passphrase"
        description="This is separate from your account password and never leaves this computer. Your administrator cannot reset it."
      />
      <UiField label="Vault passphrase" hint="Use a long passphrase you will not forget.">
        <UiInput
          v-model="passphrase"
          type="password"
          required
          minlength="12"
          autocomplete="new-password"
        />
      </UiField>
      <UiField label="Confirm passphrase" :hint="mismatch ? 'The passphrases do not match.' : ''">
        <UiInput v-model="confirm" type="password" required autocomplete="new-password" />
      </UiField>
      <label class="flex items-start gap-2 text-sm leading-6">
        <input v-model="acknowledged" type="checkbox" class="accent-primary mt-1.5" />
        I understand that if I lose this passphrase and the recovery file, my private files are lost
        for good.
      </label>
      <div class="flex flex-wrap gap-2">
        <UiButton type="submit" :disabled="busy || !acknowledged || mismatch || !confirm">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />Create vault
        </UiButton>
        <UiButton variant="ghost" :disabled="busy" @click="step = 'choose'">Back</UiButton>
      </div>
      <p class="text-muted-foreground m-0 text-sm">
        Already created a vault on another device?
        <button
          type="button"
          class="text-primary border-0 bg-transparent p-0 underline-offset-4 hover:underline"
          @click="step = 'unlock'"
        >
          Unlock it instead
        </button>
      </p>
    </form>

    <section v-else-if="step === 'save'" class="grid gap-5">
      <UiCardHeader
        title="Save your recovery file"
        description="The recovery file opens your vault if you forget the passphrase. Keep it somewhere safe and away from the server — it is only offered now."
      />
      <div class="flex flex-wrap gap-2">
        <UiButton
          :variant="exported ? 'outline' : 'primary'"
          :disabled="busy"
          @click="exportPackage"
        >
          <KeyRound aria-hidden="true" />{{
            exported ? 'Saved — save another copy…' : 'Save recovery file…'
          }}
        </UiButton>
        <UiButton :variant="exported ? 'primary' : 'ghost'" :disabled="!exported" @click="finish">
          Continue to files
        </UiButton>
      </div>
    </section>

    <form
      v-else-if="step === 'unlock'"
      class="grid gap-5"
      @submit.prevent="unlock(() => TransferService.UnlockVault(passphrase, ''))"
    >
      <UiCardHeader
        title="Unlock your vault"
        description="Private files on this server are encrypted. Enter your vault passphrase to open them."
      />
      <UiField label="Vault passphrase">
        <UiInput v-model="passphrase" type="password" required autofocus autocomplete="off" />
      </UiField>
      <div class="flex flex-wrap gap-2">
        <UiButton type="submit" :disabled="busy">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />Unlock
        </UiButton>
        <UiButton
          variant="outline"
          :disabled="busy"
          @click="unlock(() => TransferService.UnlockRememberedVault())"
          >Use saved key</UiButton
        >
        <UiButton variant="ghost" :disabled="busy" @click="step = 'recover'"
          >Use recovery file</UiButton
        >
        <UiButton v-if="mode === 'choose'" variant="ghost" :disabled="busy" @click="step = 'choose'"
          >Back</UiButton
        >
      </div>
    </form>

    <form
      v-else-if="step === 'recover'"
      class="grid gap-5"
      @submit.prevent="unlock(() => TransferService.UnlockVault('', recovery))"
    >
      <UiCardHeader
        title="Recover your vault"
        description="Paste the contents of the recovery file you saved when the vault was created."
      />
      <textarea
        v-model="recovery"
        required
        rows="6"
        autocomplete="off"
        spellcheck="false"
        aria-label="Recovery file contents"
        class="border-input bg-card focus-visible:border-ring w-full rounded-md border p-3 font-mono text-xs"
      />
      <div class="flex gap-2">
        <UiButton type="submit" :disabled="busy">Recover vault</UiButton>
        <UiButton variant="ghost" :disabled="busy" @click="step = 'unlock'">Back</UiButton>
      </div>
    </form>
  </div>
</template>
