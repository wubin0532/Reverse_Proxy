<template>
  <el-card>
    <template #header>
      <div class="card-header">
        <span>证书管理</span>
        <el-button type="primary" size="small" @click="openDialog()">新增证书</el-button>
      </div>
    </template>
    <el-table :data="certs" v-loading="loading">
      <el-table-column prop="name" label="名称" min-width="120" />
      <el-table-column label="域名" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">{{ (row.domains || []).join(', ') }}</template>
      </el-table-column>
      <el-table-column label="到期时间" min-width="160">
        <template #default="{ row }">
          {{ row.notAfter ? formatTime(row.notAfter) : '-' }}
        </template>
      </el-table-column>
      <el-table-column label="状态" width="130">
        <template #default="{ row }">
          <el-tag v-if="row.obtaining" type="warning" size="small">
            <el-icon class="is-loading"><Loading /></el-icon>
            申请中
          </el-tag>
          <el-tag v-else :type="statusTagType(row.status)" size="small">
            {{ statusText(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="最近错误" min-width="140">
        <template #default="{ row }">
          <el-tooltip v-if="row.lastError" :content="row.lastError" placement="top">
            <span class="error-text">{{ row.lastError }}</span>
          </el-tooltip>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleCert(row)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="280" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" :disabled="row.obtaining" @click="obtainCert(row)">
            {{ row.status === 'ok' || row.status === 'expiring' ? '重签' : '申请' }}
          </el-button>
          <el-button link type="primary" @click="download(row, 'cert')">证书</el-button>
          <el-button link type="primary" @click="download(row, 'key')">私钥</el-button>
          <el-button link type="primary" @click="openDialog(row)">编辑</el-button>
          <el-popconfirm title="确定删除该证书及其文件？" @confirm="deleteCert(row)">
            <template #reference>
              <el-button link type="danger">删除</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
      <template #empty><el-empty description="暂无证书，点击右上角新增" :image-size="60" /></template>
    </el-table>

    <el-dialog
      v-model="dialog.visible"
      :title="dialog.isEdit ? '编辑证书' : '新增证书'"
      width="560px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="dialog.form" :rules="rules" label-width="110px">
        <el-form-item label="证书名称" prop="name">
          <el-input v-model="dialog.form.name" placeholder="如 主域名证书" />
        </el-form-item>
        <el-form-item label="域名" prop="domainsText">
          <el-input
            v-model="dialog.form.domainsText"
            type="textarea"
            :rows="2"
            placeholder="多个域名用英文逗号分隔，支持泛域名，如 example.com, *.example.com"
          />
        </el-form-item>
        <el-form-item label="DNS 凭据" prop="providerId">
          <el-select v-model="dialog.form.providerId" style="width: 100%" placeholder="在「动态域名」页添加的服务商凭据">
            <el-option
              v-for="p in providers"
              :key="p.id"
              :label="(p.remark || p.id) + '（' + p.type + '）'"
              :value="p.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="邮箱" prop="email">
          <el-input v-model="dialog.form.email" placeholder="ACME 账号邮箱，如 admin@example.com" />
        </el-form-item>
        <el-form-item label="CA 目录地址">
          <el-input
            v-model="dialog.form.caDirUrl"
            placeholder="留空使用 Let's Encrypt 生产环境；测试可填 https://acme-staging-v02.api.letsencrypt.org/directory"
          />
        </el-form-item>
        <el-form-item label="续签天数">
          <el-input-number v-model="dialog.form.renewDays" :min="1" :max="89" />
          <span class="form-tip">到期前多少天自动续签，默认 30</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Loading } from '@element-plus/icons-vue'
import request from '../api'

const certs = ref([])
const providers = ref([])
const loading = ref(false)
let pollTimer = null

function formatTime(t) {
  return new Date(t).toLocaleString('zh-CN', { hour12: false })
}

const statusTexts = {
  pending: '待申请',
  ok: '正常',
  expiring: '即将到期',
  expired: '已过期',
  error: '错误'
}
const statusTagTypes = {
  pending: 'info',
  ok: 'success',
  expiring: 'warning',
  expired: 'danger',
  error: 'danger'
}
function statusText(s) {
  return statusTexts[s] || s
}
function statusTagType(s) {
  return statusTagTypes[s] || 'info'
}

const formRef = ref()
const dialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  form: { id: '', name: '', domainsText: '', providerId: '', email: '', caDirUrl: '', renewDays: 30, enabled: true }
})

const rules = {
  name: [{ required: true, message: '请输入证书名称', trigger: 'blur' }],
  domainsText: [{ required: true, message: '请输入域名', trigger: 'blur' }],
  providerId: [{ required: true, message: '请选择 DNS 凭据', trigger: 'change' }],
  email: [{ required: true, message: '请输入邮箱', trigger: 'blur' }]
}

function openDialog(row) {
  dialog.isEdit = !!row
  dialog.form = row
    ? {
        id: row.id,
        name: row.name,
        domainsText: (row.domains || []).join(', '),
        providerId: row.providerId,
        email: row.email,
        caDirUrl: row.caDirUrl || '',
        renewDays: row.renewDays || 30,
        enabled: row.enabled
      }
    : { id: '', name: '', domainsText: '', providerId: '', email: '', caDirUrl: '', renewDays: 30, enabled: true }
  dialog.visible = true
}

async function save() {
  await formRef.value.validate()
  const f = dialog.form
  const body = {
    name: f.name,
    enabled: f.enabled,
    domains: f.domainsText.split(',').map((s) => s.trim()).filter(Boolean),
    providerId: f.providerId,
    email: f.email,
    caDirUrl: f.caDirUrl,
    renewDays: f.renewDays
  }
  dialog.saving = true
  try {
    if (dialog.isEdit) {
      await request.put(`/api/certs/${f.id}`, body)
    } else {
      await request.post('/api/certs', body)
    }
    ElMessage.success('保存成功')
    dialog.visible = false
    load()
  } catch {
    // 拦截器已提示
  } finally {
    dialog.saving = false
  }
}

async function toggleCert(row) {
  try {
    await request.post(`/api/certs/${row.id}/toggle`)
    load()
  } catch {
    // 拦截器已提示
  }
}

async function obtainCert(row) {
  try {
    await request.post(`/api/certs/${row.id}/obtain`)
    ElMessage.info('证书申请已在后台异步执行，请稍后刷新查看结果')
    setTimeout(load, 1500)
  } catch {
    // 拦截器已提示
  }
}

function download(row, part) {
  const a = document.createElement('a')
  a.href = `/api/certs/${row.id}/download?part=${part}`
  a.download = ''
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

async function deleteCert(row) {
  try {
    await request.delete(`/api/certs/${row.id}`)
    ElMessage.success('已删除')
    load()
  } catch {
    // 拦截器已提示
  }
}

async function load() {
  loading.value = true
  try {
    const res = await request.get('/api/certs')
    certs.value = res.data || []
    const anyObtaining = certs.value.some((c) => c.obtaining)
    if (anyObtaining && !pollTimer) {
      pollTimer = setInterval(load, 5000)
    } else if (!anyObtaining && pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

async function loadProviders() {
  try {
    const res = await request.get('/api/providers')
    providers.value = res.data || []
  } catch {
    // 拦截器已提示
  }
}

onMounted(() => {
  load()
  loadProviders()
})
onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
})
</script>

<style scoped>
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.error-text {
  color: #f56c6c;
  font-size: 12px;
  max-width: 130px;
  display: inline-block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
.form-tip {
  margin-left: 10px;
  color: #999;
  font-size: 12px;
}
</style>
