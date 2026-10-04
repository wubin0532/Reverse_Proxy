<template>
  <el-card>
    <template #header
      ><div class="card-header">
        <b>{{ $t('tunnel.runtime') }}</b
        ><el-tag :type="runtime.compatible ? 'success' : 'warning'">{{
          runtime.version || $t('common.notConfigured')
        }}</el-tag>
      </div></template
    >
    <div class="runtime-info">
      <div>
        <span>{{ $t('tunnel.runtimeSource') }}</span
        ><b>{{ $t(`tunnel.sources.${runtime.source || 'missing'}`) }}</b>
      </div>
      <div>
        <span>{{ $t('tunnel.runtimePlatform') }}</span
        ><b>{{ runtime.goos || '-' }} / {{ runtime.goarch || '-' }}</b>
      </div>
      <div v-if="runtime.path">
        <span>{{ $t('tunnel.runtimePath') }}</span
        ><code>{{ runtime.path }}</code>
      </div>
      <div>
        <span>{{ $t('tunnel.latestVersion') }}</span
        ><b
          >{{ release?.version || '-'
          }}<small v-if="release"> · {{ formatBytes(release.size) }} · {{ release.asset }}</small></b
        >
      </div>
    </div>
    <p class="muted">{{ $t('tunnel.install') }}</p>
    <el-alert v-if="runtime.message" :title="runtime.message" type="warning" :closable="false" />
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <el-alert
      v-if="runtime.goos && !runtime.downloadSupported"
      :title="$t('tunnel.downloadUnsupported')"
      type="info"
      :closable="false"
    />
    <div class="actions">
      <el-button :loading="checking" :disabled="!runtime.downloadSupported || busy" @click="check(true)">{{
        $t('tunnel.checkVersion')
      }}</el-button>
      <el-button type="primary" :disabled="!release || busy || !runtime.downloadSupported" @click="open = true">{{
        $t(runtime.source === 'managed' ? 'tunnel.updateRuntime' : 'tunnel.installRuntime')
      }}</el-button>
      <small v-if="release" class="muted">{{ $t('tunnel.checkedAt') }} {{ formatTime(release.checkedAt) }}</small>
    </div>
    <div v-if="operation" class="progress">
      <b>{{ $t('tunnel.downloadTask') }} · {{ operation.version }}</b>
      <p>{{ $t(`tunnel.stages.${operation.stage}`) }} · {{ $t(`tunnel.status.${operation.status}`) }}</p>
      <el-progress
        v-if="operation.totalBytes"
        :percentage="percentage"
        :status="operation.status === 'failed' ? 'exception' : operation.status === 'succeeded' ? 'success' : undefined"
      />
      <small>{{ formatBytes(operation.downloadedBytes || 0) }} / {{ formatBytes(operation.totalBytes || 0) }}</small>
      <el-alert v-if="operation.error" :title="operation.error" type="error" :closable="false" />
    </div>
    <el-dialog v-model="open" :title="$t('tunnel.installRuntime')" width="min(560px, 95vw)" @closed="resetConfirmation">
      <p>{{ $t('tunnel.installConfirm', { version: release?.version }) }}</p>
      <el-alert :title="$t('tunnel.systemPreserved')" type="info" :closable="false" />
      <p>{{ $t('tunnel.affectedInstances') }}</p>
      <ul v-if="affected.length">
        <li v-for="instance in affected" :key="instance.id">{{ instance.name }}</li>
      </ul>
      <p v-else class="muted">{{ $t('tunnel.noneAffected') }}</p>
      <el-form label-position="top">
        <el-form-item :label="$t('dashboard.backup.currentAdminPassword')"
          ><el-input v-model="password" type="password" show-password autocomplete="current-password"
        /></el-form-item>
        <el-form-item v-if="affected.length"
          ><el-checkbox v-model="restart">{{ $t('tunnel.restartConfirm') }}</el-checkbox></el-form-item
        >
      </el-form>
      <template #footer
        ><el-button @click="open = false">{{ $t('common.cancel') }}</el-button
        ><el-button
          type="primary"
          :loading="installing"
          :disabled="!password || (affected.length > 0 && !restart) || busy"
          @click="install"
          >{{ $t('tunnel.confirmDownload') }}</el-button
        ></template
      >
    </el-dialog>
  </el-card>
