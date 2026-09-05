<template>
  <div>
    <!-- DNS 服务商凭据 -->
    <el-card class="section-card">
      <template #header>
        <div class="card-header">
          <span>{{ $t('ddns.providers') }}</span>
          <el-button type="primary" size="small" @click="openProviderDialog()">{{ $t('ddns.addProvider') }}</el-button>
        </div>
      </template>
      <el-table :data="providers" v-loading="loadingProviders" size="default">
        <el-table-column prop="remark" :label="$t('ddns.colRemark')" min-width="120">
          <template #default="{ row }">{{ row.remark || '-' }}</template>
        </el-table-column>
        <el-table-column prop="type" :label="$t('ddns.colProvider')" width="120">
          <template #default="{ row }">
            <el-tag>{{ providerTypeName(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="Key / Token" min-width="160"><template #default="{row}"><el-tag :type="row.keyConfigured ? 'success':'warning'">{{ row.keyConfigured ? $t('common.keyConfigured') : $t('common.notConfigured') }}</el-tag></template></el-table-column>
        <el-table-column :label="$t('common.actions')" width="160">
          <template #default="{ row }">
            <el-button link type="primary" @click="openProviderDialog(row)">{{ $t('common.edit') }}</el-button>
            <el-popconfirm :title="$t('ddns.deleteProviderConfirm')" @confirm="deleteProvider(row)">
              <template #reference>
                <el-button link type="danger">{{ $t('common.delete') }}</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
        <template #empty><el-empty :description="$t('ddns.emptyProviders')" :image-size="60" /></template>
      </el-table>
    </el-card>

    <!-- DDNS 任务 -->
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('ddns.tasks') }}</span>
          <el-button type="primary" size="small" @click="openTaskDialog()">{{ $t('ddns.addTask') }}</el-button>
        </div>
      </template>
      <el-table :data="tasks" v-loading="loadingTasks">
        <el-table-column prop="name" :label="$t('common.name')" min-width="110" />
        <el-table-column :label="$t('ddns.colDomains')" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">{{ (row.domains || []).join(', ') }}</template>
        </el-table-column>
        <el-table-column prop="ipType" :label="$t('ddns.colIpType')" width="80" />
        <el-table-column :label="$t('ddns.colProvider')" width="110">
          <template #default="{ row }">{{ providerNameOf(row.providerId) }}</template>
        </el-table-column>
        <el-table-column :label="$t('ddns.colCurrentIp')" min-width="150">
          <template #default="{ row }">
            <template v-if="row.status?.ip">
              {{ row.status.ip }}<template v-if="row.status.interface">（{{ row.status.interface }}）</template>
            </template>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column :label="$t('ddns.colStatus')" min-width="150">
          <template #default="{ row }">
            <template v-if="row.status">
              <el-tag :type="row.status.success ? 'success' : 'danger'" size="small">
                {{ row.status.success ? $t('common.success') : $t('common.failed') }}
              </el-tag>
              <el-tooltip v-if="row.status.message" :content="row.status.message" placement="top">
                <span class="status-msg">{{ row.status.message }}</span>
              </el-tooltip>
            </template>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column :label="$t('ddns.colEnabled')" width="70">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" @change="toggleTask(row)" />
          </template>
        </el-table-column>
        <el-table-column :label="$t('ddns.colActions')" width="200" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" :loading="row._running" @click="runTask(row)">{{ $t('ddns.runOnce') }}</el-button>
            <el-button link type="primary" @click="openTaskDialog(row)">{{ $t('common.edit') }}</el-button>
            <el-popconfirm :title="$t('ddns.deleteTaskConfirm')" @confirm="deleteTask(row)">
              <template #reference>
                <el-button link type="danger">{{ $t('common.delete') }}</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
        <template #empty><el-empty :description="$t('ddns.emptyTasks')" :image-size="60" /></template>
      </el-table>
    </el-card>

    <!-- 凭据对话框 -->
    <el-dialog
      v-model="providerDialog.visible"
      :title="providerDialog.isEdit ? $t('ddns.editProvider') : $t('ddns.addProviderTitle')"
      width="480px"
      destroy-on-close
    >
      <el-form ref="providerFormRef" :model="providerDialog.form" :rules="providerRules" label-width="110px">
        <el-form-item :label="$t('ddns.colProvider')" prop="type">
          <el-select v-model="providerDialog.form.type" :disabled="providerDialog.isEdit" style="width: 100%">
            <el-option :label="$t('ddns.providerTypes.aliyun') + ' (aliyun)'" value="aliyun" />
            <el-option label="Cloudflare" value="cloudflare" />
            <el-option label="DNSPod (dnspod)" value="dnspod" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('ddns.remark')" prop="remark">
          <el-input v-model="providerDialog.form.remark" :placeholder="$t('ddns.remarkPlaceholder')" />
        </el-form-item>
        <el-form-item :label="keyLabel" prop="key">
          <el-input v-model="providerDialog.form.key" type="password" show-password :placeholder="providerDialog.isEdit && providerDialog.form.keyConfigured ? $t('common.keepEmpty') : keyPlaceholder" />
        </el-form-item>
        <el-form-item v-if="providerDialog.form.type !== 'cloudflare'" :label="secretLabel" prop="secret">
          <el-input v-model="providerDialog.form.secret" type="password" show-password :placeholder="providerDialog.isEdit && providerDialog.form.secretConfigured ? $t('common.keepEmpty') : secretPlaceholder" />
        </el-form-item>
    <el-form-item :label="$t('ddns.customEndpoint')">
      <el-input v-model="providerDialog.form.endpoint" :placeholder="$t('ddns.endpointPlaceholder')" />
    </el-form-item>
        <el-form-item :label="$t('ddns.testDomain')">
          <el-input v-model="providerDialog.testDomain" :placeholder="$t('ddns.testDomainPlaceholder')" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :loading="providerDialog.testing" @click="testProvider">{{ $t('ddns.test') }}</el-button>
        <el-button @click="providerDialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="providerDialog.saving" @click="saveProvider">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 任务对话框 -->
    <el-dialog
      v-model="taskDialog.visible"
      :title="taskDialog.isEdit ? $t('ddns.editTask') : $t('ddns.addTaskTitle')"
      width="560px"
      destroy-on-close
    >
      <el-form ref="taskFormRef" :model="taskDialog.form" :rules="taskRules" label-width="110px">
        <el-form-item :label="$t('ddns.taskName')" prop="name">
          <el-input v-model="taskDialog.form.name" :placeholder="$t('ddns.taskNamePlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('ddns.colDomains')" prop="domainsText">
          <el-input v-model="taskDialog.form.domainsText" :placeholder="$t('ddns.taskDomainsPlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('ddns.colIpType')" prop="ipType">
          <el-select v-model="taskDialog.form.ipType" style="width: 100%">
            <el-option label="IPv4" value="ipv4" />
            <el-option label="IPv6" value="ipv6" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('ddns.providerCred')" prop="providerId">
          <el-select v-model="taskDialog.form.providerId" style="width: 100%" :placeholder="$t('ddns.providerCredPlaceholder')">
            <el-option
              v-for="p in providers"
              :key="p.id"
              :label="(p.remark || p.id) + '（' + providerTypeName(p.type) + '）'"
              :value="p.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('ddns.ipSource')" prop="ipSource">
          <el-select v-model="taskDialog.form.ipSource" style="width: 100%">
            <el-option :label="$t('ddns.ipSourceInterface')" value="interface" />
            <el-option :label="$t('ddns.ipSourceApi')" value="api" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="taskDialog.form.ipSource === 'interface'" :label="$t('ddns.interface')" prop="interface">
          <el-select
            v-model="taskDialog.form.interface"
            filterable
            allow-create
            default-first-option
            style="width: 100%"
            :placeholder="$t('ddns.interfacePlaceholder')"
          >
            <el-option :label="$t('ddns.autoOption')" value="auto" />
            <el-option v-for="n in interfaces" :key="n" :label="n" :value="n" />
          </el-select>
          <div v-if="wanInterface" class="form-tip-block">{{ $t('ddns.autoDetected', { name: wanInterface }) }}</div>
        </el-form-item>
        <el-form-item v-if="taskDialog.form.ipSource === 'api'" :label="$t('ddns.apiUrl')" prop="apiUrl">
          <el-input v-model="taskDialog.form.apiUrl" :placeholder="$t('ddns.apiUrlPlaceholder')" />
        </el-form-item>
        <el-form-item label=" " class="preview-item">
          <el-button size="small" :loading="preview.loading" @click="previewIP">{{ $t('ddns.fetchIp') }}</el-button>
          <span v-if="preview.result" class="preview-result">{{ preview.result }}</span>
        </el-form-item>
        <el-form-item :label="$t('ddns.interval')" prop="interval">
          <el-input-number v-model="taskDialog.form.interval" :min="10" :max="86400" />
        </el-form-item>
        <el-form-item label="TTL" prop="ttl">
          <el-input-number v-model="taskDialog.form.ttl" :min="0" :max="86400" />
          <span class="form-tip">{{ $t('ddns.ttlTip') }}</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="taskDialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="taskDialog.saving" @click="saveTask">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'

const { t } = useI18n()

const providers = ref([])
const tasks = ref([])
const loadingProviders = ref(false)
const loadingTasks = ref(false)

function providerTypeName(type) {
  return ['aliyun', 'cloudflare', 'dnspod'].includes(type) ? t(`ddns.providerTypes.${type}`) : type
}
function providerNameOf(id) {
  const p = providers.value.find((x) => x.id === id)
  return p ? (p.remark || providerTypeName(p.type)) : id || '-'
}

// ---------- 凭据 ----------
const providerFormRef = ref()
const providerDialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  testing: false,
  testDomain: '',
  form: { id: '', type: 'aliyun', remark: '', key: '', secret: '', endpoint: '' }
})

