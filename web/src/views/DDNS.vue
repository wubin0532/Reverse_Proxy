<template>
  <div>
    <!-- DNS 服务商凭据 -->
    <el-card class="section-card">
      <template #header>
        <div class="card-header">
          <span>DNS 服务商凭据</span>
          <el-button type="primary" size="small" @click="openProviderDialog()">新增凭据</el-button>
        </div>
      </template>
      <el-table :data="providers" v-loading="loadingProviders" size="default">
        <el-table-column prop="remark" label="备注" min-width="120">
          <template #default="{ row }">{{ row.remark || '-' }}</template>
        </el-table-column>
        <el-table-column prop="type" label="服务商" width="120">
          <template #default="{ row }">
            <el-tag>{{ providerTypeName(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="Key / Token" min-width="160"><template #default="{row}"><el-tag :type="row.keyConfigured ? 'success':'warning'">{{ row.keyConfigured ? '已安全配置':'未配置' }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="160">
          <template #default="{ row }">
            <el-button link type="primary" @click="openProviderDialog(row)">编辑</el-button>
            <el-popconfirm title="确定删除该凭据？" @confirm="deleteProvider(row)">
              <template #reference>
                <el-button link type="danger">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
        <template #empty><el-empty description="暂无凭据，请先新增" :image-size="60" /></template>
      </el-table>
    </el-card>

    <!-- DDNS 任务 -->
    <el-card>
      <template #header>
        <div class="card-header">
          <span>DDNS 任务</span>
          <el-button type="primary" size="small" @click="openTaskDialog()">新增任务</el-button>
        </div>
      </template>
      <el-table :data="tasks" v-loading="loadingTasks">
        <el-table-column prop="name" label="名称" min-width="110" />
        <el-table-column label="域名" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">{{ (row.domains || []).join(', ') }}</template>
        </el-table-column>
        <el-table-column prop="ipType" label="IP 类型" width="80" />
        <el-table-column label="服务商" width="110">
          <template #default="{ row }">{{ providerNameOf(row.providerId) }}</template>
        </el-table-column>
        <el-table-column label="当前 IP" min-width="150">
          <template #default="{ row }">
            <template v-if="row.status?.ip">
              {{ row.status.ip }}<template v-if="row.status.interface">（{{ row.status.interface }}）</template>
            </template>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" min-width="150">
          <template #default="{ row }">
            <template v-if="row.status">
              <el-tag :type="row.status.success ? 'success' : 'danger'" size="small">
                {{ row.status.success ? '成功' : '失败' }}
              </el-tag>
              <el-tooltip v-if="row.status.message" :content="row.status.message" placement="top">
                <span class="status-msg">{{ row.status.message }}</span>
              </el-tooltip>
            </template>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="70">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" @change="toggleTask(row)" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" :loading="row._running" @click="runTask(row)">执行一次</el-button>
            <el-button link type="primary" @click="openTaskDialog(row)">编辑</el-button>
            <el-popconfirm title="确定删除该任务？" @confirm="deleteTask(row)">
              <template #reference>
                <el-button link type="danger">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
        <template #empty><el-empty description="暂无 DDNS 任务" :image-size="60" /></template>
      </el-table>
    </el-card>

    <!-- 凭据对话框 -->
    <el-dialog
      v-model="providerDialog.visible"
      :title="providerDialog.isEdit ? '编辑凭据' : '新增凭据'"
      width="480px"
      destroy-on-close
    >
      <el-form ref="providerFormRef" :model="providerDialog.form" :rules="providerRules" label-width="110px">
        <el-form-item label="服务商" prop="type">
          <el-select v-model="providerDialog.form.type" :disabled="providerDialog.isEdit" style="width: 100%">
            <el-option label="阿里云 (aliyun)" value="aliyun" />
            <el-option label="Cloudflare" value="cloudflare" />
            <el-option label="DNSPod (dnspod)" value="dnspod" />
          </el-select>
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="providerDialog.form.remark" placeholder="可选，便于区分多个凭据" />
        </el-form-item>
        <el-form-item :label="keyLabel" prop="key">
          <el-input v-model="providerDialog.form.key" type="password" show-password :placeholder="providerDialog.isEdit && providerDialog.form.keyConfigured ? '已配置，留空保持不变' : keyPlaceholder" />
        </el-form-item>
        <el-form-item v-if="providerDialog.form.type !== 'cloudflare'" :label="secretLabel" prop="secret">
          <el-input v-model="providerDialog.form.secret" type="password" show-password :placeholder="providerDialog.isEdit && providerDialog.form.secretConfigured ? '已配置，留空保持不变' : secretPlaceholder" />
        </el-form-item>
		<el-form-item label="自定义端点">
		  <el-input v-model="providerDialog.form.endpoint" placeholder="可选；生产环境建议保持默认" />
		</el-form-item>
        <el-form-item label="测试域名">
          <el-input v-model="providerDialog.testDomain" placeholder="如 home.example.com，用于测试凭据" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :loading="providerDialog.testing" @click="testProvider">测试</el-button>
        <el-button @click="providerDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="providerDialog.saving" @click="saveProvider">保存</el-button>
      </template>
    </el-dialog>

    <!-- 任务对话框 -->
    <el-dialog
      v-model="taskDialog.visible"
      :title="taskDialog.isEdit ? '编辑任务' : '新增任务'"
      width="560px"
      destroy-on-close
    >
      <el-form ref="taskFormRef" :model="taskDialog.form" :rules="taskRules" label-width="110px">
        <el-form-item label="任务名称" prop="name">
          <el-input v-model="taskDialog.form.name" placeholder="如 家庭宽带" />
        </el-form-item>
        <el-form-item label="域名" prop="domainsText">
          <el-input v-model="taskDialog.form.domainsText" placeholder="多个域名用英文逗号分隔，如 home.example.com, nas.example.com" />
        </el-form-item>
        <el-form-item label="IP 类型" prop="ipType">
          <el-select v-model="taskDialog.form.ipType" style="width: 100%">
            <el-option label="IPv4" value="ipv4" />
            <el-option label="IPv6" value="ipv6" />
          </el-select>
        </el-form-item>
        <el-form-item label="服务商凭据" prop="providerId">
          <el-select v-model="taskDialog.form.providerId" style="width: 100%" placeholder="选择上方已添加的凭据">
            <el-option
              v-for="p in providers"
              :key="p.id"
              :label="(p.remark || p.id) + '（' + providerTypeName(p.type) + '）'"
              :value="p.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="IP 来源" prop="ipSource">
          <el-select v-model="taskDialog.form.ipSource" style="width: 100%">
            <el-option label="网卡接口 (interface)" value="interface" />
            <el-option label="API 查询 (api)" value="api" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="taskDialog.form.ipSource === 'interface'" label="网卡名" prop="interface">
          <el-select
            v-model="taskDialog.form.interface"
            filterable
            allow-create
            default-first-option
            style="width: 100%"
            placeholder="选择网卡，或使用 auto 自动识别"
          >
            <el-option label="auto（自动识别 WAN 口）" value="auto" />
            <el-option v-for="n in interfaces" :key="n" :label="n" :value="n" />
          </el-select>
          <div v-if="wanInterface" class="form-tip-block">当前自动识别结果：{{ wanInterface }}</div>
        </el-form-item>
        <el-form-item v-if="taskDialog.form.ipSource === 'api'" label="IP 查询地址" prop="apiUrl">
          <el-input v-model="taskDialog.form.apiUrl" placeholder="返回纯文本 IP 的地址，如 https://4.ipw.cn" />
        </el-form-item>
        <el-form-item label=" " class="preview-item">
          <el-button size="small" :loading="preview.loading" @click="previewIP">获取当前 IP</el-button>
          <span v-if="preview.result" class="preview-result">{{ preview.result }}</span>
        </el-form-item>
        <el-form-item label="检测间隔(秒)" prop="interval">
          <el-input-number v-model="taskDialog.form.interval" :min="10" :max="86400" />
        </el-form-item>
        <el-form-item label="TTL" prop="ttl">
          <el-input-number v-model="taskDialog.form.ttl" :min="0" :max="86400" />
          <span class="form-tip">0 表示使用服务商默认</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="taskDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="taskDialog.saving" @click="saveTask">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import request from '../api'

const providers = ref([])
const tasks = ref([])
const loadingProviders = ref(false)
const loadingTasks = ref(false)

const providerTypeNames = { aliyun: '阿里云', cloudflare: 'Cloudflare', dnspod: 'DNSPod' }
function providerTypeName(t) {
  return providerTypeNames[t] || t
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
  const t = providerDialog.form.type
  if (t === 'cloudflare') return 'Cloudflare API Token（只需填写此项）'
  if (t === 'dnspod') return 'DNSPod Token ID'
  return '阿里云 AccessKey ID'
})
const secretLabel = computed(() => (providerDialog.form.type === 'dnspod' ? 'Token' : 'AccessKey Secret'))
const secretPlaceholder = computed(() =>
  providerDialog.form.type === 'dnspod' ? 'DNSPod Token' : '阿里云 AccessKey Secret'
)

const providerRules = {
  type: [{ required: true, message: '请选择服务商', trigger: 'change' }],
  key: [{ validator: (_,v,done) => (v || (providerDialog.isEdit && providerDialog.form.keyConfigured)) ? done() : done(new Error('Key 不能为空')), trigger: 'blur' }],
  secret: [{ validator: (_,v,done) => (providerDialog.form.type === 'cloudflare' || v || (providerDialog.isEdit && providerDialog.form.secretConfigured)) ? done() : done(new Error('Secret 不能为空')), trigger: 'blur' }]
}

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
    ElMessage.warning('请先填写测试域名')
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
    ElMessage.success(res.data?.message || '凭据有效')
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
    ElMessage.success('保存成功')
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
    ElMessage.success('已删除')
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

const taskRules = {
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
  domainsText: [{ required: true, message: '请输入域名', trigger: 'blur' }],
  providerId: [{ required: true, message: '请选择服务商凭据', trigger: 'change' }],
  interface: [{ required: true, message: '请输入网卡名', trigger: 'blur' }],
  apiUrl: [{ required: true, message: '请输入 IP 查询地址', trigger: 'blur' }]
}

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
    ElMessage.success('保存成功')
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
    // 拦截器已提示
  }
}

async function runTask(row) {
  row._running = true
  try {
    const res = await request.post(`/api/ddns/tasks/${row.id}/run`)
    const st = res.data
    if (st && st.success) {
      ElMessage.success(`执行成功，当前 IP: ${st.ip || '-'}`)
    } else {
      ElMessage.warning(st?.message || '执行完成')
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
    ElMessage.success('已删除')
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
  color: #999;
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
  color: #999;
  font-size: 12px;
}
.form-tip-block {
  color: #999;
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
