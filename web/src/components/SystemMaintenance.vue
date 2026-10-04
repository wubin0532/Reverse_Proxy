<template>
  <div class="page-grid">
    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('dashboard.update.title') }}</span
          ><el-tag type="success" effect="plain">{{ $t('dashboard.update.noNetwork') }}</el-tag>
        </div></template
      >
      <el-alert type="info" :title="$t('dashboard.update.alert')" :closable="false" />
      <div class="update-grid">
        <el-upload
          drag
          :auto-upload="false"
          :limit="1"
          accept=".run"
          :on-change="onFile"
          :on-remove="() => (uploadFile = null)"
          ><el-icon class="upload-icon"><UploadFilled /></el-icon>
          <div>{{ $t('dashboard.update.uploadHint') }}</div></el-upload
        >
        <div class="inspection">
          <template v-if="inspection"
            ><div>
              <span>{{ $t('dashboard.update.version') }}</span
              ><b>{{ inspection.version }}</b>
            </div>
            <div>
              <span>{{ $t('dashboard.update.arch') }}</span
              ><b>{{ inspection.goos }}/{{ inspection.goarch }}</b>
            </div>
            <div>
              <span>{{ $t('dashboard.update.size') }}</span
              ><b>{{ formatBytes(inspection.size) }}</b>
            </div>
            <div>
              <span>{{ $t('dashboard.update.signature') }}</span
              ><el-tag type="success">{{ $t('dashboard.update.signatureOk') }}</el-tag>
            </div>
            <div class="digest">
              <span>SHA256</span><code>{{ inspection.sha256 }}</code>
            </div></template
          >
          <el-empty v-else :description="$t('dashboard.update.inspectPrompt')" :image-size="54" />
        </div>
      </div>
      <div class="update-actions">
        <el-button
          type="primary"
          :loading="inspecting"
          :disabled="!uploadFile || !!inspection"
          @click="inspectPackage"
          >{{ $t('dashboard.update.uploadInspect') }}</el-button
        ><el-button v-if="inspection" type="danger" plain @click="cancelPackage">{{ $t('common.cancel') }}</el-button
        ><el-button v-if="inspection" type="warning" @click="installOpen = true">{{
          $t('dashboard.update.confirmInstall')
        }}</el-button
        ><el-tag v-if="updateStatus.state && updateStatus.state !== 'idle'">{{ statusText }}</el-tag>
      </div>
    </el-card>

    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('dashboard.backup.title') }}</span
          ><el-tag type="info" effect="plain">{{ $t('dashboard.backup.crossDevice') }}</el-tag>
        </div></template
      >
      <el-alert type="info" :title="$t('dashboard.backup.alert')" :closable="false" />
      <div class="update-actions">
        <el-button type="primary" @click="exportOpen = true">{{ $t('dashboard.backup.export') }}</el-button
        ><el-button type="warning" plain @click="importOpen = true">{{ $t('dashboard.backup.import') }}</el-button>
      </div>
    </el-card>

    <el-dialog
      v-model="exportOpen"
      :title="$t('dashboard.backup.exportTitle')"
      width="440px"
      @closed="clearExportSecrets"
      ><el-alert
        type="warning"
        :title="$t('dashboard.backup.exportAlert')"
        :closable="false"
        style="margin-bottom: 12px"
      /><el-form label-position="top"
        ><el-form-item :label="$t('dashboard.backup.currentAdminPassword')"
          ><el-input v-model="exportForm.password" type="password" show-password /></el-form-item
        ><el-form-item :label="$t('dashboard.backup.backupPassword')"
          ><el-input v-model="exportForm.backupPassword" type="password" show-password /></el-form-item
        ><el-form-item :label="$t('dashboard.backup.confirmBackupPassword')"
          ><el-input v-model="exportForm.confirm" type="password" show-password /></el-form-item></el-form
      ><template #footer
        ><el-button @click="exportOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="exporting" @click="exportBackup">{{
          $t('dashboard.backup.exportDownload')
        }}</el-button></template
      ></el-dialog
    >

    <el-dialog
      v-model="importOpen"
      @closed="clearImportSecrets"
      :title="$t('dashboard.backup.importTitle')"
      width="440px"
      ><el-alert
        type="error"
        :title="$t('dashboard.backup.importAlert')"
        :closable="false"
        style="margin-bottom: 12px"
      /><el-form label-position="top"
        ><el-form-item :label="$t('dashboard.backup.backupFile')"
          ><el-upload
            :auto-upload="false"
            :limit="1"
            accept=".json"
            :on-change="onBackupFile"
            :on-remove="
              () => {
                importForm.backup = ''
                importFileName = ''
              }
            "
            ><el-button>{{ $t('dashboard.backup.chooseFile') }}</el-button
            ><template #tip
              ><span class="muted" style="margin-left: 8px">{{
                importFileName || 'andey-proxy-backup-*.json'
              }}</span></template
            ></el-upload
          ></el-form-item
        ><el-form-item :label="$t('dashboard.backup.currentAdminPassword')"
          ><el-input v-model="importForm.password" type="password" show-password /></el-form-item
        ><el-form-item :label="$t('dashboard.backup.backupPasswordLabel')"
          ><el-input v-model="importForm.backupPassword" type="password" show-password /></el-form-item></el-form
      ><template #footer
        ><el-button @click="importOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="danger" :loading="importing" @click="importBackup">{{
          $t('dashboard.backup.confirmImport')
        }}</el-button></template
      ></el-dialog
    >

    <el-dialog
      v-model="installOpen"
      @closed="installPassword = ''"
      :title="$t('dashboard.update.installTitle')"
      width="440px"
      ><el-alert
        v-if="inspection?.downgrade"
        type="warning"
        :title="$t('dashboard.update.downgradeAlert')"
        :closable="false"
      /><el-form label-position="top"
        ><el-form-item :label="$t('dashboard.backup.currentAdminPassword')"
          ><el-input v-model="installPassword" type="password" show-password /></el-form-item
        ><el-form-item v-if="inspection?.downgrade"
          ><el-checkbox v-model="allowDowngrade">{{ $t('dashboard.update.allowDowngrade') }}</el-checkbox></el-form-item
        ></el-form
      ><template #footer
        ><el-button @click="installOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="warning" :loading="installing" @click="installPackage">{{
          $t('dashboard.update.installRestart')
        }}</el-button></template
      ></el-dialog
    >
  </div>
