<template>
  <section class="trap-workspace" v-loading="loading">
    <header class="trap-header">
      <div>
        <h3>{{ words.title }}</h3>
        <p class="muted">{{ words.subtitle }}</p>
      </div>
      <div class="trap-actions">
        <el-tag :type="status.running ? 'success' : status.error ? 'danger' : 'info'">{{
          status.running ? words.running : words.stopped
        }}</el-tag
        ><el-button :disabled="editing" @click="load">{{ t('common.refresh') }}</el-button
        ><el-button v-if="!editing" type="primary" @click="edit">{{ t('common.edit') }}</el-button>
      </div>
    </header>
    <el-alert v-if="error || status.error" type="error" :title="error || status.error" :closable="false" show-icon />
    <div class="trap-summary">
      <span
        >{{ words.listening }} <b>{{ status.listening?.length || 0 }}</b></span
      ><span
        >{{ words.captured }} <b>{{ status.captured || 0 }}</b></span
      ><span
        >{{ words.exempted }} <b>{{ status.exempted || 0 }}</b></span
      >
    </div>
    <p v-if="status.listening?.length" class="trap-listening">{{ status.listening.join(' · ') }}</p>
    <el-alert :title="words.scope" type="info" :closable="false" />
    <el-form v-if="editing" class="trap-form" label-position="top" @submit.prevent="save">
      <el-form-item :label="words.enabled"
        ><el-switch v-model="draft.enabled" :aria-label="words.enabled" /><HelpHint
          :label="words.help"
          :text="words.enableHelp"
      /></el-form-item>
      <div class="trap-fields">
        <el-form-item :label="words.address"
          ><el-input v-model="draft.listenHost" :aria-label="words.address" placeholder="0.0.0.0 / ::"
        /></el-form-item>
        <el-form-item :label="words.duration"
          ><el-input-number
            v-model="draft.banSeconds"
            :min="1"
            :max="2592000"
            :aria-label="words.duration"
            controls-position="right"
        /></el-form-item>
      </div>
      <el-form-item :label="words.ports"
        ><el-input v-model="portText" :aria-label="words.ports" placeholder="18870, 18871" /><HelpHint
          :label="words.help"
          :text="words.portHelp"
      /></el-form-item>
      <el-form-item :label="words.allow"
        ><el-input
          v-model="allowText"
          type="textarea"
          :rows="4"
          :aria-label="words.allow"
          placeholder="192.168.199.0/24&#10;2001:db8::/64"
        />
        <p class="muted">{{ words.allowHelp }}</p></el-form-item
      >
      <div class="trap-actions">
        <el-button :disabled="saving" @click="cancel">{{ t('common.cancel') }}</el-button
        ><el-button type="primary" native-type="submit" :loading="saving">{{ t('common.save') }}</el-button>
      </div>
    </el-form>
    <div v-else class="trap-settings">
      <span
        >{{ words.address }}<b>{{ status.config?.listenHost || '0.0.0.0' }}</b></span
      >
      <span
        >{{ words.ports }}<b>{{ status.config?.ports?.join(', ') || '—' }}</b></span
      >
      <span
        >{{ words.duration }}<b>{{ status.config?.banSeconds || 3600 }}</b></span
      >
      <span
        >{{ words.allow }}<b>{{ status.config?.allowIps?.length || 0 }}</b></span
      >
    </div>
    <p class="muted">{{ words.runtime }}</p>
  </section>
