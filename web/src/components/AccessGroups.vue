<template>
  <section class="access-workspace" v-loading="loading">
    <div class="access-toolbar">
      <el-button @click="load">{{ zh ? '刷新' : 'Refresh' }}</el-button
      ><el-button type="primary" @click="editGroup()">{{ zh ? '新增安全组' : 'Add group' }}</el-button
      ><el-button @click="editUser()">{{ zh ? '新增本地用户' : 'Add local user' }}</el-button
      ><el-button @click="openGrant">{{ zh ? '添加 IP 授权' : 'Grant IP access' }}</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-tabs v-model="view"
      ><el-tab-pane :label="`${zh ? '安全组' : 'Groups'} · ${groups.length}`" name="groups" /><el-tab-pane
        :label="`${zh ? '本地用户' : 'Users'} · ${users.length}`"
        name="users" /><el-tab-pane
        :label="`${zh ? '会话与授权' : 'Sessions and grants'} · ${sessions.length}`"
        name="sessions"
    /></el-tabs>
    <div v-if="view === 'groups'">
      <article v-for="g in groups" :key="g.id" class="access-row">
        <div>
          <b>{{ g.name }}</b>
          <p class="muted">
            {{ g.enabled ? (zh ? '启用' : 'Enabled') : zh ? '停用' : 'Disabled' }} · {{ g.sessionMinutes }}
            {{ zh ? '分钟' : 'min' }} ·
            {{ g.userIds.map((id) => userName(id)).join(', ') || (zh ? '没有用户' : 'No users') }}
          </p>
          <el-tag v-if="g.crossWebIpBypass" type="warning">{{
            zh ? '允许跨规则 IP 复用' : 'Cross-rule IP reuse'
          }}</el-tag>
        </div>
        <div class="access-toolbar">
          <el-button @click="editGroup(g)">{{ zh ? '编辑' : 'Edit' }}</el-button
          ><el-popconfirm
            :title="
              zh ? '删除此安全组？引用中的组无法删除。' : 'Delete this group? Referenced groups cannot be deleted.'
            "
            @confirm="remove('groups', g)"
            ><template #reference
              ><el-button type="danger" plain>{{ zh ? '删除' : 'Delete' }}</el-button></template
            ></el-popconfirm
          >
        </div>
      </article>
      <el-empty
        v-if="!groups.length"
        :description="
          zh ? '创建安全组，然后在 Web 或转发规则中绑定' : 'Create a group and bind it to a Web or forwarding rule'
        "
      />
    </div>
    <div v-else-if="view === 'users'">
      <article v-for="u in users" :key="u.id" class="access-row">
        <div>
          <b>{{ u.name || u.username }}</b>
          <p class="muted">{{ u.username }} · {{ u.enabled ? (zh ? '启用' : 'Enabled') : zh ? '停用' : 'Disabled' }}</p>
        </div>
        <div class="access-toolbar">
          <el-button @click="editUser(u)">{{ zh ? '编辑' : 'Edit' }}</el-button
          ><el-popconfirm
            :title="zh ? '删除用户？需先解除安全组引用。' : 'Delete user? Remove group membership first.'"
            @confirm="remove('users', u)"
            ><template #reference
              ><el-button type="danger" plain>{{ zh ? '删除' : 'Delete' }}</el-button></template
            ></el-popconfirm
          >
        </div>
      </article>
      <el-empty v-if="!users.length" :description="zh ? '暂无本地用户' : 'No local users'" />
    </div>
    <div v-else>
      <p class="muted">
        {{
          zh
            ? '会话与 IP 授权仅存于内存，重启清空。撤销会立即失效；继续提供有效登录凭据可以重新登录。'
            : 'Sessions and IP grants are in memory and clear on restart. Revocation invalidates them immediately; valid credentials can sign in again.'
        }}
      </p>
      <article v-for="s in sessions" :key="s.id" class="access-row">
        <div>
          <b>{{ s.username }} · {{ s.ip }}</b>
          <p class="muted">
            {{ s.source.toUpperCase() }} · {{ s.host || s.entityId }} ·
            {{ s.mode === 'ip' ? (zh ? 'IP 授权' : 'IP grant') : 'Cookie' }} · {{ formatTime(s.expiresAt) }}
          </p>
        </div>
        <el-button type="danger" plain @click="revoke(s)">{{ zh ? '撤销' : 'Revoke' }}</el-button>
      </article>
      <el-empty v-if="!sessions.length" :description="zh ? '暂无有效会话或授权' : 'No active sessions or grants'" />
    </div>
    <el-dialog
      v-model="groupOpen"
      class="workspace-dialog"
      :title="zh ? '安全组设置' : 'Group settings'"
      width="min(660px,94vw)"
      destroy-on-close
    >
      <el-alert
        v-if="conflict"
        type="warning"
        :closable="false"
        :title="
          zh
            ? '配置已变更，草稿已保留；请重新打开最新配置。'
            : 'Configuration changed; draft retained. Reopen the current object.'
        "
      />
      <el-form label-position="top"
        ><el-form-item :label="zh ? '名称' : 'Name'"><el-input v-model="group.name" maxlength="128" /></el-form-item
        ><el-form-item :label="zh ? '启用' : 'Enabled'"><el-switch v-model="group.enabled" /></el-form-item
        ><el-form-item :label="zh ? '会话时长（分钟）' : 'Session length (min)'"
          ><el-input-number v-model="group.sessionMinutes" :min="1" :max="10080" /></el-form-item
        ><el-form-item :label="zh ? '本地用户' : 'Local users'"
          ><el-select v-model="group.userIds" multiple style="width: 100%"
            ><el-option
              v-for="u in users"
              :key="u.id"
              :label="userName(u.id)"
              :value="u.id" /></el-select></el-form-item
        ><el-form-item :label="zh ? '已认证 IP 跨 Web 规则免登录' : 'Reuse authenticated IP across Web rules'"
          ><el-switch v-model="group.crossWebIpBypass"
        /></el-form-item>
        <p class="muted">
          {{
            zh
              ? '默认关闭。开启后，同一安全组中共用出口 IP 的设备可能获得访问权限；全局封禁和绑定的 IP/UA 策略仍生效。转发规则需要单独的 IP 授权。停用安全组会拒绝其绑定规则。'
              : 'Off by default. Devices sharing an authenticated public IP may gain access. Global bans and IP/UA policies still apply. Forwarding rules require separate IP grants. Disabled groups deny their bound rules.'
          }}
        </p></el-form
      >
      <template #footer
        ><el-button @click="groupOpen = false">{{ zh ? '取消' : 'Cancel' }}</el-button
        ><el-button type="primary" :loading="saving" :disabled="conflict" @click="saveGroup">{{
          zh ? '保存' : 'Save'
        }}</el-button></template
      >
    </el-dialog>
    <el-dialog
      v-model="userOpen"
      class="workspace-dialog"
      :title="zh ? '本地用户设置' : 'Local user settings'"
      width="min(560px,94vw)"
      destroy-on-close
      @closed="user.password = ''"
    >
      <el-alert
        v-if="conflict"
        type="warning"
        :closable="false"
        :title="
          zh ? '配置已变更，请重新打开最新用户；草稿已保留。' : 'User changed; reopen the latest user. Draft retained.'
        "
      />
      <el-form label-position="top"
        ><el-form-item :label="zh ? '备注' : 'Name'"><el-input v-model="user.name" maxlength="128" /></el-form-item
        ><el-form-item :label="zh ? '登录账号' : 'Username'"
          ><el-input v-model="user.username" maxlength="64" /></el-form-item
        ><el-form-item :label="zh ? '密码' : 'Password'"
          ><el-input
            v-model="user.password"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="
              user.id
                ? zh
                  ? '留空保留现有密码'
                  : 'Empty keeps current password'
                : zh
                  ? '10到72字节'
                  : '10 to 72 bytes'
            " /></el-form-item
        ><el-form-item :label="zh ? '启用' : 'Enabled'"><el-switch v-model="user.enabled" /></el-form-item
      ></el-form>
      <template #footer
        ><el-button @click="userOpen = false">{{ zh ? '取消' : 'Cancel' }}</el-button
        ><el-button type="primary" :loading="saving" :disabled="conflict" @click="saveUser">{{
          zh ? '保存' : 'Save'
        }}</el-button></template
      >
    </el-dialog>
    <el-dialog
      v-model="grantOpen"
      class="workspace-dialog"
      :title="zh ? 'IP 授权' : 'IP grant'"
      width="min(620px,94vw)"
      destroy-on-close
    >
      <el-form label-position="top"
        ><el-form-item :label="zh ? '安全组' : 'Group'"
          ><el-select v-model="grant.groupId" style="width: 100%"
            ><el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" /></el-select></el-form-item
        ><el-form-item :label="zh ? '用户' : 'User'"
          ><el-select v-model="grant.userId" style="width: 100%"
            ><el-option
              v-for="id in groups.find((g) => g.id === grant.groupId)?.userIds || []"
              :key="id"
              :label="userName(id)"
              :value="id" /></el-select></el-form-item
        ><el-form-item :label="zh ? '绑定规则' : 'Bound rule'"
          ><el-select v-model="grant.binding" style="width: 100%"
            ><el-option
              v-for="b in bindings.filter((b) => b.groupId === grant.groupId)"
              :key="b.key"
              :label="b.name"
              :value="b.key" /></el-select></el-form-item
        ><el-form-item label="IP"><el-input v-model="grant.ip" placeholder="203.0.113.10" /></el-form-item
        ><el-form-item v-if="selectedBinding?.source === 'web'" label="Host"
          ><el-input v-model="grant.host" placeholder="app.example.com:443" /></el-form-item
        ><el-form-item :label="zh ? '授权时长（分钟）' : 'Duration (min)'"
          ><el-input-number
            v-model="grant.minutes"
            :min="1"
            :max="groups.find((g) => g.id === grant.groupId)?.sessionMinutes || 720" /></el-form-item
      ></el-form>
      <template #footer
        ><el-button @click="grantOpen = false">{{ zh ? '取消' : 'Cancel' }}</el-button
        ><el-button type="primary" :loading="saving" @click="saveGrant">{{
          zh ? '授权' : 'Grant'
        }}</el-button></template
      >
    </el-dialog>
  </section>
