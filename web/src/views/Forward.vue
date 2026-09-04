<template>
  <el-card>
    <template #header>
      <div class="card-header">
        <span>端口转发规则</span>
        <el-button type="primary" size="small" @click="openDialog()">新增规则</el-button>
      </div>
    </template>
    <el-table :data="rules" v-loading="loading">
      <el-table-column prop="name" label="名称" min-width="120" />
      <el-table-column label="协议" width="100">
        <template #default="{ row }">
          <el-tag size="small">{{ protoText(row.proto) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="listen" label="监听地址" width="120" />
      <el-table-column label="目标地址" min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ (row.targets || []).join(', ') }}</template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleRule(row)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openLogs(row)">日志</el-button>
          <el-button link type="primary" @click="openDialog(row)">编辑</el-button>
          <el-popconfirm title="确定删除该规则？" @confirm="deleteRule(row)">
            <template #reference>
              <el-button link type="danger">删除</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
      <template #empty><el-empty description="暂无转发规则，点击右上角新增" :image-size="60" /></template>
    </el-table>

    <el-dialog
      v-model="dialog.visible"
      :title="dialog.isEdit ? '编辑规则' : '新增规则'"
      width="520px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="dialog.form" :rules="formRules" label-width="100px">
        <el-form-item label="规则名称" prop="name">
          <el-input v-model="dialog.form.name" placeholder="如 远程桌面" />
        </el-form-item>
        <el-form-item label="协议" prop="proto">
          <el-select v-model="dialog.form.proto" style="width: 100%">
            <el-option label="TCP" value="tcp" />
            <el-option label="UDP" value="udp" />
            <el-option label="TCP + UDP" value="tcpudp" />
          </el-select>
        </el-form-item>
        <el-form-item label="监听地址" prop="listen">
          <el-input v-model="dialog.form.listen" placeholder="如 :13389" />
        </el-form-item>
        <el-form-item label="目标地址" prop="targetsText">
          <el-input
            v-model="dialog.form.targetsText"
            placeholder="英文逗号分隔，如 192.168.1.10:3389, 192.168.1.11:3389"
          />
        </el-form-item>
        <el-form-item label="IP 访问控制">
          <el-select v-model="dialog.form.ipListMode" style="width: 100%">
            <el-option value="" label="不限制" />
            <el-option value="whitelist" label="白名单（仅允许列表内 IP）" />
            <el-option value="blacklist" label="黑名单（拦截列表内 IP）" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="dialog.form.ipListMode" label="IP 列表">
          <el-input
            v-model="dialog.ipListText"
            type="textarea"
            :rows="2"
            placeholder="英文逗号分隔，支持 CIDR，如 192.168.1.0/24, 10.0.0.5"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="logsDrawer.visible" :title="`转发日志 - ${logsDrawer.name}`" size="560px">
      <div class="log-toolbar">
        <el-button size="small" @click="loadLogs">刷新</el-button>
      </div>
      <div class="log-list">
        <el-empty v-if="!logsDrawer.logs.length" description="暂无日志" :image-size="60" />
        <div v-for="(line, i) in logsDrawer.logs" :key="i" class="log-line">{{ line }}</div>
      </div>
    </el-drawer>
  </el-card>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import request from '../api'

const rules = ref([])
const loading = ref(false)

const protoTexts = { tcp: 'TCP', udp: 'UDP', tcpudp: 'TCP+UDP' }
function protoText(p) {
  return protoTexts[p] || p
}

const formRef = ref()
const dialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  ipListText: '',
  form: { id: '', name: '', proto: 'tcp', listen: '', targetsText: '', ipListMode: '', enabled: true }
})

const formRules = {
  name: [{ required: true, message: '请输入规则名称', trigger: 'blur' }],
  listen: [{ required: true, message: '请输入监听地址', trigger: 'blur' }],
  targetsText: [{ required: true, message: '请输入目标地址', trigger: 'blur' }]
}

function openDialog(row) {
  dialog.isEdit = !!row
  dialog.form = row
    ? {
        id: row.id,
        name: row.name,
        proto: row.proto || 'tcp',
        listen: row.listen,
        targetsText: (row.targets || []).join(', '),
        ipListMode: row.ipListMode || '',
        enabled: row.enabled
      }
    : { id: '', name: '', proto: 'tcp', listen: '', targetsText: '', ipListMode: '', enabled: true }
  dialog.ipListText = row ? (row.ipList || []).join(', ') : ''
  dialog.visible = true
}

function splitList(text) {
  return (text || '').split(',').map((s) => s.trim()).filter(Boolean)
}

async function save() {
  await formRef.value.validate()
  const f = dialog.form
  const body = {
    name: f.name,
    enabled: f.enabled,
    proto: f.proto,
    listen: f.listen,
    targets: splitList(f.targetsText),
    ipListMode: f.ipListMode,
    ipList: f.ipListMode ? splitList(dialog.ipListText) : []
  }
  dialog.saving = true
  try {
    if (dialog.isEdit) {
      await request.put(`/api/forwards/${f.id}`, body)
    } else {
      await request.post('/api/forwards', body)
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

async function toggleRule(row) {
  try {
    await request.post(`/api/forwards/${row.id}/toggle`)
    load()
  } catch {
    // 拦截器已提示
  }
}

async function deleteRule(row) {
  try {
    await request.delete(`/api/forwards/${row.id}`)
    ElMessage.success('已删除')
    load()
  } catch {
    // 拦截器已提示
  }
}

const logsDrawer = reactive({ visible: false, name: '', id: '', logs: [] })

function openLogs(row) {
  logsDrawer.id = row.id
  logsDrawer.name = row.name
  logsDrawer.visible = true
  loadLogs()
}

async function loadLogs() {
  try {
    const res = await request.get(`/api/forwards/${logsDrawer.id}/logs`)
    logsDrawer.logs = (res.data || []).slice().reverse()
  } catch {
    logsDrawer.logs = []
  }
}

async function load() {
  loading.value = true
  try {
    const res = await request.get('/api/forwards')
    rules.value = res.data || []
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.log-toolbar {
  margin-bottom: 10px;
}
.log-list {
  font-family: Menlo, Consolas, monospace;
  font-size: 12px;
}
.log-line {
  padding: 3px 0;
  border-bottom: 1px solid #f0f0f0;
  word-break: break-all;
  color: #333;
}
</style>
