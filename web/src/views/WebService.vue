<template>
  <div class="web-service-page workspace-chrome" v-loading="loading">
    <section class="sites-panel">
      <div class="panel-heading workspace-toolbar">
        <div>
          <h1 class="workspace-title">{{ $t('webService.title') }}</h1>
        </div>
        <div class="toolbar-actions">
          <el-button @click="poolManagerRef.open()">{{
            locale.startsWith('zh') ? '后端池' : 'Backend pools'
          }}</el-button>
          <el-button @click="refreshWorkspace">{{ $t('common.refresh') }}</el-button>
          <el-button v-if="activeSite" @click="openLogs(activeSite)">{{ $t('common.logs') }}</el-button>
          <el-button type="primary" @click="openSiteDialog()">＋ {{ $t('webService.addSite') }}</el-button>
        </div>
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
          <el-button text @click="chartExpanded = !chartExpanded">{{
            chartExpanded ? labels.hideChart : labels.showChart
          }}</el-button>
          <el-button @click="openSiteDialog(activeSite)">{{ $t('common.edit') }}</el-button>
          <el-button @click="discoveryRef.open()">{{
            locale.startsWith('zh') ? '发现内网服务' : 'Discover LAN services'
          }}</el-button>
          <el-switch
            :aria-label="activeSite.name + ' · ' + labels.enabled"
            :model-value="activeSite.enabled"
            @change="toggleSite(activeSite)"
          />
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

      <div v-if="chartExpanded" class="site-chart">
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
          <p>{{ labels.orderTip }}</p>
        </div>
      </div>
      <div class="group-pills" :aria-label="labels.group">
        <button
          type="button"
          class="status-pill"
          :class="{ active: !groupFilter }"
          :aria-pressed="!groupFilter"
          @click="groupFilter = ''"
        >
          {{ labels.allGroups }} <small>{{ activeSite.rules.length }}</small>
        </button>
        <button
          v-for="group in groups"
          :key="group"
          type="button"
          class="status-pill"
          :class="{ active: groupFilter === group }"
          :aria-pressed="groupFilter === group"
          @click="groupFilter = group"
        >
          {{ group }}
        </button>
      </div>
      <div class="rule-filters">
        <el-input v-model="query" clearable :placeholder="labels.search" :aria-label="labels.search" />
        <el-select v-model="stateFilter" :placeholder="labels.allStates" :aria-label="labels.state"
          ><el-option :label="labels.allStates" value="" /><el-option
            :label="labels.enabled"
            value="enabled" /><el-option :label="labels.disabled" value="disabled"
        /></el-select>
      </div>
      <div class="rule-table-scroll" tabindex="0" role="region" :aria-label="$t('webService.subRules')">
        <div class="rule-table-heading" aria-hidden="true">
          <span></span><span>{{ labels.rule }}</span
          ><span>{{ labels.protection }}</span
          ><span>{{ labels.route }}</span
          ><span>{{ labels.traffic }}</span
          ><span>{{ labels.health }}</span
          ><span>{{ labels.actions }}</span>
        </div>
        <div v-if="filteredRules.length" class="rule-list">
          <article
            v-for="rule in filteredRules"
            :key="rule.id"
            class="rule-row"
            :class="{ disabled: !rule.enabled, dragging: dragIndex === ruleIndex(rule) }"
            @dragover.prevent
            @drop="dropRule(ruleIndex(rule))"
          >
            <span
              class="drag-handle"
              draggable="true"
              :title="$t('webService.dragTip')"
              @dragstart="startDrag(ruleIndex(rule))"
              @dragend="dragIndex = -1"
              >⋮⋮</span
            >
            <div class="rule-name">
              <b :title="rule.name">{{ rule.name }}</b
              ><small
                >{{ ruleTypeText(rule.type) }}<template v-if="rule.group"> · {{ rule.group }}</template></small
              >
            </div>
            <div class="rule-guard">
              <span :title="securityLabel(rule)">{{ securityLabel(rule) }}</span>
            </div>
            <div class="rule-route">
              <b :title="(rule.frontendHost || '*') + (rule.frontendPath || '/')"
                >{{ rule.frontendHost || '*' }}{{ rule.frontendPath || '/' }}</b
              ><small :title="ruleTarget(rule)">{{ ruleTarget(rule) }}</small>
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
                <el-tag
                  :type="b.up ? 'success' : 'danger'"
                  size="small"
                  effect="plain"
                  class="backend-tag"
                  :title="b.backend"
                >
                  {{ shortBackend(b.backend) }}<template v-if="b.latencyMs"> · {{ b.latencyMs }}ms</template>
                </el-tag>
              </el-tooltip>
            </div>
            <div class="rule-actions">
              <el-button
                link
                :disabled="ruleIndex(rule) === 0 || orderSaving"
                :aria-label="labels.moveUp"
                @click="moveRule(ruleIndex(rule), -1)"
                >↑</el-button
              >
              <el-button
                link
                :disabled="ruleIndex(rule) === activeSite.rules.length - 1 || orderSaving"
                :aria-label="labels.moveDown"
                @click="moveRule(ruleIndex(rule), 1)"
                >↓</el-button
              >
              <el-switch
                :aria-label="rule.name + ' · ' + labels.enabled"
                :model-value="rule.enabled"
                @change="toggleRule(rule)"
              />
              <el-button link type="primary" @click="openRuleDialog(rule, ruleIndex(rule))">{{
                $t('common.edit')
              }}</el-button>
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
      </div>
    </section>

    <!-- 站点编辑对话框 / 子规则编辑对话框 -->
    <SiteDialog ref="siteDialogRef" :certs="certs" :policies="policies" @saved="onSiteSaved" />
    <RuleEditor ref="ruleEditorRef" :site="activeSite" :policies="policies" @saved="onRuleSaved" />

    <!-- 日志抽屉 -->
    <el-drawer
      v-model="logsDrawer.visible"
      :title="$t('webService.siteLogsTitle', { name: logsDrawer.name })"
      size="min(560px, 100vw)"
    >
      <div class="log-toolbar">
        <el-button size="small" @click="loadLogs">{{ $t('common.refresh') }}</el-button>
      </div>
      <div class="log-list">
        <el-empty v-if="!logsDrawer.logs.length" :description="$t('webService.noLogs')" :image-size="60" />
        <div v-for="(line, i) in logsDrawer.logs" :key="i" class="log-line">{{ line }}</div>
      </div>
    </el-drawer>
    <BackendPoolManager ref="poolManagerRef" @saved="refreshWorkspace" />
    <ServiceDiscovery ref="discoveryRef" @draft="openRuleDialog($event)" />
  </div>
