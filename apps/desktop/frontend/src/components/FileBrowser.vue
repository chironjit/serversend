<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import {
  ChevronDown,
  ChevronRight,
  Download,
  FileText,
  Folder,
  FolderOpen,
  FolderPlus,
  History,
  Home,
  RefreshCw,
  Trash2,
  TriangleAlert,
  Upload,
  X,
} from 'lucide-vue-next';
import { TransferService } from '../../bindings/github.com/chironjit/serversend/apps/desktop';
import type { File } from '../../bindings/github.com/chironjit/serversend/apps/desktop/internal/transfer/models';
import { failure } from '../format';
import RecoveryPanel from './RecoveryPanel.vue';
import UiAlert from './ui/UiAlert.vue';
import UiButton from './ui/UiButton.vue';
import UiEmpty from './ui/UiEmpty.vue';
import UiInput from './ui/UiInput.vue';

// The folder browser for the signed-in server. Trash and version history
// replace the listing instead of stacking more panels beside it.
const emit = defineEmits<{ queued: [] }>();

const mode = ref<'browse' | 'trash' | 'versions'>('browse');
const busy = ref(false);
const error = ref('');
const files = ref<File[]>([]);
const folders = ref<{ id: string; name: string }[]>([]);
const parent = computed(() => folders.value.at(-1)?.id ?? '');
const fileCursor = ref('');
const nextFileCursor = ref('');
const previousFilePages = ref<string[]>([]);
const filePageNumber = ref(1);
const catalogRevision = ref('');
const catalogSnapshot = ref('');
const catalogNotice = ref('');
const selectedIDs = ref<string[]>([]);
const restorePolicy = ref('fail');
const newFolder = ref<string>();
const folderInput = ref<InstanceType<typeof UiInput>>();
const uploadMenu = ref(false);
const pendingDelete = ref<File>();
const versionsOf = ref<File>();
let request = 0;

const selectedFile = computed(() => {
  if (selectedIDs.value.length !== 1) return undefined;
  return files.value.find((file) => file.id === selectedIDs.value[0] && !file.directory);
});

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

async function loadPage(
  folder: string,
  cursor = '',
  direction: 'reset' | 'next' | 'back' = 'reset',
) {
  const current = ++request;
  let effectiveCursor = cursor;
  let page;
  let resetNotice = '';
  try {
    page = await TransferService.FilesPage(folder, cursor);
  } catch (reason) {
    if (!cursor) throw reason;
    // A continuation cannot be trusted after a catalog mutation or reconnect.
    // Restart once; the fresh request also surfaces authentication/network errors.
    page = await TransferService.FilesPage(folder, '');
    effectiveCursor = '';
    resetNotice = 'The folder changed or its page expired. Showing the first page.';
  }
  if (
    effectiveCursor &&
    (page.revision !== catalogRevision.value || page.snapshot !== catalogSnapshot.value)
  ) {
    page = await TransferService.FilesPage(folder, '');
    effectiveCursor = '';
    resetNotice = 'The folder changed. Showing the latest first page.';
  }
  if (current !== request) return false;
  if (!effectiveCursor || direction === 'reset') {
    previousFilePages.value = [];
    filePageNumber.value = 1;
  } else if (direction === 'next') {
    previousFilePages.value = [...previousFilePages.value, fileCursor.value].slice(-100);
    filePageNumber.value++;
  } else {
    previousFilePages.value.pop();
    filePageNumber.value--;
  }
  fileCursor.value = effectiveCursor;
  nextFileCursor.value = page.nextCursor;
  catalogRevision.value = page.revision;
  catalogSnapshot.value = page.snapshot;
  files.value = page.files ?? [];
  selectedIDs.value = [];
  catalogNotice.value = resetNotice;
  return true;
}

const reload = () => attempt(async () => void (await loadPage(parent.value)));
const changePage = (back = false) =>
  attempt(async () => {
    const cursor = back ? (previousFilePages.value.at(-1) ?? '') : nextFileCursor.value;
    await loadPage(parent.value, cursor, back ? 'back' : 'next');
  });
const openFolder = (file: File) =>
  attempt(async () => {
    if (await loadPage(file.id)) folders.value.push({ id: file.id, name: file.name });
  });