const keyLabel = computed(() => {
  const t = providerDialog.form.type
  if (t === 'aliyun') return 'AccessKey ID'
  if (t === 'cloudflare') return 'API Token'
  return 'Token ID'
})
const keyPlaceholder = computed(() => {
  const t2 = providerDialog.form.type
  if (t2 === 'cloudflare') return t('ddns.cfTokenPlaceholder')
  if (t2 === 'dnspod') return t('ddns.dnspodTokenIdPlaceholder')
  return t('ddns.aliyunKeyIdPlaceholder')
})
const secretLabel = computed(() => (providerDialog.form.type === 'dnspod' ? 'Token' : 'AccessKey Secret'))
const secretPlaceholder = computed(() =>
  providerDialog.form.type === 'dnspod' ? t('ddns.dnspodTokenPlaceholder') : t('ddns.aliyunSecretPlaceholder')
)

const providerRules = computed(() => ({
  type: [{ required: true, message: t('ddns.providerRequired'), trigger: 'change' }],
  key: [{ validator: (_,v,done) => (v || (providerDialog.isEdit && providerDialog.form.keyConfigured)) ? done() : done(new Error(t('ddns.keyRequired'))), trigger: 'blur' }],
  secret: [{ validator: (_,v,done) => (providerDialog.form.type === 'cloudflare' || v || (providerDialog.isEdit && providerDialog.form.secretConfigured)) ? done() : done(new Error(t('ddns.secretRequired'))), trigger: 'blur' }]
}))