</template>

<script setup>
import BackendPoolManager from '../components/webservice/BackendPoolManager.vue'
import ServiceDiscovery from '../components/webservice/ServiceDiscovery.vue'
import { computed, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
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

const { t, locale } = useI18n()
const poolManagerRef = ref()
const discoveryRef = ref()
const backendPools = ref([])
async function loadBackendPools() {
  try {
    backendPools.value = (await request.get('/api/backend-pools')).data || []
  } catch {}
}
const labels = computed(() =>
  locale.value.startsWith('zh')
    ? {
        search: '搜索名称、域名、目标',
        group: '分组',
        state: '状态',
        allGroups: '全部分组',
        allStates: '全部状态',
        enabled: '启用',
        disabled: '停用',
        moveUp: '上移规则',
        moveDown: '下移规则',
        orderTip: '同路径优先级按规则顺序；支持拖动或上下排序',
        showChart: '流量趋势',
        hideChart: '收起趋势',
        rule: '规则',
        protection: '防护',
        route: '域名 / 路径 → 目标',
        traffic: '收发流量',
        health: '活动 / 错误',
        actions: '操作',
        local: '规则控制',
        site: '继承站点',
        global: '全局',
        bans: '临时封禁',
        unknown: '防护状态待核查'
      }
    : {
        search: 'Search name, host or target',
        group: 'Group',
        state: 'State',
        allGroups: 'All groups',
        allStates: 'All states',
        enabled: 'Enabled',
        disabled: 'Disabled',
        moveUp: 'Move rule up',
        moveDown: 'Move rule down',
        orderTip: 'Rule order breaks matching path ties. Drag or use arrows.',
        showChart: 'Traffic trend',
        hideChart: 'Hide trend',
        rule: 'Rule',
        protection: 'Protection',
        route: 'Host / path → target',
        traffic: 'Traffic',
        health: 'Active / errors',
        actions: 'Actions',
        local: 'Rule controls',
        site: 'Site policy',
        global: 'Global',
        bans: 'Temporary bans',
        unknown: 'Protection status unavailable'
      }
)
const query = ref(''),
  groupFilter = ref(''),
  stateFilter = ref(''),
  policies = ref([]),
  protectionSummary = ref({}),
  protectionKnown = ref(false)
const groups = computed(() => [...new Set((activeSite.value?.rules || []).map((r) => r.group).filter(Boolean))])
const filteredRules = computed(() =>
  (activeSite.value?.rules || []).filter(
    (r) =>
      (!groupFilter.value || r.group === groupFilter.value) &&
      (!stateFilter.value || r.enabled === (stateFilter.value === 'enabled')) &&
      (!query.value.trim() ||
        [r.name, r.frontendHost, r.frontendPath, ruleTarget(r), r.group]
          .join(' ')
          .toLowerCase()
          .includes(query.value.trim().toLowerCase()))
  )
)
const ruleIndex = (rule) => activeSite.value?.rules.findIndex((r) => r.id === rule.id) ?? -1

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
  if (type === 'webdav') return 'WebDAV'
  if (type === 'cgi') return 'CGI'
  return ['reverse', 'redirect', 'fileserver'].includes(type)
    ? t(`webService.type${type[0].toUpperCase()}${type.slice(1)}`)
    : type
}
function ruleTarget(rule) {
  if (rule.type === 'cgi') return `${rule.rootDir || ''}/${rule.cgiEntry || ''}`
  if (rule.type === 'reverse')
    return rule.backendPoolId
      ? (locale.value.startsWith('zh') ? '后端池 · ' : 'Pool · ') +
          (backendPools.value.find((p) => p.id === rule.backendPoolId)?.name || rule.backendPoolId)
      : (rule.backends || []).join(', ')
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
const chartExpanded = ref(false)
let seriesRequest = 0
onBeforeUnmount(() => {
  seriesRequest++
})
const chartRange = ref('24h')
const chartData = ref(null)
const chartLoading = ref(false)
async function loadSeries() {
  if (!selectedSiteId.value || !chartExpanded.value) return
  const requestID = ++seriesRequest,
    siteID = selectedSiteId.value,
    range = chartRange.value
  const current = () =>
    requestID === seriesRequest && siteID === selectedSiteId.value && range === chartRange.value && chartExpanded.value
  chartLoading.value = true
  try {
    const res = await request.get(`/api/sites/${siteID}/series`, { params: { range } })
    if (current()) chartData.value = seriesToChartData(res.data)
  } catch {
    if (current()) chartData.value = null
  } finally {
    if (current()) chartLoading.value = false
  }
}
watch(selectedSiteId, () => {
  groupFilter.value = ''
  seriesRequest++
  chartData.value = null
  chartLoading.value = false
  loadSeries()
})
watch(chartExpanded, (expanded) => {
  if (expanded) loadSeries()
  else {
    seriesRequest++
    chartLoading.value = false
  }
})
function activePolicy(id) {
  const policy = policies.value.find((p) => p.id === id)
  return (
    policy?.enabled &&
    ((['whitelist', 'blacklist'].includes(policy.ipListMode) && policy.ipList?.length) ||
      (['whitelist', 'blacklist'].includes(policy.uaListMode) && policy.uaList?.length) ||
      policy.waf?.enabled ||
      policy.cc?.enabled)
  )
}
function securityLabel(rule) {
  const local = [
    rule.basicAuth || rule.accessGroupId,
    ['whitelist', 'blacklist'].includes(rule.ipListMode),
    ['whitelist', 'blacklist'].includes(rule.uaListMode),
    rule.rateLimitRPS > 0,
    rule.maxRequestBodyMiB > 0
  ].filter(Boolean).length
  const parts = []
  if (local) parts.push(`${labels.value.local} ${local}`)
  if (!protectionKnown.value) {
    parts.push(labels.value.unknown)
    return parts.join(' · ')
  }
  if (activePolicy(rule.policyId)) parts.push(labels.value.protection)
  if (activePolicy(activeSite.value?.policyId)) parts.push(labels.value.site)
  const globals = policies.value.filter((p) => p.global && activePolicy(p.id)).length
  if (globals) parts.push(`${labels.value.global} ${globals}`)
  if (protectionSummary.value.activeBans) parts.push(`${labels.value.bans} ${protectionSummary.value.activeBans}`)
  return parts.join(' · ') || t('webService.noProtection')
}
async function refreshWorkspace() {
  await Promise.all([load(), loadCerts(), loadPolicies(), loadBackendPools(), loadStats(), loadSeries()])
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
    await request.post(`/api/sites/${row.id}/toggle`, null, { headers: { 'If-Match': String(row.revision ?? 0) } })
    load()
  } catch {
    // 拦截器已提示；刷新真实状态，避免开关停留在错误位置
    load()
  }
}