const navigate = (depth: number) =>
  attempt(async () => {
    if (await loadPage(folders.value[depth - 1]?.id ?? ''))
      folders.value = folders.value.slice(0, depth);
  });
async function startFolder() {
  newFolder.value = '';
  await nextTick();
  folderInput.value?.$el.focus();
}
const createFolder = () =>
  attempt(async () => {
    await TransferService.CreateDirectory(parent.value, newFolder.value ?? '');
    newFolder.value = undefined;
    await loadPage(parent.value);
  });
const deleteEntry = () =>
  attempt(async () => {
    if (!pendingDelete.value) return;
    await TransferService.DeleteEntry(pendingDelete.value.id);
    pendingDelete.value = undefined;
    await loadPage(parent.value);
  });
const upload = (folder: boolean) =>
  attempt(async () => {
    uploadMenu.value = false;
    try {
      if (folder) await TransferService.ChooseAndUploadFolder(parent.value);
      else await TransferService.ChooseAndUpload(parent.value);
    } finally {
      emit('queued');
    }
    await loadPage(parent.value);
  });
const download = (file: File) =>
  attempt(async () => void (await TransferService.ChooseAndDownload(file.id)));
const restore = (ids = selectedIDs.value) =>
  attempt(async () => {
    if (!ids.length) return;
    await TransferService.ChooseAndRestore(ids, restorePolicy.value);
    emit('queued');
  });
function toggle(file: File) {
  const at = selectedIDs.value.indexOf(file.id);
  if (at < 0) selectedIDs.value = [...selectedIDs.value, file.id];
  else selectedIDs.value = selectedIDs.value.filter((id) => id !== file.id);
}
function showVersions() {
  versionsOf.value = selectedFile.value;
  mode.value = 'versions';
}
function back() {
  mode.value = 'browse';
  versionsOf.value = undefined;
}

// At most one listing request every two seconds while uploads complete.
let timer: ReturnType<typeof setTimeout> | undefined;
let refreshedAt = 0;
function reloadSoon() {
  if (timer) return;
  const run = () => {
    timer = undefined;
    refreshedAt = Date.now();
    void loadPage(parent.value).catch((reason) => (error.value = failure(reason)));
  };
  const wait = refreshedAt + 2000 - Date.now();
  if (wait <= 0) run();
  else timer = setTimeout(run, wait);
}
// Called by the session check: reload only when the server catalog changed.
async function checkChanged() {
  if (!catalogRevision.value || busy.value) return;
  if (await TransferService.CatalogChanged(catalogRevision.value)) {
    await loadPage(parent.value);
    catalogNotice.value = 'The folder changed. Showing the latest first page.';
  }
}
defineExpose({ reload, reloadSoon, checkChanged });

onMounted(() => void reload());
onUnmounted(() => {
  request++;
  clearTimeout(timer);
});
</script>