function openProviderDialog(row) {
  providerDialog.isEdit = !!row
  providerDialog.testDomain = ''
  providerDialog.form = row
  ? { id: row.id, type: row.type, remark: row.remark || '', key: '', secret: '', endpoint: row.endpoint || '', keyConfigured: !!row.keyConfigured, secretConfigured: !!row.secretConfigured, endpointConfigured: !!row.endpoint }
  : { id: '', type: 'aliyun', remark: '', key: '', secret: '', endpoint: '' }
  providerDialog.visible = true
}

async function testProvider() {
  if (!providerDialog.testDomain) {
    ElMessage.warning(t('ddns.testDomainRequired'))
    return
  }
  providerDialog.testing = true
  try {
    const res = await request.post('/api/providers/test', {
    id: providerDialog.form.id,
      type: providerDialog.form.type,
      key: providerDialog.form.key,
      secret: providerDialog.form.secret,
    endpoint: providerDialog.form.endpoint,
      domain: providerDialog.testDomain
    })
    ElMessage.success(res.data?.message || t('ddns.credValid'))
  } catch {
    // 拦截器已提示
  } finally {
    providerDialog.testing = false
  }
}

async function saveProvider() {
  await providerFormRef.value.validate()
  providerDialog.saving = true
  try {
    const body = {
      type: providerDialog.form.type,
      remark: providerDialog.form.remark,
      key: providerDialog.form.key,
    secret: providerDialog.form.secret,
    endpoint: providerDialog.form.endpoint,
    clearEndpoint: providerDialog.isEdit && providerDialog.form.endpointConfigured && !providerDialog.form.endpoint
    }
    if (providerDialog.isEdit) {
      await request.put(`/api/providers/${providerDialog.form.id}`, body)
    } else {
      await request.post('/api/providers', body)
    }
    ElMessage.success(t('common.saveSuccess'))
    providerDialog.visible = false
    loadProviders()
  } catch {
    // 拦截器已提示
  } finally {
    providerDialog.saving = false
  }
}

