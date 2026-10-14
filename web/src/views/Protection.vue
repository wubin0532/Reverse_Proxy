<template>
  <div class="protection-workspace workspace-chrome">
    <header class="workspace-head workspace-toolbar">
      <div>
        <h1 class="workspace-title">{{ w('title') }}</h1>
        <p class="muted">{{ w('description') }}</p>
      </div>
      <div class="actions">
        <el-button @click="load">{{ t('common.refresh') }}</el-button
        ><el-button v-if="tab === 'policies'" type="primary" @click="editPolicy()">＋ {{ w('newPolicy') }}</el-button
        ><el-button v-if="tab === 'bans'" type="primary" @click="banOpen = true">＋ {{ w('newBan') }}</el-button>
      </div>
    </header>
    <div class="summary protection-summary">
      <span class="status-pill"
        >{{ w('policies') }} <b>{{ summary.policies ?? '—' }}</b></span
      >
      <span class="status-pill"
        >{{ labels.globalActive }} <b>{{ summary.globalPolicies ?? '—' }}</b></span
      >
      <span class="status-pill"
        >{{ w('activeBans') }} <b>{{ summary.activeBans ?? '—' }}</b></span
      >
      <span class="status-pill"
        >{{ w('rejected') }} <b>{{ summary.rejected ?? '—' }}</b></span
      >
    </div>
    <el-tabs v-model="tab"
      ><el-tab-pane :label="w('policies')" name="policies" /><el-tab-pane :label="w('bans')" name="bans" /><el-tab-pane
        :label="w('events')"
        name="events" /><el-tab-pane
        :label="locale.startsWith('zh') ? 'TCP 诱捕' : 'TCP trap'"
        name="trap" /><el-tab-pane :label="locale.startsWith('zh') ? '安全组' : 'Access groups'" name="groups"
    /></el-tabs>
    <el-input
      v-if="tab !== 'trap'"
      v-model="search"
      clearable
      :placeholder="w('search')"
      :aria-label="w('search')"
      class="search"
    />
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <el-alert
      v-if="['bans', 'events'].includes(tab)"
      :title="w('memoryHint')"
      type="info"
      :closable="false"
      class="memory-note"
    />
    <div v-if="tab === 'policies'" class="protection-list-head" aria-hidden="true">
      <span>{{ labels.policy }}</span
      ><span>{{ labels.controls }}</span
      ><span>{{ labels.scope }}</span
      ><span>{{ labels.actions }}</span>
    </div>
    <TCPTrap v-if="tab === 'trap'" />
    <AccessGroups v-else-if="tab === 'groups'" />
    <section v-else v-loading="loading" class="rows">
      <template v-if="tab === 'policies'">
        <article v-for="p in visiblePolicies" :key="p.id" class="workspace-row policy-row">
          <div class="policy-name">
            <b>{{ p.name }}</b>
            <div class="details">
              <el-tag size="small" :type="p.enabled ? 'success' : 'info'">{{
                t(p.enabled ? 'common.tagEnabled' : 'common.tagDisabled')
              }}</el-tag
              ><el-tag v-if="p.global" size="small" :type="p.enabled ? 'warning' : 'info'">{{ w('global') }}</el-tag>
            </div>
          </div>
          <div class="policy-controls">
            <span
              >IP / CIDR · {{ modeLabel(p.ipListMode) }} <b>{{ p.ipList?.length || 0 }}</b></span
            ><span
              >UA · {{ modeLabel(p.uaListMode) }} <b>{{ p.uaList?.length || 0 }}</b></span
            >
          </div>
          <div class="policy-scope" :title="scopeFor(p)">
            <span>{{ scopeFor(p) }}</span
            ><small>{{ labels.uaOnly }}</small>
          </div>
          <div class="actions">
            <el-button text @click="editPolicy(p)">{{ t('common.edit') }}</el-button
            ><el-popconfirm :title="w('deletePolicy')" @confirm="removePolicy(p)"
              ><template #reference
                ><el-button text type="danger">{{ t('common.delete') }}</el-button></template
              ></el-popconfirm
            >
          </div>
        </article>
        <el-empty v-if="!visiblePolicies.length" :description="w('noPolicies')" />
      </template>
      <template v-else-if="tab === 'bans'">
        <article v-for="b in visibleBans" :key="b.ip" class="workspace-row">
          <div>
            <b>{{ b.ip }}</b>
            <p>{{ b.reason }}</p>
            <small>{{ w('expires') }} {{ formatTime(b.expiresAt) }}</small>
          </div>
          <el-popconfirm :title="w('releaseConfirm')" @confirm="releaseBan(b)"
            ><template #reference
              ><el-button>{{ w('release') }}</el-button></template
            ></el-popconfirm
          >
        </article>
        <el-empty v-if="!visibleBans.length" :description="w('noBans')" />
      </template>
      <template v-else>
        <article v-for="e in visibleEvents" :key="e.id" class="workspace-row">
          <div>
            <b>{{ e.ip }}</b>
            <el-tag :type="e.action === 'observe' ? 'warning' : 'danger'" size="small">{{
              e.action === 'observe'
                ? locale.startsWith('zh')
                  ? '观察'
                  : 'Observed'
                : e.action === 'ban'
                  ? locale.startsWith('zh')
                    ? '封禁'
                    : 'Banned'
                  : labels.denied
            }}</el-tag>
            <p>{{ e.reason }}</p>
            <small
              >{{ formatTime(e.time) }} · {{ e.source.toUpperCase() }} ·
              {{ entityNames[e.entityId] || e.entityId }}</small
            >
          </div>
        </article>
        <el-empty v-if="!visibleEvents.length" :description="w('noEvents')" />
      </template>
    </section>
    <el-dialog
      class="workspace-dialog"
      v-model="policyOpen"
      :title="w(policy.id ? 'editPolicy' : 'newPolicy')"
      width="min(640px, 94vw)"
      :close-on-click-modal="false"
    >
      <el-form ref="form" :model="policy" label-position="top">
        <el-form-item :label="t('common.name')" prop="name" :rules="[{ required: true, message: w('nameRequired') }]"
          ><el-input v-model="policy.name" maxlength="128"
        /></el-form-item>
        <div class="options">
          <el-checkbox v-model="policy.enabled">{{ t('common.enabled') }}</el-checkbox
          ><el-checkbox v-model="policy.global">{{ w('global') }}</el-checkbox>
        </div>
        <p class="muted">{{ w('globalHint') }}</p>
        <el-tabs
          ><el-tab-pane :label="w('ipRules')"
            ><el-form-item :label="w('mode')"
              ><el-select v-model="policy.ipListMode"
                ><el-option value="" :label="w('off')" /><el-option
                  value="blacklist"
                  :label="w('blacklist')" /><el-option
                  value="whitelist"
                  :label="w('whitelist')" /></el-select></el-form-item
            ><el-form-item :label="w('ipHint')"
              ><el-input v-model="ipText" type="textarea" :rows="5" /></el-form-item></el-tab-pane
          ><el-tab-pane label="User-Agent"
            ><el-form-item :label="w('mode')"
              ><el-select v-model="policy.uaListMode"
                ><el-option value="" :label="w('off')" /><el-option
                  value="blacklist"
                  :label="w('blacklist')" /><el-option
                  value="whitelist"
                  :label="w('whitelist')" /></el-select></el-form-item
            ><el-form-item :label="w('uaHint')"
              ><el-input v-model="uaText" type="textarea" :rows="5" /></el-form-item></el-tab-pane
          ><el-tab-pane label="WAF / CC">
            <el-form-item :label="locale.startsWith('zh') ? '启用请求侧 WAF' : 'Enable request WAF'"
              ><el-switch v-model="policy.waf.enabled" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'waf')"
            /></el-form-item>
            <template v-if="policy.waf.enabled"
              ><el-form-item :label="locale.startsWith('zh') ? '仅观察，不阻断' : 'Observe without blocking'"
                ><el-switch v-model="policy.waf.detectionOnly" /><HelpHint
                  :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                  :text="helpText(locale, 'detectionOnly')" /></el-form-item
              ><el-form-item :label="locale.startsWith('zh') ? '请求体检测上限（KiB）' : 'Request body limit (KiB)'"
                ><el-input-number v-model="policy.waf.bodyLimitKiB" :min="16" :max="1024"
              /></el-form-item>
              <p class="muted">
                {{
                  locale.startsWith('zh')
                    ? 'Coraza + OWASP CRS；仅检测请求，不缓存响应正文。阻断模式超出请求体上限返回413；观察模式只检查前缀并完整转发。'
                    : 'Coraza + OWASP CRS, request inspection only. Blocking mode rejects oversized bodies with 413; observation inspects a prefix and forwards the complete body.'
                }}
              </p></template
            >
            <el-divider />
            <el-form-item :label="locale.startsWith('zh') ? '启用 CC 防护' : 'Enable CC protection'"
              ><el-switch v-model="policy.cc.enabled" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'cc')"
            /></el-form-item>
            <template v-if="policy.cc.enabled"
              ><el-form-item :label="locale.startsWith('zh') ? '窗口（秒）' : 'Window (s)'"
                ><el-input-number v-model="policy.cc.windowSeconds" :min="1" :max="3600" /></el-form-item
              ><el-form-item :label="locale.startsWith('zh') ? '每IP最大请求数' : 'Requests per IP'"
                ><el-input-number v-model="policy.cc.maxRequests" :min="0" :max="100000" /></el-form-item
              ><el-form-item :label="locale.startsWith('zh') ? '连续404阈值' : 'Consecutive 404 threshold'"
                ><el-input-number v-model="policy.cc.notFoundThreshold" :min="0" :max="10000" /></el-form-item
              ><el-form-item :label="locale.startsWith('zh') ? '自动封禁（秒）' : 'Automatic ban (s)'"
                ><el-input-number v-model="policy.cc.banSeconds" :min="0" :max="86400"
              /></el-form-item>
              <p class="muted">
                {{
                  locale.startsWith('zh')
                    ? '0关闭对应阈值；连续404遇到其它状态会清零。计数按策略和IP汇总，重启清空。'
                    : '0 disables a threshold. Non-404 responses reset consecutive 404 counts. Counters are grouped by policy and IP and clear on restart.'
                }}
              </p></template
            >
          </el-tab-pane>
          ></el-tabs
        > </el-form
      ><template #footer
        ><el-button @click="policyOpen = false">{{ t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="saving" @click="savePolicy">{{ t('common.save') }}</el-button></template
      >
    </el-dialog>
    <el-dialog v-model="banOpen" :title="w('newBan')" width="min(480px,94vw)" :close-on-click-modal="false"
      ><el-form label-position="top"
        ><el-form-item label="IP"><el-input v-model="ban.ip" placeholder="203.0.113.10 / 2001:db8::1" /></el-form-item
        ><el-form-item :label="w('reason')"><el-input v-model="ban.reason" maxlength="256" /></el-form-item
        ><el-form-item :label="w('duration')"
          ><el-select v-model="ban.seconds"
            ><el-option :value="3600" :label="w('oneHour')" /><el-option
              :value="86400"
              :label="w('oneDay')" /></el-select></el-form-item></el-form
      ><template #footer
        ><el-button @click="banOpen = false">{{ t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="saving" @click="saveBan">{{ t('common.save') }}</el-button></template
      ></el-dialog
    >
  </div>
</template>
<script setup>
import AccessGroups from '../components/AccessGroups.vue'
import TCPTrap from '../components/TCPTrap.vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import request from '../api'
import HelpHint from '../components/HelpHint.vue'
import { helpText } from '../components/webservice/help'
import { formatTime } from '../utils/format'
import { useIntervalFn } from '../composables/useIntervalFn'
const { t, locale } = useI18n(),
  w = (k) => t(`workspace.protection.${k}`)
const labels = computed(() =>
  locale.value.startsWith('zh')
    ? {
        policy: '策略',
        controls: '匹配条件',
        scope: '绑定范围',
        actions: '操作',
        globalActive: '全局启用',
        all: '所有业务入口',
        unused: '未绑定',
        unknown: '绑定范围待核查',
        uaOnly: 'UA 仅适用于 Web 请求',
        disabled: '已停用',
        denied: '拒绝',
        bound: '绑定'
      }
    : {
        policy: 'Policy',
        controls: 'Match conditions',
        scope: 'Applies to',
        actions: 'Actions',
        globalActive: 'Global enabled',
        all: 'All service entrances',
        unused: 'Not bound',
        unknown: 'Binding scope unavailable',
        uaOnly: 'UA applies to Web requests only',
        disabled: 'Disabled',
        denied: 'Denied',
        bound: 'Bound'
      }
)
const entityNames = ref({}),
  policyBindings = ref({}),
  scopeKnown = ref(false)
const modeLabel = (mode) => w(['blacklist', 'whitelist'].includes(mode) ? mode : 'off')
function scopeFor(p) {
  const bindings = policyBindings.value[p.id] || []
  const prefix = p.enabled ? '' : labels.value.disabled + ' · '
  if (p.global) return prefix + labels.value.all
  if (!scopeKnown.value) return prefix + labels.value.unknown
  return prefix + (bindings.length ? bindings.join('、') : labels.value.unused)
}
const tab = ref('policies'),
  search = ref(''),
  policies = ref([]),
  bans = ref([]),
  events = ref([]),
  summary = reactive({}),
  loading = ref(false),
  error = ref(''),
  saving = ref(false),
  policyOpen = ref(false),
  banOpen = ref(false),
  form = ref(),
  ipText = ref(''),
  uaText = ref('')
const defaults = () => ({
    id: '',
    name: '',
    enabled: true,
    global: false,
    ipListMode: 'blacklist',
    ipList: [],
    uaListMode: '',
    uaList: [],
    waf: { enabled: false, detectionOnly: true, bodyLimitKiB: 256 },
    cc: { enabled: false, windowSeconds: 60, maxRequests: 0, notFoundThreshold: 0, banSeconds: 0 }
  }),
  policy = reactive(defaults()),
  ban = reactive({ ip: '', reason: '', seconds: 3600 })
const matches = (x) => JSON.stringify(x).toLowerCase().includes(search.value.toLowerCase())
const visiblePolicies = computed(() => policies.value.filter(matches)),
  visibleBans = computed(() => bans.value.filter(matches)),
  visibleEvents = computed(() => events.value.filter(matches))
async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const results = await Promise.allSettled([
      ...['policies', 'bans', 'events', 'summary'].map((x) => request.get(`/api/protection/${x}`)),
      request.get('/api/sites'),
      request.get('/api/forwards')
    ])
    ;[policies, bans, events].forEach((dest, i) => {
      if (results[i].status === 'fulfilled') dest.value = results[i].value.data || []
    })
    if (results[3].status === 'fulfilled') Object.assign(summary, results[3].value.data || {})
    scopeKnown.value = results[4].status === 'fulfilled' && results[5].status === 'fulfilled'
    if (scopeKnown.value) {
      const names = {},
        bindings = {}
      const bind = (id, name) => {
        if (id) {
          ;(bindings[id] ??= []).push(name)
        }
      }
      for (const site of results[4].value.data || []) {
        names[site.id] = site.name
        bind(site.policyId, site.name)
        for (const rule of site.rules || []) {
          names[rule.id] = site.name + ' / ' + rule.name
          bind(rule.policyId, names[rule.id])
        }
      }
      for (const forward of results[5].value.data || []) {
        names[forward.id] = forward.name
        bind(forward.policyId, 'TCP/UDP · ' + forward.name)
      }
      entityNames.value = names
      policyBindings.value = bindings
    }
    if (results.some((r) => r.status === 'rejected')) error.value = w('partialError')
  } finally {
    loading.value = false
  }
}
function editPolicy(p) {
  Object.assign(policy, p ? { ...defaults(), ...JSON.parse(JSON.stringify(p)) } : defaults())
  ipText.value = (p?.ipList || []).join('\n')
  uaText.value = (p?.uaList || []).join('\n')
  policyOpen.value = true
}
const split = (x) =>
  x
    .split(/[\n,]+/)
    .map((x) => x.trim())
    .filter(Boolean)
async function savePolicy() {
  if (!(await form.value.validate().catch(() => false))) return
  saving.value = true
  try {
    const payload = { ...policy, ipList: split(ipText.value), uaList: split(uaText.value) }
    if (policy.id) await request.put(`/api/protection/policies/${policy.id}`, payload)
    else await request.post('/api/protection/policies', payload)
    policyOpen.value = false
    ElMessage.success(t('common.saveSuccess'))
    await load()
  } catch {
    // API interceptor reports the failure; keep the draft open.
  } finally {
    saving.value = false
  }
}
async function removePolicy(p) {
  try {
    await request.delete(`/api/protection/policies/${p.id}`)
    await load()
  } catch {}
}
async function saveBan() {
  saving.value = true
  try {
    await request.post('/api/protection/bans', {
      ip: ban.ip.trim(),
      reason: ban.reason.trim(),
      expiresAt: new Date(Date.now() + ban.seconds * 1000).toISOString()
    })
    banOpen.value = false
    Object.assign(ban, { ip: '', reason: '', seconds: 3600 })
    await load()
  } catch {
    // API interceptor reports the failure; keep the draft open.
  } finally {
    saving.value = false
  }
}
async function releaseBan(b) {
  try {
    await request.delete(`/api/protection/bans/${encodeURIComponent(b.ip)}`)
    await load()
  } catch {}
}
const poll = useIntervalFn(() => {
  if (!policyOpen.value && !banOpen.value) return load()
}, 10000)
onMounted(() => {
  load()
  poll.start()
})
</script>
<style scoped>
.workspace-head,
.workspace-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.workspace-head h2 {
  margin: 0;
}
.actions,
.details,
.summary,
.options {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
.summary {
  margin: 20px 0;
}
.search {
  margin-bottom: 16px;
}
.workspace-row {
  padding: 18px 0;
  border-bottom: 1px solid var(--ap-border);
  overflow-wrap: anywhere;
}
.workspace-row p {
  margin: 6px 0;
}
.workspace-row small,
.muted {
  color: var(--ap-muted);
}
.memory-note {
  margin-bottom: 12px;
}
.rows {
  padding: 0 20px;
  background: var(--ap-card);
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
}
@media (max-width: 600px) {
  .actions {
    width: 100%;
  }
  .actions .el-button {
    min-height: 44px;
    margin: 0;
  }
  .rows {
    padding: 0 12px;
  }
}

.protection-workspace {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.workspace-head {
  gap: 12px;
}
.workspace-head .workspace-title {
  font-size: 20px;
  line-height: 28px;
  margin: 0;
}
.workspace-head p {
  font-size: 12px;
  margin: 4px 0 0;
}
.protection-summary {
  margin: 0;
  gap: 8px;
}
.protection-summary .status-pill {
  display: inline-flex;
  gap: 7px;
  font-size: 12px;
}
.protection-workspace :deep(.el-tabs__header) {
  margin: 0;
}
.search {
  max-width: 380px;
  margin: 0;
}
.rows {
  padding: 0 14px;
  border-radius: 6px;
  box-shadow: none;
}
.workspace-row {
  padding: 12px 0;
  gap: 12px;
}
.protection-list-head,
.policy-row {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) minmax(140px, 1fr) minmax(190px, 1.4fr) 135px;
  gap: 12px;
}
.protection-list-head {
  padding: 7px 14px;
  border-block: 1px solid var(--ap-border);
  font-size: 11px;
  color: var(--ap-muted);
}
.policy-controls {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 12px;
  color: var(--ap-text-soft);
}
.policy-name .details {
  margin-top: 6px;
  gap: 5px;
}
.policy-scope {
  min-width: 0;
}
.policy-scope span {
  display: block;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  font-size: 12px;
}
.policy-scope small {
  font-size: 11px;
  display: block;
  margin-top: 4px;
}
.policy-row .actions {
  flex-wrap: nowrap;
  gap: 4px;
}
.policy-row .actions .el-button {
  margin: 0;
}
@media (max-width: 800px) {
  .protection-list-head {
    display: none;
  }
  .policy-row {
    grid-template-columns: 1fr auto;
  }
  .policy-controls,
  .policy-scope {
    grid-column: 1/-1;
  }
  .policy-row .actions {
    grid-row: 1;
    grid-column: 2;
  }
  .policy-scope span {
    white-space: normal;
  }
  .policy-row .actions .el-button {
    min-height: 44px;
  }
  .actions .el-button {
    min-height: 36px;
  }
  .rows {
    padding: 0 12px;
  }
  .search {
    max-width: none;
  }
}
</style>
