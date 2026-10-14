<template>
  <el-dialog v-model="visible" :title="zh ? '后端池' : 'Backend pools'" width="min(920px, 94vw)" destroy-on-close>
    <div class="pool-toolbar">
      <el-button @click="load">{{ zh ? '刷新' : 'Refresh' }}</el-button
      ><el-button type="primary" @click="edit()">{{ zh ? '新增后端池' : 'Add pool' }}</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-table :data="pools" v-loading="loading">
      <el-table-column prop="name" :label="zh ? '名称' : 'Name'" min-width="150" />
      <el-table-column :label="zh ? '节点' : 'Nodes'" width="90"
        ><template #default="{ row }"
          >{{ row.nodes.filter((n) => n.enabled).length }} / {{ row.nodes.length }}</template
        ></el-table-column
      >
      <el-table-column :label="zh ? '健康检查' : 'Health'" width="110"
        ><template #default="{ row }">{{
          row.healthCheck.enabled ? row.healthCheck.type.toUpperCase() : zh ? '关闭' : 'Off'
        }}</template></el-table-column
      >
      <el-table-column :label="zh ? '操作' : 'Actions'" width="150"
        ><template #default="{ row }"
          ><el-button link @click="edit(row)">{{ zh ? '编辑' : 'Edit' }}</el-button
          ><el-popconfirm
            :title="zh ? '删除后端池？被引用的池无法删除。' : 'Delete pool? Referenced pools cannot be deleted.'"
            @confirm="remove(row)"
            ><template #reference
              ><el-button link type="danger">{{ zh ? '删除' : 'Delete' }}</el-button></template
            ></el-popconfirm
          ></template
        ></el-table-column
      >
    </el-table>
    <el-dialog
      class="workspace-dialog"
      v-model="editing"
      append-to-body
      :title="draft.id ? (zh ? '编辑后端池' : 'Edit pool') : zh ? '新增后端池' : 'Add pool'"
      width="min(850px, 94vw)"
      destroy-on-close
    >
      <el-alert
        v-if="conflict"
        type="warning"
        :closable="false"
        :title="
          zh
            ? '配置已变更，草稿已保留。重新打开最新配置后再保存。'
            : 'Pool changed. Your draft has been kept. Reopen the latest pool to save.'
        "
      />
      <el-form label-position="top">
        <div class="pool-grid">
          <el-form-item :label="zh ? '名称' : 'Name'"><el-input v-model="draft.name" maxlength="128" /></el-form-item
          ><el-form-item :label="zh ? '池并发请求上限（0 使用 4096）' : 'Concurrent request limit (0 = 4096)'"
            ><el-input-number v-model="draft.maxConcurrent" :min="0" :max="4096"
          /></el-form-item>
        </div>
        <p class="muted">
          {{
            zh
              ? '按权重轮询；同一池的并发上限由所有绑定规则共享。所有节点不可用时返回 503。'
              : 'Weighted round robin. All referring rules share pool limits. No available node returns 503.'
          }}
        </p>
        <article v-for="(node, i) in draft.nodes" :key="i" class="pool-node">
          <el-form-item :label="`${zh ? '后端地址' : 'Backend URL'} ${i + 1}`"
            ><el-input v-model="node.url" placeholder="https://127.0.0.1:8080"
          /></el-form-item>
          <div class="pool-grid">
            <el-form-item :label="zh ? '权重' : 'Weight'"
              ><el-input-number v-model="node.weight" :min="1" :max="100" /></el-form-item
            ><el-form-item :label="zh ? '节点并发上限（0 继承池）' : 'Node limit (0 inherits pool)'"
              ><el-input-number v-model="node.maxConcurrent" :min="0" :max="4096"
            /></el-form-item>
          </div>
          <div class="pool-toolbar">
            <el-switch v-model="node.enabled" :active-text="zh ? '启用' : 'Enabled'" /><el-button
              type="danger"
              plain
              :disabled="draft.nodes.length === 1"
              @click="draft.nodes.splice(i, 1)"
              >{{ zh ? '删除节点' : 'Remove node' }}</el-button
            >
          </div>
        </article>
        <el-button :disabled="draft.nodes.length >= 32" @click="draft.nodes.push(emptyNode())">{{
          zh ? '添加节点' : 'Add node'
        }}</el-button>
        <el-divider />
        <el-form-item :label="zh ? '主动健康检查' : 'Active health checks'"
          ><el-switch v-model="draft.healthCheck.enabled"
        /></el-form-item>
        <template v-if="draft.healthCheck.enabled">
          <div class="pool-grid">
            <el-form-item :label="zh ? '探测类型' : 'Probe type'"
              ><el-select v-model="draft.healthCheck.type"
                ><el-option label="TCP" value="tcp" /><el-option label="HTTP" value="http" /></el-select></el-form-item
            ><el-form-item v-if="draft.healthCheck.type === 'http'" :label="zh ? '路径' : 'Path'"
              ><el-input v-model="draft.healthCheck.path"
            /></el-form-item>
          </div>
          <div class="pool-grid">
            <el-form-item :label="zh ? '间隔（秒）' : 'Interval (s)'"
              ><el-input-number v-model="draft.healthCheck.intervalSeconds" :min="2" :max="300" /></el-form-item
            ><el-form-item :label="zh ? '超时（秒）' : 'Timeout (s)'"
              ><el-input-number v-model="draft.healthCheck.timeoutSeconds" :min="1" :max="60" /></el-form-item
            ><el-form-item :label="zh ? '恢复次数' : 'Rise'"
              ><el-input-number v-model="draft.healthCheck.rise" :min="1" :max="10" /></el-form-item
            ><el-form-item :label="zh ? '摘除次数' : 'Fall'"
              ><el-input-number v-model="draft.healthCheck.fall" :min="1" :max="10"
            /></el-form-item>
          </div>
        </template>
      </el-form>
      <template #footer
        ><el-button @click="editing = false">{{ zh ? '取消' : 'Cancel' }}</el-button
        ><el-button type="primary" :loading="saving" :disabled="conflict" @click="save">{{
          zh ? '保存' : 'Save'
        }}</el-button></template
      >
    </el-dialog>
  </el-dialog>
