<template>
  <div class="page-grid overview-workspace" v-loading="loading">
    <header class="overview-heading">
      <div>
        <h1 class="workspace-title">{{ t('nav.dashboard') }}</h1>
        <span class="muted"
          >{{ t('dashboard.version') }} {{ data.version || '-' }} · {{ t('dashboard.systemInfo') }}
          {{ sys.goos || '-' }} / {{ sys.goarch || '-' }}</span
        >
      </div>
      <div class="workspace-tools">
        <small class="muted">{{ metrics.time ? formatTime(metrics.time) : '-' }}</small
        ><el-button @click="toggleRefresh">{{ refreshPaused ? w('resume') : w('pause') }}</el-button
        ><el-button @click="refreshAll">{{ t('common.refresh') }}</el-button>
      </div>
    </header>
    <el-alert
      v-if="!data.adminHttps"
      type="error"
      :title="t('dashboard.adminHttpAlert')"
      :closable="false"
      show-icon
      class="compact-alert"
    />
    <el-alert
      v-if="partialError || metricError"
      :title="w('partialError')"
      type="warning"
      :closable="false"
      show-icon
    />
    <section class="resource-cards" :aria-label="w('resources')">
      <article class="metric-card">
        <div class="metric-label">
          {{ w('hostCpu') }} <el-icon><Odometer /></el-icon>
        </div>
        <strong :class="{ 'metric-null': metrics.hostCpu == null }" class="numeric">{{
          percent(metrics.hostCpu)
        }}</strong>
        <div class="metric-progress">
          <span v-if="metrics.hostCpu != null" :style="{ width: Math.max(0, Math.min(100, metrics.hostCpu)) + '%' }" />
        </div>
        <footer>{{ w('hostSample') }}</footer>
      </article>
      <article class="metric-card">
        <div class="metric-label">
          {{ w('hostMemory') }} <el-icon><Monitor /></el-icon>
        </div>
        <strong :class="{ 'metric-null': metrics.memoryPercent == null }" class="numeric">{{
          metrics.memoryPercent == null ? w('unavailable') : fmtBytes(metrics.memoryUsed)
        }}</strong>
        <div class="metric-progress memory-progress">
          <span
            v-if="metrics.memoryPercent != null"
            :style="{ width: Math.max(0, Math.min(100, metrics.memoryPercent)) + '%' }"
          />
        </div>
        <footer>
          <span>{{ percent(metrics.memoryPercent) }}</span
          ><span>{{ metrics.memoryTotal ? fmtBytes(metrics.memoryTotal) : '-' }}</span>
        </footer>
      </article>
      <article class="metric-card">
        <div class="metric-label">
          {{ w('heap') }} <el-icon><Connection /></el-icon>
        </div>
        <strong :class="{ 'metric-null': metrics.processHeap == null }" class="numeric">{{
          metrics.processHeap == null ? w('unavailable') : fmtBytes(metrics.processHeap)
        }}</strong>
        <div class="metric-divider" />
        <footer>
          {{ w('goroutines') }} <b class="numeric">{{ metrics.goroutines ?? '-' }}</b>
        </footer>
      </article>
      <article class="metric-card">
        <div class="metric-label">
          {{ w('protection') }} <el-icon><Lock /></el-icon>
        </div>
        <strong class="numeric">{{ workspace.protection.rejected ?? 0 }}</strong>
        <div class="metric-divider" />
        <footer>
          <span>{{ w('ban') }}</span
          ><b class="numeric">{{ workspace.protection.activeBans ?? 0 }}</b>
        </footer>
      </article>
    </section>
    <div class="overview-charts">
      <section class="trend-panel workspace-surface">
        <div class="trend-heading">
          <h2>{{ w('history') }}</h2>
          <el-radio-group v-model="resourceRange" size="small" @change="refreshHistory"
            ><el-radio-button value="1h">1h</el-radio-button
            ><el-radio-button value="24h">24h</el-radio-button></el-radio-group
          >
        </div>
        <div class="trend-facts">
          <span
            >{{ w('hostCpu') }} <b>{{ percent(metrics.hostCpu) }}</b></span
          ><span
            >{{ w('hostMemory') }} <b>{{ percent(metrics.memoryPercent) }}</b></span
          >
        </div>
        <ResourceChart
          :points="resourceHistory"
          :sample-seconds="resourceRange === '24h' ? 60 : 5"
          :label="w('history')"
        />
        <div class="history-storage">
          <el-switch
            :model-value="historyPersistent"
            :loading="historySaving"
            :active-text="locale.startsWith('zh') ? '重启后保留历史' : 'Keep history after restart'"
            @change="setHistoryPersistence"
          />
        </div>
        <p class="muted sample-note">
          {{
            historyPersistent
              ? locale.startsWith('zh')
                ? '每分钟持久化，保留1小时实时及24小时分钟样本；文件最多2MiB。主机指标仅Linux支持。'
                : 'Saved once per minute: 1h realtime and 24h minute samples, up to 2 MiB. Host metrics require Linux.'
              : w('memoryHint')
          }}
        </p>
        <p v-if="historyStorageError" class="danger sample-note">{{ historyStorageError }}</p>
      </section>
      <section class="trend-panel workspace-surface network-panel">
        <div class="trend-heading">
          <h2>{{ w('networkHistory') }}</h2>
          <small class="muted">{{ metrics.interface || '-' }}</small>
        </div>
        <div class="trend-facts">
          <span
            >{{ t('dashboard.trafficIn') }}
            <b :class="{ 'metric-null': metrics.bytesInPerSecond == null }">{{
              rate(metrics.bytesInPerSecond)
            }}</b></span
          ><span
            >{{ t('dashboard.trafficOut') }}
            <b :class="{ 'metric-null': metrics.bytesOutPerSecond == null }">{{
              rate(metrics.bytesOutPerSecond)
            }}</b></span
          >
        </div>
        <ResourceChart
          network
          :points="resourceHistory"
          :sample-seconds="resourceRange === '24h' ? 60 : 5"
          :label="w('networkHistory')"
        />
        <p class="muted sample-note">
          {{ w('network') }} ·
          {{ !metrics.networkSupported || metrics.received == null ? w('unavailable') : fmtBytes(metrics.received) }} /
          {{ !metrics.networkSupported || metrics.sent == null ? w('unavailable') : fmtBytes(metrics.sent) }}
        </p>
      </section>
    </div>
    <section class="services-section">
      <div class="section-heading">
        <h2>{{ w('services') }}</h2>
        <span class="muted">{{ cards.length }} {{ w('modules') }}</span>
      </div>
      <div class="stats-grid">
        <button v-for="item in cards" :key="item.path" class="stat-card" @click="router.push(item.path)">
          <span class="stat-label"><NavIcon :name="item.icon" />{{ item.label }}</span
          ><strong>{{ item.value }}</strong
          ><small>{{ item.sub }}</small
          ><el-icon class="stat-arrow"><ArrowRight /></el-icon>
        </button>
      </div>
      <div class="ddns-address-strip">
        <span>{{ w('ddns') }}</span
        ><template v-for="task in workspace.ddns" :key="task.id"
          ><span class="address-item"
            ><b>{{ task.name }}</b
            ><template v-if="!task.enabled"> · {{ t('common.tagDisabled') }}</template
            ><template v-else-if="task.status?.records?.length"
              ><span v-for="record in task.status.records" :key="record.recordId">
                · {{ record.type }} {{ record.ip || '-' }}</span
              ></template
            ><template v-else> · {{ task.status?.ip || w('unavailable') }}</template></span
          ></template
        ><span v-if="!workspace.ddns.length" class="muted">{{ w('none') }}</span>
      </div>
    </section>
    <section v-if="data.issues.length" class="issues">
      <button v-for="item in data.issues" :key="item.module + item.id + item.message" @click="router.push(item.path)">
        <el-tag type="warning">{{ item.module }}</el-tag
        ><span class="truncate">{{ item.message || t('dashboard.moduleNotRunning') }}</span
        ><el-icon><ArrowRight /></el-icon>
      </button>
    </section>

    <div class="two-columns">
      <el-card>
        <template #header
          ><div class="card-header">
            <span>{{ $t('dashboard.systemInfo') }}</span
            ><el-button text :icon="Refresh" @click="load">{{ $t('common.refresh') }}</el-button>
          </div></template
        >
        <div class="info-list">
          <div>
            <span>{{ $t('dashboard.platform') }}</span
            ><b>{{ sys.goos || '-' }} / {{ sys.goarch || '-' }}</b>
          </div>
          <div>
            <span>{{ $t('dashboard.adminProtocol') }}</span
            ><el-tag :type="data.adminHttps ? 'success' : 'danger'">{{ data.adminHttps ? 'HTTPS' : 'HTTP' }}</el-tag>
          </div>
          <div>
            <span>{{ $t('dashboard.accountSecurity') }}</span
            ><el-tag :type="data.mustChangePassword ? 'warning' : data.totpEnabled ? 'success' : 'info'">{{
              data.mustChangePassword
                ? $t('dashboard.needChangePassword')
                : data.totpEnabled
                  ? $t('dashboard.totpEnabled')
                  : $t('dashboard.passwordOnly')
            }}</el-tag>
          </div>
        </div>
      </el-card>
      <el-card>
        <template #header
          ><div class="card-header">
            <span>{{ $t('dashboard.firewallTitle') }}</span
            ><el-tag :type="data.firewall.openwrt ? 'success' : 'info'">{{
              data.firewall.openwrt ? 'OpenWrt' : $t('dashboard.nonOpenwrt')
            }}</el-tag>
          </div></template
        >
        <div class="big-number">{{ data.firewall.rules.length }}</div>
        <p class="muted">{{ $t('dashboard.firewallRules') }}</p>
        <div class="tag-row">
          <el-tag v-for="r in data.firewall.rules.slice(0, 6)" :key="r.key" effect="plain"
            >{{ r.port }}/{{ r.proto }}</el-tag
          ><el-empty
            v-if="!data.firewall.rules.length"
            :description="$t('dashboard.noFirewallRules')"
            :image-size="48"
          />
        </div>
      </el-card>
    </div>

    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('dashboard.trafficTitle') }}</span
          ><el-tag type="info" effect="plain">{{ $t('dashboard.memoryStats') }}</el-tag>
        </div></template
      >
      <div v-if="data.sites.length" class="traffic-list">
        <div v-for="s in data.sites" :key="s.id">
          <span class="truncate" :title="s.name">{{ s.name || s.id }}</span>
          <span class="traffic-num"
            >{{ $t('dashboard.requests') }} <b>{{ s.requests }}</b></span
          >
          <span class="traffic-num">{{ w('active') }} {{ s.active || 0 }}</span>
          <span class="traffic-num">{{ $t('dashboard.trafficIn') }} {{ fmtBytes(s.bytesIn) }}</span>
          <span class="traffic-num">{{ $t('dashboard.trafficOut') }} {{ fmtBytes(s.bytesOut) }}</span>
          <span class="tag-row">
            <el-tag v-if="s.status2xx" type="success" size="small" effect="plain">2xx {{ s.status2xx }}</el-tag>
            <el-tag v-if="s.status3xx" type="info" size="small" effect="plain">3xx {{ s.status3xx }}</el-tag>
            <el-tag v-if="s.status4xx" type="warning" size="small" effect="plain">4xx {{ s.status4xx }}</el-tag>
            <el-tag v-if="s.status5xx" type="danger" size="small" effect="plain">5xx {{ s.status5xx }}</el-tag>
            <el-tag v-if="s.status1xx" size="small" effect="plain">1xx {{ s.status1xx }}</el-tag>
            <span v-if="!s.requests" class="muted">{{ $t('dashboard.noRequests') }}</span>
          </span>
        </div>
      </div>
      <el-empty v-else :description="$t('dashboard.noTraffic')" :image-size="54" />
    </el-card>

    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('dashboard.chartTitle') }}</span>
          <div class="chart-controls">
            <el-select
              v-model="chartSiteId"
              size="small"
              style="width: 180px"
              :placeholder="$t('dashboard.chartSitePlaceholder')"
              @change="loadSeries"
            >
              <el-option v-for="s in data.sites" :key="s.id" :label="s.name || s.id" :value="s.id" />
            </el-select>
            <el-radio-group v-model="chartRange" size="small" @change="loadSeries">
              <el-radio-button value="1h">1h</el-radio-button>
              <el-radio-button value="6h">6h</el-radio-button>
              <el-radio-button value="24h">24h</el-radio-button>
            </el-radio-group>
          </div>
        </div>
      </template>
      <div v-loading="chartLoading">
        <SiteTrafficChart v-if="chartSiteId" :data="chartData" />
        <el-empty v-else :description="$t('dashboard.noTraffic')" :image-size="54" />
      </div>
    </el-card>

    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('dashboard.recentErrors') }}</span
          ><el-button text @click="router.push('/logs')">{{ $t('dashboard.goLogs') }}</el-button>
        </div></template
      >
      <div v-if="data.recentErrors.length" class="recent-errors">
        <div v-for="e in data.recentErrors.slice(0, 5)" :key="e.time + e.message">
          <el-tag type="danger" size="small">{{ e.source }}</el-tag
          ><span class="truncate" :title="e.message">{{ e.message }}</span
          ><time>{{ formatTime(e.time) }}</time>
        </div>
      </div>
      <el-empty v-else :description="$t('dashboard.noErrors')" :image-size="54" />
    </el-card>
  </div>
