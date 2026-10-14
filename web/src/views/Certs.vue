<template>
  <div class="page-grid certificate-workspace">
    <header class="workspace-chrome">
      <div class="workspace-top">
        <h1 class="workspace-title">{{ t('certs.title') }}</h1>
        <div class="workspace-tools">
          <el-button @click="load">{{ t('common.refresh') }}</el-button
          ><el-button type="primary" @click="openDialog()">＋ {{ t('certs.add') }}</el-button>
        </div>
      </div>
      <div class="workspace-toolbar">
        <div class="workspace-pills">
          <button class="status-pill" :class="{ active: statusFilter === 'all' }" @click="statusFilter = 'all'">
            {{ w('count') }} <b>{{ certs.length }}</b></button
          ><button class="status-pill" :class="{ active: statusFilter === 'ok' }" @click="statusFilter = 'ok'">
            {{ w('ok') }} <b>{{ certs.filter((c) => c.status === 'ok').length }}</b></button
          ><button
            class="status-pill"
            :class="{ active: statusFilter === 'attention' }"
            @click="statusFilter = 'attention'"
          >
            {{ w('attention') }}
            <b>{{ certs.filter((c) => ['expired', 'expiring', 'error'].includes(c.status)).length }}</b></button
          ><button
            class="status-pill"
            :class="{ active: statusFilter === 'disabled' }"
            @click="statusFilter = 'disabled'"
          >
            {{ w('disabled') }} <b>{{ certs.filter((c) => !c.enabled).length }}</b>
          </button>
        </div>
        <el-input
          v-model="search"
          :placeholder="w('search')"
          clearable
          :aria-label="w('search')"
          class="workspace-search"
        />
      </div>
      <div class="source-filters">
        <el-radio-group v-model="sourceFilter" size="small" :aria-label="w('sourceFilter')"
          ><el-radio-button value="all">{{ w('allSources') }}</el-radio-button
          ><el-radio-button value="acme">ACME</el-radio-button
          ><el-radio-button value="uploaded">{{ w('uploaded') }}</el-radio-button></el-radio-group
        >
      </div>
    </header>
    <section class="workspace-surface cert-list" role="table" :aria-label="t('certs.title')" v-loading="loading">
      <div class="cert-row cert-columns" role="row">
        <span role="columnheader">{{ t('certs.colName') }}</span
        ><span role="columnheader">{{ t('certs.colExpiry') }}</span
        ><span role="columnheader">{{ w('sourceAlgorithm') }}</span
        ><span role="columnheader">{{ t('certs.colStatus') }}</span
        ><span role="columnheader">{{ t('common.actions') }}</span>
      </div>
      <article v-for="row in filteredCerts" :key="row.id" class="cert-row mobile-cert" role="row">
        <div class="cert-identity" role="cell">
          <b><span class="cert-state-dot" :class="row.enabled ? row.status : 'disabled'" />{{ row.name }}</b
          ><small>{{ (row.domains || []).join(', ') }}</small>
        </div>
        <div class="cert-expiry" role="cell">
          <b>{{
            row.remainingDays == null
              ? '-'
              : row.remainingDays < 0
                ? w('expired')
                : w('remaining', { n: row.remainingDays })
          }}</b
          ><small>{{ row.notAfter ? formatTime(row.notAfter) : '-' }}</small>
        </div>
        <div class="cert-source" role="cell">
          <b>{{ row.source === 'uploaded' ? w('uploaded') : 'ACME' }} · {{ row.keyType || w('defaultKey') }}</b
          ><small>{{ row.source === 'uploaded' ? w('manualRenewal') : providerName(row.providerId) }}</small>
        </div>
        <div class="cert-status" role="cell">
          <el-tag
            :type="!row.enabled ? 'info' : row.obtaining ? 'warning' : statusTagType(row.status)"
            size="small"
            effect="plain"
            >{{ !row.enabled ? w('disabled') : row.obtaining ? t('certs.obtaining') : statusText(row.status) }}</el-tag
          ><small>{{ (row.usingSites || []).map((site) => site.name || site.id).join(', ') || w('unbound') }}</small
          ><small v-if="row.lastError" class="error-text">{{ row.lastError }}</small>
        </div>
        <div class="workspace-row-actions action-cell" role="cell">
          <el-button
            size="small"
            :disabled="row.obtaining"
            @click="row.source === 'uploaded' ? openImport(row) : obtainCert(row)"
            >{{
              row.source === 'uploaded'
                ? w('replace')
                : row.status === 'ok' || row.status === 'expiring'
                  ? t('certs.reissue')
                  : t('certs.obtain')
            }}</el-button
          ><el-switch
            :model-value="row.enabled"
            :aria-label="t('certs.colEnabled') + ' ' + row.name"
            @change="toggleCert(row)"
          /><el-dropdown trigger="click" @command="certCommand(row, $event)"
            ><el-button class="workspace-more" :aria-label="w('more') + ' ' + row.name">•••</el-button
            ><template #dropdown
              ><el-dropdown-menu
                ><el-dropdown-item v-if="row.source !== 'uploaded'" command="edit">{{
                  t('common.edit')
                }}</el-dropdown-item
                ><el-dropdown-item command="mapping">{{
                  locale.startsWith('zh') ? '证书映射' : 'Mappings'
                }}</el-dropdown-item
                ><el-dropdown-item command="replace">{{ w('replace') }}</el-dropdown-item
                ><el-dropdown-item command="cert">{{ t('certs.cert') }}</el-dropdown-item
                ><el-dropdown-item command="key">{{ t('certs.key') }}</el-dropdown-item
                ><el-dropdown-item command="delete" divided>{{
                  t('common.delete')
                }}</el-dropdown-item></el-dropdown-menu
              ></template
            ></el-dropdown
          >
        </div>
      </article>
      <el-empty v-if="!filteredCerts.length" :description="t('certs.empty')" :image-size="54" />
    </section>

    <el-dialog
      class="workspace-dialog"
      v-model="dialog.visible"
      :title="dialog.isEdit ? $t('certs.editTitle') : $t('certs.addTitle')"
      width="min(560px, 94vw)"
      destroy-on-close
      @closed="clearSecrets"
    >
      <el-form ref="formRef" :model="dialog.form" :rules="rules" label-width="110px">
        <el-form-item :label="w('source')" prop="source">
          <el-radio-group v-model="dialog.form.source" :disabled="dialog.isEdit">
            <el-radio value="acme">{{ w('acme') }}</el-radio>
            <el-radio value="uploaded">{{ w('uploaded') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <template v-if="dialog.form.source">
          <el-form-item :label="$t('certs.certName')" prop="name">
            <el-input v-model="dialog.form.name" :placeholder="$t('certs.certNamePlaceholder')" />
          </el-form-item>
          <el-form-item :label="$t('certs.domains')" prop="domainsText">
            <el-input
              v-model="dialog.form.domainsText"
              type="textarea"
              :rows="2"
              :placeholder="$t('certs.domainsPlaceholder')"
            />
          </el-form-item>
          <template v-if="dialog.form.source === 'acme'">
            <el-form-item :label="$t('certs.dnsCredential')" prop="providerId">
              <el-select
                v-model="dialog.form.providerId"
                style="width: 100%"
                :placeholder="$t('certs.providerPlaceholder')"
              >
                <el-option
                  v-for="p in providers"
                  :key="p.id"
                  :label="(p.remark || p.id) + '（' + providerTypeName(p.type) + '）'"
                  :value="p.id"
                />
              </el-select>
            </el-form-item>
            <el-form-item :label="w('algorithm')">
              <el-select v-model="dialog.form.keyType" :aria-label="w('algorithm')">
                <el-option :label="w('keepDefault')" value="" />
                <el-option
                  v-for="algo in ['P256', 'P384', 'RSA2048', 'RSA4096']"
                  :key="algo"
                  :label="algo"
                  :value="algo"
                />
              </el-select>
            </el-form-item>
            <el-form-item :label="$t('certs.email')" prop="email">
              <el-input v-model="dialog.form.email" :placeholder="$t('certs.emailPlaceholder')" />
            </el-form-item>
            <el-form-item :label="$t('certs.caDir')">
              <el-input v-model="dialog.form.caDirUrl" :placeholder="$t('certs.caDirPlaceholder')" />
            </el-form-item>
            <el-collapse
              ><el-collapse-item
                :title="locale.startsWith('zh') ? '高级签发参数' : 'Advanced issuance'"
                name="advanced"
              >
                <el-form-item :label="locale.startsWith('zh') ? 'CA 信任证书' : 'CA trust certificate'"
                  ><el-input
                    v-model="dialog.form.caTrustPem"
                    type="textarea"
                    :rows="3"
                    placeholder="-----BEGIN CERTIFICATE-----"
                /></el-form-item>
                <el-form-item label="EAB"
                  ><el-switch v-model="dialog.form.eabEnabled" /><HelpHint
                    label="EAB"
                    :text="
                      locale.startsWith('zh')
                        ? '部分证书颁发机构要求外部账户绑定。需填写该 CA 提供的 Key ID 和 HMAC 密钥；不要求 EAB 的 CA 保持关闭。'
                        : 'Some certificate authorities require External Account Binding. Supply the Key ID and HMAC secret from that CA. Leave off for CAs that do not require EAB.'
                    "
                /></el-form-item>
                <template v-if="dialog.form.eabEnabled"
                  ><el-form-item label="Key ID"><el-input v-model="dialog.form.eabKeyId" /></el-form-item
                  ><el-form-item label="HMAC"
                    ><el-input
                      v-model="dialog.form.eabHmac"
                      type="password"
                      show-password
                      :placeholder="dialog.form.eabConfigured ? $t('common.keepEmpty') : ''" /></el-form-item
                ></template>
                <el-form-item :label="locale.startsWith('zh') ? '递归 DNS' : 'Recursive DNS'"
                  ><el-input v-model="dialog.form.resolversText" type="textarea" :rows="2" placeholder="1.1.1.1:53"
                /></el-form-item>
                <el-form-item :label="locale.startsWith('zh') ? '传播超时（秒）' : 'Propagation timeout (s)'"
                  ><el-input-number v-model="dialog.form.propagationTimeoutSeconds" :min="0" :max="1800"
                /></el-form-item>
                <el-form-item :label="locale.startsWith('zh') ? '轮询间隔（秒）' : 'Polling interval (s)'"
                  ><el-input-number v-model="dialog.form.propagationIntervalSeconds" :min="0" :max="60"
                /></el-form-item>
                <p class="muted">
                  {{
                    locale.startsWith('zh')
                      ? '0 沿用默认超时120秒、轮询5秒。更换 EAB Key ID 时须提供新 HMAC。'
                      : '0 uses 120s timeout and 5s polling. Changing EAB Key ID requires a new HMAC.'
                  }}
                </p>
              </el-collapse-item></el-collapse
            >
            <el-form-item :label="$t('certs.renewDays')">
              <el-input-number v-model="dialog.form.renewDays" :min="1" :max="89" />
              <span class="form-tip">{{ $t('certs.renewTip') }}</span>
            </el-form-item>
          </template>
          <template v-else>
            <el-alert :title="w('importHint')" type="info" :closable="false" />
            <el-form-item :label="w('certPEM')" prop="certPEM">
              <input
                type="file"
                accept=".pem,.crt,.cer"
                :aria-label="w('selectCert')"
                @change="readPEM($event, 'certPEM')"
              />
              <el-input
                v-model="dialog.form.certPEM"
                type="textarea"
                :rows="4"
                :placeholder="w('pasteCert')"
                autocomplete="off"
              />
            </el-form-item>
            <el-form-item :label="w('keyPEM')" prop="keyPEM">
              <input type="file" accept=".pem,.key" :aria-label="w('selectKey')" @change="readPEM($event, 'keyPEM')" />
              <el-input
                v-model="dialog.form.keyPEM"
                type="textarea"
                :rows="4"
                :placeholder="w('pasteKey')"
                autocomplete="off"
              />
            </el-form-item>
            <p class="form-tip">
              {{ w('validationHint') }}
            </p>
          </template>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
    <CertMappings ref="mappingsRef" @saved="load" />
  </div>
</template>

<script setup>
import CertMappings from '../components/CertMappings.vue'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Loading } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import HelpHint from '../components/HelpHint.vue'
import { formatTime } from '../utils/format'
import { splitList } from '../utils/list'
import { useCrudDialog } from '../composables/useCrudDialog'
import { useIntervalFn } from '../composables/useIntervalFn'

const { t, locale } = useI18n()
const w = (key, params) => t(`workspace.certs.${key}`, params)

const mappingsRef = ref()
const certs = ref([])
const providers = ref([])
const search = ref('')
const statusFilter = ref('all')
const sourceFilter = ref('all')
const filteredCerts = computed(() =>
  certs.value.filter((c) => {
    const query = search.value.trim().toLowerCase()
    const haystack = [c.name, ...(c.domains || []), ...(c.usingSites || []).map((s) => s.name || s.id)]
      .join(' ')
      .toLowerCase()
    return (
      (!query || haystack.includes(query)) &&
      (sourceFilter.value === 'all' || (c.source || 'acme') === sourceFilter.value) &&
      (statusFilter.value === 'all' ||
        (statusFilter.value === 'disabled'
          ? !c.enabled
          : statusFilter.value === 'attention'
            ? ['expired', 'expiring', 'error'].includes(c.status)
            : c.status === statusFilter.value))
    )
  })
)
const loading = ref(false)
const certPoller = useIntervalFn(load, 5000)

const PROVIDER_TYPES = ['aliyun', 'cloudflare', 'dnspod']
function providerTypeName(type) {
  return PROVIDER_TYPES.includes(type) ? t(`ddns.providerTypes.${type}`) : type
}

const isMobile = ref(window.innerWidth < 768)
function onResize() {
  isMobile.value = window.innerWidth < 768
}

const statusTagTypes = {
  pending: 'info',
  ok: 'success',
  expiring: 'warning',
  expired: 'danger',
  error: 'danger'
}
function statusText(s) {
  return ['pending', 'ok', 'expiring', 'expired', 'error'].includes(s) ? t(`certs.statusTexts.${s}`) : s
}
function statusTagType(s) {
  return statusTagTypes[s] || 'info'
}

const {
  formRef,
  dialog,
  open: openBaseDialog,
  submit: save
} = useCrudDialog({
  emptyForm: () => ({
    id: '',
    source: '',
    keyType: '',
    caTrustPem: '',
    eabEnabled: false,
    eabKeyId: '',
    eabHmac: '',
    eabConfigured: false,
    resolversText: '',
    propagationTimeoutSeconds: 0,
    propagationIntervalSeconds: 0,
    mappings: [],
    certPEM: '',
    keyPEM: '',
    name: '',
    domainsText: '',
    providerId: '',
    email: '',
    caDirUrl: '',
    renewDays: 30,
    enabled: true
  }),
  fromRow: (row) => ({
    id: row.id,
    source: row.source || 'acme',
    keyType: row.keyType || '',
    caTrustPem: row.caTrustPem || '',
    eabEnabled: !!row.eabEnabled,
    eabKeyId: row.eabKeyId || '',
    eabHmac: '',
    eabConfigured: !!row.eabConfigured,
    resolversText: (row.dnsResolvers || []).join('\n'),
    propagationTimeoutSeconds: row.propagationTimeoutSeconds || 0,
    propagationIntervalSeconds: row.propagationIntervalSeconds || 0,
    mappings: JSON.parse(JSON.stringify(row.mappings || [])),
    certPEM: '',
    keyPEM: '',
    name: row.name,
    domainsText: (row.domains || []).join(', '),
    providerId: row.providerId,
    email: row.email,
    caDirUrl: row.caDirUrl || '',
    renewDays: row.renewDays || 30,
    enabled: row.enabled
  }),
  onSubmit: (f, d) => {
    if (f.source === 'uploaded') {
      return request.post('/api/certs/import', {
        id: d.isEdit ? f.id : '',
        name: f.name,
        enabled: f.enabled,
        domains: splitList(f.domainsText),
        certPEM: f.certPEM,
        keyPEM: f.keyPEM
      })
    }
    const body = {
      source: f.source,
      keyType: f.keyType,
      caTrustPem: f.caTrustPem,
      eabEnabled: f.eabEnabled,
      eabKeyId: f.eabKeyId,
      eabHmac: f.eabHmac,
      dnsResolvers: splitList(f.resolversText),
      propagationTimeoutSeconds: f.propagationTimeoutSeconds,
      propagationIntervalSeconds: f.propagationIntervalSeconds,
      mappings: f.mappings,
      name: f.name,
      enabled: f.enabled,
      domains: splitList(f.domainsText),
      providerId: f.providerId,
      email: f.email,
      caDirUrl: f.caDirUrl,
      renewDays: f.renewDays
    }
    return d.isEdit ? request.put(`/api/certs/${f.id}`, body) : request.post('/api/certs', body)
  },
  onSaved: () => load()
})

const rules = computed(() => ({
  source: [{ required: true, message: w('sourceRequired'), trigger: 'change' }],
  certPEM: [{ required: dialog.form.source === 'uploaded', message: w('certRequired'), trigger: 'blur' }],
  keyPEM: [{ required: dialog.form.source === 'uploaded', message: w('keyRequired'), trigger: 'blur' }],
  name: [{ required: true, message: t('certs.nameRequired'), trigger: 'blur' }],
  domainsText: [{ required: dialog.form.source === 'acme', message: t('certs.domainsRequired'), trigger: 'blur' }],
  providerId: [{ required: dialog.form.source === 'acme', message: t('certs.providerRequired'), trigger: 'change' }],
  email: [{ required: dialog.form.source === 'acme', message: t('certs.emailRequired'), trigger: 'blur' }]
}))

let editGeneration = 0
function openDialog(row) {
  editGeneration++
  openBaseDialog(row)
}
function providerName(id) {
  const p = providers.value.find((p) => p.id === id)
  return p ? p.remark || providerTypeName(p.type) : '-'
}
function certCommand(row, command) {
  if (command === 'mapping') {
    mappingsRef.value.open(row)
    return
  }
  if (command === 'edit') openDialog(row)
  else if (command === 'replace') openImport(row)
  else if (command === 'delete') deleteCert(row)
  else download(row, command)
}
function openImport(row) {
  openDialog(row)
  dialog.form.source = 'uploaded'
}
function clearSecrets() {
  editGeneration++
  dialog.form.certPEM = ''
  dialog.form.keyPEM = ''
  dialog.form.eabHmac = ''
}
async function readPEM(event, field) {
  const generation = editGeneration,
    editingForm = dialog.form
  const file = event.target.files?.[0]
  if (!file) return
  if (file.size > 256 * 1024) {
    ElMessage.error(w('fileTooLarge'))
    event.target.value = ''
    return
  }
  try {
    const value = await file.text()
    if (generation === editGeneration && dialog.visible && dialog.form === editingForm) dialog.form[field] = value
  } catch {
    ElMessage.error(w('readFailed'))
  }
  event.target.value = ''
}

async function toggleCert(row) {
  try {
    await request.post(`/api/certs/${row.id}/toggle`)
    load()
  } catch {
    // 拦截器已提示；刷新真实状态，避免开关停留在错误位置
    load()
  }
}

async function obtainCert(row) {
  try {
    await ElMessageBox.confirm(t('certs.obtainConfirm'), t('certs.obtainTitle'), {
      type: 'warning',
      confirmButtonText: t('certs.continue'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  try {
    await request.post(`/api/certs/${row.id}/obtain`, {}, { timeout: 120000 })
    ElMessage.info(t('certs.obtainStarted'))
    setTimeout(load, 1500)
  } catch {
    // 拦截器已提示
  }
}

async function download(row, part) {
  if (part === 'key') {
    try {
      await ElMessageBox.confirm(t('certs.keyDownloadConfirm'), t('certs.keyDownloadTitle'), {
        type: 'warning',
        confirmButtonText: t('certs.download'),
        cancelButtonText: t('common.cancel')
      })
    } catch {
      return
    }
  }
  const a = document.createElement('a')
  a.href = `/api/certs/${row.id}/download?part=${part}`
  a.download = ''
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

async function deleteCert(row) {
  try {
    await ElMessageBox.confirm(t('certs.deleteConfirm'), t('common.delete'), { type: 'warning' })
    await request.delete(`/api/certs/${row.id}`)
    ElMessage.success(t('common.deleted'))
    load()
  } catch {
    // 拦截器已提示
  }
}

async function load() {
  loading.value = true
  try {
    const res = await request.get('/api/certs')
    certs.value = res.data || []
    // 有证书正在申请时保持轮询，全部完成后停止
    if (certs.value.some((c) => c.obtaining)) certPoller.start()
    else certPoller.stop()
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

async function loadProviders() {
  try {
    const res = await request.get('/api/providers')
    providers.value = res.data || []
  } catch {
    // 拦截器已提示
  }
}

onMounted(() => {
  load()
  loadProviders()
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
})
</script>

<style scoped>
.source-filters {
  margin-top: 10px;
}
.cert-row {
  display: grid;
  grid-template-columns: minmax(200px, 1.8fr) minmax(140px, 1fr) minmax(150px, 1fr) minmax(120px, 1fr) auto;
  align-items: center;
  gap: 18px;
  padding: 14px 16px;
  min-width: 0;
  border-bottom: 1px solid var(--ap-border);
  font-size: 13px;
}
.cert-row:last-child {
  border-bottom: 0;
}
.cert-columns {
  font-size: 12px;
  color: var(--ap-muted);
  padding-top: 10px;
  padding-bottom: 10px;
}
.cert-row b {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.cert-row small {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: var(--ap-muted);
  overflow-wrap: anywhere;
}
.cert-identity b {
  font-size: 14px;
  display: flex;
  gap: 8px;
  align-items: center;
}
.cert-identity small {
  padding-left: 16px;
}
.cert-state-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ap-info);
  flex: 0 0 auto;
}
.cert-state-dot.ok {
  background: var(--ap-success);
}
.cert-state-dot.expiring {
  background: var(--ap-warning);
}
.cert-state-dot.expired,
.cert-state-dot.error {
  background: var(--ap-danger);
}
.cert-row .error-text {
  color: var(--ap-danger);
}
.form-tip {
  margin-left: 10px;
  color: var(--ap-muted);
  font-size: 12px;
}
@media (max-width: 1100px) {
  .cert-row {
    grid-template-columns: minmax(180px, 1.5fr) minmax(130px, 1fr) minmax(140px, 1fr) auto;
    gap: 12px;
  }
  .cert-status {
    grid-column: 1/-1;
    display: flex;
    gap: 10px;
    align-items: center;
  }
  .cert-columns span:nth-child(4) {
    display: none;
  }
}
@media (max-width: 767px) {
  .cert-columns {
    display: none;
  }
  .cert-row {
    grid-template-columns: 1fr 1fr;
    gap: 14px;
    padding: 14px;
  }
  .cert-identity {
    grid-column: 1/-1;
  }
  .cert-status,
  .action-cell {
    grid-column: 1/-1;
  }
  .workspace-row-actions {
    justify-content: flex-start;
  }
  .action-cell .el-button {
    min-height: 44px;
  }
  .form-tip {
    display: block;
    margin-left: 0;
    margin-top: 4px;
  }
  .cert-row .workspace-more {
    min-width: 44px;
  }
  .source-filters .el-radio-button :deep(.el-radio-button__inner) {
    min-height: 44px;
    display: flex;
    align-items: center;
  }
}
</style>
