<template>
  <div class="web-service-page" v-loading="loading">
    <section class="sites-panel">
      <div class="panel-heading">
        <div>
          <span class="eyebrow">{{ $t('webService.siteManagement') }}</span>
          <h1>{{ $t('webService.title') }}</h1>
        </div>
        <el-button type="primary" @click="openSiteDialog()">＋ {{ $t('webService.addSite') }}</el-button>
      </div>
      <div v-if="sites.length" class="site-strip">
        <button
          v-for="site in sites"
          :key="site.id"
          type="button"
          class="site-tab"
          :class="{ active: site.id === selectedSiteId }"
          @click="selectedSiteId = site.id"
        >
          <span class="status-dot" :class="site.status" />
          <span class="site-tab-copy"
            ><b>{{ site.name }}</b
            ><small>{{ site.tls ? 'HTTPS' : 'HTTP' }} · {{ site.listen }}</small></span
          >
          <span class="rule-count">{{ (site.rules || []).length }}</span>
        </button>
      </div>
      <el-empty v-else :description="$t('webService.empty')" :image-size="60" />
    </section>

    <section v-if="activeSite" class="rules-panel">
      <header class="site-toolbar">
        <div class="site-identity">
          <span class="status-dot large" :class="activeSite.status" />
          <div>
            <h2>{{ activeSite.name }}</h2>
            <span>{{ siteStatusText(activeSite) }}</span>
          </div>
          <el-tag :type="activeSite.tls ? 'success' : 'info'" effect="plain">{{
            activeSite.tls ? 'HTTPS' : 'HTTP'
          }}</el-tag>
          <el-tag v-if="activeSite.tls && activeSite.forceHttps && activeSite.certId" type="warning" effect="plain">{{
            $t('webService.forceRedirect')
          }}</el-tag>
        </div>
        <div class="toolbar-actions">
          <el-button @click="openLogs(activeSite)">{{ $t('common.logs') }}</el-button>
          <el-button @click="openSiteDialog(activeSite)">{{ $t('common.edit') }}</el-button>
          <el-switch :model-value="activeSite.enabled" @change="toggleSite(activeSite)" />
          <el-popconfirm :title="$t('webService.deleteConfirm')" @confirm="deleteSite(activeSite)">
            <template #reference
              ><el-button type="danger" plain>{{ $t('common.delete') }}</el-button></template
            >
          </el-popconfirm>
          <el-button type="primary" @click="openRuleDialog()">＋ {{ $t('webService.addRule') }}</el-button>
        </div>
      </header>

      <div class="site-summary">
        <span
          >◉ {{ $t('webService.listenAt') }} <b>{{ activeSite.listen }}</b></span
        >
        <span>{{ activeSite.tls ? 'TLS' : 'HTTP' }}</span>
        <span
          >{{ $t('webService.allRules') }} <b>{{ activeSite.rules?.length || 0 }}</b></span
        >
        <span
          >{{ $t('webService.enabledRules') }} <b>{{ enabledRuleCount }}</b></span
        >
        <span class="traffic-chip"
          >↓ {{ fmtBytes(siteStats.bytesIn) }} <small>{{ fmtRate(siteRate.bytesIn) }}</small></span
        >
        <span class="traffic-chip outgoing"
          >↑ {{ fmtBytes(siteStats.bytesOut) }} <small>{{ fmtRate(siteRate.bytesOut) }}</small></span
        >
        <span
          >{{ $t('webService.activeConnections') }} <b>{{ siteStats.active || 0 }}</b></span
        >
        <span :class="{ danger: siteErrors > 0 }"
          >{{ $t('webService.errors') }} <b>{{ siteErrors }}</b></span
        >
      </div>

      <div class="site-chart">
        <div class="site-chart-head">
          <span>{{ $t('webService.trafficChart') }}</span>
          <el-radio-group v-model="chartRange" size="small" @change="loadSeries">
            <el-radio-button value="1h">1h</el-radio-button>
            <el-radio-button value="6h">6h</el-radio-button>
            <el-radio-button value="24h">24h</el-radio-button>
          </el-radio-group>
        </div>
        <SiteTrafficChart :data="chartData" v-loading="chartLoading" />
      </div>

      <div class="rule-list-heading">
        <div>
          <h3>{{ $t('webService.subRules') }}</h3>
          <p>{{ $t('webService.dragTip') }}</p>
        </div>
      </div>
      <div v-if="activeSite.rules?.length" class="rule-list">
        <article
          v-for="(rule, index) in activeSite.rules"
          :key="rule.id"
          class="rule-row"
          :class="{ disabled: !rule.enabled, dragging: dragIndex === index }"
          @dragover.prevent
          @drop="dropRule(index)"
        >
          <span
            class="drag-handle"
            draggable="true"
            :title="$t('webService.dragTip')"
            @dragstart="startDrag(index)"
            @dragend="dragIndex = -1"
            >⋮⋮</span
          >
          <div class="rule-name">
            <b>{{ rule.name }}</b
            ><small>{{ ruleTypeText(rule.type) }}</small>
          </div>
          <div class="rule-guard">
            <span>{{ securityLabel(rule) }}</span>
          </div>
          <div class="rule-route">
            <b>{{ rule.frontendHost || '*' }}{{ rule.frontendPath || '/' }}</b
            ><small>{{ ruleTarget(rule) }}</small>
          </div>
          <div class="rule-traffic">
            <span
              >↓ <b>{{ fmtBytes(ruleStats(rule.id).bytesIn) }}</b
              ><small>{{ fmtRate(ruleRate(rule.id).bytesIn) }}</small></span
            >
            <span class="outgoing"
              >↑ <b>{{ fmtBytes(ruleStats(rule.id).bytesOut) }}</b
              ><small>{{ fmtRate(ruleRate(rule.id).bytesOut) }}</small></span
            >
          </div>
          <div class="rule-health">
            <span :title="$t('webService.activeConnections')"
              >↔ <b>{{ ruleStats(rule.id).active || 0 }}</b></span
            >
            <span :class="{ danger: ruleErrors(rule.id) > 0 }" :title="$t('webService.errors')"
              >⚠ <b>{{ ruleErrors(rule.id) }}</b></span
            >
            <el-tooltip v-for="b in ruleBackends(rule.id)" :key="b.backend" placement="top">
              <template #content>
                <div>{{ b.backend }}</div>
                <div>
                  {{ b.activeCheck ? $t('webService.healthActive') : $t('webService.healthPassive')
                  }}<template v-if="b.lastCheck"> · {{ formatTime(b.lastCheck * 1000) }}</template>
                </div>
                <div v-if="b.lastError" style="max-width: 260px; white-space: normal">{{ b.lastError }}</div>
              </template>
              <el-tag :type="b.up ? 'success' : 'danger'" size="small" effect="plain" class="backend-tag">
                {{ shortBackend(b.backend) }}<template v-if="b.latencyMs"> · {{ b.latencyMs }}ms</template>
              </el-tag>
            </el-tooltip>
          </div>
          <div class="rule-actions">
            <el-switch :model-value="rule.enabled" @change="toggleRule(rule)" />
            <el-button link type="primary" @click="openRuleDialog(rule, index)">{{ $t('common.edit') }}</el-button>
            <el-popconfirm :title="$t('webService.deleteRuleConfirm')" @confirm="deleteRule(rule)">
              <template #reference
                ><el-button link type="danger">{{ $t('common.delete') }}</el-button></template
              >
            </el-popconfirm>
          </div>
        </article>
      </div>
      <el-empty v-else :description="$t('webService.emptySubRules')" :image-size="58">
        <el-button type="primary" @click="openRuleDialog()">{{ $t('webService.addRule') }}</el-button>
      </el-empty>
    </section>

    <!-- 站点编辑对话框 / 子规则编辑对话框 -->
    <SiteDialog ref="siteDialogRef" :certs="certs" @saved="onSiteSaved" />
    <RuleEditor ref="ruleEditorRef" :site="activeSite" @saved="onRuleSaved" />

    <!-- 日志抽屉 -->
    <el-drawer
      v-model="logsDrawer.visible"
      :title="$t('webService.siteLogsTitle', { name: logsDrawer.name })"
      size="560px"
    >
      <div class="log-toolbar">
        <el-button size="small" @click="loadLogs">{{ $t('common.refresh') }}</el-button>
      </div>
      <div class="log-list">
        <el-empty v-if="!logsDrawer.logs.length" :description="$t('webService.noLogs')" :image-size="60" />
        <div v-for="(line, i) in logsDrawer.logs" :key="i" class="log-line">{{ line }}</div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'