async function deleteSite(row) {
  try {
    await request.delete(`/api/sites/${row.id}`, { headers: { 'If-Match': String(row.revision ?? 0) } })
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
    await request.post(`/api/sites/${activeSite.value.id}/rules/${rule.id}/toggle`, null, {
      headers: { 'If-Match': String(rule.revision ?? 0) }
    })
  } finally {
    await load(false)
  }
}

async function deleteRule(rule) {
  await request.delete(`/api/sites/${activeSite.value.id}/rules/${rule.id}`, {
    headers: { 'If-Match': String(rule.revision ?? 0) }
  })
  ElMessage.success(t('common.deleted'))
  await load(false)
}

const dragIndex = ref(-1)
function startDrag(index) {
  dragIndex.value = index
}
const orderSaving = ref(false)
async function dropRule(targetIndex) {
  const sourceIndex = dragIndex.value
  dragIndex.value = -1
  await reorder(sourceIndex, targetIndex)
}
function moveRule(index, direction) {
  return reorder(index, index + direction)
}
async function reorder(sourceIndex, targetIndex) {
  const site = activeSite.value
  if (
    orderSaving.value ||
    !site ||
    sourceIndex < 0 ||
    targetIndex < 0 ||
    targetIndex >= site.rules.length ||
    sourceIndex === targetIndex
  )
    return
  const reordered = [...site.rules]
  const [moved] = reordered.splice(sourceIndex, 1)
  reordered.splice(targetIndex, 0, moved)
  orderSaving.value = true
  try {
    await request.put(`/api/sites/${site.id}/rules/order`, {
      ruleIds: reordered.map((rule) => rule.id),
      expectedRevision: site.revision ?? 0
    })
    ElMessage.success(t('webService.orderSaved'))
  } finally {
    await load(false)
    orderSaving.value = false
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

let protectionLoading = false
async function loadPolicies() {
  if (protectionLoading) return
  protectionLoading = true
  try {
    const results = await Promise.allSettled([
      request.get('/api/protection/policies'),
      request.get('/api/protection/summary')
    ])
    protectionKnown.value = results.every((r) => r.status === 'fulfilled')
    if (results[0].status === 'fulfilled') policies.value = results[0].value.data || []
    if (results[1].status === 'fulfilled') protectionSummary.value = results[1].value.data || {}
  } finally {
    protectionLoading = false
  }
}
const protectionPoll = useIntervalFn(loadPolicies, 10000)

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
const statsPoller = useIntervalFn(async () => {
  await loadStats()
  // 序列为分钟级数据，随统计刷新每 60s 拉一次即可
  if (++seriesTick % 20 === 0) await loadSeries()
}, 3000)
onMounted(async () => {
  await Promise.all([load(), loadCerts(), loadPolicies(), loadBackendPools()])
  protectionPoll.start()
  await loadStats()
  loadSeries()
  statsPoller.start()
})
</script>

<style scoped>
.web-service-page {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 16px;
  color: var(--ap-text);
}
.sites-panel,
.rules-panel {
  min-width: 0;
  width: 100%;
  border: 1px solid var(--ap-border);
  border-radius: 16px;
  background: var(--ap-card);
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
.rule-filters {
  display: flex;
  gap: 10px;
  padding: 0 20px 14px;
  flex-wrap: wrap;
}
.rule-filters > :first-child {
  flex: 1;
  min-width: 180px;
}
.rule-filters .el-select {
  width: 140px;
}
.rule-actions .el-button {
  min-height: 44px;
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
  grid-template-columns: 30px 130px 100px minmax(220px, 1.5fr) minmax(250px, 1fr) 90px 230px;
  align-items: center;
  gap: 10px;
  min-width: 1130px;
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
  min-width: 0;
  max-width: min(200px, 100%);
}
.backend-tag :deep(.el-tag__content) {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 700px) {
  .site-chart {
    padding: 10px 14px;
  }
}

/* Dense workspace: primary controls and rules remain on the first screen. */
.web-service-page {
  gap: 10px;
}
.sites-panel,
.rules-panel {
  border-radius: 12px;
  box-shadow: var(--ap-shadow);
}
.sites-panel {
  padding: 12px 16px;
}
.panel-heading h1 {
  font-size: 20px;
  line-height: 26px;
  font-weight: 650;
  margin: 0;
}
.site-strip {
  gap: 6px;
  margin-top: 12px;
}
.site-tab {
  min-width: 152px;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 6px;
}
.site-tab:hover {
  transform: none;
}
.rule-count {
  min-width: 20px;
  height: 20px;
  border-radius: 4px;
  font-size: 11px;
}
.site-toolbar {
  padding: 10px 16px;
}
.site-identity h2 {
  font-size: 15px;
}
.site-summary {
  padding: 8px 16px;
  gap: 14px;
}
.site-summary > span {
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
}
.rule-list-heading {
  padding: 12px 16px 6px;
}
.rule-list-heading h3 {
  font-size: 13px;
}
.group-pills {
  display: flex;
  gap: 6px;
  padding: 4px 16px 10px;
  overflow-x: auto;
}
.group-pills .status-pill {
  font: inherit;
  white-space: nowrap;
  cursor: pointer;
  border: 1px solid var(--ap-border);
  background: transparent;
  color: var(--ap-text-soft);
  border-radius: 16px;
  padding: 4px 10px;
}
.group-pills .status-pill.active {
  border-color: var(--ap-accent);
  color: var(--ap-accent);
  background: var(--ap-primary-soft);
}
.group-pills small {
  margin-left: 4px;
}
.rule-filters {
  padding: 0 16px 12px;
  gap: 8px;
}
.rule-table-heading,
.rule-row {
  grid-template-columns:
    20px minmax(95px, 0.7fr) minmax(115px, 0.8fr) minmax(190px, 1.5fr) minmax(175px, 1fr)
    100px 175px;
  min-width: 940px;
  gap: 8px;
}
.rule-table-heading {
  display: grid;
  margin: 0 16px;
  padding: 7px 8px;
  background: var(--ap-surface, var(--ap-card));
  border-block: 1px solid var(--ap-border);
  font-size: 12px;
  color: var(--ap-muted);
}
.rule-list {
  padding: 0 16px 10px;
  overflow: visible;
}
.rule-table-scroll {
  overflow-x: auto;
}
.rule-row {
  min-height: 56px;
  padding: 6px 8px;
}
.rule-actions {
  gap: 5px;
}
.rule-actions .el-button {
  min-height: 32px;
}
.rule-traffic > span {
  padding: 0;
  border: 0;
  border-radius: 0;
  font-size: 11px;
}
.rule-traffic {
  grid-template-columns: 1fr;
  gap: 3px;
}
.rule-guard span {
  max-width: 100%;
  padding: 3px 5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rule-health {
  flex-wrap: wrap;
  gap: 5px;
}
@media (max-width: 1100px) and (min-width: 701px) {
  .site-toolbar,
  .site-summary,
  .rule-list-heading,
  .rule-filters,
  .group-pills {
    min-width: 0;
  }
  .rule-table-heading {
    min-width: 940px;
  }
}
@media (max-width: 700px) {
  .rule-table-scroll {
    overflow: visible;
  }
  .sites-panel {
    padding: 12px;
  }
  .panel-heading {
    flex-wrap: wrap;
  }
  .toolbar-actions {
    gap: 5px;
  }
  .toolbar-actions .el-button {
    min-height: 36px;
  }
  .site-tab {
    min-width: 142px;
    min-height: 44px;
  }
  .site-toolbar {
    padding: 10px 12px;
  }
  .site-summary {
    padding: 8px 12px;
  }
  .group-pills {
    padding-inline: 12px;
  }
  .group-pills .status-pill {
    min-height: 36px;
  }
  .rule-list {
    padding: 0 12px 10px;
  }
  .rule-table-heading {
    display: none;
  }
  .rule-row {
    grid-template-columns: 18px minmax(0, 1fr) auto;
    min-width: 0;
    gap: 7px;
    padding: 10px;
    margin-bottom: 7px;
    border-radius: 6px !important;
  }
  .rule-actions .el-button {
    min-height: 44px;
  }
  .rule-traffic {
    grid-template-columns: 1fr 1fr;
  }
  .rule-guard {
    max-width: 140px;
  }
  .rule-filters {
    padding: 0 12px 10px;
  }
}
</style>
