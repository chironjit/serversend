<script setup lang="ts">
import { ref } from 'vue';
import { LogOut, Plus, Server } from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type { Profile } from '../../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import { failure } from '../format';
import StorageDashboard from './StorageDashboard.vue';
import VaultSettings from './VaultSettings.vue';
import UiAlert from './ui/UiAlert.vue';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiCard from './ui/UiCard.vue';
import UiCardHeader from './ui/UiCardHeader.vue';
import UiField from './ui/UiField.vue';
import UiInput from './ui/UiInput.vue';

// Saved servers, their connection settings, and settings for the signed-in one.
const props = defineProps<{
  profiles: Profile[];
  active?: string;
  signedIn: boolean;
  busy: boolean;
  privateMode: boolean;
  unlocked: boolean;
}>();
const emit = defineEmits<{
  open: [origin: string];
  add: [origin?: string];
  signOut: [];
  changed: [];
  chooseVault: [];
}>();

const error = ref('');
const working = ref(false);
const editing = ref('');
const name = ref('');
const routes = ref('');
const confirm = ref<{ origin: string; kind: 'remove' | 'repair' }>();

async function attempt(action: () => Promise<void>) {
  working.value = true;
  error.value = '';
  try {
    await action();
  } catch (reason) {
    error.value = failure(reason);
  } finally {
    working.value = false;
  }
}
function edit(saved: Profile) {
  confirm.value = undefined;
  editing.value = saved.origin;
  name.value = saved.name ?? '';
  routes.value = (saved.endpoints ?? []).join('\n');
}
const save = () =>
  attempt(async () => {
    await TransferService.EditProfile(
      editing.value,
      name.value,
      routes.value.split(/\s+/).filter(Boolean),
    );
    editing.value = '';
    emit('changed');
  });
const confirmed = () =>
  attempt(async () => {
    const target = confirm.value;
    if (!target) return;
    await TransferService.RemoveProfile(target.origin);
    confirm.value = undefined;
    emit('changed');
    if (target.kind === 'repair') emit('add', target.origin);
  });
const isActive = (saved: Profile) => saved.origin === props.active;
</script>

<template>
  <div class="grid gap-6">
    <div class="flex items-center justify-between gap-3">
      <p class="text-muted-foreground m-0 text-sm">
        Servers this computer has paired with. Transfers continue for every signed-in server.
      </p>
      <UiButton :disabled="busy || working" @click="emit('add')">
        <Plus aria-hidden="true" />Add server
      </UiButton>
    </div>
    <UiAlert v-if="error">{{ error }}</UiAlert>

    <UiCard v-for="saved in profiles" :key="saved.origin" as="section" class="min-w-0">
      <div class="flex flex-wrap items-center gap-3 p-5">
        <span
          class="grid size-10 shrink-0 place-items-center rounded-lg"
          :class="
            isActive(saved) ? 'bg-accent text-accent-foreground' : 'bg-muted text-muted-foreground'
          "
          aria-hidden="true"
        >
          <Server class="size-5" />
        </span>
        <div class="grid min-w-0 flex-1 gap-0.5">
          <h2 class="m-0 flex flex-wrap items-center gap-2 text-base font-semibold">
            {{ saved.name || saved.origin }}
            <UiBadge v-if="isActive(saved) && signedIn" tone="success" dot>Signed in</UiBadge>
            <UiBadge v-else-if="isActive(saved)" tone="warning" dot>Sign-in needed</UiBadge>
            <UiBadge v-if="saved.private_default" tone="primary">Private vault</UiBadge>
          </h2>
          <p class="text-muted-foreground m-0 truncate text-sm">
            {{ saved.username || 'No account yet' }} · {{ saved.origin }}
          </p>
        </div>
        <div class="flex flex-wrap gap-1">
          <UiButton
            v-if="!isActive(saved) || !signedIn"
            size="sm"
            :disabled="busy || working"
            @click="emit('open', saved.origin)"
            >{{ isActive(saved) ? 'Sign in' : 'Switch to this server' }}</UiButton
          >
          <UiButton
            v-else
            variant="outline"
            size="sm"
            :disabled="busy || working"
            @click="emit('signOut')"
            ><LogOut aria-hidden="true" />Sign out</UiButton
          >
          <UiButton variant="ghost" size="sm" :disabled="working" @click="edit(saved)"
            >Edit</UiButton
          >
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="working"
            @click="confirm = { origin: saved.origin, kind: 'repair' }"
            >Repair trust</UiButton
          >
          <UiButton
            variant="ghost"
            size="sm"
            class="hover:text-destructive"
            :disabled="working"
            @click="confirm = { origin: saved.origin, kind: 'remove' }"
            >Remove</UiButton
          >
        </div>
      </div>

      <div
        v-if="confirm?.origin === saved.origin"
        role="alert"
        class="bg-muted mx-5 mb-5 grid gap-3 rounded-lg p-4 text-sm"
      >
        <p class="m-0">
          {{
            confirm.kind === 'repair'
              ? `Forget trust and saved credentials for ${saved.origin}? Compare the new CA with your operator before pairing again.`
              : `Remove ${saved.name || saved.origin} from this computer? Saved sign-in and any remembered vault key are deleted.`
          }}
          Local queue records and downloaded files are kept.
        </p>
        <div class="flex gap-2">
          <UiButton size="sm" :disabled="working" @click="confirmed">{{
            confirm.kind === 'repair' ? 'Forget trust and pair again' : 'Remove server'
          }}</UiButton>
          <UiButton variant="ghost" size="sm" @click="confirm = undefined">Cancel</UiButton>
        </div>
      </div>

      <form
        v-if="editing === saved.origin"
        class="mx-5 mb-5 grid gap-4 rounded-lg border p-4"
        aria-label="Edit server profile"
        @submit.prevent="save"
      >
        <UiField label="Display name">
          <UiInput v-model="name" maxlength="100" :placeholder="saved.origin" />
        </UiField>
        <UiField
          label="Alternate routes"
          hint="One HTTPS address per line, for example a VPN address. Reopen the server to apply."
        >
          <textarea
            v-model="routes"
            rows="3"
            spellcheck="false"
            class="border-input bg-card focus-visible:border-ring rounded-md border p-2 font-mono text-xs"
          />
        </UiField>
        <div class="flex gap-2">
          <UiButton type="submit" size="sm" :disabled="working">Save profile</UiButton>
          <UiButton variant="ghost" size="sm" @click="editing = ''">Cancel</UiButton>
        </div>
      </form>

      <template v-if="isActive(saved) && signedIn">
        <div class="border-t p-5">
          <UiCardHeader title="Storage" level="h3" class="mb-4" />
          <VaultSettings
            :private-mode="privateMode"
            :unlocked="unlocked"
            :minutes="saved.vault_minutes"
            :remember="saved.remember_vault"
            @changed="emit('changed')"
            @choose="emit('chooseVault')"
          />
        </div>
        <div class="border-t p-5">
          <UiCardHeader title="Usage" level="h3" class="mb-4" />
          <StorageDashboard />
        </div>
      </template>
    </UiCard>
  </div>
</template>
