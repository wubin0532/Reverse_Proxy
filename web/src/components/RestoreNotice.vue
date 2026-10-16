<template>
  <section v-if="restoreWarnings.length" class="restore-notice" role="alert">
    <strong>{{ $t('dashboard.backup.partialTitle') }}</strong>
    <p>{{ $t('dashboard.backup.partialDescription') }}</p>
    <ul>
      <li v-for="(warning, index) in restoreWarnings" :key="index">{{ warning }}</li>
    </ul>
    <el-button @click="setRestoreWarnings([])">{{ $t('dashboard.backup.acknowledgeWarnings') }}</el-button>
  </section>
</template>
<script setup>
import { restoreWarnings, setRestoreWarnings } from '../utils/restore-notice'
</script>
<style scoped>
.restore-notice {
  margin: 12px;
  padding: 16px;
  border: 1px solid var(--ap-warning-border);
  border-radius: 12px;
  background: var(--ap-warning-soft);
  color: var(--ap-text);
  overflow-wrap: anywhere;
}
.restore-notice p,
.restore-notice ul {
  margin: 8px 0;
}
</style>
