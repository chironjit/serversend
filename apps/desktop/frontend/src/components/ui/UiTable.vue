<template>
  <div class="overflow-auto rounded-lg border">
    <table class="w-full caption-bottom text-sm">
      <slot />
    </table>
  </div>
</template>

<style scoped>
table :slotted(thead tr) {
  border-bottom: 1px solid var(--border);
}
table :slotted(th) {
  height: 2.5rem;
  padding: 0 1rem;
  text-align: left;
  vertical-align: middle;
  font-weight: 500;
  color: var(--muted-foreground);
  white-space: nowrap;
}
table :slotted(tbody tr) {
  border-bottom: 1px solid var(--border);
  transition: background-color 150ms ease;
}
table :slotted(tbody tr:last-child) {
  border-bottom: 0;
}
table :slotted(tbody tr:hover) {
  background: color-mix(in oklab, var(--muted) 50%, transparent);
}
table :slotted(td) {
  padding: 0.75rem 1rem;
  vertical-align: middle;
}
</style>
