<script setup lang="ts">
import { computed, ref } from 'vue';
import { Loader2, ShieldCheck } from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type { Account } from '../../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import type { ServerProbe } from '../../bindings/github.com/chironjit/serversend/shared/client/models';
import { failure } from '../format';
import UiAlert from './ui/UiAlert.vue';
import UiButton from './ui/UiButton.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiCode from './ui/UiCode.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';

export type AuthStep =
  | 'connect'
  | 'approve'
  | 'password'
  | 'replace'
  | 'enroll'
  | 'recovery'
  | 'code'
  | 'reauth';

// Each sign-in step is one small form. The parent remounts this component to
// start a new flow, so no state leaks between servers.
const props = defineProps<{
  step: AuthStep;
  origin?: string;
  username?: string;
  remember?: boolean;
  notice?: string;
  cancellable?: boolean;
}>();
const emit = defineEmits<{ done: [account: Account]; cancel: [] }>();

const view = ref<AuthStep>(props.step);
const busy = ref(false);
const error = ref(props.notice ?? '');
const address = ref(props.origin ?? '');
const probe = ref<ServerProbe>();
const username = ref(props.username ?? '');
const password = ref('');
const newPassword = ref('');
const code = ref('');
const remember = ref(props.remember ?? false);
const enrollment = ref<{ secret: string; qr: string }>();
const recoveryCodes = ref<string[]>([]);
const fingerprint = computed(() => probe.value?.fingerprint.match(/.{1,4}/g)?.join(' ') ?? '');

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
  if (next === 'replace_password') view.value = 'replace';
  else if (next === 'enroll_totp') {
    enrollment.value = await TransferService.BeginEnrollment();
    view.value = 'enroll';
  } else view.value = 'code';
}

const check = () =>
  attempt(async () => {
    probe.value = await TransferService.Probe(address.value);
    view.value = 'approve';
  });