async function deleteProvider(row) {
  try {
    await request.delete(`/api/providers/${row.id}`)
    ElMessage.success(t('common.deleted'))
    loadProviders()
  } catch {
    // 拦截器已提示
  }
}

async function loadProviders() {
  loadingProviders.value = true
  try {
    const res = await request.get('/api/providers')
    providers.value = res.data || []
  } catch {
    // 拦截器已提示
  } finally {
    loadingProviders.value = false
  }
}

// ---------- 网卡与 IP 预览 ----------
const interfaces = ref([])
const wanInterface = ref('')
const preview = reactive({ loading: false, result: '' })

async function loadInterfaces() {
  try {
    const res = await request.get('/api/ddns/interfaces')
    interfaces.value = res.data?.interfaces || []
    wanInterface.value = res.data?.wan || ''
  } catch {
    // 拦截器已提示
  }
}

async function previewIP() {
  preview.loading = true
  preview.result = ''
  try {
    const f = taskDialog.form
    const res = await request.post('/api/ddns/preview-ip', {
      ipType: f.ipType,
      ipSource: f.ipSource,
      interface: f.ipSource === 'interface' ? f.interface : '',
      apiUrl: f.ipSource === 'api' ? f.apiUrl : ''
    })
    const ip = res.data?.ip || ''
    const iface = res.data?.interface || ''
    preview.result = iface ? `${iface}: ${ip}` : ip
  } catch {
    // 拦截器已提示
  } finally {
    preview.loading = false
  }
}

// ---------- 任务 ----------
const taskFormRef = ref()
const taskDialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  form: {
    id: '', name: '', domainsText: '', ipType: 'ipv4', providerId: '',
    ipSource: 'interface', interface: '', apiUrl: '', interval: 300, ttl: 600, enabled: true
  }
})

const taskRules = computed(() => ({
  name: [{ required: true, message: t('ddns.taskNameRequired'), trigger: 'blur' }],
  domainsText: [{ required: true, message: t('ddns.domainsRequired'), trigger: 'blur' }],
  providerId: [{ required: true, message: t('ddns.providerCredRequired'), trigger: 'change' }],
  interface: [{ required: true, message: t('ddns.interfaceRequired'), trigger: 'blur' }],
  apiUrl: [{ required: true, message: t('ddns.apiUrlRequired'), trigger: 'blur' }]
}))

