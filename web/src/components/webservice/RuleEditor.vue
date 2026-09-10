<template>
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
            <el-input v-model="ruleDialog.form.backendsText" :placeholder="$t('webService.backendsPlaceholder')" />
            <div class="backend-test-row">
              <el-button size="small" :loading="backendTesting" @click="testBackend">{{
                $t('webService.testFirstBackend')
              }}</el-button>
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
              <el-input
                v-model="h.value"
                type="password"
                show-password
                :placeholder="h.configured ? $t('common.keepEmpty') : $t('webService.headerValuePlaceholder')"
                style="width: 240px"
              />
              <el-button link type="danger" @click="ruleDialog.headersList.splice(i, 1)">{{
                $t('common.delete')
              }}</el-button>
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
                <el-input-number
                  v-model="ruleDialog.form.connectTimeoutSeconds"
                  :min="0"
                  :max="30"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.responseHeaderTimeout')">
                <el-input-number
                  v-model="ruleDialog.form.responseHeaderTimeoutSeconds"
                  :min="0"
                  :max="600"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.rateLimitRps')">
                <el-input-number
                  v-model="ruleDialog.form.rateLimitRPS"
                  :min="0"
                  :max="100000"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.rateLimitBurst')">
                <el-input-number
                  v-model="ruleDialog.form.rateLimitBurst"
                  :min="0"
                  :max="200000"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.maxBody')">
                <el-input-number
                  v-model="ruleDialog.form.maxRequestBodyMiB"
                  :min="0"
                  :max="10240"
                  controls-position="right"
                />
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
              <div class="rewrite-pair">
                <el-input
                  v-model="ruleDialog.form.cookieDomainFrom"
                  :placeholder="$t('webService.cookieDomainFromPlaceholder')"
                /><span>→</span
                ><el-input
                  v-model="ruleDialog.form.cookieDomainTo"
                  :placeholder="$t('webService.cookieDomainToPlaceholder')"
                />
              </div>
            </el-form-item>
            <el-form-item :label="$t('webService.cookiePath')">
              <div class="rewrite-pair">
                <el-input
                  v-model="ruleDialog.form.cookiePathFrom"
                  :placeholder="$t('webService.cookiePathFromPlaceholder')"
                /><span>→</span
                ><el-input
                  v-model="ruleDialog.form.cookiePathTo"
                  :placeholder="$t('webService.cookiePathToPlaceholder')"
                />
              </div>
            </el-form-item>
          </el-collapse-item>
          <el-collapse-item :title="$t('webService.healthTitle')" name="health">
            <el-form-item :label="$t('webService.healthEnable')">
              <el-switch v-model="ruleDialog.health.enabled" />
              <span class="form-tip">{{ $t('webService.healthEnableTip') }}</span>
            </el-form-item>
            <template v-if="ruleDialog.health.enabled">
              <el-form-item :label="$t('webService.healthType')">
                <el-radio-group v-model="ruleDialog.health.type">
                  <el-radio-button value="tcp">TCP</el-radio-button>
                  <el-radio-button value="http">HTTP</el-radio-button>
                </el-radio-group>
              </el-form-item>
              <el-form-item v-if="ruleDialog.health.type === 'http'" :label="$t('webService.healthPath')">
                <el-input v-model="ruleDialog.health.path" placeholder="/" style="width: 240px" />
              </el-form-item>
              <div class="number-grid">
                <el-form-item :label="$t('webService.healthInterval')">
                  <el-input-number
                    v-model="ruleDialog.health.intervalSeconds"
                    :min="2"
                    :max="300"
                    controls-position="right"
                  />
                </el-form-item>
                <el-form-item :label="$t('webService.healthTimeout')">
                  <el-input-number
                    v-model="ruleDialog.health.timeoutSeconds"
                    :min="1"
                    :max="60"
                    controls-position="right"
                  />
                </el-form-item>
                <el-form-item :label="$t('webService.healthRise')">
                  <el-input-number v-model="ruleDialog.health.rise" :min="1" :max="10" controls-position="right" />
                </el-form-item>
                <el-form-item :label="$t('webService.healthFall')">
                  <el-input-number v-model="ruleDialog.health.fall" :min="1" :max="10" controls-position="right" />
                </el-form-item>
              </div>
              <div class="collapse-tip">{{ $t('webService.healthTip') }}</div>
            </template>
          </el-collapse-item>
        </el-collapse>
      </template>

      <template v-if="ruleDialog.form.type === 'redirect'">
        <el-form-item :label="$t('webService.redirectTarget')" required>
          <el-input v-model="ruleDialog.form.redirectUrl" :placeholder="$t('webService.redirectUrlPlaceholder')" />
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
              <el-input
                v-model="ruleDialog.form.authPass"
                type="password"
                show-password
                :placeholder="
                  ruleDialog.form.authPassConfigured ? $t('common.keepEmpty') : $t('webService.enterPassword')
                "
                style="width: 240px"
              />
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
      <el-button type="primary" :loading="ruleDialog.saving" @click="confirmRule">{{
        $t('webService.confirmOk')
      }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../../api'
import { splitList } from '../../utils/list'
import { ipListError } from '../../utils/validate'

const props = defineProps({ site: { type: Object, default: null } })
const emit = defineEmits(['saved'])

const { t } = useI18n()

const emptyRule = () => ({
  id: '',
  name: '',
  type: 'reverse',
  enabled: true,
  frontendHost: '',
  frontendPath: '',
  backendsText: '',
  redirectUrl: '',
  redirectCode: 302,
  rootDir: '',
  preserveHost: false,
  autoProxyHeaders: true,
  skipBackendTlsVerify: false,
  stripPrefix: false,
  connectTimeoutSeconds: 5,
  responseHeaderTimeoutSeconds: 60,
  rateLimitRPS: 0,
  rateLimitBurst: 0,
  maxRequestBodyMiB: 0,
  rewriteLocation: false,
  cookieDomainFrom: '',
  cookieDomainTo: '',
  cookiePathFrom: '',
  cookiePathTo: '',
  basicAuth: false,
  authUser: '',
  authPass: '',
  authPassConfigured: false,
  ipListMode: '',
  uaListMode: ''
})

const emptyHealth = () => ({
  enabled: false,
  type: 'tcp',
  path: '/',
  intervalSeconds: 10,
  timeoutSeconds: 3,
  rise: 1,
  fall: 2
})

const ruleFormRef = ref()
const ruleDialog = reactive({
  visible: false,
  saving: false,
  index: -1,
  form: emptyRule(),
  headersList: [],
  ipListText: '',
  uaListText: '',
  health: emptyHealth(),
  healthWasEnabled: false
})
const backendTesting = ref(false)
const backendTestResult = ref('')

function open(row, index = -1) {
  if (!props.site) return
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
  ruleDialog.health = emptyHealth()
  ruleDialog.healthWasEnabled = false
  backendTestResult.value = ''
  ruleDialog.visible = true
  if (row?.id && (row.type || 'reverse') === 'reverse') loadRuleHealth(row.id)
}

async function loadRuleHealth(ruleID) {
  try {
    const res = await request.get(`/api/sites/${props.site.id}/rules/${ruleID}/health`)
    const conf = res.data?.conf
    if (conf && ruleDialog.visible && ruleDialog.form.id === ruleID) {
      ruleDialog.health = { ...emptyHealth(), ...conf, enabled: !!conf.enabled }
      ruleDialog.healthWasEnabled = !!conf.enabled
    }
  } catch {
    // 健康配置加载失败时保持默认，不影响规则编辑
  }
}

function healthError() {
  const h = ruleDialog.health
  if (!h.enabled) return ''
  if (h.type === 'http' && !(h.path || '').startsWith('/')) return t('webService.healthPathError')
  if (h.timeoutSeconds > h.intervalSeconds) return t('webService.healthTimeoutError')
  return ''
}

async function saveRuleHealth(siteID, ruleID) {
  if (ruleDialog.form.type !== 'reverse') return
  if (ruleDialog.health.enabled) {
    await request.put(`/api/sites/${siteID}/rules/${ruleID}/health`, { ...ruleDialog.health })
  } else if (ruleDialog.healthWasEnabled) {
    await request.delete(`/api/sites/${siteID}/rules/${ruleID}/health`)
  }
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
  if (f.type === 'reverse') {
    const hErr = healthError()
    if (hErr) {
      ElMessage.warning(hErr)
      return
    }
  }
  if (f.ipListMode) {
    const ipErr = ipListError(t, ruleDialog.ipListText)
    if (ipErr) {
      ElMessage.warning(ipErr)
      return
    }
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
    rateLimitBurst: f.rateLimitRPS ? f.rateLimitBurst || f.rateLimitRPS * 2 : 0,
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
    let ruleID = rule.id
    if (ruleDialog.index >= 0) {
      await request.put(`/api/sites/${props.site.id}/rules/${rule.id}`, rule)
    } else {
      const res = await request.post(`/api/sites/${props.site.id}/rules`, rule)
      ruleID = res.data?.id || ruleID
    }
    try {
      await saveRuleHealth(props.site.id, ruleID)
    } catch {
      // 规则已保存成功，仅健康检查配置失败时拦截器已提示
    }
    ElMessage.success(t('common.saveSuccess'))
    ruleDialog.visible = false
    emit('saved')
  } finally {
    ruleDialog.saving = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.form-tip {
  margin-left: 10px;
  color: var(--ap-muted);
  font-size: 12px;
}
.headers-editor,
.backend-editor {
  width: 100%;
}
.backend-test-row {
  display: flex;
  align-items: center;
  margin-top: 8px;
}
.number-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}
.number-grid :deep(.el-input-number) {
  width: 100%;
}
.rewrite-pair {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 8px;
  width: 100%;
}
.collapse-tip {
  margin: -4px 0 12px 110px;
  color: var(--ap-muted);
  font-size: 12px;
}
.header-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.security-collapse {
  width: 100%;
  margin-top: 4px;
}
@media (max-width: 700px) {
  .number-grid,
  .rewrite-pair {
    grid-template-columns: 1fr;
  }
  .rewrite-pair > span {
    display: none;
  }
  .collapse-tip {
    margin-left: 0;
  }
  :deep(.el-form-item) {
    display: block;
  }
  :deep(.el-form-item__label) {
    width: 100% !important;
    height: auto;
    justify-content: flex-start;
    margin-bottom: 6px;
  }
  :deep(.el-form-item__content) {
    margin-left: 0 !important;
  }
}
</style>
