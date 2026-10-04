<template>
  <component
    :is="inline ? 'div' : ElDrawer"
    :model-value="true"
    :title="`${$t('tunnel.routes')} · ${instance.name}`"
    size="min(960px, 98vw)"
    @close="$emit('close')"
  >
    <div class="routes" v-loading="loading">
      <el-alert v-if="cloudError" type="warning" :title="cloudError" :closable="false" />
      <el-alert v-else-if="readOnly" type="info" :title="$t('tunnel.readonlyTip')" :closable="false" />
      <div class="actions">
        <el-button @click="load">{{ $t('tunnel.refreshCloud') }}</el-button
        ><el-button type="primary" :disabled="busy" @click="edit()">{{ $t('tunnel.addRoute') }}</el-button
        ><el-button :disabled="busy || readOnly || !digest" type="success" @click="$emit('sync', digest)">{{
          $t('tunnel.sync')
        }}</el-button>
      </div>
      <template v-if="conflicts.length">
        <el-alert type="warning" :title="$t('tunnel.cloudChanged')" :closable="false" />
        <el-table :data="conflicts" row-key="hostname">
          <el-table-column prop="hostname" :label="$t('tunnel.hostname')" min-width="140" />
          <el-table-column prop="service" :label="$t('tunnel.service')" min-width="170" />
          <el-table-column :label="$t('tunnel.nextPublish')" min-width="170">
            <template #default="{ row }">{{ row.removing ? $t('common.delete') : row.desiredService }}</template>
          </el-table-column>
          <el-table-column width="150">
            <template #default="{ row }">
              <el-popconfirm :title="$t('tunnel.reconcileConfirm')" @confirm="reconcile(row)">
                <template #reference>
                  <el-button size="small" :disabled="busy || saving || loading || readOnly || !row.recoverable">{{
                    $t('tunnel.reconcile')
                  }}</el-button>
                </template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </template>
      <h3>{{ $t('tunnel.localRoutes') }}</h3>
      <el-table :data="rows" row-key="id"
        ><el-table-column prop="hostname" :label="$t('tunnel.hostname')" min-width="150" /><el-table-column
          :label="$t('tunnel.target')"
          min-width="180"
          ><template #default="{ row }"
            >{{ row.target === 'site' ? siteName(row.siteId) : row.url
            }}<small v-if="row.target === 'site'">{{ services[row.id] || row.appliedService }}</small></template
          ></el-table-column
        ><el-table-column :label="$t('common.actions')" width="160"
          ><template #default="{ row }"
            ><el-button size="small" :disabled="busy" @click="edit(row)">{{ $t('common.edit') }}</el-button
            ><el-popconfirm :title="$t('tunnel.localDelete')" @confirm="remove(row)"
              ><template #reference
                ><el-button size="small" :disabled="busy" type="danger">{{ $t('common.delete') }}</el-button></template
              ></el-popconfirm
            ></template
          ></el-table-column
        ></el-table
      >
      <h3>{{ $t('tunnel.cloudRoutes') }}</h3>
      <el-table :data="remote"
        ><el-table-column prop="hostname" :label="$t('tunnel.hostname')" min-width="150" /><el-table-column
          prop="service"
          :label="$t('tunnel.service')"
          min-width="180"
        /><el-table-column prop="path" :label="$t('tunnel.path')" /><el-table-column width="150"
          ><template #default="{ row }"
            ><el-button
              v-if="row.editable && !isManaged(row.hostname)"
              size="small"
              :disabled="busy || readOnly"
              @click="adopt(row)"
              >{{ $t('tunnel.adopt') }}</el-button
            ></template
          ></el-table-column
        ></el-table
      >
    </div>
    <el-dialog v-model="editOpen" :title="$t('tunnel.saveRoute')" width="min(560px, 95vw)" append-to-body>
      <el-form label-position="top">
        <el-form-item :label="$t('tunnel.hostname')"
          ><el-input v-model="form.hostname" placeholder="app.example.com"
        /></el-form-item>
        <el-form-item v-if="zones.length" :label="$t('tunnel.zone')"
          ><el-select v-model="form.zoneId"
            ><el-option v-for="zone in zones" :key="zone.id" :label="zone.name" :value="zone.id" /></el-select
        ></el-form-item>
        <el-form-item v-else :label="$t('tunnel.zoneId')"><el-input v-model="form.zoneId" /></el-form-item>
        <el-form-item :label="$t('tunnel.target')"
          ><el-radio-group v-model="form.target"
            ><el-radio value="site">{{ $t('tunnel.site') }}</el-radio
            ><el-radio value="url">{{ $t('tunnel.url') }}</el-radio></el-radio-group
          ></el-form-item
        >
        <el-form-item v-if="form.target === 'site'" :label="$t('tunnel.site')"
          ><el-select v-model="form.siteId"
            ><el-option
              v-for="site in sites"
              :key="site.id"
              :label="site.name"
              :value="site.id"
              :disabled="!site.enabled" /></el-select
        ></el-form-item>
        <template v-else>
          <el-alert :title="$t('tunnel.directTip')" type="info" :closable="false" />
          <el-form-item :label="$t('tunnel.service')"
            ><el-input v-model="form.url" placeholder="http://192.168.1.100:8080"
          /></el-form-item>
          <el-form-item
            ><el-checkbox v-model="form.skipTlsVerify">{{ $t('tunnel.tls') }}</el-checkbox
            ><small>{{ $t('tunnel.tlsTip') }}</small></el-form-item
          >
        </template>
      </el-form>
      <template #footer
        ><el-button @click="editOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="saving" :disabled="busy" @click="save">{{
          $t('common.save')
        }}</el-button></template
      >
    </el-dialog>
  </component>