</template>

<script setup>
import { computed, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowRight, Compass, Connection, Lock, Monitor, Odometer, Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import NavIcon from '../components/NavIcon.vue'
import { formatTime, formatBytes as fmtBytes } from '../utils/format'
import { seriesToChartData, topSitesByRequests } from '../utils/series'
import { useIntervalFn } from '../composables/useIntervalFn'
import SiteTrafficChart from '../components/SiteTrafficChart.vue'
import ResourceChart from '../components/ResourceChart.vue'

const { t, locale } = useI18n()
const w = (k) => t(`workspace.dashboard.${k}`)
const metricError = ref(false),
  partialError = ref(false),
  metrics = reactive({ hostCpu: null, memoryPercent: null, processHeap: null }),
  workspace = reactive({ ddns: [], certs: [], protection: {} }),
  resourceRange = ref('1h'),
  resourceHistory = ref([])
const historyPersistent = ref(false),
  historySaving = ref(false),
  historyStorageError = ref('')
async function setHistoryPersistence(enabled) {
  historySaving.value = true
  try {
    await request.post('/api/metrics/history/settings', { enabled })
    historyPersistent.value = enabled
    await loadMetrics()
  } catch {
  } finally {
    historySaving.value = false
  }
}
const nextCert = computed(
  () =>
    workspace.certs
      .filter((c) => c.enabled && c.remainingDays != null)
      .sort((a, b) => a.remainingDays - b.remainingDays)[0]
)
const percent = (x) => (x == null ? w('unavailable') : Number(x).toFixed(1) + '%')
const rate = (x) => (x == null ? w('unavailable') : fmtBytes(x) + '/s')
let metricsLoading = false,
  metricsPending = false,
  metricsDisposed = false
onBeforeUnmount(() => {
  metricsDisposed = true
})
function refreshHistory() {
  resourceHistory.value = []
  return loadMetrics()
}
async function loadMetrics() {
  if (metricsDisposed) return
  if (metricsLoading) {
    metricsPending = true
    return
  }
  metricsLoading = true
  const requestedRange = resourceRange.value
  try {
    const results = await Promise.allSettled([
      request.get('/api/metrics'),
      request.get('/api/metrics/history', { params: { range: requestedRange } })
    ])
    metricError.value = results.some((r) => r.status === 'rejected')
    if (results[0].status === 'fulfilled') Object.assign(metrics, results[0].value.data || {})
    if (results[1].status === 'fulfilled' && requestedRange === resourceRange.value && !metricsDisposed) {
      resourceHistory.value = results[1].value.data?.points || []
      historyPersistent.value = results[1].value.data?.storage === 'disk'
      historyStorageError.value = results[1].value.data?.storageError || ''
    }
  } finally {
    metricsLoading = false
    if (metricsPending && !metricsDisposed) {
      metricsPending = false
      loadMetrics()
    }
  }
}
const metricPoll = useIntervalFn(loadMetrics, 5000)
const refreshPaused = ref(false)
function toggleRefresh() {
  refreshPaused.value = !refreshPaused.value
  if (refreshPaused.value) {
    metricPoll.stop()
    refreshPoller.stop()
  } else {
    metricPoll.start()
    refreshPoller.start()
    refreshAll()
  }
}
function refreshAll() {
  return Promise.allSettled([load(), loadMetrics()])
}

const router = useRouter(),
  loading = ref(false)
const data = reactive({
    version: '',
    adminHttps: true,
    mustChangePassword: false,
    totpEnabled: false,
    stats: {},
    sites: [],
    issues: [],
    firewall: { openwrt: false, rules: [] },
    recentErrors: [],
    lastUpdate: { state: 'idle' },
    lastUpdateEntries: []
  }),
  sys = reactive({})
const cards = computed(() =>
  [
    [
      data.stats.tunnels || 0,
      t('nav.tunnels'),
      t('tunnel.ready') + ': ' + (data.stats.tunnelsReady || 0),
      '/tunnels',
      'tunnel'
    ],
    [
      data.stats.ddns || 0,
      t('dashboard.cardDdns'),
      t('dashboard.cardDdnsSub', { n: data.stats.ddnsEnabled || 0 }),
      '/ddns',
      'ddns'
    ],
    [
      data.stats.certs || 0,
      t('dashboard.cardCerts'),
      nextCert.value
        ? nextCert.value.name + ' · ' + nextCert.value.remainingDays + ' ' + w('days')
        : t('dashboard.cardCertsSub', { n: data.stats.certsOk || 0 }),
      '/certs',
      'cert'
    ],
    [
      data.stats.sites || 0,
      t('dashboard.cardSites'),
      t('dashboard.cardSitesSub', { n: data.stats.sitesListening || 0 }),
      '/web-service',
      'web'
    ],
    [
      data.stats.forwards || 0,
      t('dashboard.cardForwards'),
      t('dashboard.cardForwardsSub', { n: data.stats.forwardsEnabled || 0 }),
      '/forward',
      'forward'
    ]
  ].map(([value, label, sub, path, icon]) => ({ value, label, sub, path, icon }))
)
const updateSummary = computed(
  () =>
    data.lastUpdateEntries?.[0]?.message ||
    {
      idle: t('dashboard.update.summary.idle'),
      inspecting: t('dashboard.update.summary.inspecting'),
      inspected: t('dashboard.update.summary.inspected'),
      installing: t('dashboard.update.summary.installing'),
      restarting: t('dashboard.update.summary.restarting'),
      done: t('dashboard.update.summary.done'),
      failed: t('dashboard.update.summary.failed')
    }[data.lastUpdate?.state] ||
    data.lastUpdate?.state ||
    t('dashboard.update.summary.idle')
)
const refreshPoller = useIntervalFn(load, 30000)
const chartSiteId = ref(''),
  chartRange = ref('24h'),
  chartData = ref(null),
  chartLoading = ref(false)
let seriesGeneration = 0
async function loadSeries() {
  const generation = ++seriesGeneration,
    site = chartSiteId.value,
    range = chartRange.value
  if (!site) {
    chartData.value = null
    return
  }
  chartLoading.value = true
  try {
    const res = await request.get(`/api/sites/${site}/series`, { params: { range } })
    if (generation === seriesGeneration && site === chartSiteId.value && range === chartRange.value)
      chartData.value = seriesToChartData(res.data)
  } catch {
    if (generation === seriesGeneration) chartData.value = null
  } finally {
    if (generation === seriesGeneration) chartLoading.value = false
  }
}
watch(
  () => data.sites,
  (sites) => {
    if (!sites.some((s) => s.id === chartSiteId.value)) chartSiteId.value = topSitesByRequests(sites, 1)[0]?.id || ''
    if (chartSiteId.value && !chartData.value) loadSeries()
  }
)
async function load() {
  if (loading.value) return
  loading.value = true
  try {
    const results = await Promise.allSettled([
      request.get('/api/dashboard'),
      request.get('/api/system/info'),
      request.get('/api/dashboard/workspace')
    ])
    if (results[0].status === 'fulfilled') Object.assign(data, results[0].value.data || {})
    if (results[1].status === 'fulfilled') Object.assign(sys, results[1].value.data || {})
    if (results[2].status === 'fulfilled') Object.assign(workspace, results[2].value.data || {})
    partialError.value = results.some((r) => r.status === 'rejected')
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  refreshPoller.start()
  loadMetrics()
  metricPoll.start()
})
</script>

<style scoped>
.overview-heading {
  display: flex;
  justify-content: space-between;
  gap: 14px;
  align-items: center;
  flex-wrap: wrap;
  padding: 12px 2px;
}
.overview-heading > div:first-child {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  flex-direction: column;
}
.overview-heading .muted {
  font-size: 12px;
}
.resource-cards {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}
.metric-card {
  padding: 18px 20px;
  background: var(--ap-card);
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
  min-width: 0;
}
.metric-label {
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: var(--ap-muted);
  font-size: 13px;
}
.metric-card strong {
  display: block;
  margin: 14px 0 18px;
  font-size: 32px;
  font-weight: 600;
  min-height: 45px;
  overflow-wrap: anywhere;
}
.metric-card strong.metric-null {
  font-family: inherit;
  font-size: 16px;
  line-height: 24px;
  display: flex;
  align-items: center;
}
.metric-progress {
  height: 4px;
  background: var(--ap-border);
  border-radius: 3px;
  overflow: hidden;
  margin: 0 0 16px;
}
.metric-progress span {
  display: block;
  height: 100%;
  background: var(--ap-primary);
}
.memory-progress span {
  background: var(--ap-chart-resource-memory);
}
.metric-divider {
  height: 1px;
  background: var(--ap-border);
  margin: 0 0 18px;
}
.metric-card footer {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  color: var(--ap-muted);
  font-size: 12px;
}
.metric-card footer b {
  color: var(--ap-text);
  font-weight: 600;
}
.overview-charts {
  display: grid;
  grid-template-columns: minmax(0, 1.7fr) minmax(0, 1fr);
  gap: 16px;
}
.trend-panel {
  padding: 18px 20px;
  min-width: 0;
}
.trend-heading,
.section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 18px;
}
.trend-heading h2,
.section-heading h2 {
  margin: 0;
  font-size: 16px;
  line-height: 22px;
  font-weight: 600;
  letter-spacing: 0;
}
.trend-facts {
  display: flex;
  gap: 28px;
  margin-bottom: 16px;
  color: var(--ap-muted);
  font-size: 12px;
}
.trend-facts b {
  display: block;
  color: var(--ap-text);
  font-variant-numeric: tabular-nums;
  font-size: 18px;
  font-weight: 600;
  margin-top: 5px;
}
.trend-facts .metric-null {
  font-size: 14px;
}
.sample-note {
  font-size: 12px;
  margin: 12px 0 0;
}
.services-section {
  padding: 12px 0;
}
.stats-grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
}
.stat-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px;
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
  background: var(--ap-card);
  color: var(--ap-text);
  text-align: left;
  cursor: pointer;
  min-width: 0;
}
.stat-card:hover {
  border-color: var(--ap-border-hover);
  background: var(--ap-primary-softer);
}
.stat-label {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-right: 16px;
  font-size: 13px;
  font-weight: 600;
}
.stat-card strong {
  font-size: 22px;
  font-variant-numeric: tabular-nums;
}
.stat-card small {
  color: var(--ap-muted);
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.stat-arrow {
  position: absolute;
  right: 12px;
  top: 16px;
  color: var(--ap-muted);
}
.ddns-address-strip {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 12px;
  color: var(--ap-muted);
  font-size: 12px;
}
.address-item {
  color: var(--ap-text-soft);
}
.issues {
  display: grid;
  gap: 8px;
}
.issues button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--ap-warning-border);
  border-radius: 10px;
  background: var(--ap-warning-soft);
  color: var(--ap-text);
  cursor: pointer;
}
.issues button span:nth-child(2) {
  flex: 1;
  text-align: left;
}
.two-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.info-list {
  display: grid;
  gap: 12px;
}
.info-list > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.info-list span,
.muted {
  color: var(--ap-muted);
}
.big-number {
  font-size: 28px;
  font-variant-numeric: tabular-nums;
}
.tag-row {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.traffic-list {
  display: grid;
  gap: 12px;
}
.traffic-list > div {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  padding: 10px 0;
  border-bottom: 1px solid var(--ap-border);
}
.traffic-list .truncate {
  min-width: 120px;
}
.traffic-num {
  font-size: 12px;
  color: var(--ap-muted);
}
.chart-controls {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.recent-errors > div {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 8px 0;
}
.recent-errors .truncate {
  flex: 1;
}
.recent-errors time {
  font-size: 12px;
  color: var(--ap-muted);
}
@media (max-width: 1050px) {
  .stats-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .overview-charts {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 700px) {
  .resource-cards,
  .stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }
  .metric-card {
    padding: 14px;
  }
  .metric-card strong {
    font-size: 25px;
  }
  .two-columns {
    grid-template-columns: 1fr;
  }
  .trend-panel {
    padding: 14px;
  }
  .overview-heading {
    align-items: flex-start;
  }
  .overview-heading .el-button {
    min-height: 44px;
  }
  .resource-cards {
    gap: 10px;
  }
  .trend-heading .el-radio-button {
    min-height: 44px;
  }
  .stat-card {
    padding: 14px;
  }
  .overview-heading > div:first-child {
    gap: 8px;
  }
  .recent-errors > div {
    flex-wrap: wrap;
  }
}
</style>
