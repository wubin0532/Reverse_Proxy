<template>
  <svg
    class="nav-icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <template v-if="name === 'overview'">
      <path d="M4 17a9 9 0 1 1 16 0" />
      <path d="m12 12 4-4M4 17h16" />
      <circle cx="12" cy="12" r="1.5" />
    </template>
    <template v-else-if="name === 'ddns'">
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c-5 5-5 13 0 18 5-5 5-13 0-18M5 6h14M5 18h14" />
    </template>
    <template v-else-if="name === 'web'">
      <rect x="3" y="4" width="18" height="13" rx="2" />
      <path d="M3 8h18M8 21h8m-4-4v4" />
      <path d="M6 6h.01M9 6h.01" />
    </template>
    <template v-else-if="name === 'cert'">
      <path d="M14 3H5v18h9m0-18 5 5v4M14 3v5h5" />
      <circle cx="17" cy="16" r="3" />
      <path d="m15 18-1 4 3-1 3 1-1-4M8 8h3M8 12h3" />
    </template>
    <template v-else-if="name === 'protection'">
      <path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z" />
      <path d="m8.5 12 2.5 2.5 4.5-5" />
    </template>
    <template v-else-if="name === 'forward'"><path d="M4 7h15m-4-4 4 4-4 4M20 17H5m4-4-4 4 4 4" /></template>
    <template v-else-if="name === 'tunnel'">
      <path d="M6 17H5a4 4 0 0 1-1-8 5 5 0 0 1 9-3 4 4 0 0 1 6 3 4 4 0 0 1 0 8h-1M9 15l3-3 3 3m-3-3v9" />
    </template>
    <template v-else-if="name === 'logs'">
      <rect x="4" y="3" width="16" height="18" rx="2" />
      <path d="M8 7h8M8 11h8M8 15h4M8 18h6" />
    </template>
    <template v-else-if="name === 'notifications'">
      <path d="M5 16h14l-2-3V9a5 5 0 0 0-10 0v4l-2 3Zm5 4h4M12 2v2" />
      <circle cx="18" cy="5" r="2" />
    </template>
    <template v-else-if="name === 'settings'">
      <path d="M4 6h16M4 12h16M4 18h16" />
      <circle cx="8" cy="6" r="2" fill="var(--ap-card)" />
      <circle cx="16" cy="12" r="2" fill="var(--ap-card)" />
      <circle cx="10" cy="18" r="2" fill="var(--ap-card)" />
    </template>
  </svg>
</template>
<script setup>
defineProps({ name: { type: String, required: true } })
</script>
<style scoped>
.nav-icon {
  display: inline-block;
  width: 22px;
  height: 22px;
  flex: 0 0 auto;
  vertical-align: middle;
}
</style>
