<template>
  <div class="web-service-page" v-loading="loading">
    <section class="sites-panel">
      <div class="panel-heading">
        <div><span class="eyebrow">{{ $t('webService.siteManagement') }}</span><h1>{{ $t('webService.title') }}</h1></div>
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
          <span class="site-tab-copy"><b>{{ site.name }}</b><small>{{ site.tls ? 'HTTPS' : 'HTTP' }} · {{ site.listen }}</small></span>
          <span class="rule-count">{{ (site.rules || []).length }}</span>
        </button>
      </div>
      <el-empty v-else :description="$t('webService.empty')" :image-size="60" />
    </section>

    <section v-if="activeSite" class="rules-panel">
      <header class="site-toolbar">
        <div class="site-identity">
          <span class="status-dot large" :class="activeSite.status" />
          <div><h2>{{ activeSite.name }}</h2><span>{{ siteStatusText(activeSite) }}</span></div>
          <el-tag :type="activeSite.tls ? 'success' : 'info'" effect="plain">{{ activeSite.tls ? 'HTTPS' : 'HTTP' }}</el-tag>
          <el-tag v-if="activeSite.tls && activeSite.forceHttps && activeSite.certId" type="warning" effect="plain">{{ $t('webService.forceRedirect') }}</el-tag>
        </div>
        <div class="toolbar-actions">
          <el-button @click="openLogs(activeSite)">{{ $t('common.logs') }}</el-button>
          <el-button @click="openSiteDialog(activeSite)">{{ $t('common.edit') }}</el-button>
          <el-switch :model-value="activeSite.enabled" @change="toggleSite(activeSite)" />
          <el-popconfirm :title="$t('webService.deleteConfirm')" @confirm="deleteSite(activeSite)">
            <template #reference><el-button type="danger" plain>{{ $t('common.delete') }}</el-button></template>
          </el-popconfirm>
          <el-button type="primary" @click="openRuleDialog()">＋ {{ $t('webService.addRule') }}</el-button>
        </div>
      </header>

      <div class="site-summary">
        <span>◉ {{ $t('webService.listenAt') }} <b>{{ activeSite.listen }}</b></span>
        <span>{{ activeSite.tls ? 'TLS' : 'HTTP' }}</span>
        <span>{{ $t('webService.allRules') }} <b>{{ activeSite.rules?.length || 0 }}</b></span>
        <span>{{ $t('webService.enabledRules') }} <b>{{ enabledRuleCount }}</b></span>
        <span class="traffic-chip">↓ {{ fmtBytes(siteStats.bytesIn) }} <small>{{ fmtRate(siteRate.bytesIn) }}</small></span>
        <span class="traffic-chip outgoing">↑ {{ fmtBytes(siteStats.bytesOut) }} <small>{{ fmtRate(siteRate.bytesOut) }}</small></span>
        <span>{{ $t('webService.activeConnections') }} <b>{{ siteStats.active || 0 }}</b></span>
        <span :class="{ danger: siteErrors > 0 }">{{ $t('webService.errors') }} <b>{{ siteErrors }}</b></span>
      </div>

      <div class="rule-list-heading">
        <div><h3>{{ $t('webService.subRules') }}</h3><p>{{ $t('webService.dragTip') }}</p></div>
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
          <span class="drag-handle" draggable="true" :title="$t('webService.dragTip')" @dragstart="startDrag(index)" @dragend="dragIndex = -1">⋮⋮</span>
          <div class="rule-name"><b>{{ rule.name }}</b><small>{{ ruleTypeText(rule.type) }}</small></div>
          <div class="rule-guard"><span>{{ securityLabel(rule) }}</span></div>
          <div class="rule-route"><b>{{ rule.frontendHost || '*' }}{{ rule.frontendPath || '/' }}</b><small>{{ ruleTarget(rule) }}</small></div>
          <div class="rule-traffic">
            <span>↓ <b>{{ fmtBytes(ruleStats(rule.id).bytesIn) }}</b><small>{{ fmtRate(ruleRate(rule.id).bytesIn) }}</small></span>
            <span class="outgoing">↑ <b>{{ fmtBytes(ruleStats(rule.id).bytesOut) }}</b><small>{{ fmtRate(ruleRate(rule.id).bytesOut) }}</small></span>
          </div>
          <div class="rule-health">
            <span :title="$t('webService.activeConnections')">↔ <b>{{ ruleStats(rule.id).active || 0 }}</b></span>
            <span :class="{ danger: ruleErrors(rule.id) > 0 }" :title="$t('webService.errors')">⚠ <b>{{ ruleErrors(rule.id) }}</b></span>
          </div>
          <div class="rule-actions">
            <el-switch :model-value="rule.enabled" @change="toggleRule(rule)" />
            <el-button link type="primary" @click="openRuleDialog(rule, index)">{{ $t('common.edit') }}</el-button>
            <el-popconfirm :title="$t('webService.deleteRuleConfirm')" @confirm="deleteRule(rule)">
              <template #reference><el-button link type="danger">{{ $t('common.delete') }}</el-button></template>
            </el-popconfirm>
          </div>
        </article>
      </div>
      <el-empty v-else :description="$t('webService.emptySubRules')" :image-size="58">
        <el-button type="primary" @click="openRuleDialog()">{{ $t('webService.addRule') }}</el-button>
      </el-empty>
    </section>

    <!-- 站点编辑对话框 -->
    <el-dialog
      v-model="siteDialog.visible"
      :title="siteDialog.isEdit ? $t('webService.editSite') : $t('webService.addSiteTitle')"
      width="620px"
      destroy-on-close
    >
      <el-form ref="siteFormRef" :model="siteDialog.form" :rules="siteRules" label-width="100px">
        <el-form-item :label="$t('webService.siteName')" prop="name">
          <el-input v-model="siteDialog.form.name" :placeholder="$t('webService.siteNamePlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('webService.colListen')" prop="listen">
          <el-input v-model="siteDialog.form.listen" :placeholder="$t('webService.listenPlaceholder')" style="width: 260px" />
        </el-form-item>
        <el-form-item :label="$t('webService.enableTls')">
          <el-switch v-model="siteDialog.form.tls" />
        </el-form-item>
        <el-form-item v-if="siteDialog.form.tls" :label="$t('webService.cert')">
          <el-select v-model="siteDialog.form.certId" clearable :placeholder="$t('webService.certPlaceholder')" style="width: 100%">
            <el-option
              v-for="c in certs"
              :key="c.id"
              :label="c.name + '（' + (c.domains || []).join(', ') + '）'"
              :value="c.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item v-if="siteDialog.form.tls" :label="$t('webService.forceHttps')">
          <el-switch v-model="siteDialog.form.forceHttps" :disabled="!siteDialog.form.certId" />
          <span class="form-tip">
            {{ siteDialog.form.certId ? $t('webService.forceHttpsTipOn') : $t('webService.forceHttpsTipOff') }}
          </span>
        </el-form-item>
        <el-form-item :label="$t('webService.firewall')">
          <el-switch v-model="siteDialog.form.autoFw" />
          <span class="form-tip">{{ $t('webService.firewallTip') }}</span>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="siteDialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="siteDialog.saving" @click="saveSite">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 子规则编辑对话框 -->
    <el-dialog
      v-model="ruleDialog.visible"
      :title="ruleDialog.index >= 0 ? $t('webService.editSubRule') : $t('webService.addSubRule')"
      width="640px"
      append-to-body
      destroy-on-close
    >
      <el-form ref="ruleFormRef" :model="ruleDialog.form" label-width="110px">
        <el-form-item :label="$t('webService.ruleName')">
          <el-input v-model="ruleDialog.form.name" :placeholder="$t('webService.optional')" />
        </el-form-item>
        <el-form-item :label="$t('webService.colType')" required>
          <el-radio-group v-model="ruleDialog.form.type">
            <el-radio-button value="reverse">{{ $t('webService.typeReverse') }}</el-radio-button>
            <el-radio-button value="redirect">{{ $t('webService.typeRedirect') }}</el-radio-button>
            <el-radio-button value="fileserver">{{ $t('webService.typeFileserver') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('webService.frontendHost')">
          <el-input v-model="ruleDialog.form.frontendHost" :placeholder="$t('webService.frontendHostPlaceholder')" />
        </el-form-item>
        <el-form-item :label="$t('webService.frontendPath')">
          <el-input v-model="ruleDialog.form.frontendPath" :placeholder="$t('webService.frontendPathPlaceholder')" />
        </el-form-item>

        <template v-if="ruleDialog.form.type === 'reverse'">
          <el-form-item :label="$t('webService.backends')" required>
            <div class="backend-editor">
              <el-input
                v-model="ruleDialog.form.backendsText"
                :placeholder="$t('webService.backendsPlaceholder')"
              />
              <div class="backend-test-row">
                <el-button size="small" :loading="backendTesting" @click="testBackend">{{ $t('webService.testFirstBackend') }}</el-button>
                <span v-if="backendTestResult" class="form-tip">{{ backendTestResult }}</span>
              </div>
            </div>
          </el-form-item>
          <el-form-item :label="$t('webService.stripPrefix')">
            <el-switch v-model="ruleDialog.form.stripPrefix" />
            <span class="form-tip">{{ $t('webService.stripPrefixTip') }}</span>
          </el-form-item>
          <el-form-item :label="$t('webService.preserveHost')">
            <el-switch v-model="ruleDialog.form.preserveHost" />
            <span class="form-tip">{{ $t('webService.preserveHostTip') }}</span>
          </el-form-item>
          <el-form-item :label="$t('webService.autoProxyHeaders')">
            <el-switch v-model="ruleDialog.form.autoProxyHeaders" />
            <span class="form-tip">{{ $t('webService.autoProxyHeadersTip') }}</span>
          </el-form-item>
          <el-form-item :label="$t('webService.skipBackendTls')">
            <el-switch v-model="ruleDialog.form.skipBackendTlsVerify" />
            <span class="form-tip">{{ $t('webService.skipBackendTlsTip') }}</span>
          </el-form-item>
          <el-form-item :label="$t('webService.extraHeaders')">
            <div class="headers-editor">
              <div v-for="(h, i) in ruleDialog.headersList" :key="i" class="header-row">
                <el-input v-model="h.key" :placeholder="$t('webService.headerKeyPlaceholder')" style="width: 220px" />
                <el-input v-model="h.value" type="password" show-password :placeholder="h.configured ? $t('common.keepEmpty') : $t('webService.headerValuePlaceholder')" style="width: 240px" />
                <el-button link type="danger" @click="ruleDialog.headersList.splice(i, 1)">{{ $t('common.delete') }}</el-button>
              </div>
              <el-button size="small" plain @click="ruleDialog.headersList.push({ key: '', value: '' })">
                {{ $t('webService.addHeader') }}
              </el-button>
            </div>
          </el-form-item>

          <el-collapse class="security-collapse">
            <el-collapse-item :title="$t('webService.stabilityTitle')" name="stability">
              <div class="number-grid">
                <el-form-item :label="$t('webService.connectTimeout')">
                  <el-input-number v-model="ruleDialog.form.connectTimeoutSeconds" :min="0" :max="30" controls-position="right" />
                </el-form-item>
                <el-form-item :label="$t('webService.responseHeaderTimeout')">
                  <el-input-number v-model="ruleDialog.form.responseHeaderTimeoutSeconds" :min="0" :max="600" controls-position="right" />
                </el-form-item>
                <el-form-item :label="$t('webService.rateLimitRps')">
                  <el-input-number v-model="ruleDialog.form.rateLimitRPS" :min="0" :max="100000" controls-position="right" />
                </el-form-item>
                <el-form-item :label="$t('webService.rateLimitBurst')">
                  <el-input-number v-model="ruleDialog.form.rateLimitBurst" :min="0" :max="200000" controls-position="right" />
                </el-form-item>
                <el-form-item :label="$t('webService.maxBody')">
                  <el-input-number v-model="ruleDialog.form.maxRequestBodyMiB" :min="0" :max="10240" controls-position="right" />
                </el-form-item>
              </div>
              <div class="collapse-tip">{{ $t('webService.collapseTip') }}</div>
            </el-collapse-item>
            <el-collapse-item :title="$t('webService.responseRewrite')" name="response">
              <el-form-item :label="$t('webService.rewriteLocation')">
                <el-switch v-model="ruleDialog.form.rewriteLocation" />
                <span class="form-tip">{{ $t('webService.rewriteLocationTip') }}</span>
              </el-form-item>
              <el-form-item :label="$t('webService.cookieDomain')">
                <div class="rewrite-pair"><el-input v-model="ruleDialog.form.cookieDomainFrom" :placeholder="$t('webService.cookieDomainFromPlaceholder')" /><span>→</span><el-input v-model="ruleDialog.form.cookieDomainTo" :placeholder="$t('webService.cookieDomainToPlaceholder')" /></div>
              </el-form-item>
              <el-form-item :label="$t('webService.cookiePath')">
                <div class="rewrite-pair"><el-input v-model="ruleDialog.form.cookiePathFrom" :placeholder="$t('webService.cookiePathFromPlaceholder')" /><span>→</span><el-input v-model="ruleDialog.form.cookiePathTo" :placeholder="$t('webService.cookiePathToPlaceholder')" /></div>
              </el-form-item>
            </el-collapse-item>
          </el-collapse>
        </template>

        <template v-if="ruleDialog.form.type === 'redirect'">
          <el-form-item :label="$t('webService.redirectTarget')" required>
            <el-input
              v-model="ruleDialog.form.redirectUrl"
              :placeholder="$t('webService.redirectUrlPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="$t('webService.statusCode')">
            <el-select v-model="ruleDialog.form.redirectCode" style="width: 200px">
              <el-option :value="301" :label="$t('webService.redirect301')" />
              <el-option :value="302" :label="$t('webService.redirect302')" />
              <el-option :value="307" :label="$t('webService.redirect307')" />
              <el-option :value="308" :label="$t('webService.redirect308')" />
            </el-select>
          </el-form-item>
        </template>

        <template v-if="ruleDialog.form.type === 'fileserver'">
          <el-form-item :label="$t('webService.rootDir')" required>
            <el-input v-model="ruleDialog.form.rootDir" :placeholder="$t('webService.rootDirPlaceholder')" />
          </el-form-item>
        </template>

        <el-collapse class="security-collapse">
          <el-collapse-item :title="$t('webService.securityOptions')" name="sec">
            <el-form-item :label="$t('webService.basicAuth')">
              <el-switch v-model="ruleDialog.form.basicAuth" />
            </el-form-item>
            <template v-if="ruleDialog.form.basicAuth">
              <el-form-item :label="$t('webService.authUser')">
                <el-input v-model="ruleDialog.form.authUser" style="width: 240px" />
              </el-form-item>
              <el-form-item :label="$t('webService.authPass')">
                <el-input v-model="ruleDialog.form.authPass" type="password" show-password :placeholder="ruleDialog.form.authPassConfigured ? $t('common.keepEmpty') : $t('webService.enterPassword')" style="width: 240px" />
              </el-form-item>
            </template>
            <el-form-item :label="$t('forward.ipAccess')">
              <el-select v-model="ruleDialog.form.ipListMode" style="width: 200px">
                <el-option value="" :label="$t('common.noLimit')" />
                <el-option value="whitelist" :label="$t('common.ipWhitelist')" />
                <el-option value="blacklist" :label="$t('common.ipBlacklist')" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="ruleDialog.form.ipListMode" :label="$t('common.ipList')">
              <el-input
                v-model="ruleDialog.ipListText"
                type="textarea"
                :rows="2"
                :placeholder="$t('common.ipListPlaceholder')"
              />
            </el-form-item>
            <el-form-item :label="$t('webService.uaAccess')">
              <el-select v-model="ruleDialog.form.uaListMode" style="width: 200px">
                <el-option value="" :label="$t('common.noLimit')" />
                <el-option value="whitelist" :label="$t('common.uaWhitelist')" />
                <el-option value="blacklist" :label="$t('common.uaBlacklist')" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="ruleDialog.form.uaListMode" :label="$t('webService.uaKeywords')">
              <el-input
                v-model="ruleDialog.uaListText"
                type="textarea"
                :rows="2"
                :placeholder="$t('webService.uaListPlaceholder')"
              />
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="ruleDialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="ruleDialog.saving" @click="confirmRule">{{ $t('webService.confirmOk') }}</el-button>
      </template>
    </el-dialog>

    <!-- 日志抽屉 -->
    <el-drawer v-model="logsDrawer.visible" :title="$t('webService.siteLogsTitle', { name: logsDrawer.name })" size="560px">
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
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'
import { calculateTrafficRates } from '../utils/traffic'

const { t } = useI18n()

const sites = ref([])
const certs = ref([])
const loading = ref(false)
const selectedSiteId = ref('')
const stats = ref({})
const rates = ref({})
const activeSite = computed(() => sites.value.find((site) => site.id === selectedSiteId.value) || null)
const enabledRuleCount = computed(() => (activeSite.value?.rules || []).filter((rule) => rule.enabled).length)
const emptyStats = () => ({ requests: 0, bytesIn: 0, bytesOut: 0, active: 0, status1xx: 0, status2xx: 0, status3xx: 0, status4xx: 0, status5xx: 0, rules: {} })
const siteStats = computed(() => stats.value[selectedSiteId.value] || emptyStats())
const siteRate = computed(() => rates.value[selectedSiteId.value] || { bytesIn: 0, bytesOut: 0 })
const siteErrors = computed(() => (siteStats.value.status4xx || 0) + (siteStats.value.status5xx || 0))

function siteStatusText(row) {
  if (!row.enabled) return t('webService.statusDisabled')
  const map = { listening: t('webService.statusListening'), error: t('webService.statusError'), stopped: t('webService.statusStopped') }
  return map[row.status] || row.status || '-'
}
function siteStatusType(row) {
  if (!row.enabled) return 'info'
  return row.status === 'listening' ? 'success' : row.status === 'error' ? 'danger' : 'info'
}

function ruleTypeText(type) {
  return ['reverse', 'redirect', 'fileserver'].includes(type) ? t(`webService.type${type[0].toUpperCase()}${type.slice(1)}`) : type
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
function fmtRate(value = 0) { return `${fmtBytes(Math.max(0, Math.round(value)))}/s` }
function ruleStats(ruleID) { return siteStats.value.rules?.[ruleID] || emptyStats() }
function ruleRate(ruleID) { return siteRate.value.rules?.[ruleID] || { bytesIn: 0, bytesOut: 0 } }
function ruleErrors(ruleID) { const value = ruleStats(ruleID); return (value.status4xx || 0) + (value.status5xx || 0) }
function securityLabel(rule) {
  const count = [rule.basicAuth, rule.ipListMode, rule.uaListMode, rule.rateLimitRPS > 0, rule.maxRequestBodyMiB > 0, Object.keys(rule.headers || {}).length > 0].filter(Boolean).length
  return count ? t('webService.protectionCount', { n: count }) : t('webService.noProtection')
}

// ---------- 站点对话框 ----------
const siteFormRef = ref()
const siteDialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  form: { id: '', name: '', listen: '', tls: false, certId: '', forceHttps: false, autoFw: false, enabled: true }
})

const siteRules = computed(() => ({
  name: [{ required: true, message: t('webService.nameRequired'), trigger: 'blur' }],
  listen: [{ required: true, message: t('webService.listenRequired'), trigger: 'blur' }]
}))

function openSiteDialog(row) {
  siteDialog.isEdit = !!row
  siteDialog.form = row
    ? { id: row.id, name: row.name, listen: row.listen, tls: row.tls, certId: row.certId || '', forceHttps: !!(row.forceHttps && row.certId), autoFw: !!row.autoFw, enabled: row.enabled }
    : { id: '', name: '', listen: '', tls: false, certId: '', forceHttps: false, autoFw: false, enabled: true }
  siteDialog.visible = true
}

async function saveSite() {
  await siteFormRef.value.validate()
  const f = siteDialog.form
  const body = {
    name: f.name,
    enabled: f.enabled,
    listen: f.listen,
    tls: f.tls,
    certId: f.tls ? f.certId : '',
    forceHttps: !!(f.tls && f.certId && f.forceHttps),
    autoFw: f.autoFw,
    rules: siteDialog.isEdit ? (sites.value.find((site) => site.id === f.id)?.rules || []) : []
  }
  siteDialog.saving = true
  try {
    if (siteDialog.isEdit) {
      await request.put(`/api/sites/${f.id}`, body)
    } else {
      const res = await request.post('/api/sites', body)
      selectedSiteId.value = res.data?.id || ''
    }
    ElMessage.success(t('common.saveSuccess'))
    siteDialog.visible = false
    load()
  } catch {
    // 拦截器已提示
  } finally {
    siteDialog.saving = false
  }
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
const emptyRule = () => ({
  id: '', name: '', type: 'reverse', enabled: true,
  frontendHost: '', frontendPath: '',
  backendsText: '', redirectUrl: '', redirectCode: 302, rootDir: '',
  preserveHost: false, autoProxyHeaders: true, skipBackendTlsVerify: false,
  stripPrefix: false, connectTimeoutSeconds: 5, responseHeaderTimeoutSeconds: 60,
  rateLimitRPS: 0, rateLimitBurst: 0, maxRequestBodyMiB: 0,
  rewriteLocation: false, cookieDomainFrom: '', cookieDomainTo: '', cookiePathFrom: '', cookiePathTo: '',
  basicAuth: false, authUser: '', authPass: '', authPassConfigured: false,
  ipListMode: '', uaListMode: ''
})

const ruleDialog = reactive({
  visible: false,
  saving: false,
  index: -1,
  form: emptyRule(),
  headersList: [],
  ipListText: '',
  uaListText: ''
})
const backendTesting = ref(false)
const backendTestResult = ref('')

function openRuleDialog(row, index = -1) {
  if (!activeSite.value) return
  ruleDialog.index = index
  ruleDialog.form = row
    ? {
        id: row.id || '',
        name: row.name || '',
        type: row.type || 'reverse',
        enabled: row.enabled !== false,
        frontendHost: row.frontendHost || '',
        frontendPath: row.frontendPath || '',
        backendsText: (row.backends || []).join(', '),
        redirectUrl: row.redirectUrl || '',
        redirectCode: row.redirectCode || 302,
        rootDir: row.rootDir || '',
        preserveHost: !!row.preserveHost,
        autoProxyHeaders: row.autoProxyHeaders !== false,
        skipBackendTlsVerify: !!row.skipBackendTlsVerify,
    stripPrefix: !!row.stripPrefix,
    connectTimeoutSeconds: row.connectTimeoutSeconds ?? 0,
    responseHeaderTimeoutSeconds: row.responseHeaderTimeoutSeconds ?? 0,
    rateLimitRPS: row.rateLimitRPS ?? 0,
    rateLimitBurst: row.rateLimitBurst ?? 0,
    maxRequestBodyMiB: row.maxRequestBodyMiB ?? 0,
    rewriteLocation: !!row.rewriteLocation,
    cookieDomainFrom: row.cookieDomainFrom || '',
    cookieDomainTo: row.cookieDomainTo || '',
    cookiePathFrom: row.cookiePathFrom || '',
    cookiePathTo: row.cookiePathTo || '',
        basicAuth: !!row.basicAuth,
        authUser: row.authUser || '',
        authPass: '',
        authPassConfigured: !!row.basicAuth,
        ipListMode: row.ipListMode || '',
        uaListMode: row.uaListMode || ''
      }
    : emptyRule()
  ruleDialog.headersList = row
    ? Object.entries(row.headers || {}).map(([key]) => ({ key, value: '', configured: true }))
    : []
  ruleDialog.ipListText = row ? (row.ipList || []).join(', ') : ''
  ruleDialog.uaListText = row ? (row.uaList || []).join(', ') : ''
  backendTestResult.value = ''
  ruleDialog.visible = true
}

async function testBackend() {
  const backend = splitList(ruleDialog.form.backendsText)[0]
  if (!backend) {
    ElMessage.warning(t('webService.fillBackendFirst'))
    return
  }
  backendTesting.value = true
  backendTestResult.value = ''
  try {
    const res = await request.post('/api/sites/backend-test', {
      url: backend,
      connectTimeoutSeconds: ruleDialog.form.connectTimeoutSeconds || 5,
      skipBackendTlsVerify: ruleDialog.form.skipBackendTlsVerify
    })
    const data = res.data || {}
    backendTestResult.value = `${t('webService.backendOk', { ms: data.latencyMs ?? 0 })}${data.tls ? t('webService.tlsOk') : ''}`
    ElMessage.success(t('webService.backendTestPassed'))
  } catch {
    backendTestResult.value = t('webService.backendFailed')
  } finally {
    backendTesting.value = false
  }
}

function splitList(text) {
  return (text || '').split(',').map((s) => s.trim()).filter(Boolean)
}

async function confirmRule() {
  const f = ruleDialog.form
  if (!f.name.trim()) {
    ElMessage.warning(t('webService.ruleNameRequired'))
    return
  }
  if (f.type === 'reverse' && !splitList(f.backendsText).length) {
    ElMessage.warning(t('webService.fillBackend'))
    return
  }
  if (f.type === 'redirect' && !f.redirectUrl) {
    ElMessage.warning(t('webService.fillTarget'))
    return
  }
  if (f.type === 'fileserver' && !f.rootDir) {
    ElMessage.warning(t('webService.fillRootDir'))
    return
  }
  const headers = {}
  for (const h of ruleDialog.headersList) {
    // 空值保留提交：后端 mergeRuleSecrets（internal/webproxy/api.go）会把空值与原有值合并，实现"留空保持不变"
    if (h.key.trim()) headers[h.key.trim()] = h.value
  }
  const rule = {
    id: f.id,
    name: f.name,
    type: f.type,
    enabled: f.enabled,
    frontendHost: f.frontendHost.trim(),
    frontendPath: f.frontendPath.trim(),
    backends: f.type === 'reverse' ? splitList(f.backendsText) : [],
    redirectUrl: f.type === 'redirect' ? f.redirectUrl.trim() : '',
    redirectCode: f.type === 'redirect' ? f.redirectCode : 0,
    rootDir: f.type === 'fileserver' ? f.rootDir.trim() : '',
    headers,
    preserveHost: f.preserveHost,
    autoProxyHeaders: f.autoProxyHeaders,
    skipBackendTlsVerify: f.skipBackendTlsVerify,
  stripPrefix: f.stripPrefix,
  connectTimeoutSeconds: f.connectTimeoutSeconds || 0,
  responseHeaderTimeoutSeconds: f.responseHeaderTimeoutSeconds || 0,
  rateLimitRPS: f.rateLimitRPS || 0,
  rateLimitBurst: f.rateLimitRPS ? (f.rateLimitBurst || f.rateLimitRPS * 2) : 0,
  maxRequestBodyMiB: f.maxRequestBodyMiB || 0,
  rewriteLocation: f.rewriteLocation,
  cookieDomainFrom: f.cookieDomainFrom.trim(),
  cookieDomainTo: f.cookieDomainTo.trim(),
  cookiePathFrom: f.cookiePathFrom.trim(),
  cookiePathTo: f.cookiePathTo.trim(),
    basicAuth: f.basicAuth,
    authUser: f.basicAuth ? f.authUser : '',
    authPass: f.basicAuth ? f.authPass : '',
    ipListMode: f.ipListMode,
    ipList: f.ipListMode ? splitList(ruleDialog.ipListText) : [],
    uaListMode: f.uaListMode,
    uaList: f.uaListMode ? splitList(ruleDialog.uaListText) : []
  }
  ruleDialog.saving = true
  try {
    if (ruleDialog.index >= 0) {
      await request.put(`/api/sites/${activeSite.value.id}/rules/${rule.id}`, rule)
    } else {
      await request.post(`/api/sites/${activeSite.value.id}/rules`, rule)
    }
    ElMessage.success(t('common.saveSuccess'))
    ruleDialog.visible = false
    await load(false)
  } finally {
    ruleDialog.saving = false
  }
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
function startDrag(index) { dragIndex.value = index }
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
  logsDrawer.logs = (res.data?.entries || []).map((entry) => `${formatTime(entry.time)} [${entry.level}] ${entry.message}`)
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

let statsTimer
onMounted(async () => {
  await Promise.all([load(), loadCerts()])
  await loadStats()
  statsTimer = setInterval(loadStats, 3000)
})
onUnmounted(() => clearInterval(statsTimer))
</script>

<style scoped>
.web-service-page{display:grid;gap:16px;color:var(--ap-text)}
.sites-panel,.rules-panel{border:1px solid #cfe4e1;border-radius:16px;background:linear-gradient(180deg,#fbfefd,#f7fbfa);box-shadow:0 10px 28px rgba(20,93,88,.07)}
.sites-panel{padding:20px}.panel-heading,.site-toolbar,.site-identity,.toolbar-actions{display:flex;align-items:center}.panel-heading,.site-toolbar{justify-content:space-between;gap:16px}.panel-heading h1{margin:3px 0 0;font-size:22px}.eyebrow{color:#248b82;font-size:11px;font-weight:700;letter-spacing:.14em;text-transform:uppercase}
.site-strip{display:flex;gap:10px;margin-top:18px;padding-bottom:3px;overflow-x:auto}.site-tab{display:flex;align-items:center;gap:10px;min-width:210px;padding:13px 14px;border:1px solid #d2e4e1;border-radius:12px;background:#fff;color:var(--ap-text);font:inherit;text-align:left;cursor:pointer;transition:.18s ease}.site-tab:hover{border-color:#72bbb3;transform:translateY(-1px)}.site-tab.active{border-color:#14998b;background:#effaf8;box-shadow:inset 0 -3px #14998b}.site-tab-copy{min-width:0;flex:1}.site-tab-copy b,.site-tab-copy small{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.site-tab-copy small{margin-top:3px;color:var(--ap-muted);font-size:11px}.rule-count{display:grid;place-items:center;min-width:25px;height:25px;border-radius:8px;background:#e5f3f1;color:#177d74;font-weight:700}
.status-dot{width:9px;height:9px;flex:0 0 auto;border-radius:50%;background:#95a7a6;box-shadow:0 0 0 3px rgba(149,167,166,.13)}.status-dot.listening{background:#20a97a;box-shadow:0 0 0 3px rgba(32,169,122,.13)}.status-dot.error{background:#dc5d63;box-shadow:0 0 0 3px rgba(220,93,99,.14)}.status-dot.large{width:11px;height:11px}
.rules-panel{overflow:hidden}.site-toolbar{padding:18px 20px;border-bottom:1px solid #dbeae8}.site-identity{gap:10px;min-width:0}.site-identity h2{margin:0;font-size:18px}.site-identity div>span{display:block;margin-top:2px;color:var(--ap-muted);font-size:11px}.toolbar-actions{justify-content:flex-end;gap:8px;flex-wrap:wrap}
.site-summary{display:flex;align-items:center;gap:8px;padding:12px 18px;border-bottom:1px solid #dbeae8;overflow-x:auto}.site-summary>span{flex:0 0 auto;padding:7px 10px;border:1px solid #d9e8e6;border-radius:9px;background:#fff;color:#56706e;font-size:12px}.site-summary b{color:#254946}.site-summary .traffic-chip{margin-left:auto}.traffic-chip small,.rule-traffic small{margin-left:7px;color:#819593}.outgoing{color:#0a8d78!important}.danger{color:#d85059!important}
.rule-list-heading{display:flex;justify-content:space-between;padding:16px 20px 10px}.rule-list-heading h3{margin:0;font-size:15px}.rule-list-heading p{margin:4px 0 0;color:var(--ap-muted);font-size:11px}.rule-list{padding:0 12px 14px;overflow-x:auto}.rule-row{display:grid;grid-template-columns:30px 130px 100px minmax(220px,1.5fr) minmax(250px,1fr) 90px 175px;align-items:center;gap:10px;min-width:1080px;min-height:62px;padding:8px 10px;border-bottom:1px solid #deebe9;background:#fff;transition:.15s ease}.rule-row:first-child{border-radius:11px 11px 0 0}.rule-row:last-child{border-bottom:0;border-radius:0 0 11px 11px}.rule-row:hover{background:#f4fbfa}.rule-row.disabled{opacity:.58}.rule-row.dragging{background:#e7f6f3;opacity:.7}.drag-handle{color:#8ba5a2;font-size:18px;letter-spacing:-4px;cursor:grab;user-select:none}.drag-handle:active{cursor:grabbing}.rule-name b,.rule-name small,.rule-route b,.rule-route small{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.rule-name small{margin-top:4px;color:#1b8178;font-size:11px}.rule-guard span{display:inline-block;padding:5px 8px;border-radius:7px;background:#eff6f5;color:#56716e;font-size:11px}.rule-route small{margin-top:5px;color:#1a887c}.rule-traffic{display:grid;grid-template-columns:1fr 1fr;gap:6px}.rule-traffic>span{display:flex;align-items:center;padding:7px 8px;border:1px solid #dbe8e6;border-radius:8px;color:#526c69;font-size:11px}.rule-traffic small{margin-left:auto}.rule-health,.rule-actions{display:flex;align-items:center;gap:10px}.rule-health span{color:#607977;font-size:12px}.rule-actions{justify-content:flex-end}.form-tip{margin-left:10px;color:var(--ap-muted);font-size:12px}.headers-editor,.backend-editor{width:100%}.backend-test-row{display:flex;align-items:center;margin-top:8px}.number-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 16px}.number-grid :deep(.el-input-number){width:100%}.rewrite-pair{display:grid;grid-template-columns:1fr auto 1fr;align-items:center;gap:8px;width:100%}.collapse-tip{margin:-4px 0 12px 110px;color:var(--ap-muted);font-size:12px}.header-row{display:flex;align-items:center;gap:8px;margin-bottom:8px}.security-collapse{width:100%;margin-top:4px}.log-toolbar{margin-bottom:10px}.log-list{font-family:Menlo,Consolas,monospace;font-size:12px}.log-line{padding:3px 0;border-bottom:1px solid #f0f0f0;color:var(--ap-text);word-break:break-all}
@media(max-width:900px){.site-toolbar{align-items:flex-start;flex-direction:column}.toolbar-actions{justify-content:flex-start}.site-summary .traffic-chip{margin-left:0}}
@media(max-width:700px){.sites-panel{padding:15px}.panel-heading{align-items:flex-start}.panel-heading h1{font-size:19px}.site-tab{min-width:185px}.site-toolbar{padding:15px}.site-summary{padding:10px 14px}.rule-list{overflow:visible}.rule-row{grid-template-columns:22px minmax(0,1fr) auto;gap:9px;min-width:0;margin-bottom:9px;padding:13px;border:1px solid #dbe8e6;border-radius:11px!important}.rule-name{grid-column:2}.rule-guard{grid-column:3}.rule-route,.rule-traffic,.rule-health{grid-column:2/-1}.rule-actions{grid-column:2/-1;justify-content:flex-start;padding-top:4px}.number-grid,.rewrite-pair{grid-template-columns:1fr}.rewrite-pair>span{display:none}.collapse-tip{margin-left:0}:deep(.el-form-item){display:block}:deep(.el-form-item__label){width:100%!important;height:auto;justify-content:flex-start;margin-bottom:6px}:deep(.el-form-item__content){margin-left:0!important}}
</style>