import { calculateTrafficRates } from '../utils/traffic'
import { seriesToChartData } from '../utils/series'
import { useIntervalFn } from '../composables/useIntervalFn'
import SiteTrafficChart from '../components/SiteTrafficChart.vue'
import SiteDialog from '../components/webservice/SiteDialog.vue'
import RuleEditor from '../components/webservice/RuleEditor.vue'

const { t } = useI18n()

const sites = ref([])
const certs = ref([])
const loading = ref(false)
const selectedSiteId = ref('')
const stats = ref({})
const rates = ref({})
const activeSite = computed(() => sites.value.find((site) => site.id === selectedSiteId.value) || null)
const enabledRuleCount = computed(() => (activeSite.value?.rules || []).filter((rule) => rule.enabled).length)
const emptyStats = () => ({
  requests: 0,
  bytesIn: 0,
  bytesOut: 0,
  active: 0,
  status1xx: 0,
  status2xx: 0,
  status3xx: 0,
  status4xx: 0,
  status5xx: 0,
  rules: {}
})
const siteStats = computed(() => stats.value[selectedSiteId.value] || emptyStats())
const siteRate = computed(() => rates.value[selectedSiteId.value] || { bytesIn: 0, bytesOut: 0 })
const siteErrors = computed(() => (siteStats.value.status4xx || 0) + (siteStats.value.status5xx || 0))