</template>
<script setup>
import { onMounted, ref } from 'vue'
import request from '../api'
import { ElDrawer } from 'element-plus'
const props = defineProps({
  instance: { type: Object, required: true },
  sites: { type: Array, default: () => [] },
  inline: Boolean,
  busy: Boolean
})
const emit = defineEmits(['close', 'saved', 'sync'])
const rows = ref([]),
  remote = ref([]),
  conflicts = ref([]),
  zones = ref([]),
  services = ref({}),
  digest = ref(''),
  readOnly = ref(true),
  cloudError = ref('')
const loading = ref(false),
  saving = ref(false),
  editOpen = ref(false),
  form = ref({})
const path = `/api/tunnels/instances/${props.instance.id}/routes`
async function load() {
  loading.value = true
  try {
    const res = await request.get(path, { timeout: 45000 })
    rows.value = res.data.routes
    remote.value = res.data.remote
    conflicts.value = res.data.conflicts || []
    services.value = res.data.services || {}
    digest.value = res.data.digest
    readOnly.value = res.data.readOnly
    cloudError.value = res.data.cloudError
  } catch {
    /* Request interceptor displays errors. */
  } finally {
    loading.value = false
  }
}
function clean(row) {
  const item = {
    id: row.id || '',
    hostname: row.hostname || '',
    zoneId: row.zoneId || '',
    target: row.target || 'site',
    siteId: row.siteId || '',
    url: row.url || '',
    skipTlsVerify: !!row.skipTlsVerify
  }
  if (row.adoptHostname) item.adoptHostname = row.adoptHostname
  return item
}
function edit(row) {
  form.value = clean(row || {})
  editOpen.value = true
}
function adopt(row) {
  edit({
    hostname: row.hostname,
    target: 'url',
    url: row.service,
    skipTlsVerify: row.skipTlsVerify,
    adoptHostname: row.hostname
  })
}
function siteName(id) {
  return props.sites.find((site) => site.id === id)?.name || id
}
function isManaged(hostname) {
  return rows.value.some((row) => row.appliedHostname === hostname || row.hostname === hostname)
}
async function persist(next) {
  saving.value = true
  try {
    const res = await request.put(path, { routes: next.map(clean), digest: digest.value }, { timeout: 45000 })
    rows.value = res.data
    editOpen.value = false
    emit('saved')
    await load()
  } catch {
    /* Preserve form on failure. */
  } finally {
    saving.value = false
  }
}
async function save() {
  const next = rows.value.map(clean),
    item = clean(form.value)
  const index = next.findIndex((row) => item.id && row.id === item.id)
  if (index >= 0) next[index] = item
  else next.push(item)
  await persist(next)
}
async function remove(row) {
  await persist(rows.value.filter((item) => item.id !== row.id))
}
async function reconcile(row) {
  saving.value = true
  try {
    await request.post(`${path}/reconcile`, { digest: digest.value, hostnames: [row.hostname] }, { timeout: 45000 })
    emit('saved')
    await load()
  } catch {
    /* Preserve the displayed conflict until a successful refresh. */
  } finally {
    saving.value = false
  }
}
onMounted(async () => {
  await load()
  if (props.instance.accountRef) {
    try {
      const res = await request.get(`/api/tunnels/accounts/${props.instance.accountRef}/zones`, { timeout: 45000 })
      zones.value = res.data
    } catch {
      /* Manual Zone ID remains available. */
    }
  }
})
</script>
<style scoped>
.routes {
  display: grid;
  gap: 12px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.actions .el-button {
  margin: 0;
}
.el-select {
  width: 100%;
}
small {
  display: block;
  color: var(--el-text-color-secondary);
  word-break: break-all;
  margin-top: 6px;
}
.el-alert {
  margin-bottom: 12px;
}
</style>