const chooseCA = () =>
  attempt(async () => {
    probe.value = await TransferService.ChooseServerCA(probe.value?.origin ?? address.value);
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
const finish = () =>
  attempt(async () => {
    let account: Account;
    if (view.value === 'reauth')
      account = await TransferService.CompleteReauthentication(code.value);
    else {
      await TransferService.SetRemember(remember.value);
      account = await TransferService.CompleteSignIn(code.value);
    }
    code.value = '';
    recoveryCodes.value = [];
    emit('done', account);
  });
</script>

<template>
  <div class="grid gap-5">
    <UiAlert v-if="error">{{ error }}</UiAlert>

    <form v-if="view === 'connect'" class="grid gap-5" @submit.prevent="check">
      <UiCardHeader
        title="Connect to a server"
        description="Enter the address your server operator gave you."
      />
      <UiField label="Server address">
        <UiInput
          v-model="address"
          required
          autofocus
          placeholder="https://192.168.1.10:58443"
          spellcheck="false"
        />
      </UiField>
      <div class="flex flex-wrap gap-2">
        <UiButton type="submit" :disabled="busy">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ busy ? 'Checking…' : 'Check server' }}
        </UiButton>
        <UiButton v-if="cancellable" variant="ghost" :disabled="busy" @click="emit('cancel')">
          Cancel
        </UiButton>
      </div>
    </form>

    <section v-else-if="view === 'approve' && probe" class="grid gap-5">
      <UiCardHeader title="Is this your server?" />
      <p v-if="probe.publicly_trusted" class="text-muted-foreground m-0 text-sm leading-6">
        This computer already trusts the certificate for
        <strong class="text-foreground font-medium">{{ probe.origin }}</strong>.
      </p>
      <p v-else class="text-muted-foreground m-0 text-sm leading-6">
        <strong class="text-foreground font-medium">{{ probe.origin }}</strong> uses a private
        certificate. Choose the public CA certificate supplied by your administrator, then compare
        its fingerprint through a trusted channel before continuing.
      </p>
      <UiCode block data-test="fingerprint">{{ fingerprint }}</UiCode>
      <template v-if="probe.authority_fingerprint">
        <p class="m-0 text-sm">Imported CA fingerprint</p>
        <UiCode block>{{ probe.authority_fingerprint }}</UiCode>
      </template>
      <UiButton v-if="!probe.publicly_trusted" variant="outline" :disabled="busy" @click="chooseCA"
        >Choose server CA certificate</UiButton
      >
      <p class="text-muted-foreground m-0 text-xs">Certificate: {{ probe.subject }}</p>
      <div class="flex flex-wrap gap-2">
        <UiButton
          :disabled="busy || (!probe.publicly_trusted && !probe.authority_fingerprint)"
          @click="approve"
        >
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          <ShieldCheck v-else aria-hidden="true" />
          They match — continue
        </UiButton>
        <UiButton variant="ghost" :disabled="busy" @click="view = 'connect'">Back</UiButton>
      </div>
    </section>

    <form v-else-if="view === 'password'" class="grid gap-5" @submit.prevent="signIn">
      <UiCardHeader
        title="Sign in"
        :description="origin || 'Use the account your administrator created for you.'"
      />
      <UiField label="Username">
        <UiInput v-model="username" required autofocus autocomplete="username" spellcheck="false" />
      </UiField>
      <UiField
        label="Password"
        hint="New account? Enter the temporary password from your administrator."
      >
        <UiInput v-model="password" type="password" required autocomplete="current-password" />
      </UiField>
      <label class="flex items-center gap-2 text-sm">
        <input v-model="remember" type="checkbox" />Remember sign-in on this device
      </label>
      <div class="flex flex-wrap gap-2">
        <UiButton type="submit" :disabled="busy">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ busy ? 'Signing in…' : 'Continue' }}
        </UiButton>
        <UiButton v-if="cancellable" variant="ghost" :disabled="busy" @click="emit('cancel')">
          Cancel
        </UiButton>
      </div>
    </form>

    <form v-else-if="view === 'replace'" class="grid gap-5" @submit.prevent="replacePassword">
      <UiCardHeader
        title="Choose your password"
        description="Your temporary password must be replaced before you continue."
      />
      <UiField label="New password" hint="At least 12 characters.">
        <UiInput
          v-model="newPassword"
          type="password"
          required
          minlength="12"
          autofocus
          autocomplete="new-password"
        />
      </UiField>
      <UiButton type="submit" :disabled="busy">
        <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
        {{ busy ? 'Saving…' : 'Set password' }}
      </UiButton>
    </form>

    <form
      v-else-if="view === 'enroll' && enrollment"
      class="grid gap-5"
      @submit.prevent="confirmEnrollment"
    >
      <UiCardHeader
        title="Set up two-factor sign-in"
        description="Scan this code with an authenticator app, then enter the six-digit code it shows."
      />
      <img
        class="mx-auto rounded-lg border bg-white p-2"
        :src="enrollment.qr"
        alt="QR code for your authenticator app"
        width="200"
        height="200"
      />
      <p class="text-muted-foreground m-0 text-xs leading-6">
        Can't scan? Enter this key manually:
        <UiCode class="wrap-anywhere">{{ enrollment.secret }}</UiCode>
      </p>
      <UiField label="Six-digit code">
        <UiInput
          v-model="code"
          required
          inputmode="numeric"
          pattern="[0-9]{6}"
          maxlength="6"
          autocomplete="one-time-code"
          class="font-mono"
        />
      </UiField>
      <UiButton type="submit" :disabled="busy">
        <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
        {{ busy ? 'Checking…' : 'Confirm' }}
      </UiButton>
    </form>

    <section v-else-if="view === 'recovery'" class="grid gap-5">
      <UiCardHeader
        title="Save your recovery codes"
        description="Each code signs you in once if you lose your authenticator. They are shown only now — store them somewhere safe before continuing."
      />
      <ul class="m-0 grid list-none grid-cols-2 gap-2 p-0">
        <li v-for="value in recoveryCodes" :key="value">
          <UiCode block class="text-center">{{ value }}</UiCode>
        </li>
      </ul>
      <UiButton @click="view = 'code'">I saved them — continue</UiButton>
    </section>

    <form
      v-else-if="view === 'code' || view === 'reauth'"
      class="grid gap-5"
      @submit.prevent="finish"
    >
      <UiCardHeader
        :title="view === 'reauth' ? 'Verify this device' : 'Enter your code'"
        :description="
          view === 'reauth'
            ? 'This device needs authenticator verification every 60 days. Your password is not required.'
            : 'Enter the current code from your authenticator app, or a recovery code.'
        "
      />
      <UiField label="Code">
        <UiInput
          v-model="code"
          required
          autofocus
          maxlength="64"
          autocomplete="one-time-code"
          spellcheck="false"
          class="font-mono"
        />
      </UiField>
      <div class="flex flex-wrap gap-2">
        <UiButton type="submit" :disabled="busy">
          <Loader2 v-if="busy" class="animate-spin" aria-hidden="true" />
          {{ busy ? 'Signing in…' : 'Sign in' }}
        </UiButton>
        <UiButton v-if="view === 'code'" variant="ghost" :disabled="busy" @click="view = 'password'"
          >Start again</UiButton
        >
        <UiButton v-else-if="cancellable" variant="ghost" :disabled="busy" @click="emit('cancel')"
          >Cancel</UiButton
        >
      </div>
    </form>
  </div>
</template>