</template>
<script setup>
import { computed, ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'
import { useIntervalFn } from '../composables/useIntervalFn'
const { locale } = useI18n(),
  zh = computed(() => locale.value.startsWith('zh'))
const groups = ref([]),
  users = ref([]),
  sessions = ref([]),
  bindings = ref([]),
  view = ref('groups'),
  loading = ref(false),
  saving = ref(false),
  error = ref(''),
  conflict = ref(false),
  groupOpen = ref(false),
  userOpen = ref(false),
  grantOpen = ref(false)
const groupDefault = () => ({
  id: '',
  revision: 0,
  name: '',
  enabled: true,
  sessionMinutes: 720,
  crossWebIpBypass: false,
  userIds: []
})
const userDefault = () => ({ id: '', revision: 0, name: '', username: '', enabled: true, password: '' })
const group = reactive(groupDefault()),
  user = reactive(userDefault()),
  grant = reactive({ groupId: '', userId: '', binding: '', ip: '', host: '', minutes: 60 })
const selectedBinding = computed(() => bindings.value.find((b) => b.key === grant.binding))
function userName(id) {
  const u = users.value.find((u) => u.id === id)
  return u ? `${u.name || u.username}${u.enabled ? '' : zh.value ? '（停用）' : ' (disabled)'}` : id
}
async function load() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const results = await Promise.allSettled(
      ['groups', 'users', 'sessions'].map((x) => request.get(`/api/protection/access/${x}`))
    )
    ;[groups, users, sessions].forEach((v, i) => {
      if (results[i].status === 'fulfilled') v.value = results[i].value.data || []
    })
    if (results.some((r) => r.status === 'rejected'))
      error.value = zh.value ? '部分安全组数据读取失败' : 'Some group data could not be loaded'
  } finally {
    loading.value = false
  }
}
function editGroup(row) {
  Object.assign(group, row ? JSON.parse(JSON.stringify(row)) : groupDefault())
  conflict.value = false
  groupOpen.value = true
}
function editUser(row) {
  Object.assign(user, row ? { ...row, password: '' } : userDefault())
  conflict.value = false
  userOpen.value = true
}
async function saveObject(kind, data) {
  saving.value = true
  try {
    if (data.id) await request.put(`/api/protection/access/${kind}/${data.id}`, data)
    else await request.post(`/api/protection/access/${kind}`, data)
    return true
  } catch (e) {
    conflict.value = e.response?.status === 409
    return false
  } finally {
    saving.value = false
  }
}
async function saveGroup() {
  if (await saveObject('groups', { ...group })) {
    groupOpen.value = false
    await load()
  }
}
async function saveUser() {
  if (await saveObject('users', { ...user })) {
    user.password = ''
    userOpen.value = false
    await load()
  }
}
async function remove(kind, row) {
  try {
    await request.delete(`/api/protection/access/${kind}/${row.id}`, { headers: { 'If-Match': String(row.revision) } })
    await load()
  } catch {}
}
async function revoke(row) {
  try {
    await request.delete(`/api/protection/access/sessions/${row.id}`)
    await load()
  } catch {}
}
async function openGrant() {
  grantOpen.value = true
  try {
    const [sites, forwards] = await Promise.all([request.get('/api/sites'), request.get('/api/forwards')])
    bindings.value = [
      ...(sites.data || []).flatMap((s) =>
        (s.rules || [])
          .filter((r) => r.accessGroupId)
          .map((r) => ({
            key: 'web:' + r.id,
            source: 'web',
            entityId: r.id,
            groupId: r.accessGroupId,
            name: s.name + ' / ' + r.name
          }))
      ),
      ...(forwards.data || [])
        .filter((r) => r.accessGroupId)
        .map((r) => ({
          key: 'forward:' + r.id,
          source: 'forward',
          entityId: r.id,
          groupId: r.accessGroupId,
          name: 'TCP/UDP · ' + r.name
        }))
    ]
  } catch {
    bindings.value = []
  }
}
async function saveGrant() {
  if (!selectedBinding.value) return
  saving.value = true
  try {
    await request.post('/api/protection/access/grants', {
      groupId: grant.groupId,
      userId: grant.userId,
      entityId: selectedBinding.value.entityId,
      source: selectedBinding.value.source,
      ip: grant.ip,
      host: selectedBinding.value.source === 'web' ? grant.host : '',
      expiresAt: new Date(Date.now() + grant.minutes * 60000).toISOString()
    })
    grantOpen.value = false
    await load()
  } catch {
  } finally {
    saving.value = false
  }
}
const poll = useIntervalFn(() => {
  if (!groupOpen.value && !userOpen.value && !grantOpen.value) return load()
}, 10000)
onMounted(() => {
  load()
  poll.start()
})
</script>
<style scoped>
.access-workspace {
  min-width: 0;
  display: grid;
  gap: 12px;
}
.access-toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.access-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--ap-border);
  border-radius: var(--ap-radius);
  background: var(--ap-card);
  margin-bottom: 10px;
  overflow-wrap: anywhere;
}
.access-row p {
  margin: 6px 0;
}
.muted {
  font-size: 12px;
  color: var(--ap-muted);
  line-height: 1.6;
}
@media (max-width: 600px) {
  .access-row {
    align-items: flex-start;
    flex-direction: column;
  }
  .access-toolbar .el-button {
    min-height: 44px;
  }
}
</style>
