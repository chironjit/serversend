<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { RefreshCw } from 'lucide-vue-next';
import Accounts from './components/Accounts.vue';
import ThemePicker from './components/ThemePicker.vue';
import BrandMark from './components/ui/BrandMark.vue';
import UiBadge from './components/ui/UiBadge.vue';
import UiButton from './components/ui/UiButton.vue';
import UiCard from './components/ui/UiCard.vue';
import { checkReadiness } from './readiness';

const pages = ['dashboard', 'accounts', 'logs', 'settings'];
const page = ref('dashboard');
function navigate() {
  const value = location.hash.slice(1);
  page.value = pages.includes(value) ? value : 'dashboard';
}
const status = ref<'checking' | 'ready' | 'unavailable'>('checking');
let activeRequest: AbortController | undefined;
let disposed = false;

async function refresh() {
  if (activeRequest) return;
  const controller = new AbortController();
  activeRequest = controller;
  status.value = 'checking';
  const timeout = window.setTimeout(() => controller.abort(), 5000);
  try {
    const ready = await checkReadiness(controller.signal);
    if (!disposed) status.value = ready ? 'ready' : 'unavailable';
  } catch {
    if (!disposed) status.value = 'unavailable';
  } finally {
    window.clearTimeout(timeout);
    activeRequest = undefined;
  }
}

onMounted(() => {
  navigate();
  window.addEventListener('hashchange', navigate);
  void refresh();
});
onUnmounted(() => {
  window.removeEventListener('hashchange', navigate);
  disposed = true;
  activeRequest?.abort();
});
</script>

<template>
  <a href="#main" class="skip-link">Skip to server status</a>
  <div class="grid min-h-screen lg:grid-cols-[224px_minmax(0,1fr)]">
    <aside
      class="bg-sidebar border-border sticky top-0 hidden h-screen flex-col border-r lg:flex"
      aria-label="Administration navigation"
    >
      <div class="flex h-14 items-center gap-2.5 px-4">
        <BrandMark />
        <span class="text-[0.9375rem] font-semibold tracking-tight">ServerSend</span>
        <UiBadge tone="primary" class="ml-auto">Admin</UiBadge>
      </div>
      <nav class="flex flex-1 flex-col gap-1 px-3 pt-4">
        <p
          class="text-muted-foreground m-0 px-3 pb-2 text-[0.6875rem] font-medium tracking-[0.08em] uppercase"
        >
          Administration
        </p>
        <a
          v-for="item in pages"
          :key="item"
          :href="'#' + item"
          :aria-current="page === item ? 'page' : undefined"
          class="rounded-md px-3 py-2 text-sm capitalize"
          :class="page === item ? 'bg-accent text-accent-foreground' : 'text-muted-foreground'"
          >{{ item }}</a
        >
      </nav>
      <div class="border-border m-3 rounded-lg border p-3">
        <p class="m-0 text-xs font-medium">Server administration</p>
        <p class="text-muted-foreground m-0 mt-1 text-xs leading-5">
          File transfers live in the desktop app.
        </p>
      </div>
    </aside>

    <div class="flex min-w-0 flex-col">
      <header
        class="border-border bg-card/80 sticky top-0 z-10 flex h-14 items-center gap-3 border-b px-4 backdrop-blur sm:px-6"
      >
        <BrandMark class="lg:hidden" />
        <p class="m-0 text-sm font-semibold tracking-tight lg:hidden">ServerSend</p>
        <p class="text-muted-foreground m-0 hidden text-sm lg:block">Server administration</p>
        <div class="ml-auto">
          <ThemePicker />
        </div>
      </header>

      <main id="main" tabindex="-1" class="mx-auto w-full max-w-5xl flex-1 p-4 sm:p-6">
        <div class="mb-6">
          <h1 class="m-0 text-xl font-semibold tracking-tight">Server administration</h1>
          <p class="text-muted-foreground mt-1.5 max-w-xl text-sm leading-6">
            Manage user accounts and check server health. File transfers are available through the
            desktop app.
          </p>
        </div>

        <nav class="mb-4 flex gap-4 lg:hidden">
          <a
            v-for="item in pages"
            :key="item"
            :href="'#' + item"
            :aria-current="page === item ? 'page' : undefined"
            class="capitalize"
            >{{ item }}</a
          >
        </nav>
        <Accounts :page="page" restore />

        <UiCard
          v-if="page === 'dashboard'"
          as="section"
          aria-labelledby="status-heading"
          class="mt-5 p-5 sm:p-6"
        >
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h2 id="status-heading" class="m-0 text-base font-semibold tracking-tight">
                Server status
              </h2>
              <div role="status" aria-live="polite" :aria-busy="status === 'checking'" class="mt-3">
                <p v-if="status === 'checking'" class="m-0 flex items-center gap-2 font-medium">
                  <UiBadge dot>Checking</UiBadge>
                  Checking server…
                </p>
                <p v-else-if="status === 'ready'" class="m-0 flex items-center gap-2 font-medium">
                  <UiBadge tone="success" dot>Operational</UiBadge>
                  Server is responding
                </p>
                <p v-else class="m-0 flex items-center gap-2 font-medium">
                  <UiBadge tone="warning" dot>Unavailable</UiBadge>
                  Server is unavailable
                </p>
              </div>
            </div>
            <UiButton variant="outline" :disabled="status === 'checking'" @click="refresh">
              <RefreshCw :class="status === 'checking' ? 'animate-spin' : ''" aria-hidden="true" />
              Check again
            </UiButton>
          </div>
          <p
            v-if="status === 'unavailable'"
            class="text-muted-foreground mt-4 max-w-xl text-sm leading-6"
          >
            Check your network connection and try again. If this continues, ask the server operator
            to check the process and its storage.
          </p>
          <p v-else class="text-muted-foreground mt-4 max-w-xl text-sm leading-6">
            This checks the server process and metadata connection. It does not confirm file storage
            capacity or transfer availability.
          </p>
        </UiCard>
      </main>

      <footer class="border-border text-muted-foreground border-t px-4 py-4 text-xs sm:px-6">
        Private storage for your network · Development build
      </footer>
    </div>
  </div>
</template>
