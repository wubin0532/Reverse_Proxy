<template>
  <div class="page-grid ddns-workspace">
    <section class="workspace-chrome">
      <header class="workspace-header">
        <h1>{{ text.title }}</h1>
        <div class="header-actions">
          <el-button @click="providersVisible = true">{{ $t('ddns.providers') }}</el-button>
          <el-button @click="loadTasks">{{ $t('common.refresh') }}</el-button>
          <el-button type="primary" @click="openTaskDialog()">{{ $t('ddns.addTask') }}</el-button>
        </div>
      </header>
      <div class="workspace-toolbar">
        <div class="status-pills" role="group" :aria-label="text.tasks">
          <button
            v-for="item in filterPills"
            :key="item.key"
            class="status-pill"
            :class="{ active: filter === item.key }"
            :aria-pressed="filter === item.key"
            @click="filter = item.key"
          >
            {{ item.label }} <b>{{ item.count }}</b>
          </button>
        </div>
        <el-input v-model="search" clearable :placeholder="text.search" class="search-input" />
      </div>
    </section>
    <section class="task-list" v-loading="loadingTasks" :aria-label="text.tasks">
      <article v-for="task in filteredTasks" :key="task.id" class="task-card" :data-task-id="task.id">
        <div class="task-summary">
          <button
            class="expand-toggle"
            :aria-expanded="expandedTasks.has(task.id)"
            :aria-label="`${text.records} ${task.name}`"
            @click="toggleExpanded(task.id)"
          >
            {{ expandedTasks.has(task.id) ? '▾' : '▸' }}
          </button>
          <div class="task-identity">
            <h2>{{ task.name }}</h2>
            <div class="muted">
              {{ providerNameOf(task.providerId) }} · {{ familiesOf(task) }} · {{ recordsOf(task).length }}
              {{ text.records }}
            </div>
          </div>
          <el-tag
            :type="!task.enabled ? 'info' : task.status?.success ? 'success' : task.status ? 'danger' : 'info'"
            effect="plain"
            class="task-state"
            >{{
              !task.enabled
                ? text.disabled
                : task.status
                  ? task.status.success
                    ? $t('common.success')
                    : $t('common.failed')
                  : text.pending
            }}</el-tag
          >
          <div class="task-controls">
            <el-button :loading="task._running" @click="runTask(task)">{{ $t('ddns.runOnce') }}</el-button>
            <span class="switch-target"
              ><el-switch
                :model-value="task.enabled"
                :aria-label="`${text.enabled} ${task.name}`"
                @change="toggleTask(task)"
            /></span>
            <el-dropdown trigger="click" @command="taskCommand(task, $event)">
              <el-button class="more-button" :aria-label="`${text.more} ${task.name}`">•••</el-button>
              <template #dropdown
                ><el-dropdown-menu
                  ><el-dropdown-item command="history">{{ text.history }}</el-dropdown-item
                  ><el-dropdown-item command="edit">{{ $t('common.edit') }}</el-dropdown-item
                  ><el-dropdown-item command="delete" divided>{{
                    $t('common.delete')
                  }}</el-dropdown-item></el-dropdown-menu
                ></template
              >
            </el-dropdown>
          </div>
        </div>
        <div v-if="expandedTasks.has(task.id)" class="record-panel">
          <div class="record-panel-header">
            <span class="muted">{{ text.lastCheck }} {{ formatTime(task.status?.lastTime) }}</span
            ><el-button @click="openRecordDialog(task)">{{ text.addRecord }}</el-button>
          </div>
          <div v-for="record in recordsOf(task)" :key="record.id" class="record-row" :data-record-id="record.id">
            <div class="record-identity">
              <b>{{ record.domain }}</b>
              <div>
                <el-tag size="small" effect="plain">{{ record.type }}</el-tag
                ><span v-if="record.name" class="muted">{{ record.name }}</span>
              </div>
            </div>
            <div class="record-runtime">
              <div class="record-address">
                {{ recordStatus(task, record)?.ip || record.value || '-'
                }}<el-tag
                  v-if="recordStatus(task, record)"
                  :type="recordStatus(task, record).success ? 'success' : 'danger'"
                  size="small"
                  effect="plain"
                  >{{ recordStatus(task, record).success ? $t('common.success') : $t('common.failed') }}</el-tag
                >
              </div>
              <div class="muted record-message">
                {{ !record.enabled ? text.disabled : recordStatus(task, record)?.message || text.pending }}
              </div>
            </div>
            <span class="record-ttl">TTL {{ record.ttl || text.defaultTTL }}</span>
            <div class="record-controls">
              <span class="switch-target"
                ><el-switch
                  :model-value="record.enabled"
                  :aria-label="`${text.enabled} ${record.domain} ${record.type}`"
                  @change="toggleRecord(task, record)" /></span
              ><el-dropdown trigger="click" @command="recordCommand(task, record, $event)"
                ><el-button class="more-button" :aria-label="`${text.more} ${record.domain} ${record.type}`"
                  >•••</el-button
                ><template #dropdown
                  ><el-dropdown-menu
                    ><el-dropdown-item command="edit">{{ $t('common.edit') }}</el-dropdown-item
                    ><el-dropdown-item command="delete" divided>{{
                      $t('common.delete')
                    }}</el-dropdown-item></el-dropdown-menu
                  ></template
                ></el-dropdown
              >
            </div>
          </div>
        </div>
      </article>
      <el-empty
        v-if="!filteredTasks.length"
        :description="search || filter !== 'all' ? text.noMatches : $t('ddns.emptyTasks')"
        :image-size="60"
      />
    </section>
    <el-dialog v-model="providersVisible" :title="$t('ddns.providers')" width="760px">
      <el-card class="section-card">
        <template #header>
          <div class="card-header">
            <span>{{ $t('ddns.providers') }}</span>
            <el-button type="primary" size="small" @click="openProviderDialog()">{{
              $t('ddns.addProvider')
            }}</el-button>
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
    </el-dialog>
    <ProviderDialog ref="providerDialogRef" @saved="loadProviders" />
    <el-dialog
      class="workspace-dialog"
      v-model="taskDialog.visible"
      :title="taskDialog.isEdit ? $t('ddns.editTask') : $t('ddns.addTaskTitle')"
      width="760px"
      destroy-on-close
    >
      <el-alert v-if="taskConflict" type="warning" :title="text.conflict" :closable="false" class="draft-conflict"
        ><el-button @click="reloadTaskDraft">{{ text.reloadDraft }}</el-button></el-alert
      >
      <el-form ref="taskFormRef" :model="taskDialog.form" :rules="taskRules" label-width="130px">
        <el-tabs v-model="editorTab">
          <el-tab-pane :label="text.general" name="general">
            <el-form-item :label="$t('ddns.taskName')" prop="name"
              ><el-input v-model="taskDialog.form.name"
            /></el-form-item>
            <el-form-item :label="$t('ddns.providerCred')" prop="providerId"
              ><el-select v-model="taskDialog.form.providerId" style="width: 100%"
                ><el-option
                  v-for="provider in providers"
                  :key="provider.id"
                  :label="provider.remark || providerTypeName(provider.type)"
                  :value="provider.id" /></el-select
            ></el-form-item>
            <el-form-item :label="text.enabled"><el-switch v-model="taskDialog.form.enabled" /></el-form-item>
            <el-form-item :label="$t('ddns.interval')"
              ><el-input-number v-model="taskDialog.form.interval" :min="30" :max="86400" /><span class="form-tip">{{
                text.seconds
              }}</span></el-form-item
            >
            <el-form-item :label="text.forceInterval"
              ><el-input-number v-model="taskDialog.form.forceInterval" :min="30" :max="604800" /><span
                class="form-tip"
                >{{ text.seconds }}</span
              ></el-form-item
            >
            <el-alert type="info" :title="text.forceTip" :closable="false" />
          </el-tab-pane>
          <el-tab-pane
            v-if="taskDialog.form.records.some((record) => !isStatic(record))"
            :label="text.sources"
            name="sources"
          >
            <div v-for="family in ['ipv4', 'ipv6']" :key="family" class="source-section">
              <h3>{{ family === 'ipv4' ? 'IPv4 / A' : 'IPv6 / AAAA' }}</h3>
              <IPSourceEditor v-model="taskDialog.form[family + 'Source']" :family="family" :interfaces="interfaces" />
              <el-form-item label=" "
                ><el-button :loading="preview.loading === family" @click="previewIP(family)">{{
                  $t('ddns.fetchIp')
                }}</el-button
                ><span class="preview-result">{{ preview[family] }}</span></el-form-item
              >
            </div>
          </el-tab-pane>
          <el-tab-pane :label="text.records" name="records">
            <div class="card-header">
              <span class="muted">{{ text.recordsTip }}</span
              ><el-button size="small" @click="taskDialog.form.records.push(emptyRecord())">{{
                text.addRecord
              }}</el-button>
            </div>
            <div v-for="(record, index) in taskDialog.form.records" :key="index" class="record-editor">
              <el-input v-model="record.domain" :placeholder="$t('ddns.colDomains')" class="record-domain" />
              <el-select v-model="record.type" class="record-type"
                ><el-option v-for="type in recordTypes" :key="type" :label="type" :value="type"
              /></el-select>
              <el-input
                v-if="isStatic(record)"
                v-model="record.value"
                :aria-label="text.recordValue"
                :placeholder="record.type === 'CNAME' ? 'target.example.com' : text.txtPlaceholder"
                class="static-value"
                maxlength="2048"
              />
              <el-input-number v-model="record.ttl" :min="0" :max="86400" controls-position="right" />
              <el-switch v-model="record.enabled" />
              <el-button
                link
                type="danger"
                :disabled="taskDialog.form.records.length === 1"
                @click="taskDialog.form.records.splice(index, 1)"
                >{{ $t('common.delete') }}</el-button
              >
            </div>
          </el-tab-pane>
        </el-tabs>
      </el-form>
      <template #footer
        ><el-button @click="taskDialog.visible = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="taskDialog.saving" :disabled="taskConflict" @click="saveTask">{{
          $t('common.save')
        }}</el-button></template
      >
    </el-dialog>
    <el-dialog
      class="workspace-dialog"
      v-model="recordDialog.visible"
      :title="recordDialog.isEdit ? text.editRecord : text.addRecord"
      width="560px"
      destroy-on-close
    >
      <el-alert
        v-if="recordDialog.conflict"
        type="warning"
        :title="text.conflict"
        :closable="false"
        class="draft-conflict"
        ><el-button @click="reloadRecordDraft">{{ text.reloadDraft }}</el-button></el-alert
      >
      <el-form label-width="105px">
        <el-form-item :label="$t('common.name')"><el-input v-model="recordDialog.form.name" /></el-form-item>
        <el-form-item :label="$t('ddns.colDomains')"><el-input v-model="recordDialog.form.domain" /></el-form-item>
        <el-form-item label="Type"
          ><el-select v-model="recordDialog.form.type"
            ><el-option v-for="type in recordTypes" :key="type" :label="type" :value="type" /></el-select
        ></el-form-item>
        <el-form-item v-if="isStatic(recordDialog.form)" :label="text.recordValue">
          <el-input
            v-model="recordDialog.form.value"
            :type="recordDialog.form.type === 'TXT' ? 'textarea' : 'text'"
            :rows="3"
            maxlength="2048"
            :placeholder="recordDialog.form.type === 'CNAME' ? 'target.example.com' : text.txtPlaceholder"
          />
          <p class="muted">{{ text.staticHint }}</p>
        </el-form-item>
        <el-form-item label="TTL"
          ><el-input-number v-model="recordDialog.form.ttl" :min="0" :max="86400"
        /></el-form-item>
        <el-form-item :label="text.enabled"><el-switch v-model="recordDialog.form.enabled" /></el-form-item> </el-form
      ><template #footer
        ><el-button @click="recordDialog.visible = false">{{ $t('common.cancel') }}</el-button
        ><el-button
          type="primary"
          :loading="recordDialog.saving"
          :disabled="recordDialog.conflict"
          @click="saveRecord"
          >{{ $t('common.save') }}</el-button
        ></template
      >
    </el-dialog>
    <el-dialog
      class="workspace-dialog"
      v-model="historyDialog.visible"
      :title="`${text.history} · ${historyDialog.name}`"
      width="min(850px, 94vw)"
    >
      <el-alert type="info" :title="text.historyTip" :closable="false" />
      <el-table :data="historyDialog.entries" v-loading="historyDialog.loading" class="history-table">
        <el-table-column :label="text.lastCheck" min-width="160"
          ><template #default="{ row }">{{ formatTime(row.time) }}</template></el-table-column
        >
        <el-table-column prop="domain" :label="$t('ddns.colDomains')" min-width="160" /><el-table-column
          prop="type"
          label="Type"
          width="70"
        />
        <el-table-column :label="text.result" width="100"
          ><template #default="{ row }"
            ><el-tag :type="row.kind === 'failed' ? 'danger' : row.kind === 'changed' ? 'success' : 'info'">{{
              text[row.kind] || row.kind
            }}</el-tag></template
          ></el-table-column
        >
        <el-table-column prop="ip" :label="text.recordValue" min-width="135" /><el-table-column
          prop="message"
          :label="text.details"
          min-width="220"
        />
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { useCrudDialog } from '../composables/useCrudDialog'
import { useIntervalFn } from '../composables/useIntervalFn'
import { formatTime } from '../utils/format'
import ProviderDialog from '../components/ddns/ProviderDialog.vue'
import IPSourceEditor from '../components/ddns/IPSourceEditor.vue'

