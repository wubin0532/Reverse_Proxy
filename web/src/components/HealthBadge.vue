<template>
  <span class="health-badge" :class="{ warning: issues > 0 }">{{ issues > 0 ? issues : '✓' }}</span>
</template>
<script setup>
defineProps({ issues: { type: Number, default: 0 } })
</script>
<style scoped>
.health-badge {
  display: grid;
  place-items: center;
  width: 68px;
  height: 68px;
  border-radius: 50%;
  background: var(--ap-glass);
  font-size: 25px;
  font-weight: 800;
}
.health-badge.warning {
  background: var(--ap-warning-glow);
}
</style>
