<template>
  <div class="settings-page">
    <el-card class="settings-card">
      <template #header>
        <span>修改账号密码</span>
      </template>
      <el-alert
        v-if="auth.needChangePassword"
        type="warning"
        title="正在使用默认账号密码（666 / 666），请立即修改"
        :closable="false"
        class="tip"
      />
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="100px"
        class="settings-form"
      >
        <el-form-item label="账号" prop="username">
          <el-input v-model="form.username" placeholder="登录账号" />
        </el-form-item>
        <el-form-item v-if="!auth.needChangePassword" label="原密码" prop="oldPassword">
          <el-input v-model="form.oldPassword" type="password" show-password placeholder="当前密码" />
        </el-form-item>
        <el-form-item label="新密码" prop="newPassword">
          <el-input v-model="form.newPassword" type="password" show-password placeholder="新密码" />
        </el-form-item>
        <el-form-item label="确认新密码" prop="confirmPassword">
          <el-input v-model="form.confirmPassword" type="password" show-password placeholder="再次输入新密码" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="onSubmit">保存</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 消息通知 -->
    <el-card class="settings-card">
      <template #header>
        <span>消息通知</span>
      </template>
      <el-form :model="webhook" label-width="100px" class="settings-form" v-loading="webhookLoading">
        <el-form-item label="启用通知">
          <el-switch v-model="webhook.enabled" />
        </el-form-item>
        <template v-if="webhook.enabled">
          <el-form-item label="通知类型">
            <el-select v-model="webhook.type" style="width: 100%">
              <el-option label="Server酱" value="serverchan" />
              <el-option label="Bark" value="bark" />
              <el-option label="Telegram" value="telegram" />
              <el-option label="自定义 Webhook" value="custom" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="webhook.type === 'serverchan'" label="SendKey">
            <el-input v-model="webhook.key" placeholder="Server酱 SendKey" />
          </el-form-item>
          <el-form-item v-if="webhook.type === 'bark'" label="Key">
            <el-input v-model="webhook.key" placeholder="Bark 推送 Key" />
          </el-form-item>
          <template v-if="webhook.type === 'telegram'">
            <el-form-item label="Bot Token">
              <el-input v-model="webhook.key" placeholder="Telegram Bot Token" />
            </el-form-item>
            <el-form-item label="Chat ID">
              <el-input v-model="webhook.chatId" placeholder="接收消息的 Chat ID" />
            </el-form-item>
          </template>
          <el-form-item v-if="webhook.type === 'custom'" label="Webhook 地址">
            <el-input v-model="webhook.url" placeholder="完整 URL，将以 POST JSON {title, content} 调用" />
          </el-form-item>
          <el-form-item label="通知事件">
            <el-checkbox-group v-model="webhook.events">
              <el-checkbox value="ddns">DDNS 更新</el-checkbox>
              <el-checkbox value="cert">证书申请/续签</el-checkbox>
            </el-checkbox-group>
          </el-form-item>
        </template>
        <el-form-item>
          <el-button type="primary" :loading="webhookSaving" @click="saveWebhook">保存</el-button>
          <el-button :loading="webhookTesting" @click="testWebhook">发送测试消息</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 系统升级 -->
    <el-card class="settings-card">
      <template #header>
        <div class="card-header">
          <span>系统升级</span>
          <el-button size="small" @click="loadSystemInfo">刷新</el-button>
        </div>
      </template>
      <el-descriptions :column="1" border class="sys-info" v-loading="sysInfoLoading">
        <el-descriptions-item label="当前版本">{{ sysInfo.version || '-' }}</el-descriptions-item>
        <el-descriptions-item label="系统架构">
          {{ sysInfo.goos ? `${sysInfo.goos} / ${sysInfo.goarch}` : '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="运行时长">{{ formatUptime(sysInfo.uptime) }}</el-descriptions-item>
      </el-descriptions>
      <div class="upgrade-actions">
        <el-button :loading="upgrade.checking" @click="checkUpdate">检查更新</el-button>
        <template v-if="upgrade.checked">
          <el-tag v-if="upgrade.hasUpdate" type="warning">发现新版本 {{ upgrade.latest }}</el-tag>
          <el-tag v-else type="success">已是最新版本（{{ upgrade.latest || sysInfo.version }}）</el-tag>
          <el-link
            v-if="upgrade.releaseUrl"
            :href="upgrade.releaseUrl"
            target="_blank"
            type="primary"
            class="release-link"
          >查看发布页</el-link>
        </template>
      </div>
      <div v-if="upgrade.hasUpdate" class="upgrade-actions">
        <el-popconfirm title="确定立即升级？升级过程中服务将短暂中断" @confirm="startUpgrade">
          <template #reference>
            <el-button type="warning" :disabled="upgradeBusy">立即升级</el-button>
          </template>
        </el-popconfirm>
      </div>
      <div v-if="upgrade.status.state && upgrade.status.state !== 'idle'" class="upgrade-status">
        <el-progress
          :percentage="upgradeProgress"
          :status="upgrade.status.state === 'failed' ? 'exception' : upgrade.status.state === 'done' ? 'success' : undefined"
        />
        <div class="upgrade-status-text">{{ upgradeStatusText }}</div>
        <el-alert
          v-if="upgrade.status.state === 'restarting'"
          type="warning"
          title="服务重启中，请稍后刷新页面"
          :closable="false"
          class="tip"
        />
        <el-alert
          v-if="upgrade.status.state === 'failed'"
          type="error"
          :title="'升级失败：' + (upgrade.status.error || '未知原因')"
          :closable="false"
          class="tip"
        />
        <el-alert
          v-if="upgrade.status.note"
          type="info"
          :title="upgrade.status.note"
          :closable="false"
          class="tip"
        />
      </div>
    </el-card>

    <!-- 防火墙状态 -->
    <el-card class="settings-card">
      <template #header>
        <div class="card-header">
          <span>防火墙自动放行</span>
          <el-button size="small" @click="loadFirewall">刷新</el-button>
        </div>
      </template>
      <el-alert
        v-if="!firewall.openwrt"
        type="info"
        title="当前系统不是 OpenWrt，防火墙自动放行不生效"
        :closable="false"
        class="tip"
      />
      <el-table :data="firewall.rules" size="small" v-loading="firewall.loading">
        <el-table-column prop="key" label="规则 ID" min-width="140" />
        <el-table-column prop="port" label="端口" width="100" />
        <el-table-column prop="proto" label="协议" width="100" />
        <template #empty><el-empty description="当前没有自动放行的规则" :image-size="60" /></template>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../api'
import { useAuthStore } from '../store/auth'

const router = useRouter()
const auth = useAuthStore()

// ---------- 修改账号密码 ----------
const formRef = ref()
const loading = ref(false)
const form = reactive({
  username: auth.username,
  oldPassword: '',
  newPassword: '',
  confirmPassword: ''
})

const rules = {
  username: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  oldPassword: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  newPassword: [{ required: true, min: 6, message: '新密码至少 6 位', trigger: 'blur' }],
  confirmPassword: [
    {
      validator: (rule, value, callback) => {
        if (!value) {
          callback(new Error('请再次输入新密码'))
        } else if (value !== form.newPassword) {
          callback(new Error('两次输入的密码不一致'))
        } else {
          callback()
        }
      },
      trigger: 'blur'
    }
  ]
}

async function onSubmit() {
  await formRef.value.validate()
  loading.value = true
  try {
    await request.post('/api/settings/password', {
      username: form.username,
      oldPassword: auth.needChangePassword ? '' : form.oldPassword,
      newPassword: form.newPassword
    })
    auth.username = form.username
    auth.needChangePassword = false
    ElMessage.success('账号密码修改成功')
    form.oldPassword = ''
    form.newPassword = ''
    form.confirmPassword = ''
    router.push('/dashboard')
  } catch {
    // 错误提示已由响应拦截器统一处理
  } finally {
    loading.value = false
  }
}

// ---------- 消息通知 ----------
const webhook = reactive({ enabled: false, type: 'serverchan', key: '', chatId: '', url: '', events: [] })
const webhookLoading = ref(false)
const webhookSaving = ref(false)
const webhookTesting = ref(false)

async function loadWebhook() {
  webhookLoading.value = true
  try {
    const res = await request.get('/api/settings/webhook')
    const d = res.data || {}
    webhook.enabled = !!d.enabled
    webhook.type = d.type || 'serverchan'
    webhook.key = d.key || ''
    webhook.chatId = d.chatId || ''
    webhook.url = d.url || ''
    webhook.events = d.events || []
  } catch {
    // 拦截器已提示
  } finally {
    webhookLoading.value = false
  }
}

async function saveWebhook() {
  webhookSaving.value = true
  try {
    await request.put('/api/settings/webhook', {
      enabled: webhook.enabled,
      type: webhook.type,
      key: webhook.key,
      chatId: webhook.chatId,
      url: webhook.url,
      events: webhook.events
    })
    ElMessage.success('保存成功')
  } catch {
    // 拦截器已提示
  } finally {
    webhookSaving.value = false
  }
}

async function testWebhook() {
  webhookTesting.value = true
  try {
    await request.post('/api/settings/webhook/test')
    ElMessage.success('测试消息已发送，请检查接收端')
  } catch {
    // 拦截器已提示
  } finally {
    webhookTesting.value = false
  }
}

// ---------- 系统升级 ----------
const sysInfo = reactive({ version: '', goos: '', goarch: '', uptime: 0 })
const sysInfoLoading = ref(false)
const upgrade = reactive({
  checking: false,
  checked: false,
  hasUpdate: false,
  latest: '',
  releaseUrl: '',
  status: { state: '', version: '', error: '', note: '' }
})
let statusTimer = null

const upgradeBusy = computed(() =>
  ['downloading', 'installing', 'restarting'].includes(upgrade.status.state)
)

const upgradeStatusText = computed(() => {
  const map = {
    downloading: '下载中…',
    installing: '安装中…',
    restarting: '服务重启中…',
    done: `升级完成${upgrade.status.version ? '（' + upgrade.status.version + '）' : ''}`,
    failed: '升级失败'
  }
  return map[upgrade.status.state] || ''
})

const upgradeProgress = computed(() => {
  const map = { downloading: 40, installing: 70, restarting: 90, done: 100, failed: 100 }
  return map[upgrade.status.state] || 0
})

function formatUptime(sec) {
  if (!sec) return '-'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d} 天 ${h} 小时 ${m} 分钟`
  if (h > 0) return `${h} 小时 ${m} 分钟`
  return `${m} 分钟`
}

async function loadSystemInfo() {
  sysInfoLoading.value = true
  try {
    const res = await request.get('/api/system/info')
    Object.assign(sysInfo, res.data || {})
  } catch {
    // 拦截器已提示
  } finally {
    sysInfoLoading.value = false
  }
}

async function checkUpdate() {
  upgrade.checking = true
  try {
    const res = await request.get('/api/system/upgrade/check')
    const d = res.data || {}
    upgrade.checked = true
    upgrade.hasUpdate = !!d.hasUpdate
    upgrade.latest = d.latest || ''
    upgrade.releaseUrl = d.releaseUrl || ''
  } catch {
    // 拦截器已提示
  } finally {
    upgrade.checking = false
  }
}

async function startUpgrade() {
  try {
    await request.post('/api/system/upgrade', { version: upgrade.latest })
    ElMessage.info('升级已开始，请勿关闭页面')
    pollUpgradeStatus()
  } catch {
    // 拦截器已提示
  }
}

async function pollUpgradeStatus() {
  stopStatusPoll()
  await fetchUpgradeStatus()
  if (['downloading', 'installing', 'restarting'].includes(upgrade.status.state)) {
    statusTimer = setInterval(fetchUpgradeStatus, 3000)
  }
}

async function fetchUpgradeStatus() {
  try {
    const res = await request.get('/api/system/upgrade/status')
    upgrade.status = res.data || { state: '', version: '', error: '', note: '' }
    if (!['downloading', 'installing', 'restarting'].includes(upgrade.status.state)) {
      stopStatusPoll()
    }
  } catch {
    // 轮询失败（服务重启中短暂连不上）保持轮询
  }
}

function stopStatusPoll() {
  if (statusTimer) {
    clearInterval(statusTimer)
    statusTimer = null
  }
}

// ---------- 防火墙状态 ----------
const firewall = reactive({ openwrt: false, rules: [], loading: false })

async function loadFirewall() {
  firewall.loading = true
  try {
    const res = await request.get('/api/firewall/status')
    firewall.openwrt = !!res.data?.openwrt
    firewall.rules = res.data?.rules || []
  } catch {
    // 拦截器已提示
  } finally {
    firewall.loading = false
  }
}

onMounted(() => {
  loadWebhook()
  loadSystemInfo()
  loadFirewall()
  fetchUpgradeStatus()
})
onUnmounted(stopStatusPoll)
</script>

<style scoped>
.settings-page {
  max-width: 640px;
}
.settings-card {
  margin-bottom: 16px;
}
.tip {
  margin-bottom: 20px;
}
.settings-form {
  margin-top: 8px;
}
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.sys-info {
  margin-bottom: 16px;
}
.upgrade-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.release-link {
  font-size: 13px;
}
.upgrade-status {
  margin-top: 8px;
}
.upgrade-status-text {
  margin-top: 6px;
  color: #606266;
  font-size: 13px;
}
</style>
