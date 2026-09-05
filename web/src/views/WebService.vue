<template>
  <el-card>
    <template #header>
      <div class="card-header">
        <span>{{ $t('webService.title') }}</span>
        <el-button type="primary" size="small" @click="openSiteDialog()">{{ $t('webService.addSite') }}</el-button>
      </div>
    </template>
    <el-table :data="sites" v-loading="loading">
      <el-table-column prop="name" :label="$t('webService.colName')" min-width="110" />
      <el-table-column prop="listen" :label="$t('webService.colListen')" width="120" />
      <el-table-column label="TLS" width="110">
        <template #default="{ row }">
          <el-tag v-if="row.tls" type="success" size="small">HTTPS</el-tag>
          <el-tag v-else type="info" size="small">HTTP</el-tag>
          <el-tag v-if="row.tls && row.forceHttps && row.certId" type="warning" size="small" style="margin-left: 4px">{{ $t('webService.forceRedirect') }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="$t('webService.colSubRules')" width="80">
        <template #default="{ row }">{{ $t('webService.rulesCount', { n: (row.rules || []).length }) }}</template>
      </el-table-column>
      <el-table-column :label="$t('webService.colStatus')" min-width="140">
        <template #default="{ row }">
          <el-tag :type="siteStatusType(row)" size="small">{{ siteStatusText(row) }}</el-tag>
          <el-tooltip v-if="row.error" :content="row.error" placement="top">
            <span class="error-text">{{ row.error }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column :label="$t('webService.colEnabled')" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleSite(row)" />
        </template>
      </el-table-column>
      <el-table-column :label="$t('webService.colActions')" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openLogs(row)">{{ $t('common.logs') }}</el-button>
          <el-button link type="primary" @click="openSiteDialog(row)">{{ $t('common.edit') }}</el-button>
          <el-popconfirm :title="$t('webService.deleteConfirm')" @confirm="deleteSite(row)">
            <template #reference>
              <el-button link type="danger">{{ $t('common.delete') }}</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
      <template #empty><el-empty :description="$t('webService.empty')" :image-size="60" /></template>
    </el-table>

    <!-- 站点编辑对话框 -->
    <el-dialog
      v-model="siteDialog.visible"
      :title="siteDialog.isEdit ? $t('webService.editSite') : $t('webService.addSiteTitle')"
      width="720px"
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

      <div class="rules-header">
        <span class="rules-title">{{ $t('webService.subRules') }}</span>
        <el-button size="small" type="primary" plain @click="openRuleDialog()">{{ $t('webService.addRule') }}</el-button>
      </div>
      <el-table :data="siteDialog.rules" size="small" border>
        <el-table-column label="#" width="46">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="name" :label="$t('webService.colRuleName')" min-width="90">
          <template #default="{ row }">{{ row.name || '-' }}</template>
        </el-table-column>
        <el-table-column :label="$t('webService.colType')" width="90">
          <template #default="{ row }">
            <el-tag size="small">{{ ruleTypeText(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="$t('webService.colMatch')" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.frontendHost || '*' }}{{ row.frontendPath || '/' }}
          </template>
        </el-table-column>
        <el-table-column :label="$t('webService.colTarget')" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ ruleTarget(row) }}</template>
        </el-table-column>
        <el-table-column :label="$t('common.actions')" width="150">
          <template #default="{ row, $index }">
            <el-button link type="primary" :disabled="$index === 0" @click="moveRule($index, -1)">{{ $t('webService.moveUp') }}</el-button>
            <el-button link type="primary" @click="openRuleDialog(row, $index)">{{ $t('common.edit') }}</el-button>
            <el-button link type="danger" @click="siteDialog.rules.splice($index, 1)">{{ $t('common.delete') }}</el-button>
          </template>
        </el-table-column>
        <template #empty><span class="rules-empty">{{ $t('webService.emptySubRules') }}</span></template>
      </el-table>

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
        <el-button type="primary" @click="confirmRule">{{ $t('webService.confirmOk') }}</el-button>
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
  </el-card>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'

const { t } = useI18n()

const sites = ref([])
const certs = ref([])
const loading = ref(false)

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

// ---------- 站点对话框 ----------
const siteFormRef = ref()
const siteDialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  form: { id: '', name: '', listen: '', tls: false, certId: '', forceHttps: false, autoFw: false, enabled: true },
  rules: []
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
  siteDialog.rules = row ? JSON.parse(JSON.stringify(row.rules || [])) : []
  siteDialog.visible = true
}

function moveRule(index, delta) {
  const target = index + delta
  if (target < 0 || target >= siteDialog.rules.length) return
  const arr = siteDialog.rules
  ;[arr[index], arr[target]] = [arr[target], arr[index]]
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
    rules: siteDialog.rules
  }
  siteDialog.saving = true
  try {
    if (siteDialog.isEdit) {
      await request.put(`/api/sites/${f.id}`, body)
    } else {
      await request.post('/api/sites', body)
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
  index: -1,
  form: emptyRule(),
  headersList: [],
  ipListText: '',
  uaListText: ''
})
const backendTesting = ref(false)
const backendTestResult = ref('')

function openRuleDialog(row, index = -1) {
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

function confirmRule() {
  const f = ruleDialog.form
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
  if (ruleDialog.index >= 0) {
    siteDialog.rules[ruleDialog.index] = rule
  } else {
    siteDialog.rules.push(rule)
  }
  ruleDialog.visible = false
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

async function load() {
  loading.value = true
  try {
    const res = await request.get('/api/sites')
    sites.value = res.data || []
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
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

onMounted(() => {
  load()
  loadCerts()
})
</script>

<style scoped>
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.error-text {
  margin-left: 6px;
  color: var(--el-color-danger);
  font-size: 12px;
  max-width: 120px;
  display: inline-block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
.rules-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 8px 0 10px;
}
.rules-title {
  font-weight: 600;
  font-size: 14px;
}
.rules-empty {
  color: var(--ap-muted);
  font-size: 13px;
}
.form-tip {
  margin-left: 10px;
  color: var(--ap-muted);
  font-size: 12px;
}
.headers-editor {
  width: 100%;
}
.backend-editor { width: 100%; }
.backend-test-row { display: flex; align-items: center; margin-top: 8px; }
.number-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
.number-grid :deep(.el-input-number) { width: 100%; }
.rewrite-pair { width: 100%; display: grid; grid-template-columns: 1fr auto 1fr; gap: 8px; align-items: center; }
.collapse-tip { margin: -4px 0 12px 110px; color: var(--ap-muted); font-size: 12px; }
@media (max-width: 599px) {
  .number-grid { grid-template-columns: 1fr; }
  .rewrite-pair { grid-template-columns: 1fr; }
  .rewrite-pair > span { display: none; }
  .collapse-tip { margin-left: 0; }
  :deep(.el-form-item) { display: block; }
  :deep(.el-form-item__label) {
    width: 100% !important;
    height: auto;
    justify-content: flex-start;
    margin-bottom: 6px;
  }
  :deep(.el-form-item__content) { margin-left: 0 !important; }
}
.header-row {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
  align-items: center;
}
.security-collapse {
  width: 100%;
  margin-top: 4px;
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