function siteStatusText(row) {
  if (!row.enabled) return t('webService.statusDisabled')
  const map = {
    listening: t('webService.statusListening'),
    error: t('webService.statusError'),
    stopped: t('webService.statusStopped')
  }
  return map[row.status] || row.status || '-'
}
function siteStatusType(row) {
  if (!row.enabled) return 'info'
  return row.status === 'listening' ? 'success' : row.status === 'error' ? 'danger' : 'info'
}

function ruleTypeText(type) {
  return ['reverse', 'redirect', 'fileserver'].includes(type)
    ? t(`webService.type${type[0].toUpperCase()}${type.slice(1)}`)
    : type
}
function ruleTarget(rule) {
  if (rule.type === 'reverse') return (rule.backends || []).join(', ')
  if (rule.type === 'redirect') return `${rule.redirectUrl || '-'} (${rule.redirectCode || 302})`
  return rule.rootDir || '-'
}
function fmtBytes(value = 0) {
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(2)} MB`
  return `${(value / 1024 ** 3).toFixed(2)} GB`
}
function fmtRate(value = 0) {
  return `${fmtBytes(Math.max(0, Math.round(value)))}/s`
}
function ruleStats(ruleID) {
  return siteStats.value.rules?.[ruleID] || emptyStats()
}
function ruleRate(ruleID) {
  return siteRate.value.rules?.[ruleID] || { bytesIn: 0, bytesOut: 0 }
}
function ruleErrors(ruleID) {
  const value = ruleStats(ruleID)
  return (value.status4xx || 0) + (value.status5xx || 0)
}
function ruleBackends(ruleID) {
  return ruleStats(ruleID).backends || []
}
function shortBackend(backend) {
  const text = String(backend || '')
  return text.length > 26 ? `${text.slice(0, 24)}…` : text
}

// ---------- 站点流量图表 ----------
const chartRange = ref('24h')
const chartData = ref(null)
const chartLoading = ref(false)
async function loadSeries() {
  if (!selectedSiteId.value) {
    chartData.value = null
    return
  }
  chartLoading.value = true
  try {
    const res = await request.get(`/api/sites/${selectedSiteId.value}/series`, { params: { range: chartRange.value } })
    chartData.value = seriesToChartData(res.data)
  } catch {
    chartData.value = null
  } finally {
    chartLoading.value = false
  }
}
watch(selectedSiteId, loadSeries)
function securityLabel(rule) {
  const count = [
    rule.basicAuth,
    rule.ipListMode,
    rule.uaListMode,
    rule.rateLimitRPS > 0,
    rule.maxRequestBodyMiB > 0,
    Object.keys(rule.headers || {}).length > 0
  ].filter(Boolean).length
  return count ? t('webService.protectionCount', { n: count }) : t('webService.noProtection')
}

// ---------- 站点对话框 ----------
const siteDialogRef = ref()
function openSiteDialog(row) {
  siteDialogRef.value?.open(row)
}
function onSiteSaved(createdId) {
  if (createdId) selectedSiteId.value = createdId
  load()
}

async function toggleSite(row) {
  try {
    await request.post(`/api/sites/${row.id}/toggle`)
    load()
  } catch {
    // 拦截器已提示；刷新真实状态，避免开关停留在错误位置
    load()
  }
}

async function deleteSite(row) {
  try {
    await request.delete(`/api/sites/${row.id}`)
    ElMessage.success(t('common.deleted'))
    load()
  } catch {
    // 拦截器已提示
  }
}

// ---------- 子规则对话框 ----------
const ruleEditorRef = ref()
function openRuleDialog(rule, index = -1) {
  ruleEditorRef.value?.open(rule, index)
}
function onRuleSaved() {
  load(false)
}

async function toggleRule(rule) {
  try {
    await request.post(`/api/sites/${activeSite.value.id}/rules/${rule.id}/toggle`)
  } finally {
    await load(false)
  }
}

async function deleteRule(rule) {
  await request.delete(`/api/sites/${activeSite.value.id}/rules/${rule.id}`)
  ElMessage.success(t('common.deleted'))
  await load(false)
}

const dragIndex = ref(-1)
function startDrag(index) {
  dragIndex.value = index
}
async function dropRule(targetIndex) {
  const sourceIndex = dragIndex.value
  dragIndex.value = -1
  if (sourceIndex < 0 || sourceIndex === targetIndex || !activeSite.value) return
  const reordered = [...activeSite.value.rules]
  const [moved] = reordered.splice(sourceIndex, 1)
  reordered.splice(targetIndex, 0, moved)
  activeSite.value.rules = reordered
  try {
    await request.put(`/api/sites/${activeSite.value.id}/rules/order`, { ruleIds: reordered.map((rule) => rule.id) })
    ElMessage.success(t('webService.orderSaved'))
  } catch {
    await load(false)
  }
}

// ---------- 日志抽屉 ----------
const logsDrawer = reactive({ visible: false, name: '', id: '', logs: [] })

function openLogs(row) {
  logsDrawer.id = row.id
  logsDrawer.name = row.name
  logsDrawer.visible = true
  loadLogs()
}

async function loadLogs() {
  try {
    const res = await request.get('/api/logs', { params: { entityId: logsDrawer.id, limit: 200 } })
    logsDrawer.logs = (res.data?.entries || []).map(
      (entry) => `${formatTime(entry.time)} [${entry.level}] ${entry.message}`
    )
  } catch {
    logsDrawer.logs = []
  }
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await request.get('/api/sites')
    sites.value = res.data || []
    if (!sites.value.some((site) => site.id === selectedSiteId.value)) {
      selectedSiteId.value = sites.value[0]?.id || ''
    }
  } catch {
    // 拦截器已提示
  } finally {
    if (showLoading) loading.value = false
  }
}

async function loadCerts() {
  try {
    const res = await request.get('/api/certs')
    certs.value = res.data || []
  } catch {
    // 拦截器已提示
  }
}

let previousStats = null
let previousStatsAt = 0
async function loadStats() {
  try {
    const res = await request.get('/api/sites/stats')
    const current = res.data || {}
    const now = Date.now()
    const elapsed = previousStatsAt ? (now - previousStatsAt) / 1000 : 0
    const nextRates = calculateTrafficRates(current, previousStats || {}, elapsed)
    stats.value = current
    rates.value = nextRates
    previousStats = current
    previousStatsAt = now
  } catch {
    // 高频刷新失败时保留最后一次有效快照
  }
}

let seriesTick = 0
const statsPoller = useIntervalFn(() => {
  loadStats()
  // 序列为分钟级数据，随统计刷新每 60s 拉一次即可
  if (++seriesTick % 20 === 0) loadSeries()
}, 3000)
onMounted(async () => {
  await Promise.all([load(), loadCerts()])
  await loadStats()
  loadSeries()
  statsPoller.start()
})
</script>

<style scoped>
.web-service-page {
  display: grid;
  gap: 16px;
  color: var(--ap-text);
}
.sites-panel,
.rules-panel {
  border: 1px solid var(--ap-border);
  border-radius: 16px;
  background: linear-gradient(180deg, var(--ap-card), var(--ap-primary-softer));
  box-shadow: var(--ap-shadow);
}
.sites-panel {
  padding: 20px;
}
.panel-heading,
.site-toolbar,
.site-identity,
.toolbar-actions {
  display: flex;
  align-items: center;
}
.panel-heading,
.site-toolbar {
  justify-content: space-between;
  gap: 16px;
}
.panel-heading h1 {
  margin: 3px 0 0;
  font-size: 22px;
}
.eyebrow {
  color: var(--ap-accent);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}
.site-strip {
  display: flex;
  gap: 10px;
  margin-top: 18px;
  padding-bottom: 3px;
  overflow-x: auto;
}
.site-tab {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 210px;
  padding: 13px 14px;
  border: 1px solid var(--ap-border);
  border-radius: 12px;
  background: var(--ap-card);
  color: var(--ap-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: 0.18s ease;
}
.site-tab:hover {
  border-color: var(--ap-border-hover);
  transform: translateY(-1px);
}
.site-tab.active {
  border-color: var(--ap-accent);
  background: var(--ap-primary-soft);
  box-shadow: inset 0 -3px var(--ap-accent);
}
.site-tab-copy {
  min-width: 0;
  flex: 1;
}
.site-tab-copy b,
.site-tab-copy small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.site-tab-copy small {
  margin-top: 3px;
  color: var(--ap-muted);
  font-size: 11px;
}
.rule-count {
  display: grid;
  place-items: center;
  min-width: 25px;
  height: 25px;
  border-radius: 8px;
  background: var(--ap-primary-soft);
  color: var(--ap-accent);
  font-weight: 700;
}
.status-dot {
  width: 9px;
  height: 9px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--ap-info);
  box-shadow: 0 0 0 3px var(--ap-info-glow);
}
.status-dot.listening {
  background: var(--ap-success);
  box-shadow: 0 0 0 3px var(--ap-success-glow);
}
.status-dot.error {
  background: var(--ap-danger);
  box-shadow: 0 0 0 3px var(--ap-danger-glow);
}
.status-dot.large {
  width: 11px;
  height: 11px;
}
.rules-panel {
  overflow: hidden;
}
.site-toolbar {
  padding: 18px 20px;
  border-bottom: 1px solid var(--ap-border);
}
.site-identity {
  gap: 10px;
  min-width: 0;
}
.site-identity h2 {
  margin: 0;
  font-size: 18px;
}
.site-identity div > span {
  display: block;
  margin-top: 2px;
  color: var(--ap-muted);
  font-size: 11px;
}
.toolbar-actions {
  justify-content: flex-end;
  gap: 8px;
  flex-wrap: wrap;
}
.site-summary {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--ap-border);
  overflow-x: auto;
}
.site-summary > span {
  flex: 0 0 auto;
  padding: 7px 10px;
  border: 1px solid var(--ap-border);
  border-radius: 9px;
  background: var(--ap-card);
  color: var(--ap-text-soft);
  font-size: 12px;
}
.site-summary b {
  color: var(--ap-text);
}
.site-summary .traffic-chip {
  margin-left: auto;
}
.traffic-chip small,
.rule-traffic small {
  margin-left: 7px;
  color: var(--ap-muted);
}
.outgoing {
  color: var(--ap-accent) !important;
}
.danger {
  color: var(--ap-danger) !important;
}
.rule-list-heading {
  display: flex;
  justify-content: space-between;
  padding: 16px 20px 10px;
}
.rule-list-heading h3 {
  margin: 0;
  font-size: 15px;
}
.rule-list-heading p {
  margin: 4px 0 0;
  color: var(--ap-muted);
  font-size: 11px;
}
.rule-list {
  padding: 0 12px 14px;
  overflow-x: auto;
}
.rule-row {
  display: grid;
  grid-template-columns: 30px 130px 100px minmax(220px, 1.5fr) minmax(250px, 1fr) 90px 175px;
  align-items: center;
  gap: 10px;
  min-width: 1080px;
  min-height: 62px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--ap-border);
  background: var(--ap-card);
  transition: 0.15s ease;
}
.rule-row:first-child {
  border-radius: 11px 11px 0 0;
}
.rule-row:last-child {
  border-bottom: 0;
  border-radius: 0 0 11px 11px;
}
.rule-row:hover {
  background: var(--ap-primary-softer);
}
.rule-row.disabled {
  opacity: 0.58;
}
.rule-row.dragging {
  background: var(--ap-primary-soft);
  opacity: 0.7;
}
.drag-handle {
  color: var(--ap-info);
  font-size: 18px;
  letter-spacing: -4px;
  cursor: grab;
  user-select: none;
}
.drag-handle:active {
  cursor: grabbing;
}
.rule-name b,
.rule-name small,
.rule-route b,
.rule-route small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rule-name small {
  margin-top: 4px;
  color: var(--ap-accent);
  font-size: 11px;
}
.rule-guard span {
  display: inline-block;
  padding: 5px 8px;
  border-radius: 7px;
  background: var(--ap-primary-soft);
  color: var(--ap-text-soft);
  font-size: 11px;
}
.rule-route small {
  margin-top: 5px;
  color: var(--ap-accent);
}
.rule-traffic {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px;
}
.rule-traffic > span {
  display: flex;
  align-items: center;
  padding: 7px 8px;
  border: 1px solid var(--ap-border);
  border-radius: 8px;
  color: var(--ap-text-soft);
  font-size: 11px;
}
.rule-traffic small {
  margin-left: auto;
}
.rule-health,
.rule-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.rule-health span {
  color: var(--ap-text-soft);
  font-size: 12px;
}
.rule-actions {
  justify-content: flex-end;
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
  border-bottom: 1px solid var(--ap-border-soft);
  color: var(--ap-text);
  word-break: break-all;
}
@media (max-width: 900px) {
  .site-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }
  .toolbar-actions {
    justify-content: flex-start;
  }
  .site-summary .traffic-chip {
    margin-left: 0;
  }
}
@media (max-width: 700px) {
  .sites-panel {
    padding: 15px;
  }
  .panel-heading {
    align-items: flex-start;
  }
  .panel-heading h1 {
    font-size: 19px;
  }
  .site-tab {
    min-width: 185px;
  }
  .site-toolbar {
    padding: 15px;
  }
  .site-summary {
    padding: 10px 14px;
  }
  .rule-list {
    overflow: visible;
  }
  .rule-row {
    grid-template-columns: 22px minmax(0, 1fr) auto;
    gap: 9px;
    min-width: 0;
    margin-bottom: 9px;
    padding: 13px;
    border: 1px solid var(--ap-border);
    border-radius: 11px !important;
  }
  .rule-name {
    grid-column: 2;
  }
  .rule-guard {
    grid-column: 3;
  }
  .rule-route,
  .rule-traffic,
  .rule-health {
    grid-column: 2/-1;
  }
  .rule-actions {
    grid-column: 2/-1;
    justify-content: flex-start;
    padding-top: 4px;
  }
}
.site-chart {
  padding: 12px 18px;
  border-bottom: 1px solid var(--ap-border);
}
.site-chart-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 8px;
  color: var(--ap-text);
  font-size: 13px;
  font-weight: 600;
}
.backend-tag {
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
}
@media (max-width: 700px) {
  .site-chart {
    padding: 10px 14px;
  }
}
</style>
