<template>
  <el-dialog
    v-model="visible"
    class="workspace-dialog"
    :title="labels.title"
    width="min(850px, 94vw)"
    append-to-body
    destroy-on-close
  >
    <el-form label-position="top">
      <el-form-item :label="labels.targets"
        ><el-input
          v-model="targetsText"
          type="textarea"
          :rows="2"
          :disabled="running"
          :placeholder="labels.targetPlaceholder"
      /></el-form-item>
      <div class="discovery-options">
        <el-form-item :label="labels.ports"
          ><el-input v-model="portsText" :disabled="running" placeholder="80, 443, 8080"
        /></el-form-item>
        <el-form-item :label="labels.timeout"
          ><el-input-number
            v-model="timeoutMs"
            :min="50"
            :max="1000"
            :step="50"
            :disabled="running"
            controls-position="right"
        /></el-form-item>
      </div>
    </el-form>
    <p class="discovery-note">
      {{ labels.boundary }}<HelpHint :label="labels.start" :text="helpText(locale, 'discovery')" />
    </p>
    <div v-if="job" class="discovery-progress" role="status">
      <el-tag :type="running ? 'warning' : 'info'">{{ statusText }}</el-tag
      ><span>{{ job.completed }} / {{ job.total }} · {{ job.results?.length || 0 }} {{ labels.open }}</span
      ><el-progress :percentage="progress" :show-text="false" />
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <div class="discovery-results">
      <div v-for="result in job?.results || []" :key="result.address" class="discovery-row">
        <div>
          <b>{{ result.address }}</b
          ><small>{{ result.latencyMs }} ms · {{ labels.tcpOnly }}</small>
        </div>
        <el-select v-model="schemes[result.address]" :aria-label="labels.scheme" style="width: 100px"
          ><el-option label="HTTP" value="http" /><el-option label="HTTPS" value="https"
        /></el-select>
        <el-button @click="choose(result)">{{ labels.draft }}</el-button>
      </div>
      <el-empty v-if="!job?.results?.length" :description="labels.empty" :image-size="50" />
    </div>
    <template #footer
      ><el-button @click="visible = false">{{ labels.close }}</el-button
      ><el-button v-if="running" type="warning" @click="cancel">{{ labels.cancel }}</el-button
      ><el-button v-else type="primary" :loading="starting" @click="start">{{ labels.start }}</el-button></template
    >
  </el-dialog>
</template>
<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import request from '../../api'
import HelpHint from '../HelpHint.vue'
import { helpText } from './help'
import { splitList } from '../../utils/list'
import { useIntervalFn } from '../../composables/useIntervalFn'
const emit = defineEmits(['draft'])
const { locale } = useI18n()
const labels = computed(() =>
  locale.value.startsWith('zh')
    ? {
        title: '发现内网服务',
        targets: '目标 IP / CIDR',
        targetPlaceholder: '明确填写目标，例如 192.168.199.7 或 192.168.199.0/28',
        ports: '端口（最多16个）',
        timeout: '连接超时（毫秒）',
        boundary:
          '仅检查明确填写的内网或回环地址；最多256个地址、16个端口，60秒自动结束。结果只证明TCP可连接，请选择协议并检查规则草稿。',
        open: '个可连接端口',
        tcpOnly: 'TCP 可连接',
        scheme: '服务协议',
        draft: '创建规则草稿',
        empty: '尚无发现结果',
        close: '关闭',
        cancel: '取消发现',
        start: '开始发现',
        running: '发现中',
        completed: '已完成',
        cancelled: '已取消',
        timeoutStatus: '已到时限',
        failed: '发现失败，请重试',
        invalid: '请填写目标和1到16个不重复的有效端口'
      }
    : {
        title: 'Discover LAN services',
        targets: 'Target IP / CIDR',
        targetPlaceholder: 'Enter explicit targets, such as 192.168.199.7 or 192.168.199.0/28',
        ports: 'Ports (up to 16)',
        timeout: 'Connection timeout (ms)',
        boundary:
          'Check only explicit private or loopback targets: up to 256 addresses and 16 ports, with a 60-second deadline. Results confirm TCP connectivity only. Choose a protocol and review the rule draft.',
        open: 'open ports',
        tcpOnly: 'TCP reachable',
        scheme: 'Service protocol',
        draft: 'Create rule draft',
        empty: 'No results yet',
        close: 'Close',
        cancel: 'Cancel discovery',
        start: 'Start discovery',
        running: 'Discovering',
        completed: 'Completed',
        cancelled: 'Cancelled',
        timeoutStatus: 'Deadline reached',
        failed: 'Discovery failed. Retry.',
        invalid: 'Enter targets and 1–16 unique valid ports'
      }
)
const visible = ref(false),
  starting = ref(false),
  error = ref(''),
  job = ref(null),
  targetsText = ref(''),
  portsText = ref('80, 443, 8080'),
  timeoutMs = ref(300),
  schemes = reactive({})
