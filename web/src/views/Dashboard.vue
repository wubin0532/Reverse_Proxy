<template>
  <div>
    <el-row :gutter="16">
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-value">{{ stats.ddns }}</div>
          <div class="stat-label">DDNS 任务</div>
          <div class="stat-sub">启用 {{ stats.ddnsEnabled }} 个</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-value">{{ stats.certs }}</div>
          <div class="stat-label">证书</div>
          <div class="stat-sub">正常 {{ stats.certsOk }} 张</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-value">{{ stats.sites }}</div>
          <div class="stat-label">Web 站点</div>
          <div class="stat-sub">监听中 {{ stats.sitesListening }} 个</div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-value">{{ stats.forwards }}</div>
          <div class="stat-label">转发规则</div>
          <div class="stat-sub">启用 {{ stats.forwardsEnabled }} 条</div>
        </el-card>
      </el-col>
    </el-row>

    <el-card class="info-card">
      <template #header>
        <div class="card-header">
          <span>运行信息</span>
          <el-button size="small" @click="load">刷新</el-button>
        </div>
      </template>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="当前用户">{{ auth.username || '-' }}</el-descriptions-item>
        <el-descriptions-item label="密码状态">
          <el-tag v-if="auth.needChangePassword" type="warning" size="small">使用默认密码</el-tag>
          <el-tag v-else type="success" size="small">已修改</el-tag>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>

<script setup>
import { onMounted, reactive } from 'vue'
import request from '../api'
import { useAuthStore } from '../store/auth'

const auth = useAuthStore()

const stats = reactive({
  ddns: 0, ddnsEnabled: 0,
  certs: 0, certsOk: 0,
  sites: 0, sitesListening: 0,
  forwards: 0, forwardsEnabled: 0
})

async function load() {
  const [ddnsRes, certsRes, sitesRes, forwardsRes] = await Promise.allSettled([
    request.get('/api/ddns/tasks'),
    request.get('/api/certs'),
    request.get('/api/sites'),
    request.get('/api/forwards')
  ])
  if (ddnsRes.status === 'fulfilled') {
    const list = ddnsRes.value.data || []
    stats.ddns = list.length
    stats.ddnsEnabled = list.filter((t) => t.enabled).length
  }
  if (certsRes.status === 'fulfilled') {
    const list = certsRes.value.data || []
    stats.certs = list.length
    stats.certsOk = list.filter((c) => c.status === 'ok').length
  }
  if (sitesRes.status === 'fulfilled') {
    const list = sitesRes.value.data || []
    stats.sites = list.length
    stats.sitesListening = list.filter((s) => s.status === 'listening').length
  }
  if (forwardsRes.status === 'fulfilled') {
    const list = forwardsRes.value.data || []
    stats.forwards = list.length
    stats.forwardsEnabled = list.filter((f) => f.enabled).length
  }
}

onMounted(() => {
  load()
  auth.fetchMe()
})
</script>

<style scoped>
.stat-card {
  text-align: center;
}
.stat-value {
  font-size: 32px;
  font-weight: 600;
  color: #303133;
}
.stat-label {
  margin-top: 6px;
  color: #606266;
  font-size: 14px;
}
.stat-sub {
  margin-top: 4px;
  color: #909399;
  font-size: 12px;
}
.info-card {
  margin-top: 16px;
}
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>
