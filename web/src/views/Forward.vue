<template>
  <el-card>
    <template #header>
      <div class="card-header">
        <span>{{ $t('forward.title') }}</span>
        <el-button type="primary" size="small" @click="openDialog()">{{ $t('forward.addRule') }}</el-button>
      </div>
    </template>
    <el-table :data="rules" v-loading="loading">
      <el-table-column prop="name" :label="$t('forward.colName')" min-width="120" />
      <el-table-column :label="$t('forward.colProto')" width="100">
        <template #default="{ row }">
          <el-tag size="small">{{ protoText(row.proto) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="listen" :label="$t('forward.colListen')" width="120" />
      <el-table-column :label="$t('forward.colTargets')" min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ (row.targets || []).join(', ') }}</template>
      </el-table-column>
      <el-table-column :label="$t('forward.colEnabled')" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleRule(row)" />
        </template>
      </el-table-column>
      <el-table-column :label="$t('forward.colActions')" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openLogs(row)">{{ $t('common.logs') }}</el-button>
          <el-button link type="primary" @click="openDialog(row)">{{ $t('common.edit') }}</el-button>
          <el-popconfirm :title="$t('forward.deleteConfirm')" @confirm="deleteRule(row)">
            <template #reference>
              <el-button link type="danger">{{ $t('common.delete') }}</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
      <template #empty><el-empty :description="$t('forward.empty')" :image-size="60" /></template>
    </el-table>

    <el-dialog
      v-model="dialog.visible"
      :title="dialog.isEdit ? $t('forward.editRule') : $t('forward.addRuleTitle')"
      width="520px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="dialog.form" :rules="formRules" label-width="100px">
        <el-form-item :label="$t('forward.ruleName')" prop="name">
          <el-input v-model="dialog.form.name" :placeholder="$t('forward.ruleNamePlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('forward.colProto')" prop="proto">
          <el-select v-model="dialog.form.proto" style="width: 100%">
            <el-option label="TCP" value="tcp" />
            <el-option label="UDP" value="udp" />
            <el-option label="TCP + UDP" value="tcpudp" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('forward.colListen')" prop="listen">
          <el-input v-model="dialog.form.listen" :placeholder="$t('forward.listenPlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('forward.targets')" prop="targetsText">
          <el-input
            v-model="dialog.form.targetsText"
            :placeholder="$t('forward.targetsPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('forward.firewall')">
          <el-switch v-model="dialog.form.autoFw" />
          <span class="form-tip">{{ $t('forward.firewallTip') }}</span>
        </el-form-item>
        <el-form-item :label="$t('forward.ipAccess')">
          <el-select v-model="dialog.form.ipListMode" style="width: 100%">
            <el-option value="" :label="$t('common.noLimit')" />
            <el-option value="whitelist" :label="$t('common.ipWhitelist')" />
            <el-option value="blacklist" :label="$t('common.ipBlacklist')" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="dialog.form.ipListMode" :label="$t('common.ipList')">
          <el-input
            v-model="dialog.ipListText"
            type="textarea"
            :rows="2"
            :placeholder="$t('common.ipListPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="logsDrawer.visible" :title="$t('forward.logsTitle', { name: logsDrawer.name })" size="560px">
      <div class="log-toolbar">
        <el-button size="small" @click="loadLogs">{{ $t('common.refresh') }}</el-button>
      </div>
      <div class="log-list">
        <el-empty v-if="!logsDrawer.logs.length" :description="$t('forward.noLogs')" :image-size="60" />
        <div v-for="(line, i) in logsDrawer.logs" :key="i" class="log-line">{{ line }}</div>
      </div>
    </el-drawer>
  </el-card>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'

const { t } = useI18n()

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
  form: { id: '', name: '', proto: 'tcp', listen: '', targetsText: '', autoFw: false, ipListMode: '', enabled: true }
})

const formRules = computed(() => ({
  name: [{ required: true, message: t('forward.nameRequired'), trigger: 'blur' }],
  listen: [{ required: true, message: t('forward.listenRequired'), trigger: 'blur' }],
  targetsText: [{ required: true, message: t('forward.targetsRequired'), trigger: 'blur' }]
}))

function openDialog(row) {
  dialog.isEdit = !!row
  dialog.form = row
    ? {
        id: row.id,
        name: row.name,
        proto: row.proto || 'tcp',
        listen: row.listen,
        targetsText: (row.targets || []).join(', '),
        autoFw: !!row.autoFw,
        ipListMode: row.ipListMode || '',
        enabled: row.enabled
      }
    : { id: '', name: '', proto: 'tcp', listen: '', targetsText: '', autoFw: false, ipListMode: '', enabled: true }
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
    autoFw: f.autoFw,
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
    ElMessage.success(t('common.saveSuccess'))
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
    // 拦截器已提示；刷新真实状态，避免开关停留在错误位置
    load()
  }
}

async function deleteRule(row) {
  try {
    await request.delete(`/api/forwards/${row.id}`)
    ElMessage.success(t('common.deleted'))
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
.form-tip {
  margin-left: 10px;
  color: var(--ap-muted);
  font-size: 12px;
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
  color: var(--ap-text);
}
</style>