</template>
<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import HelpHint from './HelpHint.vue'
import { useIntervalFn } from '../composables/useIntervalFn'
const { t, locale } = useI18n()
const words = computed(() =>
  locale.value.startsWith('zh')
    ? {
        title: 'TCP 端口诱捕',
        subtitle: '捕获访问诱捕端口的来源 IP，加入临时封禁列表。',
        running: '运行中',
        stopped: '已停止',
        listening: '监听端口',
        captured: '捕获次数',
        exempted: '豁免次数',
        scope:
          '封禁对本插件的 Web 服务及 TCP/UDP 转发生效；管理后台保持可访问。到期自动解封，也可在“临时封禁”中手动解封。',
        enabled: '启用诱捕',
        help: '查看说明',
        enableHelp: 'TCP握手完成即触发，随后立即关闭连接。请仅使用空闲端口；启用前填写可信管理设备的豁免 IP/CIDR。',
        address: '监听地址',
        duration: '封禁时长（秒）',
        ports: 'TCP 端口',
        portHelp:
          '最多20个端口，用逗号、空格或换行分隔。不能占用管理或已启用业务端口。WAN访问需由你在路由器防火墙中放行这些诱捕端口。',
        allow: '豁免 IP / CIDR',
        allowHelp: '每行一项。匹配的来源不捕获、不封禁。本机回环地址始终豁免。名单修改不会撤销已有封禁。',
        runtime: '诱捕配置随备份保存、启动时恢复监听；捕获计数、事件和临时封禁在重启后清空。',
        invalidPorts: '请填写1到65535的整数端口，最多20个且不能重复。'
      }
    : {
        title: 'TCP port trap',
        subtitle: 'Capture source IPs connecting to trap ports and temporarily ban them.',
        running: 'Running',
        stopped: 'Stopped',
        listening: 'Listeners',
        captured: 'Captures',
        exempted: 'Exemptions',
        scope:
          'Bans apply to this plugin’s Web services and TCP/UDP forwarding. Administration stays accessible. Bans expire automatically or can be released in Temporary bans.',
        enabled: 'Enable trap',
        help: 'Help',
        enableHelp:
          'A completed TCP handshake triggers capture and immediate connection closure. Use idle ports and exempt trusted management IPs/CIDRs.',
        address: 'Listen address',
        duration: 'Ban duration (seconds)',
        ports: 'TCP ports',
        portHelp:
          'Up to 20 ports separated by commas, spaces or newlines. Administration and enabled service ports cannot be used. WAN access requires router firewall rules.',
        allow: 'Exempt IPs / CIDRs',
        allowHelp:
          'One per line. Matching sources are not captured or banned. Loopback is always exempt. Editing exceptions does not remove existing bans.',
        runtime:
          'Configuration is backed up and listeners resume on startup. Counters, events and temporary bans clear on restart.',
        invalidPorts: 'Enter unique integer ports from 1 to 65535, up to 20.'
      }
)
const status = reactive({ config: {}, listening: [], running: false, captured: 0, exempted: 0 })
const loading = ref(false),
  saving = ref(false),
  editing = ref(false),
  error = ref(''),
  portText = ref(''),
  allowText = ref('')
const draft = reactive({ enabled: false, listenHost: '0.0.0.0', ports: [], allowIps: [], banSeconds: 3600 })
async function load() {
  if (loading.value || editing.value) return
  loading.value = true
  error.value = ''
  try {
    const res = await request.get('/api/protection/trap')
    Object.assign(status, res.data, { error: res.data.error || '' })
  } catch (err) {
    error.value = err.response?.data?.msg || err.message
  } finally {
    loading.value = false
  }
}
function edit() {
  Object.assign(
    draft,
    { enabled: false, listenHost: '0.0.0.0', banSeconds: 3600 },
    JSON.parse(JSON.stringify(status.config || {}))
  )
  portText.value = (draft.ports || []).join(', ')
  allowText.value = (draft.allowIps || []).join('\n')
  editing.value = true
  error.value = ''
}
async function cancel() {
  editing.value = false
  await load()
}
async function save() {
  if (saving.value) return
  const raw = portText.value.trim() ? portText.value.trim().split(/[\s,，]+/) : []
  const ports = raw.map(Number)
  if (
    raw.some((v) => !/^\d+$/.test(v)) ||
    ports.some((v) => !Number.isInteger(v) || v < 1 || v > 65535) ||
    new Set(ports).size !== ports.length ||
    ports.length > 20 ||
    (draft.enabled && !ports.length)
  ) {
    error.value = words.value.invalidPorts
    return
  }
  saving.value = true
  error.value = ''
  try {
    const res = await request.put('/api/protection/trap', {
      ...draft,
      ports,
      allowIps: allowText.value
        .split('\n')
        .map((v) => v.trim())
        .filter(Boolean)
    })
    Object.assign(status, res.data, { error: res.data.error || '' })
    editing.value = false
  } catch (err) {
    error.value = err.response?.data?.msg || err.message
  } finally {
    saving.value = false
  }
}
const poll = useIntervalFn(load, 5000)
onMounted(() => {
  load()
  poll.start()
})
</script>
<style scoped>
.trap-workspace {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 20px;
  background: var(--ap-card);
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
  min-width: 0;
}
.trap-header,
.trap-actions,
.trap-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.trap-header h3 {
  font-size: 18px;
  margin: 0;
}
.trap-header p {
  margin: 6px 0 0;
}
.trap-actions {
  justify-content: flex-end;
}
.trap-summary {
  justify-content: flex-start;
  font-size: 13px;
  gap: 24px;
}
.trap-summary b {
  margin-left: 8px;
}
.muted {
  color: var(--ap-muted);
  font-size: 13px;
  margin: 4px 0;
}
.trap-fields {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.trap-form :deep(.el-input-number) {
  width: 100%;
}
.trap-settings {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  font-size: 13px;
}
.trap-settings span {
  display: flex;
  justify-content: space-between;
  gap: 16px;
}
.trap-settings b,
.trap-listening {
  overflow-wrap: anywhere;
  min-width: 0;
}
.trap-listening {
  margin: 0;
  font-size: 13px;
}
@media (max-width: 600px) {
  .trap-workspace {
    padding: 12px;
  }
  .trap-fields,
  .trap-settings {
    grid-template-columns: 1fr;
  }
  .trap-actions .el-button {
    min-height: 44px;
    margin: 0;
  }
  .trap-header .trap-actions {
    width: 100%;
    justify-content: flex-start;
  }
}
</style>
