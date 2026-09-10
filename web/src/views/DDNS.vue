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
        <el-table-column label="Key / Token" min-width="160"
          ><template #default="{ row }"
            ><el-tag :type="row.keyConfigured ? 'success' : 'warning'">{{
              row.keyConfigured ? $t('common.keyConfigured') : $t('common.notConfigured')
            }}</el-tag></template
          ></el-table-column
        >
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
            <el-button link type="primary" :loading="row._running" @click="runTask(row)">{{
              $t('ddns.runOnce')
            }}</el-button>
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
    <ProviderDialog ref="providerDialogRef" @saved="loadProviders" />

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
          <el-select
            v-model="taskDialog.form.providerId"
            style="width: 100%"
            :placeholder="$t('ddns.providerCredPlaceholder')"
          >
            <el-option
              v-for="p in ddnsProviders"
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
import { splitList } from '../utils/list'
import { useCrudDialog } from '../composables/useCrudDialog'
import ProviderDialog from '../components/ddns/ProviderDialog.vue'

const { t } = useI18n()

const providers = ref([])
const tasks = ref([])
const loadingProviders = ref(false)
const loadingTasks = ref(false)

const PROVIDER_TYPES = ['aliyun', 'cloudflare', 'dnspod', 'tencentcloud', 'huaweicloud', 'godaddy', 'route53']

function providerTypeName(type) {
  return PROVIDER_TYPES.includes(type) ? t(`ddns.providerTypes.${type}`) : type
}
function providerNameOf(id) {
  const p = providers.value.find((x) => x.id === id)
  return p ? p.remark || providerTypeName(p.type) : id || '-'
}
// godaddy / route53 仅供 ACME 证书申请，DDNS 任务不可选
const ddnsProviders = computed(() => providers.value.filter((p) => !['godaddy', 'route53'].includes(p.type)))

// ---------- 凭据 ----------
const providerDialogRef = ref()
function openProviderDialog(row) {
  providerDialogRef.value?.open(row)
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
const {
  formRef: taskFormRef,
  dialog: taskDialog,
  open: openTask,
  submit: saveTask
} = useCrudDialog({
  emptyForm: () => ({
    id: '',
    name: '',
    domainsText: '',
    ipType: 'ipv4',
    providerId: '',
    ipSource: 'interface',
    interface: 'auto',
    apiUrl: '',
    interval: 300,
    ttl: 0,
    enabled: true
  }),
  fromRow: (row) => ({
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
  }),
  onSubmit: async (f, d) => {
    const body = {
      name: f.name,
      enabled: f.enabled,
      providerId: f.providerId,
      domains: splitList(f.domainsText),
      ipType: f.ipType,
      ipSource: f.ipSource,
      interface: f.ipSource === 'interface' ? f.interface : '',
      apiUrl: f.ipSource === 'api' ? f.apiUrl : '',
      interval: f.interval,
      ttl: f.ttl
    }
    if (d.isEdit) {
      await request.put(`/api/ddns/tasks/${f.id}`, body)
    } else {
      await request.post('/api/ddns/tasks', body)
    }
  },
  onSaved: () => loadTasks()
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
  openTask(row)
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
  color: var(--ap-success);
  font-size: 13px;
  font-family: Menlo, Consolas, monospace;
}
</style>