let generation = 0
const running = computed(() => job.value?.status === 'running')
const progress = computed(() => Math.round((100 * (job.value?.completed || 0)) / (job.value?.total || 1)))
const statusText = computed(
  () => labels.value[job.value?.status === 'timeout' ? 'timeoutStatus' : job.value?.status] || job.value?.status
)
const poll = useIntervalFn(load, 1000)
async function cancelId(id) {
  try {
    await request.post(`/api/sites/discovery/${id}/cancel`)
  } catch {}
}
watch(visible, (shown) => {
  if (!shown) {
    generation++
    poll.stop()
    if (running.value) cancelId(job.value.id)
  }
})
onBeforeUnmount(() => {
  generation++
  if (running.value) cancelId(job.value.id)
})
function open() {
  generation++
  job.value = null
  error.value = ''
  starting.value = false
  Object.keys(schemes).forEach((k) => delete schemes[k])
  visible.value = true
}
async function start() {
  const ports = splitList(portsText.value).map(Number),
    targets = splitList(targetsText.value)
  if (
    !targets.length ||
    !ports.length ||
    ports.length > 16 ||
    new Set(ports).size !== ports.length ||
    ports.some((p) => !Number.isInteger(p) || p < 1 || p > 65535)
  ) {
    error.value = labels.value.invalid
    return
  }
  const epoch = generation
  starting.value = true
  error.value = ''
  try {
    const res = await request.post('/api/sites/discovery', { targets, ports, timeoutMs: timeoutMs.value })
    if (epoch !== generation || !visible.value) {
      if (res.data?.id) await cancelId(res.data.id)
      return
    }
    job.value = res.data
    applySchemes()
    if (running.value) poll.start()
  } catch {
    if (epoch === generation) error.value = labels.value.failed
  } finally {
    if (epoch === generation) starting.value = false
  }
}
function applySchemes() {
  for (const result of job.value?.results || []) {
    if (!schemes[result.address]) schemes[result.address] = result.address.endsWith(':443') ? 'https' : 'http'
  }
}
async function load() {
  if (!visible.value || !running.value) return
  const epoch = generation,
    id = job.value.id
  try {
    const res = await request.get(`/api/sites/discovery/${id}`)
    if (epoch !== generation || !visible.value || job.value?.id !== id) return
    job.value = res.data
    applySchemes()
    if (!running.value) poll.stop()
  } catch {
    if (epoch === generation) {
      error.value = labels.value.failed
      poll.stop()
    }
  }
}
async function cancel() {
  if (job.value?.id) {
    await cancelId(job.value.id)
    await load()
  }
}
function choose(result) {
  const draft = {
    name: result.address,
    type: 'reverse',
    enabled: false,
    backends: [`${schemes[result.address] || 'http'}://${result.address}`]
  }
  visible.value = false
  emit('draft', draft)
}
defineExpose({ open })
</script>
<style scoped>
.discovery-options {
  display: grid;
  grid-template-columns: 1fr 220px;
  gap: 16px;
}
.discovery-note {
  color: var(--ap-muted);
  font-size: 12px;
  line-height: 1.6;
}
.discovery-progress {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 10px;
  align-items: center;
  margin: 16px 0;
}
.discovery-progress .el-progress {
  grid-column: 1/-1;
}
.discovery-results {
  margin-top: 16px;
}
.discovery-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--ap-border);
}
.discovery-row > div:first-child {
  flex: 1;
  min-width: 0;
}
.discovery-row b {
  display: block;
  overflow-wrap: anywhere;
}
.discovery-row small {
  display: block;
  color: var(--ap-muted);
  margin-top: 4px;
}
.discovery-row .el-button {
  flex-shrink: 0;
}
@media (max-width: 600px) {
  .discovery-options {
    grid-template-columns: 1fr;
  }
  .discovery-row {
    flex-wrap: wrap;
  }
  .discovery-row > div:first-child {
    flex-basis: 100%;
  }
  .discovery-row .el-button {
    min-height: 44px;
  }
}
</style>
