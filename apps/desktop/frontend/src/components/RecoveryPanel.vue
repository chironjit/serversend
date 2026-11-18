<script setup lang="ts">
import { ref, onMounted, watch } from 'vue';
import { FileText, History, Trash2 } from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type {
  CatalogEntry,
  VersionInfo,
} from '../../bindings/github.com/chironjit/serversend/shared/api/models';
import { date, failure, size } from '../format';
import UiAlert from './ui/UiAlert.vue';
import UiBadge from './ui/UiBadge.vue';
import UiButton from './ui/UiButton.vue';
import UiEmpty from './ui/UiEmpty.vue';

// Trash when no file is given, otherwise the version history of that file.
const props = defineProps<{ fileId?: string; name?: string }>();
const emit = defineEmits<{ changed: [] }>();
const versions = ref<VersionInfo[]>([]),
  trash = ref<CatalogEntry[]>([]);
const cursor = ref(''),
  next = ref(''),
  busy = ref(false),
  loaded = ref(false),
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
    loaded.value = true;
  } catch (e) {
    error.value = failure(e);
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
    error.value = failure(e);
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
    class="px-5 py-4"
    :aria-label="fileId ? 'Versions of ' + name : 'Trash'"
    :aria-busy="busy"
  >
    <p class="text-muted-foreground m-0 text-sm">
      {{
        fileId
          ? 'Every upload keeps a version. When two devices edit the same file, both edits are kept — restore the one you want as current.'
          : 'Deleted items stay here until purged. Restore puts an item back where it was.'
      }}
    </p>
    <UiAlert v-if="error" class="mt-3">{{ error }}</UiAlert>

    <ul v-if="fileId && versions.length" class="m-0 mt-3 list-none p-0">
      <li
        v-for="v in versions"
        :key="v.id"
        class="hover:bg-muted/60 flex flex-wrap items-center gap-3 rounded-lg px-2 py-2"
      >
        <span
          class="bg-muted text-muted-foreground grid size-8 place-items-center rounded-md"
          aria-hidden="true"
          ><History class="size-4"
        /></span>
        <span class="min-w-0 flex-1 text-sm">
          {{ date(v.created_at) }}
          <span class="text-muted-foreground"> · {{ size(v.transmitted_bytes) }}</span>
        </span>
        <UiBadge v-if="v.conflicted" tone="warning">Conflict</UiBadge>
        <UiButton
          :disabled="busy"
          variant="ghost"
          size="sm"
          @click="action(() => TransferService.ChooseAndDownloadVersion(fileId!, v.id))"
          >Download…</UiButton
        >
        <UiButton
          :disabled="busy"
          variant="outline"
          size="sm"
          @click="action(() => TransferService.SelectVersion(fileId!, v.id))"
          >Restore as current</UiButton
        >
      </li>
    </ul>

    <ul v-else-if="!fileId && trash.length" class="m-0 mt-3 list-none p-0">
      <li
        v-for="entry in trash"
        :key="entry.id"
        class="hover:bg-muted/60 grid gap-2 rounded-lg px-2 py-2"
      >
        <div class="flex items-center gap-3">
          <span
            class="bg-muted text-muted-foreground grid size-8 place-items-center rounded-md"
            aria-hidden="true"
            ><FileText class="size-4"
          /></span>
          <span class="min-w-0 flex-1 truncate text-sm">{{
            entry.standard?.name ?? 'Private item'
          }}</span>
          <UiButton
            :disabled="busy"
            variant="outline"
            size="sm"
            @click="action(() => TransferService.RestoreTrash(entry.id))"
            >Restore</UiButton
          >
          <UiButton
            :disabled="busy"
            variant="ghost"
            size="sm"
            class="hover:text-destructive"
            @click="purge = entry.id"
            >Purge…</UiButton
          >
        </div>
        <div
          v-if="purge === entry.id"
          role="alert"
          class="border-destructive/30 bg-destructive/5 flex flex-wrap items-center gap-2 rounded-lg border p-3 text-sm"
        >
          <span class="flex-1">Permanently delete this item and every retained version?</span>
          <UiButton
            variant="destructive"
            size="sm"
            :disabled="busy"
            @click="action(() => TransferService.PurgeTrash(entry.id))"
            >Purge permanently</UiButton
          >
          <UiButton :disabled="busy" variant="ghost" size="sm" @click="purge = ''">Cancel</UiButton>
        </div>
      </li>
    </ul>

    <UiEmpty v-else-if="loaded" :title="fileId ? 'No retained versions' : 'Trash is empty'">
      <template #icon><History v-if="fileId" /><Trash2 v-else /></template>
    </UiEmpty>
    <p v-else-if="busy" class="text-muted-foreground mt-3 text-sm" role="status">Loading…</p>

    <div v-if="cursor || next" class="mt-3 flex gap-2">
      <UiButton v-if="cursor" :disabled="busy" variant="outline" size="sm" @click="load('')"
        >First page</UiButton
      >
      <UiButton v-if="next" :disabled="busy" variant="outline" size="sm" @click="load(next)"
        >Next page</UiButton
      >
    </div>
  </section>
</template>