</template>
<script setup>
import { computed, ref, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import request from '../../api'
const emit = defineEmits(['saved'])
const { locale } = useI18n()
const zh = computed(() => locale.value.startsWith('zh'))
const visible = ref(false),
  editing = ref(false),
  saving = ref(false),
  loading = ref(false),
  conflict = ref(false),
  pools = ref([]),
  error = ref('')
const emptyNode = () => ({ url: '', weight: 1, enabled: true, maxConcurrent: 0 })
const defaults = () => ({
  id: '',
  revision: 0,
  name: '',
  maxConcurrent: 0,
  nodes: [emptyNode()],
  healthCheck: { enabled: false, type: 'tcp', path: '/', intervalSeconds: 10, timeoutSeconds: 3, rise: 1, fall: 2 }
})
const draft = reactive(defaults())
async function load() {
  loading.value = true
  error.value = ''
  try {
    pools.value = (await request.get('/api/backend-pools')).data || []
  } catch {
    error.value = zh.value ? '后端池读取失败，请重试' : 'Could not load pools'
  } finally {
    loading.value = false
  }
}
function open() {
  visible.value = true
  load()
}
function edit(row) {
  Object.assign(draft, row ? JSON.parse(JSON.stringify(row)) : defaults())
  conflict.value = false
  editing.value = true
}
async function save() {
  saving.value = true
  try {
    const data = JSON.parse(JSON.stringify(draft))
    if (draft.id) await request.put(`/api/backend-pools/${draft.id}`, data)
    else await request.post('/api/backend-pools', data)
    editing.value = false
    ElMessage.success(zh.value ? '已保存' : 'Saved')
    await load()
    emit('saved')
  } catch (e) {
    conflict.value = e.response?.status === 409
  } finally {
    saving.value = false
  }
}
async function remove(row) {
  try {
    await request.delete(`/api/backend-pools/${row.id}`, { headers: { 'If-Match': String(row.revision) } })
    await load()
    emit('saved')
  } catch {}
}
defineExpose({ open })
</script>
<style scoped>
.pool-toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.pool-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.pool-node {
  padding: 14px;
  margin-bottom: 12px;
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
  background: var(--ap-card);
}
.pool-grid :deep(.el-select),
.pool-grid :deep(.el-input-number) {
  width: 100%;
  max-width: 100%;
}
.muted {
  color: var(--ap-muted);
  font-size: 12px;
}
@media (max-width: 600px) {
  .pool-grid {
    grid-template-columns: minmax(0, 1fr);
  }
  .pool-toolbar .el-button {
    min-height: 44px;
  }
  .pool-node {
    padding: 10px;
  }
}
</style>
