<script setup lang="ts">
import { ref, onMounted, watch } from 'vue';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type {
  CatalogEntry,
  VersionInfo,
} from '../../bindings/github.com/chironjit/serversend/shared/api/models';
import UiButton from './ui/UiButton.vue';
const props = defineProps<{ fileId?: string; name?: string }>();
const emit = defineEmits<{ changed: []; close: [] }>();
const versions = ref<VersionInfo[]>([]),
  trash = ref<CatalogEntry[]>([]);
const cursor = ref(''),
  next = ref(''),
  busy = ref(false),
  error = ref(''),
  purge = ref('');
async function load(value = '') {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (props.fileId) {
      const page = await TransferService.History(props.fileId, value);
      versions.value = page.versions ?? [];
      next.value = page.next_cursor ?? '';
    } else {
      const page = await TransferService.TrashPage(value);
      trash.value = page.entries ?? [];
      next.value = page.next_cursor ?? '';
    }
    cursor.value = value;
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function action(run: () => Promise<unknown>) {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  let succeeded = false;
  try {
    await run();
    succeeded = true;
    purge.value = '';
    emit('changed');
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
  if (succeeded) await load(cursor.value);
}
onMounted(() => load());
watch(
  () => props.fileId,
  () => load(),
);
</script>
<template>
  <section
    class="border-border m-4 rounded-lg border p-4"
    aria-label="File recovery"
    :aria-busy="busy"
  >
    <div class="flex items-center justify-between">
      <h3>{{ fileId ? 'Versions of ' + name : 'Trash' }}</h3>
      <UiButton variant="ghost" :disabled="busy" @click="emit('close')">Close</UiButton>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="busy" role="status">Loading…</p>
    <template v-if="fileId">
      <p>
        Both edits are kept when devices update the same revision. Select a retained version to
        resolve a conflict.
      </p>
      <div
        v-for="v in versions"
        :key="v.id"
        class="border-border flex flex-wrap items-center gap-2 border-t py-2"
      >
        <span
          >{{ new Date(v.created_at).toLocaleString() }} · {{ v.transmitted_bytes }} bytes
          {{ v.conflicted ? '· Conflict' : '' }}</span
        >
        <UiButton
          :disabled="busy"
          variant="outline"
          @click="action(() => TransferService.ChooseAndDownloadVersion(fileId!, v.id))"
          >Download version</UiButton
        >
        <UiButton
          :disabled="busy"
          variant="outline"
          @click="action(() => TransferService.SelectVersion(fileId!, v.id))"
          >Restore as current</UiButton
        >
      </div>
      <p v-if="!busy && !versions.length">No retained versions.</p>
    </template>
    <template v-else>
      <p>
        Restore items to their original location. Purge permanently removes the item and all its
        versions.
      </p>
      <div
        v-for="entry in trash"
        :key="entry.id"
        class="border-border flex flex-wrap items-center gap-2 border-t py-2"
      >
        <span>{{ entry.standard?.name }}</span>
        <UiButton
          :disabled="busy"
          variant="outline"
          @click="action(() => TransferService.RestoreTrash(entry.id))"
          >Restore</UiButton
        >
        <UiButton :disabled="busy" variant="outline" @click="purge = entry.id">Purge…</UiButton>
        <span v-if="purge === entry.id" role="alert"
          >Permanently delete this item and every retained version?
          <UiButton :disabled="busy" @click="action(() => TransferService.PurgeTrash(entry.id))"
            >Purge permanently</UiButton
          >
          <UiButton :disabled="busy" variant="ghost" @click="purge = ''">Cancel</UiButton>
        </span>
      </div>
      <p v-if="!busy && !trash.length">Trash is empty.</p>
    </template>
    <UiButton :disabled="busy" variant="outline" @click="load('')">Refresh</UiButton>
    <UiButton v-if="cursor" :disabled="busy" variant="outline" @click="load('')"
      >First page</UiButton
    >
    <UiButton v-if="next" :disabled="busy" variant="outline" @click="load(next)"
      >Next page</UiButton
    >
  </section>
</template>
