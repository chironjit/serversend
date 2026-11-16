<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import Accounts from './components/Accounts.vue';
import ThemePicker from './components/ThemePicker.vue';
import { checkReadiness } from './readiness';

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
  void refresh();
});
onUnmounted(() => {
  disposed = true;
  activeRequest?.abort();
});
</script>

<template>
  <a href="#main" class="skip-link">Skip to server status</a>
  <div class="mx-auto flex min-h-screen max-w-5xl flex-col px-5 py-8 sm:px-10 sm:py-12">
    <header
      class="flex flex-wrap items-start justify-between gap-6 border-b border-slate-200 pb-8 dark:border-slate-800"
    >
      <div>
        <p class="text-2xl font-semibold tracking-tight">
          ServerSend<span class="text-teal-700 dark:text-teal-300">.</span>
        </p>
        <p class="mt-1 text-sm text-slate-600 dark:text-slate-400">Server administration</p>
      </div>
      <ThemePicker />
    </header>

    <main id="main" tabindex="-1" class="flex-1 py-12 sm:py-16">
      <p class="text-sm font-semibold uppercase tracking-widest text-teal-800 dark:text-teal-300">
        Server foundation
      </p>
      <h1 class="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">A home for your files.</h1>
      <p class="mt-4 max-w-xl text-base leading-7 text-slate-600 dark:text-slate-300">
        Manage user accounts below. File transfers are available through the desktop app.
      </p>

      <section
        aria-labelledby="status-heading"
        class="mt-10 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm sm:p-8 dark:border-slate-700 dark:bg-slate-900"
      >
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2 id="status-heading" class="text-lg font-semibold">Server status</h2>
            <div role="status" aria-live="polite" :aria-busy="status === 'checking'" class="mt-3">
              <p
                v-if="status === 'checking'"
                class="font-medium text-slate-600 dark:text-slate-300"
              >
                Checking server…
              </p>
              <p
                v-else-if="status === 'ready'"
                class="font-medium text-teal-800 dark:text-teal-300"
              >
                Server is responding
              </p>
              <p v-else class="font-medium text-amber-800 dark:text-amber-200">
                Server is unavailable
              </p>
            </div>
          </div>
          <button
            type="button"
            :disabled="status === 'checking'"
            class="action-button"
            @click="refresh"
          >
            Check again
          </button>
        </div>
        <p
          v-if="status === 'unavailable'"
          class="mt-4 max-w-xl text-sm leading-6 text-slate-600 dark:text-slate-300"
        >
          Check your network connection and try again. If this continues, ask the server operator to
          check the process and its storage.
        </p>
        <p v-else class="mt-4 max-w-xl text-sm leading-6 text-slate-600 dark:text-slate-300">
          This checks the server process and metadata connection. It does not confirm file storage
          capacity or transfer availability.
        </p>
      </section>

      <Accounts />
    </main>

    <footer
      class="border-t border-slate-200 pt-6 text-sm text-slate-600 dark:border-slate-800 dark:text-slate-400"
    >
      Private storage for your network · Development build
    </footer>
  </div>
</template>
