<template>
  <div class="page-grid workspace-page">
    <WorkspaceHeader :title="$t('settings.title')" :description="$t('settings.subtitle')" icon="settings" />
    <el-tabs v-model="tab" class="workspace-page-tabs">
      <el-tab-pane :label="$t('settings.general')" name="general">
        <el-card>
          <template #header
            ><div class="card-header">
              <span>{{ $t('layout.accountSecurity') }}</span>
            </div></template
          >
          <div class="account-setting">
            <div class="account-copy">
              <h2>{{ $t('settings.accountManagement') }}</h2>
              <p class="muted">{{ $t('settings.accountSecurityDesc') }}</p>
            </div>
            <el-button type="primary" @click="openAccountSecurity">{{ $t('settings.openAccountSecurity') }}</el-button>
          </div>
        </el-card>
      </el-tab-pane>
      <el-tab-pane :label="$t('settings.maintenance')" name="maintenance" lazy><SystemMaintenance /></el-tab-pane>
    </el-tabs>
  </div>
</template>
<script setup>
import { inject, ref } from 'vue'
import WorkspaceHeader from '../components/WorkspaceHeader.vue'
import SystemMaintenance from '../components/SystemMaintenance.vue'
const openAccountSecurity = inject('openAccountSecurity', () => {})
const tab = ref('general')
</script>
<style scoped>
.account-setting {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 4px 0;
}
.account-copy h2 {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 8px;
}
.account-copy p {
  font-size: 13px;
  margin: 0;
  line-height: 1.7;
}
@media (max-width: 700px) {
  .account-setting {
    align-items: stretch;
    flex-direction: column;
    gap: 16px;
  }
}
</style>
