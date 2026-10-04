<template>
  <div class="page-grid" v-loading="loading">
    <el-alert
      v-if="!data.adminHttps"
      type="error"
      :title="$t('dashboard.adminHttpAlert')"
      :closable="false"
      show-icon
    />
    <section class="hero">
      <div>
        <span class="eyebrow">{{ $t('dashboard.heroEyebrow') }}</span>
        <h1>
          {{ data.issues.length ? $t('dashboard.issuesToHandle', { n: data.issues.length }) : $t('dashboard.allOk') }}
        </h1>
        <p>
          {{ $t('dashboard.version') }} {{ data.version || '-' }} ·
          {{ data.adminHttps ? $t('dashboard.httpsEnabled') : $t('dashboard.httpCompat') }} ·
          {{ $t('dashboard.lastUpdate') }} {{ updateSummary }}
        </p>
      </div>
      <HealthBadge :issues="data.issues.length" />
    </section>
    <div v-if="data.issues.length" class="issues">
      <button v-for="item in data.issues" :key="item.module + item.id + item.message" @click="router.push(item.path)">
        <el-tag type="warning">{{ item.module }}</el-tag
        ><span class="truncate">{{ item.message || $t('dashboard.moduleNotRunning') }}</span
        ><el-icon><ArrowRight /></el-icon>
      </button>
    </div>

    <div class="stats-grid">
      <button v-for="item in cards" :key="item.path" class="stat-card" @click="router.push(item.path)">
        <span class="stat-icon"
          ><el-icon><component :is="item.icon" /></el-icon></span
        ><span
          ><strong>{{ item.value }}</strong
          ><b>{{ item.label }}</b
          ><small>{{ item.sub }}</small></span
        ><el-icon class="stat-arrow"><ArrowRight /></el-icon>
      </button>
    </div>

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
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowRight, Compass, Connection, Lock, Monitor, Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime, formatBytes as fmtBytes } from '../utils/format'
import { seriesToChartData, topSitesByRequests } from '../utils/series'
import { useIntervalFn } from '../composables/useIntervalFn'
import HealthBadge from '../components/HealthBadge.vue'
import SiteTrafficChart from '../components/SiteTrafficChart.vue'

const { t } = useI18n()

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
      Connection
    ],
    [
      data.stats.ddns || 0,
      t('dashboard.cardDdns'),
      t('dashboard.cardDdnsSub', { n: data.stats.ddnsEnabled || 0 }),
      '/ddns',
      Compass
    ],
    [
      data.stats.certs || 0,
      t('dashboard.cardCerts'),
      t('dashboard.cardCertsSub', { n: data.stats.certsOk || 0 }),
      '/certs',
      Lock
    ],
    [
      data.stats.sites || 0,
      t('dashboard.cardSites'),
      t('dashboard.cardSitesSub', { n: data.stats.sitesListening || 0 }),
      '/web-service',
      Monitor
    ],
    [
      data.stats.forwards || 0,
      t('dashboard.cardForwards'),
      t('dashboard.cardForwardsSub', { n: data.stats.forwardsEnabled || 0 }),
      '/forward',
      Connection
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
async function loadSeries() {
  if (!chartSiteId.value) {
    chartData.value = null
    return
  }
  chartLoading.value = true
  try {
    const res = await request.get(`/api/sites/${chartSiteId.value}/series`, { params: { range: chartRange.value } })
    chartData.value = seriesToChartData(res.data)
  } catch {
    chartData.value = null
  } finally {
    chartLoading.value = false
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
  loading.value = true
  try {
    const [dash, info] = await Promise.all([request.get('/api/dashboard'), request.get('/api/system/info')])
    Object.assign(data, dash.data || {})
    Object.assign(sys, info.data || {})
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  refreshPoller.start()
})
</script>

<style scoped>
.hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 26px 30px;
  border-radius: var(--ap-radius);
  color: var(--ap-text);
  background: var(--ap-card);
  border: 1px solid var(--ap-border);
  box-shadow: var(--ap-shadow);
}
.hero h1 {
  margin: 7px 0;
  font-size: 26px;
}
.hero p {
  margin: 0;
  color: var(--ap-hero-text-soft);
}
.eyebrow {
  font-size: 12px;
  letter-spacing: 0.15em;
  text-transform: uppercase;
}
.health-orb {
  display: grid;
  place-items: center;
  width: 68px;
  height: 68px;
  border-radius: 50%;
  background: var(--ap-glass);
  font-size: 25px;
  font-weight: 800;
}
.health-orb.warning {
  background: var(--ap-warning-glow);
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
.stats-grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 16px;
}
.stat-card {
  position: relative;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 20px;
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
  background: white;
  box-shadow: var(--ap-shadow);
  color: var(--ap-text);
  text-align: left;
  cursor: pointer;
}
.stat-card:hover {
  transform: translateY(-2px);
  border-color: var(--ap-border-hover);
}
.stat-icon {
  display: grid;
  place-items: center;
  width: 46px;
  height: 46px;
  border-radius: 13px;
  background: var(--ap-primary-soft);
  color: var(--ap-primary);
  font-size: 21px;
}
.stat-card strong,
.stat-card b,
.stat-card small {
  display: block;
}
.stat-card strong {
  font-size: 26px;
}
.stat-card b {
  margin-top: 2px;
}
.stat-card small {
  margin-top: 3px;
  color: var(--ap-muted);
}
.stat-arrow {
  position: absolute;
  right: 14px;
  color: var(--ap-text-faint);
}
.two-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}
.info-list > div {
  display: flex;
  justify-content: space-between;
  align-items: center;
  min-height: 44px;
  border-bottom: 1px solid var(--ap-border-soft);
}
.big-number {
  font-size: 36px;
  font-weight: 750;
}
.tag-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.recent-errors > div {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 9px;
  padding: 9px 0;
  border-bottom: 1px solid var(--ap-border-soft);
}
.recent-errors time {
  font-size: 11px;
  color: var(--ap-muted);
}
.traffic-list > div {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) repeat(3, auto) minmax(0, 2fr);
  align-items: center;
  gap: 14px;
  padding: 10px 0;
  border-bottom: 1px solid var(--ap-border-soft);
}
.traffic-list > div:last-child {
  border-bottom: none;
}
.traffic-num {
  color: var(--ap-muted);
}
.traffic-num b {
  color: var(--ap-text);
}
@media (max-width: 1100px) {
  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
  }
  .stat-card:last-child {
    grid-column: span 2;
  }
}
@media (max-width: 760px) {
  .two-columns {
    grid-template-columns: 1fr;
  }
  .hero {
    padding: 21px;
  }
  .hero h1 {
    font-size: 21px;
  }
  .health-orb {
    width: 54px;
    height: 54px;
  }
  .recent-errors > div {
    grid-template-columns: auto minmax(0, 1fr);
  }
  .recent-errors time {
    display: none;
  }
  .traffic-list > div {
    grid-template-columns: minmax(0, 1fr);
    gap: 6px;
  }
}
@media (max-width: 480px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }
  .stat-card:last-child {
    grid-column: auto;
  }
}
.chart-controls {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}
@media (max-width: 560px) {
  .chart-controls {
    width: 100%;
  }
  .chart-controls .el-select {
    flex: 1;
  }
}
</style>