const { t, locale } = useI18n()
const text = computed(() =>
  locale.value.startsWith('zh')
    ? {
        title: '动态域名',
        subtitle: '管理动态地址与固定 CNAME/TXT，追踪记录变化和远端同步。',
        tasks: '任务',
        enabled: '启用',
        disabled: '停用',
        records: '记录',
        needsAttention: '需要处理',
        search: '搜索任务、域名或服务商',
        all: '全部',
        noMatches: '没有符合条件的任务',
        record: '记录 / 域名',
        addRecord: '添加记录',
        editRecord: '编辑记录',
        deleteRecord: '删除这条记录？',
        lastCheck: '最近检查',
        pending: '等待运行',
        history: '同步历史',
        general: '任务设置',
        sources: 'IP 来源',
        seconds: '秒',
        forceInterval: '远端核查间隔',
        forceTip: '即使记录值没有变化，也按此间隔检查远端记录，修复被外部修改的值与 TTL。',
        defaultTTL: '默认',
        recordsTip:
          '支持 A、AAAA、CNAME、TXT；CNAME/TXT 使用填写的固定值。每条记录可独立停用。TTL 0 使用服务商默认值。',
        recordValue: '记录值',
        txtPlaceholder: '输入原始文本，无需添加外层引号',
        staticHint:
          'CNAME填写目标域名；TXT按原文保存。停用或删除仅停止本任务管理，不删除服务商上的记录。同名同类型多条远程记录会报错，请先在服务商控制台整理。',
        valueRequired: '请填写CNAME/TXT记录值',
        historyTip: '保留最近 100 条变化、同步和失败记录；历史存于内存，服务重启后清空。',
        result: '结果',
        details: '详情',
        changed: '已变更',
        synced: '已核查',
        failed: '失败',
        domainRequired: '请填写域名',
        recordsRequired: '至少保留一条记录',
        more: '更多',
        conflict: '任务已在其他位置变更。草稿已保留；重新加载将丢弃草稿。',
        reloadDraft: '放弃草稿并加载最新',
        discardConfirm: '放弃当前草稿并加载最新配置？',
        deleted: '任务或记录已删除'
      }
    : {
        title: 'Dynamic DNS',
        subtitle: 'Manage dynamic addresses and static CNAME/TXT records with synchronization history.',
        tasks: 'Tasks',
        enabled: 'Enabled',
        disabled: 'Disabled',
        records: 'Records',
        needsAttention: 'Needs attention',
        search: 'Search tasks, domains or providers',
        all: 'All',
        noMatches: 'No matching tasks',
        record: 'Record / domain',
        addRecord: 'Add record',
        editRecord: 'Edit record',
        deleteRecord: 'Delete this record?',
        lastCheck: 'Last check',
        pending: 'Pending',
        history: 'Sync history',
        general: 'Task settings',
        sources: 'IP sources',
        seconds: 'seconds',
        forceInterval: 'Remote check interval',
        forceTip:
          'Recheck remote records at this interval even when the value stays the same, repairing externally changed values and TTL.',
        defaultTTL: 'Default',
        recordsTip: 'Supports A, AAAA, CNAME and TXT. CNAME/TXT use literal values. TTL 0 uses the provider default.',
        recordValue: 'Record value',
        txtPlaceholder: 'Literal text without extra enclosing quotes',
        staticHint:
          'CNAME uses a target hostname; TXT stays literal. Disabling or removing stops management without deleting remote DNS records. Ambiguous remote records require cleanup in the provider console.',
        valueRequired: 'Enter the CNAME/TXT record value',
        historyTip: 'The latest 100 changes, synchronizations and failures are held in memory and cleared on restart.',
        result: 'Result',
        details: 'Details',
        changed: 'Changed',
        synced: 'Checked',
        failed: 'Failed',
        domainRequired: 'Enter a domain',
        recordsRequired: 'Keep at least one record',
        more: 'More',
        conflict: 'This task changed elsewhere. Your draft is kept; reloading will discard it.',
        reloadDraft: 'Discard draft and reload',
        discardConfirm: 'Discard this draft and load the latest configuration?',
        deleted: 'The task or record was deleted'
      }
)
const providers = ref([]),
  tasks = ref([]),
  loadingProviders = ref(false),
  loadingTasks = ref(false)