function openTaskDialog(row) {
  preview.result = ''
  loadInterfaces()
  taskDialog.isEdit = !!row
  taskDialog.form = row
    ? {
        id: row.id,
        name: row.name,
        domainsText: (row.domains || []).join(', '),
        ipType: row.ipType || 'ipv4',
        providerId: row.providerId,
        ipSource: row.ipSource || 'interface',
        interface: row.interface || '',
        apiUrl: row.apiUrl || '',
        interval: row.interval || 300,
        ttl: row.ttl || 0,
        enabled: row.enabled
      }
    : {
        id: '', name: '', domainsText: '', ipType: 'ipv4', providerId: '',
        ipSource: 'interface', interface: 'auto', apiUrl: '', interval: 300, ttl: 0, enabled: true
      }
  taskDialog.visible = true
}

async function saveTask() {
  await taskFormRef.value.validate()
  const f = taskDialog.form
  const body = {
    name: f.name,
    enabled: f.enabled,
    providerId: f.providerId,
    domains: f.domainsText.split(',').map((s) => s.trim()).filter(Boolean),
    ipType: f.ipType,
    ipSource: f.ipSource,
    interface: f.ipSource === 'interface' ? f.interface : '',
    apiUrl: f.ipSource === 'api' ? f.apiUrl : '',
    interval: f.interval,
    ttl: f.ttl
  }
  taskDialog.saving = true
  try {
    if (taskDialog.isEdit) {
      await request.put(`/api/ddns/tasks/${f.id}`, body)
    } else {
      await request.post('/api/ddns/tasks', body)
    }
    ElMessage.success(t('common.saveSuccess'))
    taskDialog.visible = false
    loadTasks()
  } catch {
    // 拦截器已提示
  } finally {
    taskDialog.saving = false
  }
}

async function toggleTask(row) {
  try {
    await request.post(`/api/ddns/tasks/${row.id}/toggle`)
    loadTasks()
  } catch {
    // 拦截器已提示；刷新真实状态，避免开关停留在错误位置
    loadTasks()
  }
}

async function runTask(row) {
  row._running = true
  try {
    const res = await request.post(`/api/ddns/tasks/${row.id}/run`, {}, { timeout: 120000 })
    const st = res.data
    if (st && st.success) {
      ElMessage.success(t('ddns.runSuccess', { ip: st.ip || '-' }))
    } else {
      ElMessage.warning(st?.message || t('ddns.runDone'))
    }
    loadTasks()
  } catch {
    // 拦截器已提示
  } finally {
    row._running = false
  }
}

async function deleteTask(row) {
  try {
    await request.delete(`/api/ddns/tasks/${row.id}`)
    ElMessage.success(t('common.deleted'))
    loadTasks()
  } catch {
    // 拦截器已提示
  }
}

async function loadTasks() {
  loadingTasks.value = true
  try {
    const res = await request.get('/api/ddns/tasks')
    tasks.value = (res.data || []).map((t) => ({ ...t, _running: false }))
  } catch {
    // 拦截器已提示
  } finally {
    loadingTasks.value = false
  }
}

onMounted(() => {
  loadProviders()
  loadTasks()
})
</script>

<style scoped>
.section-card {
  margin-bottom: 16px;
}
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.status-msg {
  margin-left: 6px;
  color: var(--ap-muted);
  font-size: 12px;
  max-width: 100px;
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
.form-tip-block {
  color: var(--ap-muted);
  font-size: 12px;
  line-height: 1.4;
  margin-top: 4px;
}
.preview-result {
  margin-left: 10px;
  color: #67c23a;
  font-size: 13px;
  font-family: Menlo, Consolas, monospace;
}
</style>
