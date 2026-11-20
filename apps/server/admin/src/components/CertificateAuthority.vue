<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { Download } from 'lucide-vue-next';
import UiCode from './ui/UiCode.vue';
const fingerprint = ref(''),
  error = ref('');
onMounted(async () => {
  try {
    const response = await fetch('/api/v1/admin/ca', { cache: 'no-store' });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error?.message ?? 'Certificate unavailable');
    fingerprint.value = result.sha256;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
});
</script>
<template>
  <section class="my-4 grid max-w-2xl gap-3" aria-labelledby="ca-heading">
    <h3 id="ca-heading">HTTPS certificate authority</h3>
    <p v-if="error">{{ error }}</p>
    <template v-else-if="fingerprint">
      <p>
        Install this certificate as a trusted authority on browsers and devices that connect to this
        server. Before trusting it, check that the SHA-256 fingerprint matches the one in the server
        startup log.
      </p>
      <UiCode block>{{ fingerprint }}</UiCode>
      <a
        href="/api/v1/admin/ca.crt"
        download="serversend-ca.crt"
        class="border-input bg-card hover:bg-accent hover:text-accent-foreground inline-flex h-9 w-fit items-center gap-2 rounded-md border px-4 text-sm font-medium no-underline shadow-xs [&_svg]:size-4"
      >
        <Download aria-hidden="true" />
        Download CA certificate
      </a>
    </template>
  </section>
</template>