const providersVisible = ref(false),
  expandedTasks = ref(new Set()),
  taskConflict = ref(false)
const search = ref(''),
  filter = ref('all'),
  editorTab = ref('general')
const providerDialogRef = ref(),
  interfaces = ref([])
const preview = reactive({ loading: '', ipv4: '', ipv6: '' })
const recordsOf = (task) =>
  task.effectiveRecords ||
  task.records ||
  (task.domains || []).map((domain) => ({
    domain,
    type: task.ipType === 'ipv6' ? 'AAAA' : 'A',
    ttl: task.ttl || 0,
    enabled: true
  }))
const recordTypes = ['A', 'AAAA', 'CNAME', 'TXT']
const isStatic = (record) => ['CNAME', 'TXT'].includes(record.type)
const recordStatus = (task, record) => task.status?.records?.find((status) => status.recordId === record.id)
const familiesOf = (task) =>
  [
    ...new Set(
      recordsOf(task).map((record) => (record.type === 'AAAA' ? 'IPv6' : record.type === 'A' ? 'IPv4' : record.type))
    )
  ].join(' + ')
const filterPills = computed(() => [
  { key: 'all', label: text.value.all, count: tasks.value.length },
  { key: 'enabled', label: text.value.enabled, count: tasks.value.filter((task) => task.enabled).length },
  { key: 'disabled', label: text.value.disabled, count: tasks.value.filter((task) => !task.enabled).length },
  {
    key: 'failed',
    label: text.value.needsAttention,
    count: tasks.value.filter((task) => task.enabled && task.status && !task.status.success).length
  }
])
function toggleExpanded(id) {
  const expanded = new Set(expandedTasks.value)
  if (expanded.has(id)) expanded.delete(id)
  else expanded.add(id)
  expandedTasks.value = expanded
}
async function taskCommand(task, command) {
  if (command === 'edit') openTaskDialog(task)
  else if (command === 'history') openHistory(task)
  else if (command === 'delete') await deleteTask(task)
}
async function recordCommand(task, record, command) {
  if (command === 'edit') openRecordDialog(task, record)
  else if (command === 'delete') await deleteRecord(task, record)
}
const isConflict = (error) => error?.response?.status === 409 || error?.response?.data?.code === 409
const filteredTasks = computed(() =>
  tasks.value.filter((task) => {
    const query = search.value.trim().toLowerCase()
    const matches =
      !query ||
      [
        task.name,
        providerNameOf(task.providerId),
        ...recordsOf(task).flatMap((record) => [record.domain, record.name, record.type, record.value])
      ].some((value) => (value || '').toLowerCase().includes(query))
    return (
      matches &&
      (filter.value === 'all' ||
        (filter.value === 'enabled' && task.enabled) ||
        (filter.value === 'disabled' && !task.enabled) ||
        (filter.value === 'failed' && task.enabled && task.status && !task.status.success))
    )
  })
)
const PROVIDER_TYPES = ['aliyun', 'cloudflare', 'dnspod']
function providerTypeName(type) {
  return PROVIDER_TYPES.includes(type) ? t(`ddns.providerTypes.${type}`) : type
}
function providerNameOf(id) {
  const provider = providers.value.find((row) => row.id === id)
  return provider ? provider.remark || providerTypeName(provider.type) : id || '-'
}
function openProviderDialog(row) {
  providerDialogRef.value?.open(row)
}
async function deleteProvider(row) {
  try {
    await request.delete(`/api/providers/${row.id}`)
    ElMessage.success(t('common.deleted'))
    await loadProviders()
  } catch {
    /* API interceptor reports errors. */
  }
}
async function loadProviders() {
  loadingProviders.value = true
  try {
    const res = await request.get('/api/providers')
    providers.value = res.data || []
  } catch {
    /* API interceptor */
  } finally {
    loadingProviders.value = false
  }
}
async function loadInterfaces() {
  try {
    const res = await request.get('/api/ddns/interfaces')
    interfaces.value = res.data?.interfaces || []
  } catch {
    /* API interceptor */
  }
}
async function previewIP(family) {
  preview.loading = family
  preview[family] = ''
  try {
    const source = taskDialog.form[family + 'Source']
    const res = await request.post('/api/ddns/preview-ip', {
      ipType: family,
      ipSource: source.source,
      interface: source.interface,
      apiUrl: source.apiUrl,
      fallbacks: source.fallbacks || []
    })
    preview[family] = [res.data?.interface, res.data?.ip].filter(Boolean).join(': ')
  } catch {
    /* API interceptor */
  } finally {
    preview.loading = ''
  }
}
function emptyRecord() {
  return { id: '', name: '', domain: '', type: 'A', value: '', ttl: 0, enabled: true }
}
function sourceOf(task, family) {
  return {
    source: task.ipType === family ? task.ipSource || 'interface' : 'interface',
    interface: task.ipType === family ? task.interface || 'auto' : 'auto',
    apiUrl: task.ipType === family ? task.apiUrl || '' : '',
    fallbacks: [],
    ...(task[family + 'Source'] ? JSON.parse(JSON.stringify(task[family + 'Source'])) : {})
  }
}
const {
  formRef: taskFormRef,
  dialog: taskDialog,
  open: openTask,
  submit: saveTask
} = useCrudDialog({
  emptyForm: () => ({
    id: '',
    revision: '',
    name: '',
    providerId: '',
    enabled: true,
    interval: 300,
    forceInterval: 21600,
    ipv4Source: sourceOf({}, 'ipv4'),
    ipv6Source: sourceOf({}, 'ipv6'),
    records: [emptyRecord()]
  }),
  fromRow: (row) => ({
    id: row.id,
    revision: row.revision,
    name: row.name,
    providerId: row.providerId,
    enabled: row.enabled,
    interval: row.interval || 300,
    forceInterval: row.forceInterval || 21600,
    ipv4Source: sourceOf(row, 'ipv4'),
    ipv6Source: sourceOf(row, 'ipv6'),
    records: recordsOf(row).map((record) => ({ ...record }))
  }),
  onSubmit: async (form, dialog) => {
    if (taskConflict.value) throw new Error('conflict')
    if (!form.records.length) {
      ElMessage.warning(text.value.recordsRequired)
      throw new Error('validation')
    }
    if (form.records.some((record) => !record.domain.trim())) {
      editorTab.value = 'records'
      ElMessage.warning(text.value.domainRequired)
      throw new Error('validation')
    }
    if (form.records.some((record) => isStatic(record) && !record.value)) {
      editorTab.value = 'records'
      ElMessage.warning(text.value.valueRequired)
      throw new Error('validation')
    }
    const body = {
      name: form.name,
      providerId: form.providerId,
      enabled: form.enabled,
      interval: form.interval,
      forceInterval: form.forceInterval,
      records: form.records,
      ipv4Source: form.ipv4Source,
      ipv6Source: form.ipv6Source,
      ...(dialog.isEdit ? { revision: form.revision } : {})
    }
    try {
      return dialog.isEdit
        ? await request.put(`/api/ddns/tasks/${form.id}`, body)
        : await request.post('/api/ddns/tasks', body)
    } catch (error) {
      if (isConflict(error)) {
        taskConflict.value = true
        await loadTasks()
      }
      throw error
    }
  },
  onSaved: loadTasks
})
const taskRules = computed(() => ({
  name: [{ required: true, message: t('ddns.taskNameRequired'), trigger: 'blur' }],
  providerId: [{ required: true, message: t('ddns.providerCredRequired'), trigger: 'change' }]
}))
function openTaskDialog(row) {
  taskConflict.value = false
  editorTab.value = 'general'
  preview.ipv4 = ''
  preview.ipv6 = ''
  loadInterfaces()
  openTask(row)
}
async function toggleTask(row) {
  try {
    await request.post(`/api/ddns/tasks/${row.id}/toggle`, { revision: row.revision })
  } catch {
    /* API interceptor */
  } finally {
    await loadTasks()
  }
}
async function runTask(row) {
  row._running = true
  try {
    const res = await request.post(`/api/ddns/tasks/${row.id}/run`, {}, { timeout: 120000 })
    if (res.data?.success) ElMessage.success(t('ddns.runSuccess', { ip: res.data.ip || '-' }))
    else ElMessage.warning(res.data?.message || t('ddns.runDone'))
  } catch {
    /* API interceptor */
  } finally {
    row._running = false
    await loadTasks()
  }
}
async function deleteTask(row) {
  try {
    await ElMessageBox.confirm(t('ddns.deleteTaskConfirm'), t('common.delete'), { type: 'warning' })
    await request.delete(`/api/ddns/tasks/${row.id}`, { data: { revision: row.revision } })
    ElMessage.success(t('common.deleted'))
    await loadTasks()
  } catch {
    /* API interceptor */
  }
}
async function loadTasks() {
  loadingTasks.value = true
  try {
    const running = new Set(tasks.value.filter((task) => task._running).map((task) => task.id))
    const res = await request.get('/api/ddns/tasks')
    tasks.value = (res.data || []).map((task) => ({ ...task, _running: running.has(task.id) }))
    const ids = new Set(tasks.value.map((task) => task.id))
    expandedTasks.value = new Set([...expandedTasks.value].filter((id) => ids.has(id)))
  } catch {
    /* API interceptor */
  } finally {
    loadingTasks.value = false
  }
}
const recordDialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  task: null,
  conflict: false,
  form: emptyRecord()
})
function openRecordDialog(task, record) {
  recordDialog.conflict = false
  recordDialog.task = { id: task.id, revision: task.revision }
  recordDialog.isEdit = !!record
  recordDialog.form = record ? { ...record } : emptyRecord()
  recordDialog.visible = true
}
async function saveRecord() {
  if (recordDialog.conflict) return
  if (!recordDialog.form.domain.trim()) {
    ElMessage.warning(text.value.domainRequired)
    return
  }
  if (isStatic(recordDialog.form) && !recordDialog.form.value) {
    ElMessage.warning(text.value.valueRequired)
    return
  }
  recordDialog.saving = true
  try {
    const task = recordDialog.task
    const body = { revision: task.revision, record: recordDialog.form }
    if (recordDialog.isEdit) await request.put(`/api/ddns/tasks/${task.id}/records/${recordDialog.form.id}`, body)
    else await request.post(`/api/ddns/tasks/${task.id}/records`, body)
    recordDialog.visible = false
    await loadTasks()
  } catch (error) {
    if (isConflict(error)) recordDialog.conflict = true
    await loadTasks()
  } finally {
    recordDialog.saving = false
  }
}
async function toggleRecord(task, record) {
  try {
    await request.post(`/api/ddns/tasks/${task.id}/records/${record.id}/toggle`, { revision: task.revision })
  } catch {
    /* API interceptor */
  } finally {
    await loadTasks()
  }
}
async function deleteRecord(task, record) {
  try {
    await ElMessageBox.confirm(text.value.deleteRecord, t('common.delete'), { type: 'warning' })
    await request.delete(`/api/ddns/tasks/${task.id}/records/${record.id}`, { data: { revision: task.revision } })
  } catch {
    /* API interceptor */
  } finally {
    await loadTasks()
  }
}
async function reloadTaskDraft() {
  try {
    await ElMessageBox.confirm(text.value.discardConfirm, text.value.reloadDraft, { type: 'warning' })
  } catch {
    return
  }
  await loadTasks()
  const current = tasks.value.find((task) => task.id === taskDialog.form.id)
  if (!current) {
    taskDialog.visible = false
    ElMessage.warning(text.value.deleted)
    return
  }
  openTaskDialog(current)
}
async function reloadRecordDraft() {
  try {
    await ElMessageBox.confirm(text.value.discardConfirm, text.value.reloadDraft, { type: 'warning' })
  } catch {
    return
  }
  await loadTasks()
  const current = tasks.value.find((task) => task.id === recordDialog.task.id)
  const record = current && recordsOf(current).find((item) => item.id === recordDialog.form.id)
  if (!current || (recordDialog.isEdit && !record)) {
    recordDialog.visible = false
    ElMessage.warning(text.value.deleted)
    return
  }
  openRecordDialog(current, recordDialog.isEdit ? record : undefined)
}
const historyDialog = reactive({ visible: false, name: '', entries: [], loading: false })
async function openHistory(task) {
  historyDialog.visible = true
  historyDialog.name = task.name
  historyDialog.entries = []
  historyDialog.loading = true
  try {
    const res = await request.get(`/api/ddns/tasks/${task.id}/history`)
    historyDialog.entries = res.data?.entries || []
  } catch {
    /* API interceptor */
  } finally {
    historyDialog.loading = false
  }
}
const taskPoller = useIntervalFn(async () => {
  if (!taskDialog.visible && !recordDialog.visible && !historyDialog.visible) await loadTasks()
}, 30000)
onMounted(() => {
  loadProviders()
  loadTasks()
  taskPoller.start()
})
</script>

