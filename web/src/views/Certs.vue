<template>
  <el-card>
    <template #header>
      <div class="card-header">
        <span>{{ $t('certs.title') }}</span>
        <el-button type="primary" size="small" @click="openDialog()">{{ $t('certs.add') }}</el-button>
      </div>
    </template>
    <el-table :data="certs" v-loading="loading">
      <el-table-column prop="name" :label="$t('certs.colName')" min-width="120" />
      <el-table-column :label="$t('certs.colDomains')" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">{{ (row.domains || []).join(', ') }}</template>
      </el-table-column>
      <el-table-column :label="$t('certs.colExpiry')" min-width="160">
        <template #default="{ row }">
          {{ row.notAfter ? formatTime(row.notAfter) : '-' }}
        </template>
      </el-table-column>
      <el-table-column :label="$t('certs.colStatus')" width="130">
        <template #default="{ row }">
          <el-tag v-if="row.obtaining" type="warning" size="small">
            <el-icon class="is-loading"><Loading /></el-icon>
            {{ $t('certs.obtaining') }}
          </el-tag>
          <el-tag v-else :type="statusTagType(row.status)" size="small">
            {{ statusText(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="$t('certs.colLastError')" min-width="140">
        <template #default="{ row }">
          <el-tooltip v-if="row.lastError" :content="row.lastError" placement="top">
            <span class="error-text">{{ row.lastError }}</span>
          </el-tooltip>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column :label="$t('certs.colEnabled')" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleCert(row)" />
        </template>
      </el-table-column>
      <el-table-column :label="$t('certs.colActions')" width="280" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" :disabled="row.obtaining" @click="obtainCert(row)">
            {{ row.status === 'ok' || row.status === 'expiring' ? $t('certs.reissue') : $t('certs.obtain') }}
          </el-button>
          <el-button link type="primary" @click="download(row, 'cert')">{{ $t('certs.cert') }}</el-button>
          <el-button link type="primary" @click="download(row, 'key')">{{ $t('certs.key') }}</el-button>
          <el-button link type="primary" @click="openDialog(row)">{{ $t('common.edit') }}</el-button>
          <el-popconfirm :title="$t('certs.deleteConfirm')" @confirm="deleteCert(row)">
            <template #reference>
              <el-button link type="danger">{{ $t('common.delete') }}</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
      <template #empty><el-empty :description="$t('certs.empty')" :image-size="60" /></template>
    </el-table>

    <el-dialog
      v-model="dialog.visible"
      :title="dialog.isEdit ? $t('certs.editTitle') : $t('certs.addTitle')"
      width="560px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="dialog.form" :rules="rules" label-width="110px">
        <el-form-item :label="$t('certs.certName')" prop="name">
          <el-input v-model="dialog.form.name" :placeholder="$t('certs.certNamePlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('certs.domains')" prop="domainsText">
          <el-input
            v-model="dialog.form.domainsText"
            type="textarea"
            :rows="2"
            :placeholder="$t('certs.domainsPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('certs.dnsCredential')" prop="providerId">
          <el-select v-model="dialog.form.providerId" style="width: 100%" :placeholder="$t('certs.providerPlaceholder')">
            <el-option
              v-for="p in providers"
              :key="p.id"
              :label="(p.remark || p.id) + '（' + p.type + '）'"
              :value="p.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('certs.email')" prop="email">
          <el-input v-model="dialog.form.email" :placeholder="$t('certs.emailPlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('certs.caDir')">
          <el-input
            v-model="dialog.form.caDirUrl"
            :placeholder="$t('certs.caDirPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('certs.renewDays')">
          <el-input-number v-model="dialog.form.renewDays" :min="1" :max="89" />
          <span class="form-tip">{{ $t('certs.renewTip') }}</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Loading } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'

const { t } = useI18n()

const certs = ref([])
const providers = ref([])
const loading = ref(false)
let pollTimer = null

const statusTagTypes = {
  pending: 'info',
  ok: 'success',
  expiring: 'warning',
  expired: 'danger',
  error: 'danger'
}
function statusText(s) {
  return ['pending', 'ok', 'expiring', 'expired', 'error'].includes(s) ? t(`certs.statusTexts.${s}`) : s
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

const rules = computed(() => ({
  name: [{ required: true, message: t('certs.nameRequired'), trigger: 'blur' }],
  domainsText: [{ required: true, message: t('certs.domainsRequired'), trigger: 'blur' }],
  providerId: [{ required: true, message: t('certs.providerRequired'), trigger: 'change' }],
  email: [{ required: true, message: t('certs.emailRequired'), trigger: 'blur' }]
}))

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
    ElMessage.success(t('common.saveSuccess'))
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
    // 拦截器已提示；刷新真实状态，避免开关停留在错误位置
    load()
  }
}

async function obtainCert(row) {
  try {
    await ElMessageBox.confirm(t('certs.obtainConfirm'), t('certs.obtainTitle'), { type: 'warning', confirmButtonText: t('certs.continue'), cancelButtonText: t('common.cancel') })
  } catch {
    return
  }
  try {
    await request.post(`/api/certs/${row.id}/obtain`, {}, { timeout: 120000 })
    ElMessage.info(t('certs.obtainStarted'))
    setTimeout(load, 1500)
  } catch {
    // 拦截器已提示
  }
}

async function download(row, part) {
  if (part === 'key') {
    try {
      await ElMessageBox.confirm(t('certs.keyDownloadConfirm'), t('certs.keyDownloadTitle'), { type: 'warning', confirmButtonText: t('certs.download'), cancelButtonText: t('common.cancel') })
    } catch {
      return
    }
  }
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
    ElMessage.success(t('common.deleted'))
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
  color: var(--el-color-danger);
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
  color: var(--ap-muted);
  font-size: 12px;
}
</style>
