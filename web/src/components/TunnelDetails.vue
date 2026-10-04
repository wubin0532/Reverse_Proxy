<template>
  <el-drawer :model-value="true" :title="instance.name" size="min(960px, 98vw)" @close="$emit('close')">
    <div class="state-row">
      <el-tag :type="instance.status?.sync === 'synced' ? 'success' : 'info'">{{
        $t(instance.status?.sync === 'synced' ? 'tunnel.published' : 'tunnel.draft')
      }}</el-tag>
      <el-tag>{{ $t(`tunnel.status.${instance.status?.process || 'stopped'}`) }}</el-tag>
      <el-tag :type="instance.status?.ready ? 'success' : 'warning'">{{
        $t(instance.status?.ready ? 'tunnel.ready' : 'tunnel.waiting')
      }}</el-tag>
    </div>
    <el-tabs v-model="tab">
      <el-tab-pane :label="$t('tunnel.config')" name="config">
        <el-descriptions :column="1" border>
          <el-descriptions-item :label="$t('tunnel.source')">{{
            $t(instance.source === 'managed' ? 'tunnel.managed' : 'tunnel.imported')
          }}</el-descriptions-item>
          <el-descriptions-item :label="$t('tunnel.tunnelId')">{{ instance.tunnelId || '-' }}</el-descriptions-item>
          <el-descriptions-item :label="$t('tunnel.protocol')">{{ instance.protocol }}</el-descriptions-item>
          <el-descriptions-item :label="$t('tunnel.ipVersion')">{{ instance.ipVersion }}</el-descriptions-item>
          <el-descriptions-item :label="$t('tunnel.token')">{{
            $t(instance.tokenConfigured ? 'common.keyConfigured' : 'common.notConfigured')
          }}</el-descriptions-item>
        </el-descriptions>
        <el-button class="edit" type="primary" plain @click="$emit('edit')">{{ $t('tunnel.editInstance') }}</el-button>
      </el-tab-pane>
      <el-tab-pane :label="$t('tunnel.routes')" name="routes" lazy>
        <TunnelRoutes
          v-if="tab === 'routes'"
          inline
          :instance="instance"
          :sites="sites"
          :busy="busy"
          @saved="$emit('saved')"
          @sync="(digest) => $emit('sync', digest)"
        />
      </el-tab-pane>
      <el-tab-pane :label="$t('tunnel.logs')" name="logs" lazy>
        <el-button @click="loadLogs">{{ $t('common.refresh') }}</el-button>
        <el-button @click="router.push({ path: '/logs', query: { source: 'tunnel', entityId: instance.id } })">{{
          $t('tunnel.viewLogs')
        }}</el-button>
        <div class="logs" v-loading="loading">
          <div v-for="entry in logs" :key="entry.time + entry.message">
            <small>{{ formatTime(entry.time) }} · {{ entry.level }}</small>
            <p>{{ entry.message }}</p>
          </div>
          <el-empty v-if="!logs.length" :description="$t('logs.empty')" />
        </div>
      </el-tab-pane>
    </el-tabs>
  </el-drawer>
</template>
<script setup>
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import request from '../api'
import { formatTime } from '../utils/format'
import TunnelRoutes from './TunnelRoutes.vue'
const props = defineProps({ instance: { type: Object, required: true }, sites: Array, busy: Boolean })
defineEmits(['close', 'edit', 'saved', 'sync'])
const router = useRouter(),
  tab = ref('config'),
  logs = ref([]),
  loading = ref(false)
async function loadLogs() {
  loading.value = true
  try {
    logs.value =
      (await request.get('/api/logs', { params: { source: 'tunnel', entityId: props.instance.id, limit: 50 } })).data
        .entries || []
  } catch {
    /* The request interceptor displays the error. */
  } finally {
    loading.value = false
  }
}
watch(tab, (value) => {
  if (value === 'logs') loadLogs()
})
</script>
<style scoped>
.state-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.edit {
  margin-top: 16px;
}
.logs {
  margin-top: 16px;
}
.logs > div {
  border-bottom: 1px solid var(--ap-border);
  padding: 10px 0;
}
small {
  color: var(--ap-muted);
}
p {
  overflow-wrap: anywhere;
  margin: 6px 0;
}
</style>
