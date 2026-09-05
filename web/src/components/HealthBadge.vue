<template><span class="health-badge" :class="{ warning: issues > 0 }">{{ issues > 0 ? issues : '✓' }}</span></template>
<script setup>
defineProps({ issues: { type: Number, default: 0 } })
</script>
<style scoped>
.health-badge{display:grid;place-items:center;width:68px;height:68px;border-radius:50%;background:rgba(255,255,255,.18);font-size:25px;font-weight:800}.health-badge.warning{background:rgba(255,185,83,.25)}
</style>