<template>
  <div class="min-w-0">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-2 border-b px-5 py-3">
      <template v-if="mode === 'browse'">
        <div class="relative" @keydown.esc="uploadMenu = false">
          <UiButton
            size="sm"
            :disabled="busy"
            :aria-expanded="uploadMenu"
            @click="uploadMenu = !uploadMenu"
          >
            <Upload aria-hidden="true" />
            Upload
            <ChevronDown class="size-3.5" aria-hidden="true" />
          </UiButton>
          <div
            v-if="uploadMenu"
            class="bg-popover absolute top-[calc(100%+6px)] left-0 z-20 grid min-w-40 gap-1 rounded-lg border p-1.5 shadow-md"
          >
            <UiButton variant="ghost" size="sm" class="justify-start" @click="upload(false)">
              <FileText aria-hidden="true" />Files…
            </UiButton>
            <UiButton variant="ghost" size="sm" class="justify-start" @click="upload(true)">
              <Folder aria-hidden="true" />Folder…
            </UiButton>
          </div>
        </div>
        <UiButton variant="outline" size="sm" :disabled="busy" @click="startFolder">
          <FolderPlus aria-hidden="true" />New folder
        </UiButton>
        <div class="ml-auto flex items-center gap-1">
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="busy || !selectedFile"
            :title="selectedFile ? 'Show versions' : 'Select one file to see its versions'"
            @click="showVersions"
          >
            <History aria-hidden="true" />Versions
          </UiButton>
          <UiButton variant="ghost" size="sm" :disabled="busy" @click="mode = 'trash'">
            <Trash2 aria-hidden="true" />Trash
          </UiButton>
          <UiButton
            variant="ghost"
            size="icon-sm"
            :disabled="busy"
            aria-label="Refresh files"
            title="Refresh"
            @click="reload"
          >
            <RefreshCw aria-hidden="true" />
          </UiButton>
        </div>
      </template>
      <template v-else>
        <UiButton variant="ghost" size="sm" @click="back">
          <ChevronRight class="rotate-180" aria-hidden="true" />Back to files
        </UiButton>
        <span class="text-sm font-medium">
          {{ mode === 'trash' ? 'Trash' : 'Versions of ' + versionsOf?.name }}
        </span>
      </template>
    </div>

    <RecoveryPanel
      v-if="mode !== 'browse'"
      :file-id="versionsOf?.id"
      :name="versionsOf?.name"
      @changed="reloadSoon"
    />

    <template v-else>
      <UiAlert v-if="error" class="mx-5 mt-4">{{ error }}</UiAlert>

      <nav
        class="text-muted-foreground flex flex-wrap items-center gap-0.5 px-4 pt-3 text-sm"
        aria-label="Folder path"
      >
        <UiButton variant="ghost" size="sm" :disabled="busy" @click="navigate(0)">
          <Home class="size-3.5" aria-hidden="true" />All files
        </UiButton>
        <template v-for="(folder, index) in folders" :key="folder.id">
          <ChevronRight class="size-3.5 shrink-0" aria-hidden="true" />
          <UiButton
            variant="ghost"
            size="sm"
            :disabled="busy"
            :aria-current="index === folders.length - 1 ? 'page' : undefined"
            :class="index === folders.length - 1 ? 'text-foreground font-medium' : ''"
            @click="navigate(index + 1)"
          >
            {{ folder.name }}
          </UiButton>
        </template>
      </nav>

      <p v-if="catalogNotice" class="text-muted-foreground m-0 px-5 pt-2 text-sm" role="status">
        {{ catalogNotice }}
      </p>

      <section
        v-if="pendingDelete"
        class="border-destructive/30 bg-destructive/5 mx-5 mt-3 flex items-start gap-3 rounded-lg border p-4"
        role="alertdialog"
        aria-label="Confirm deletion"
      >
        <TriangleAlert class="text-destructive mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <div class="grid min-w-0 gap-2">
          <p class="m-0 text-sm font-semibold">Move “{{ pendingDelete.name }}” to trash?</p>
          <p class="text-muted-foreground m-0 text-sm">
            {{
              pendingDelete.directory
                ? 'This folder and everything inside it will move to trash.'
                : 'This file and all its versions will move to trash.'
            }}
            You can restore it from Trash.
          </p>
          <div class="flex gap-2">
            <UiButton variant="destructive" size="sm" :disabled="busy" @click="deleteEntry">
              Move to trash
            </UiButton>
            <UiButton variant="ghost" size="sm" :disabled="busy" @click="pendingDelete = undefined">
              Cancel
            </UiButton>
          </div>
        </div>
      </section>

      <div
        v-if="selectedIDs.length"
        class="bg-accent text-accent-foreground mx-5 mt-3 flex flex-wrap items-center gap-3 rounded-lg px-3 py-2 text-sm"
        aria-label="Restore selection"
      >
        <span class="font-medium">{{ selectedIDs.length }} selected</span>
        <label class="ml-auto flex items-center gap-2">
          If a local file exists
          <select
            v-model="restorePolicy"
            :disabled="busy"
            aria-label="Existing file policy"
            class="border-input bg-card text-foreground h-8 rounded-md border px-2"
          >
            <option value="fail">Stop and report</option>
            <option value="skip">Skip it</option>
            <option value="replace">Replace it</option>
          </select>
        </label>
        <UiButton size="sm" :disabled="busy" @click="restore()">
          <Download aria-hidden="true" />Download {{ selectedIDs.length }}…
        </UiButton>
        <UiButton
          variant="ghost"
          size="icon-sm"
          aria-label="Clear selection"
          @click="selectedIDs = []"
        >
          <X aria-hidden="true" />
        </UiButton>
      </div>

      <div class="px-3 pt-2 pb-3">
        <form
          v-if="newFolder !== undefined"
          class="flex items-center gap-2 px-2 py-1.5"
          @submit.prevent="createFolder"
          @keydown.esc="newFolder = undefined"
        >
          <span
            class="bg-accent text-accent-foreground grid size-8 shrink-0 place-items-center rounded-md"
            aria-hidden="true"
          >
            <FolderPlus class="size-4" />
          </span>
          <UiInput
            ref="folderInput"
            v-model="newFolder"
            required
            maxlength="255"
            placeholder="Folder name"
            aria-label="New folder name"
            class="h-8 max-w-72"
          />
          <UiButton type="submit" size="sm" :disabled="busy">Create</UiButton>
          <UiButton variant="ghost" size="sm" @click="newFolder = undefined">Cancel</UiButton>
        </form>

        <UiEmpty v-if="files.length === 0 && newFolder === undefined" title="This folder is empty">
          <template #icon><FolderOpen /></template>
          Use Upload to send files or a folder from this computer.
        </UiEmpty>
        <ul v-else class="m-0 list-none p-0" aria-label="Files">
          <li
            v-if="files.length"
            class="text-muted-foreground flex items-center gap-2 px-2 py-1.5 text-xs"
          >
            <input
              type="checkbox"
              :disabled="busy"
              :checked="selectedIDs.length === files.length"
              aria-label="Select this page"
              class="accent-primary"
              @change="
                selectedIDs = ($event.target as HTMLInputElement).checked
                  ? files.map((file) => file.id)
                  : []
              "
            />
            <span class="flex-1 pl-1">Name</span>
            <span>{{ files.length }} {{ files.length === 1 ? 'item' : 'items' }}</span>
          </li>
          <li
            v-for="file in files"
            :key="file.id"
            class="group hover:bg-muted/60 flex items-center gap-2 rounded-lg px-2 py-1 transition-colors"
            :class="selectedIDs.includes(file.id) ? 'bg-muted/60' : ''"
          >
            <input
              v-model="selectedIDs"
              type="checkbox"
              :value="file.id"
              :disabled="busy"
              :aria-label="'Select ' + file.name"
              class="accent-primary"
            />
            <button
              type="button"
              class="hover:text-primary flex min-w-0 flex-1 items-center gap-3 rounded-md border-0 bg-transparent p-1 text-left text-inherit"
              :disabled="busy"
              @click="file.directory ? openFolder(file) : toggle(file)"
            >
              <span
                class="grid size-8 shrink-0 place-items-center rounded-md"
                :class="
                  file.directory
                    ? 'bg-accent text-accent-foreground'
                    : 'bg-muted text-muted-foreground'
                "
                aria-hidden="true"
              >
                <Folder v-if="file.directory" class="size-4" />
                <FileText v-else class="size-4" />
              </span>
              <span class="truncate text-sm" :class="file.directory ? 'font-medium' : ''">
                {{ file.name }}
              </span>
            </button>
            <div
              class="flex items-center opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100"
            >
              <UiButton
                variant="ghost"
                size="icon-sm"
                :disabled="busy"
                :aria-label="'Download ' + file.name"
                title="Download…"
                @click="file.directory ? restore([file.id]) : download(file)"
              >
                <Download aria-hidden="true" />
              </UiButton>
              <UiButton
                variant="ghost"
                size="icon-sm"
                class="hover:text-destructive"
                :aria-label="'Delete ' + file.name"
                title="Move to trash"
                :disabled="busy"
                @click="pendingDelete = file"
              >
                <Trash2 aria-hidden="true" />
              </UiButton>
            </div>
          </li>
        </ul>
      </div>

      <nav
        v-if="previousFilePages.length || nextFileCursor"
        aria-label="File pages"
        class="flex items-center justify-between gap-2 border-t px-5 py-3"
      >
        <UiButton
          variant="outline"
          size="sm"
          :disabled="busy || !previousFilePages.length"
          @click="changePage(true)"
          >Previous</UiButton
        >
        <span class="text-muted-foreground text-xs">Page {{ filePageNumber }}</span>
        <UiButton
          variant="outline"
          size="sm"
          :disabled="busy || !nextFileCursor"
          @click="changePage()"
          >Next</UiButton
        >
      </nav>
    </template>
  </div>
</template>
