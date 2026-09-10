<template>
  <div class="page-grid">
    <el-card>
      <template #header
        ><div class="card-header">
          <div>
            <div>{{ $t('logs.title') }}</div>
            <small class="muted">{{ $t('logs.subtitle') }}</small>
          </div>
          <div class="actions">
            <el-button :icon="paused ? VideoPlay : VideoPause" @click="paused = !paused">{{
              paused ? $t('logs.resume') : $t('logs.pause')
            }}</el-button
            ><el-button :icon="Download" @click="download">{{ $t('logs.downloadFiltered') }}</el-button
            ><el-button type="danger" plain :icon="Delete" @click="clearOpen = true">{{ $t('logs.clear') }}</el-button>
          </div>
        </div></template
      >
      <div class="filters">
        <el-select v-model="filters.type" :placeholder="$t('logs.typeAll')" @change="reload">
          <el-option :label="$t('logs.typeAll')" value="" />
          <el-option :label="$t('logs.typeAccess')" value="access" />
          <el-option :label="$t('logs.typeSystem')" value="system" />
        </el-select>
        <el-select v-model="filters.level" clearable :placeholder="$t('logs.allLevels')"
          ><el-option label="Debug" value="debug" /><el-option label="Info" value="info" /><el-option
            label="Warn"
            value="warn" /><el-option label="Error" value="error"
        /></el-select>
        <el-input v-model="filters.source" clearable :placeholder="$t('logs.sourcePlaceholder')" />
        <el-date-picker
          v-model="filters.range"
          type="datetimerange"
          :range-separator="$t('logs.rangeSeparator')"
          :start-placeholder="$t('logs.startTime')"
          :end-placeholder="$t('logs.endTime')"
        />
        <el-input v-model="filters.keyword" clearable :placeholder="$t('logs.keyword')" @keyup.enter="reload" />
        <el-button type="primary" :icon="Search" @click="reload">{{ $t('logs.filter') }}</el-button>
      </div>
      <el-table :data="entries" v-loading="loading" stripe class="log-table">
        <el-table-column :label="$t('logs.colTime')" width="180"
          ><template #default="{ row }">{{ formatTime(row.time) }}</template></el-table-column
        >
        <el-table-column :label="$t('logs.colLevel')" width="90"
          ><template #default="{ row }"
            ><el-tag :type="levelType(row.level)" size="small">{{ row.level }}</el-tag></template
          ></el-table-column
        >
        <el-table-column prop="source" :label="$t('logs.colSource')" width="130" show-overflow-tooltip />
        <el-table-column prop="entityId" :label="$t('logs.colEntityId')" width="140" show-overflow-tooltip />
        <el-table-column prop="clientIp" :label="$t('logs.colClientIp')" width="145" show-overflow-tooltip />
        <el-table-column prop="message" :label="$t('logs.colMessage')" min-width="320" show-overflow-tooltip />
        <template #empty><el-empty :description="$t('logs.empty')" :image-size="70" /></template>
      </el-table>
      <div class="pager">
        <span class="muted">{{ $t('logs.pageCount', { n: entries.length }) }}</span
        ><el-button v-if="pagedOut" type="warning" plain @click="resumeAuto">{{ $t('logs.resumeAuto') }}</el-button
        ><el-button :disabled="nextCursor < 0" @click="loadMore">{{ $t('logs.loadMore') }}</el-button>
      </div>
    </el-card>
    <el-dialog v-model="clearOpen" :title="$t('logs.clearTitle')" width="420px"
      ><el-alert type="warning" :title="$t('logs.clearAlert')" :closable="false" /><el-form
        label-position="top"
        class="clear-form"
        ><el-form-item :label="$t('logs.passwordLabel')"
          ><el-input
            v-model="password"
            type="password"
            show-password
            @keyup.enter="clearLogs" /></el-form-item></el-form
      ><template #footer
        ><el-button @click="clearOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="danger" :loading="clearing" @click="clearLogs">{{
          $t('logs.confirmClear')
        }}</el-button></template
      ></el-dialog
    >
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Delete, Download, Search, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'

const { t } = useI18n()

const PAGE_LIMIT = 100,
  SSE_RETRY_MS = 10000
const filters = reactive({ type: '', level: '', source: '', keyword: '', range: null }),
  entries = ref([]),
  nextCursor = ref(-1),
  loading = ref(false),
  paused = ref(false),
  pagedOut = ref(false),
  clearOpen = ref(false),
  password = ref(''),
  clearing = ref(false)
let pollTimer = null,
  retryTimer = null,
  es = null
