<template>
  <el-dialog
    v-model="visible"
    class="workspace-dialog"
    :title="`${zh ? '证书映射' : 'Certificate mappings'} · ${cert?.name || ''}`"
    width="min(850px,94vw)"
    destroy-on-close
  >
    <p class="muted">
      {{
        zh
          ? '映射到配置目录内 exports/证书ID/名称/current，提供 fullchain.pem、privkey.pem 和合并的 bundle.pem。服务分别读取证书和私钥时，应先解析 current，再从同一个版本目录读取。首次配置后可手动执行；移除配置会停止更新并保留已有文件。'
          : 'Exports fullchain.pem, privkey.pem and a combined bundle.pem to exports/certificate-ID/name/current. Services opening the certificate and key separately must resolve current once and read the same version directory. Run manually after setup; removing a mapping stops updates and retains existing files.'
      }}
    </p>
    <el-alert
      v-if="conflict"
      type="warning"
      :closable="false"
      :title="
        zh
          ? '映射已变更，草稿已保留；重新打开最新配置后再保存。'
          : 'Mappings changed. Draft retained; reopen current settings before saving.'
      "
    />
    <el-form label-position="top">
      <div v-for="(mapping, i) in mappings" :key="i" class="mapping-row">
        <el-form-item :label="zh ? '映射名称' : 'Mapping name'"
          ><el-input v-model="mapping.name" maxlength="64" placeholder="nginx-site"
        /></el-form-item>
        <el-form-item :label="zh ? '完成后重载服务' : 'Reload after export'"
          ><el-select v-model="mapping.reloadService"
            ><el-option :label="zh ? '不重载' : 'None'" value="" /><el-option
              v-for="name in ['nginx', 'uhttpd', 'haproxy']"
              :key="name"
              :label="name"
              :value="name" /></el-select
        ></el-form-item>
        <el-button type="danger" plain @click="mappings.splice(i, 1)">{{ zh ? '移除' : 'Remove' }}</el-button>
      </div>
    </el-form>
    <el-button :disabled="mappings.length >= 8" @click="mappings.push({ name: '', reloadService: '' })">{{
      zh ? '添加映射' : 'Add mapping'
    }}</el-button>
    <div v-for="(path, i) in cert?.mappingPaths || []" :key="path" class="mapping-status">
      <code>{{ path }}</code
      ><el-tag :type="results[i]?.error ? 'danger' : results[i] ? 'success' : 'info'">{{
        results[i]?.error ||
        (results[i] ? (zh ? '已映射' : 'Exported') : zh ? '尚无本次运行记录' : 'No operation recorded')
      }}</el-tag>
    </div>
    <template #footer
      ><el-button @click="visible = false">{{ zh ? '关闭' : 'Close' }}</el-button
      ><el-button :disabled="dirty || !cert?.mappings?.length" :loading="running" @click="sync">{{
        zh ? '执行已保存映射' : 'Run saved mappings'
      }}</el-button
      ><el-button type="primary" :loading="saving" :disabled="conflict" @click="save">{{
        zh ? '保存配置' : 'Save'
      }}</el-button></template
    >
  </el-dialog>
</template>
<script setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import request from '../api'
const emit = defineEmits(['saved']),
  { locale } = useI18n(),
  zh = computed(() => locale.value.startsWith('zh'))
const visible = ref(false),
  cert = ref(null),
  mappings = ref([]),
  results = ref([]),
  saving = ref(false),
  running = ref(false),
  conflict = ref(false)
const dirty = computed(() => JSON.stringify(mappings.value) !== JSON.stringify(cert.value?.mappings || []))
function open(row) {
  cert.value = JSON.parse(JSON.stringify(row))
  mappings.value = JSON.parse(JSON.stringify(row.mappings || []))
  results.value = row.mappingResults || []
  conflict.value = false
  visible.value = true
}
async function save() {
  saving.value = true
  try {
    const res = await request.put(`/api/certs/${cert.value.id}/mappings`, {
      revision: cert.value.mappingRevision,
      mappings: mappings.value
    })
    cert.value = res.data
    results.value = res.data?.mappingResults || []
    ElMessage.success(zh.value ? '已保存' : 'Saved')
    emit('saved')
  } catch (e) {
    conflict.value = e.response?.status === 409
  } finally {
    saving.value = false
  }
}
async function sync() {
  running.value = true
  try {
    const res = await request.post(`/api/certs/${cert.value.id}/mappings/sync`, {}, { timeout: 70000 })
    results.value = res.data || []
    emit('saved')
  } catch {
    try {
      results.value = (await request.get(`/api/certs/${cert.value.id}/mappings`)).data || []
    } catch {}
  } finally {
    running.value = false
  }
}
defineExpose({ open })
</script>
<style scoped>
.mapping-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
  padding: 12px 0;
  border-bottom: 1px solid var(--ap-border);
}
.mapping-row :deep(.el-select) {
  width: 100%;
}
.mapping-status {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 12px;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.mapping-status code {
  word-break: break-all;
}
.muted {
  color: var(--ap-muted);
  font-size: 12px;
  line-height: 1.6;
}
@media (max-width: 600px) {
  .mapping-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .mapping-row .el-button {
    min-height: 44px;
  }
  .mapping-row :deep(.el-form-item) {
    margin: 0;
  }
}
</style>