</template>
<script setup>
import { computed, onUnmounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatBytes, formatTime } from '../utils/format'
const props = defineProps({
  runtime: { type: Object, required: true },
  instances: { type: Array, default: () => [] },
  busy: Boolean
})
const emit = defineEmits(['installed', 'busy'])
const { t } = useI18n()
const checking = ref(false),
  installing = ref(false),
  open = ref(false),
  password = ref(''),
  restart = ref(false),
  release = ref(null),
  operation = ref(null),
  error = ref('')
const affected = computed(() => props.instances.filter((instance) => instance.enabled))
const percentage = computed(() =>
  Math.min(100, Math.floor(((operation.value?.downloadedBytes || 0) * 100) / (operation.value?.totalBytes || 1)))
)
let alive = true,
  timer
function resetConfirmation() {
  password.value = ''
  restart.value = false
}
async function check(force = false) {
  checking.value = true
  error.value = ''
  try {
    const result = await request.get(`/api/tunnels/runtime/release${force ? '?refresh=1' : ''}`)
    if (alive) release.value = result.data
  } catch (err) {
    if (alive) error.value = err.response?.data?.msg || err.message || t('tunnel.checkFailed')
  } finally {
    checking.value = false
  }
}
async function install() {
  installing.value = true
  try {
    const result = await request.post('/api/tunnels/runtime/install', {
      releaseId: release.value.id,
      password: password.value,
      restartRunning: restart.value,
      affectedInstances: affected.value.map((instance) => instance.id)
    })
    operation.value = result.data
    open.value = false
    password.value = ''
  } catch {
    /* Preserve the form on failure. */
  } finally {
    installing.value = false
  }
}
async function poll() {
  if (!alive || operation.value?.status !== 'running') return
  try {
    const result = await request.get(`/api/tunnels/operations/${operation.value.id}`)
    if (!alive) return
    operation.value = result.data
    if (result.data.status === 'succeeded') {
      ElMessage.success(t('common.success'))
      emit('installed')
    }
  } catch {
    /* A closed page does not cancel the background task. */
  }
  if (alive && operation.value?.status === 'running') timer = setTimeout(poll, 1000)
}
watch(
  () => props.runtime.downloadSupported,
  (supported, old) => {
    if (supported && !old) check()
  },
  { immediate: true }
)
watch(
  () => props.runtime.operation,
  (value) => {
    if (value && !operation.value) operation.value = value
  },
  { immediate: true }
)
watch(
  () => operation.value?.status,
  (status) => {
    emit('busy', status === 'running')
    clearTimeout(timer)
    if (status === 'running') timer = setTimeout(poll, 1000)
  },
  { immediate: true }
)
onUnmounted(() => {
  alive = false
  clearTimeout(timer)
  password.value = ''
})
</script>
<style scoped>
.runtime-info {
  display: grid;
  gap: 10px;
}
.runtime-info > div {
  display: grid;
  grid-template-columns: 150px minmax(0, 1fr);
  gap: 12px;
}
.runtime-info span,
small {
  color: var(--ap-muted);
}
code {
  overflow-wrap: anywhere;
}
.actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
}
.actions .el-button {
  margin: 0;
}
.progress {
  border-top: 1px solid var(--ap-border);
  margin-top: 20px;
  padding-top: 16px;
}
.el-alert {
  margin: 12px 0;
}
@media (max-width: 600px) {
  .runtime-info > div {
    grid-template-columns: 1fr;
    gap: 4px;
  }
}
</style>