<style scoped>
.workspace-header,
.header-actions,
.workspace-toolbar,
.status-pills,
.task-summary,
.task-controls,
.record-controls,
.record-panel-header,
.card-header {
  display: flex;
  align-items: center;
  gap: 12px;
}
.workspace-header,
.workspace-toolbar,
.record-panel-header,
.card-header {
  justify-content: space-between;
}
.workspace-header h1 {
  margin: 0;
  font-size: 20px;
  line-height: 1.4;
  font-weight: 650;
}
.header-actions {
  gap: 8px;
  flex-wrap: wrap;
}
.header-actions .el-button + .el-button {
  margin-left: 0;
}
.workspace-toolbar {
  flex-wrap: wrap;
  gap: 12px;
}
.status-pills {
  flex-wrap: wrap;
  gap: 6px;
}
.status-pill {
  display: flex;
  align-items: center;
  gap: 7px;
  min-height: 44px;
  padding: 5px 13px;
  border: 1px solid var(--ap-border);
  border-radius: 999px;
  background: var(--ap-card);
  color: var(--ap-muted);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
}
.status-pill.active {
  color: var(--ap-primary);
  border-color: var(--ap-primary);
  background: var(--ap-bg);
}
.status-pill b {
  font-weight: 650;
}
.search-input {
  width: 300px;
  margin-left: auto;
}
.task-list {
  display: grid;
  gap: 12px;
}
.task-card {
  border: 1px solid var(--ap-border);
  border-radius: 12px;
  background: var(--ap-card);
  overflow: hidden;
}
.task-summary {
  padding: 12px 16px;
  gap: 12px;
}
.task-identity {
  flex: 1;
  min-width: 0;
}
.task-identity h2 {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 650;
  overflow-wrap: anywhere;
}
.muted {
  color: var(--ap-muted);
  font-size: 12px;
}
.expand-toggle {
  border: 0;
  background: transparent;
  color: var(--ap-muted);
  width: 44px;
  min-height: 44px;
  flex: none;
  cursor: pointer;
  font-size: 19px;
}
.task-controls {
  gap: 8px;
  flex: none;
}
.switch-target {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
}
.more-button {
  min-width: 44px;
}
.task-state {
  flex: none;
}
.record-panel {
  border-top: 1px solid var(--ap-border);
  padding: 0 16px 8px 72px;
}
.record-panel-header {
  padding: 8px 0;
}
.record-row {
  display: grid;
  grid-template-columns: minmax(180px, 1.1fr) minmax(180px, 1.6fr) 85px auto;
  gap: 16px;
  align-items: center;
  min-height: 76px;
  padding: 10px 0;
  border-top: 1px solid var(--ap-border);
}
.record-identity,
.record-runtime {
  min-width: 0;
  overflow-wrap: anywhere;
}
.record-identity b {
  font-size: 13px;
  font-weight: 600;
}
.record-identity > div {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 5px;
}
.record-address {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 13px;
  font-family: Menlo, Consolas, monospace;
}
.record-message {
  margin-top: 5px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.record-ttl {
  font-size: 12px;
  color: var(--ap-muted);
  white-space: nowrap;
}
.record-controls {
  gap: 8px;
}
.ddns-workspace :deep(.el-button) {
  min-height: 44px;
}
.search-input :deep(.el-input__wrapper) {
  min-height: 40px;
}
.task-controls :deep(.el-button + .el-button) {
  margin-left: 0;
}
.draft-conflict {
  margin-bottom: 16px;
}
.source-section {
  margin-bottom: 20px;
  padding: 8px 0;
  border-bottom: 1px solid var(--ap-border);
}
.record-editor {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding: 14px 0;
  border-bottom: 1px solid var(--ap-border);
}
.record-domain {
  flex: 1;
  min-width: 200px;
}
.record-type {
  width: 100px;
}
.form-tip {
  margin-left: 10px;
  color: var(--ap-muted);
  font-size: 12px;
}
.preview-result {
  margin-left: 10px;
  color: var(--ap-success);
  font-family: Menlo, Consolas, monospace;
}
.history-table {
  margin-top: 16px;
}
@media (max-width: 760px) {
  .workspace-header {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .header-actions {
    gap: 6px;
  }
  .workspace-toolbar {
    align-items: stretch;
  }
  .search-input {
    width: 100%;
    margin-left: 0;
  }
  .task-summary {
    flex-wrap: wrap;
    gap: 8px;
    padding: 10px 12px;
  }
  .task-identity {
    flex-basis: calc(100% - 60px);
  }
  .task-state {
    margin-left: 52px;
  }
  .task-controls {
    margin-left: auto;
    gap: 4px;
  }
  .record-panel {
    padding: 0 12px 8px;
  }
  .record-row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 8px 12px;
  }
  .record-identity {
    grid-column: 1;
  }
  .record-runtime {
    grid-column: 1 / -1;
    grid-row: 2;
  }
  .record-ttl {
    grid-column: 1;
    grid-row: 3;
  }
  .record-controls {
    grid-column: 2;
    grid-row: 1 / span 3;
    align-self: start;
  }
  .record-address {
    padding-right: 4px;
  }
  .record-editor {
    gap: 8px;
  }
  .record-domain {
    flex-basis: 100%;
  }
}
.static-value {
  flex: 1 1 100%;
  min-width: 0;
}
.record-address {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
</style>