function params(cursor = 0) {
  return {
    type: filters.type || undefined,
    level: filters.level || undefined,
    source: filters.source || undefined,
    q: filters.keyword || undefined,
    from: filters.range?.[0]?.toISOString(),
    to: filters.range?.[1]?.toISOString(),
    cursor,
    limit: PAGE_LIMIT
  }
}
async function reload() {
  pagedOut.value = false
  loading.value = true
  try {
    const d = (await request.get('/api/logs', { params: params() })).data || {}
    entries.value = d.entries || []
    nextCursor.value = d.nextCursor ?? -1
  } finally {
    loading.value = false
  }
  startStream()
}
async function loadMore() {
  if (nextCursor.value < 0) return
  const d = (await request.get('/api/logs', { params: params(nextCursor.value) })).data || {}
  entries.value.push(...(d.entries || []))
  nextCursor.value = d.nextCursor ?? -1
  pagedOut.value = true
  stopStream()
}
function entryKey(e) {
  return [e.time, e.level, e.source, e.entityId, e.clientIp, e.message].join('|')
}
async function poll() {
  if (paused.value || pagedOut.value) return
  try {
    const d = (await request.get('/api/logs', { params: params() })).data || {}
    const known = new Set(entries.value.map(entryKey))
    const added = (d.entries || []).filter((e) => !known.has(entryKey(e)))
    if (added.length) entries.value.unshift(...added)
  } catch {}
}
// SSE 实时日志流；连接失败时回退到 5s 轮询，并在 SSE_RETRY_MS 后重试 SSE
function startStream() {
  stopStream()
  if (paused.value || pagedOut.value) return
  stopPolling()
  const q = new URLSearchParams()
  if (filters.type) q.set('type', filters.type)
  if (filters.level) q.set('level', filters.level)
  if (filters.source) q.set('source', filters.source)
  if (filters.keyword) q.set('q', filters.keyword)
  const source = new EventSource(`/api/logs/stream?${q}`)
  es = source
  source.addEventListener('log', (ev) => {
    if (es !== source) return
    try {
      entries.value.unshift(JSON.parse(ev.data))
      if (entries.value.length > PAGE_LIMIT) entries.value.length = PAGE_LIMIT
    } catch {}
  })
  source.onerror = () => {
    if (es !== source) return
    stopStream()
    startPolling()
    clearTimeout(retryTimer)
    retryTimer = setTimeout(() => {
      if (!paused.value && !pagedOut.value) reload()
    }, SSE_RETRY_MS)
  }
}
function stopStream() {
  if (es) {
    es.close()
    es = null
  }
}
function startPolling() {
  if (!pollTimer) pollTimer = setInterval(poll, 5000)
}
function stopPolling() {
  clearInterval(pollTimer)
  pollTimer = null
}
function download() {
  const q = new URLSearchParams()
  Object.entries(params()).forEach(([k, v]) => v !== undefined && q.set(k, v))
  window.location.href = `/api/logs/download?${q}`
}
async function clearLogs() {
  if (!password.value) return ElMessage.warning(t('logs.passwordLabel'))
  clearing.value = true
  try {
    await request.post('/api/logs/clear', { password: password.value })
    clearOpen.value = false
    password.value = ''
    ElMessage.success(t('logs.cleared'))
    await reload()
  } finally {
    clearing.value = false
  }
}
function resumeAuto() {
  pagedOut.value = false
  reload()
}
function levelType(v) {
  return { error: 'danger', warn: 'warning', info: 'success', debug: 'info' }[v] || 'info'
}
watch(paused, (v) => {
  if (v) {
    stopStream()
    stopPolling()
    clearTimeout(retryTimer)
  } else {
    reload()
  }
})
onMounted(reload)
onUnmounted(() => {
  stopStream()
  stopPolling()
  clearTimeout(retryTimer)
})
</script>

<style scoped>
.filters {
  display: grid;
  grid-template-columns: 120px 140px 180px minmax(300px, 1.3fr) minmax(220px, 1fr) auto;
  gap: 10px;
  margin-bottom: 16px;
}
.actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.pager {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 14px;
}
.clear-form {
  margin-top: 16px;
}
@media (max-width: 1100px) {
  .filters {
    grid-template-columns: 1fr 1fr;
  }
}
@media (max-width: 850px) {
  .card-header {
    align-items: flex-start;
    flex-direction: column;
  }
  .log-table {
    width: 100%;
  }
}
@media (max-width: 520px) {
  .filters {
    grid-template-columns: 1fr;
  }
  .actions {
    width: 100%;
  }
  .actions .el-button {
    flex: 1;
    margin-left: 0;
  }
}
</style>