</template>
<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElNotification } from 'element-plus'
import { UploadFilled } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { useIntervalFn } from '../composables/useIntervalFn'
const { t } = useI18n()
const emit = defineEmits(['updated'])
const router = useRouter(),
  uploadFile = ref(null),
  inspecting = ref(false),
  inspection = ref(null),
  installOpen = ref(false),
  installPassword = ref(''),
  allowDowngrade = ref(false),
  installing = ref(false)
const exportOpen = ref(false),
  exporting = ref(false),
  exportForm = reactive({ password: '', backupPassword: '', confirm: '' })
const importOpen = ref(false),
  importing = ref(false),
  importFileName = ref(''),
  importForm = reactive({ password: '', backupPassword: '', backup: '' })
const updateStatus = reactive({ state: 'idle' })
const statusText = computed(() => {
  const m = {
    inspecting: t('dashboard.update.status.inspecting'),
    inspected: t('dashboard.update.status.inspected'),
    installing: t('dashboard.update.status.installing'),
    restarting: t('dashboard.update.status.restarting'),
    done: t('dashboard.update.status.done'),
    failed: t('dashboard.update.status.failed')
  }
  return m[updateStatus.state] || updateStatus.state
})
const statusPoller = useIntervalFn(pollStatus, 3000)
function clearExportSecrets() {
  Object.assign(exportForm, { password: '', backupPassword: '', confirm: '' })
}
function clearImportSecrets() {
  importForm.password = ''
  importForm.backupPassword = ''
}
function onFile(file) {
  uploadFile.value = file.raw
  inspection.value = null
}
async function inspectPackage() {
  const form = new FormData()
  form.append('package', uploadFile.value)
  inspecting.value = true
  try {
    inspection.value = (await request.post('/api/system/update/inspect', form, { timeout: 120000 })).data
    ElMessage.success(t('dashboard.update.signOk'))
  } finally {
    inspecting.value = false
  }
}
async function cancelPackage() {
  await request.delete(`/api/system/update/${inspection.value.uploadId}`)
  inspection.value = null
  uploadFile.value = null
}
async function installPackage() {
  if (!installPassword.value) return ElMessage.warning(t('dashboard.update.enterAdminPassword'))
  installing.value = true
  try {
    await request.post(
      `/api/system/update/${inspection.value.uploadId}/install`,
      { password: installPassword.value, allowDowngrade: allowDowngrade.value },
      { timeout: 120000 }
    )
    installOpen.value = false
    startStatusPoll()
  } finally {
    installing.value = false
  }
}
async function pollStatus() {
  try {
    const prev = updateStatus.state
    Object.assign(updateStatus, (await request.get('/api/system/update/status')).data || {})
    if (updateStatus.inspection && !inspection.value) inspection.value = updateStatus.inspection
    if (['done', 'failed', 'idle'].includes(updateStatus.state)) {
      statusPoller.stop()
      if (prev !== updateStatus.state && updateStatus.state === 'done') {
        ElNotification.success({
          title: t('dashboard.update.doneTitle'),
          message: t('dashboard.update.newVersion', {
            version: updateStatus.version || inspection.value?.version || '-'
          })
        })
        emit('updated')
      } else if (prev !== updateStatus.state && updateStatus.state === 'failed') {
        ElNotification.error({
          title: t('dashboard.update.failedTitle'),
          message: updateStatus.error || updateStatus.note || t('dashboard.update.installFailedLog')
        })
      }
    }
  } catch {}
}
function startStatusPoll() {
  statusPoller.restart()
  pollStatus()
}
function formatBytes(n) {
  return n < 1048576 ? `${(n / 1024).toFixed(1)} KiB` : `${(n / 1048576).toFixed(1)} MiB`
}
async function exportBackup() {
  if (!exportForm.password) return ElMessage.warning(t('dashboard.update.enterAdminPassword'))
  if (exportForm.backupPassword.length < 8) return ElMessage.warning(t('dashboard.backup.passwordTooShort'))
  if (exportForm.backupPassword !== exportForm.confirm) return ElMessage.warning(t('dashboard.backup.passwordMismatch'))
  exporting.value = true
  try {
    const blob = await request.post(
      '/api/system/backup/export',
      { password: exportForm.password, backupPassword: exportForm.backupPassword },
      { responseType: 'blob', timeout: 30000 }
    )
    const d = new Date(),
      p = (n) => String(n).padStart(2, '0'),
      url = URL.createObjectURL(blob),
      a = document.createElement('a')
    a.href = url
    a.download = `andey-proxy-backup-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}.json`
    a.click()
    URL.revokeObjectURL(url)
    exportOpen.value = false
    Object.assign(exportForm, { password: '', backupPassword: '', confirm: '' })
    ElMessage.success(t('dashboard.backup.exported'))
  } catch {
  } finally {
    exporting.value = false
  }
}
function onBackupFile(file) {
  importFileName.value = file.name
  file.raw
    .text()
    .then((t2) => {
      importForm.backup = t2
    })
    .catch(() => ElMessage.error(t('dashboard.backup.readFailed')))
}
async function importBackup() {
  if (!importForm.backup) return ElMessage.warning(t('dashboard.backup.chooseFileFirst'))
  if (!importForm.password || !importForm.backupPassword) return ElMessage.warning(t('dashboard.backup.enterPasswords'))
  importing.value = true
  try {
    await request.post(
      '/api/system/backup/import',
      { password: importForm.password, backupPassword: importForm.backupPassword, backup: importForm.backup },
      { timeout: 30000 }
    )
    importOpen.value = false
    ElMessage.success(t('dashboard.backup.importSuccess'))
    setTimeout(() => {
      window.location.href = '/login'
    }, 1500)
  } catch {
  } finally {
    importing.value = false
  }
}
watch(
  () => updateStatus.state,
  (s) => {
    if (['installing', 'restarting'].includes(s) && !statusPoller.active.value) startStatusPoll()
  }
)
onMounted(() => {
  pollStatus()
})
</script>
<style scoped>
.update-grid {
  display: grid;
  grid-template-columns: minmax(280px, 1fr) minmax(300px, 1fr);
  gap: 18px;
  margin-top: 16px;
}
.upload-icon {
  font-size: 40px;
  color: var(--ap-primary);
}
.inspection > div {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 8px;
  padding: 7px 0;
}
.inspection span {
  color: var(--ap-muted);
}
.inspection code {
  overflow: hidden;
  text-overflow: ellipsis;
}
.update-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 16px;
}
@media (max-width: 760px) {
  .update-grid {
    grid-template-columns: 1fr;
  }
}
</style>
