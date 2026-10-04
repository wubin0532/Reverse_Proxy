import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'

export function useTunnels() {
  const { t } = useI18n()
  const runtime = ref({}),
    accounts = ref([]),
    instances = ref([]),
    sites = ref([])
  const loading = ref(false),
    saving = ref(false),
    operation = ref(null)
  const instanceOpen = ref(false),
    accountOpen = ref(false),
    accountListOpen = ref(false)
  const instanceForm = ref({}),
    accountForm = ref({}),
    selected = ref(null)
  let timer,
    polling = false,
    alive = true
  const base = '/api/tunnels'
  async function refresh() {
    loading.value = true
    try {
      const [rt, ac, ins, si] = await Promise.all([
        request.get(`${base}/runtime`),
        request.get(`${base}/accounts`),
        request.get(`${base}/instances`),
        request.get('/api/sites')
      ])
      if (!alive) return
      runtime.value = rt.data
      accounts.value = ac.data
      instances.value = ins.data
      restoreOperation(ins.data)
      sites.value = si.data
    } catch {
      /* Request interceptor displays errors. */
    } finally {
      loading.value = false
    }
  }
  function editInstance(row) {
    instanceForm.value = {
      id: row?.id || '',
      name: row?.name || '',
      source: row?.source || 'managed',
      accountRef: row?.accountRef || '',
      tunnelId: row?.tunnelId || '',
      token: '',
      protocol: row?.protocol || 'auto',
      ipVersion: row?.ipVersion || 'auto',
      tokenConfigured: !!row?.tokenConfigured
    }
    instanceOpen.value = true
  }
  function editAccount(row) {
    accountForm.value = {
      id: row?.id || '',
      name: row?.name || '',
      accountId: row?.accountId || '',
      token: '',
      tokenConfigured: !!row?.tokenConfigured
    }
    accountOpen.value = true
  }
  async function saveInstance() {
    saving.value = true
    try {
      const { id, tokenConfigured, ...payload } = instanceForm.value
      const res = id
        ? await request.put(`${base}/instances/${id}`, payload)
        : await request.post(`${base}/instances`, payload)
      instanceForm.value.token = ''
      instanceOpen.value = false
      await refresh()
      if (!id && payload.source === 'managed') await runOperation(res.data, 'create')
    } catch {
      /* Preserve form on failure. */
    } finally {
      saving.value = false
    }
  }
  async function saveAccount() {
    saving.value = true
    try {
      const { id, tokenConfigured, ...payload } = accountForm.value
      if (id) await request.put(`${base}/accounts/${id}`, payload)
      else await request.post(`${base}/accounts`, payload)
      accountForm.value.token = ''
      accountOpen.value = false
      await refresh()
    } catch {
      /* Preserve form on failure. */
    } finally {
      saving.value = false
    }
  }
  async function testAccount(row) {
    try {
      const res = await request.post(`${base}/accounts/${row.id}/test`)
      ElMessage.success(res.data.message)
    } catch {
      /* Displayed by interceptor. */
    }
  }
  async function deleteAccount(row) {
    try {
      await request.delete(`${base}/accounts/${row.id}`)
      await refresh()
    } catch {
      /* Displayed by interceptor. */
    }
  }
  async function control(row, action) {
    try {
      await request.post(`${base}/instances/${row.id}/${action}`)
      await refresh()
    } catch {
      /* Displayed by interceptor. */
    }
  }
  async function removeInstance(row) {
    try {
      await request.delete(`${base}/instances/${row.id}`)
      await refresh()
    } catch {
      /* Displayed by interceptor. */
    }
  }
  async function runOperation(row, kind, digest = '') {
    try {
      const endpoint = kind === 'delete' ? 'delete-cloud' : kind
      const res = await request.post(`${base}/instances/${row.id}/${endpoint}`, { digest })
      operation.value = res.data
    } catch {
      /* Displayed by interceptor. */
    }
  }
  async function retryOperation() {
    const row = instances.value.find((item) => item.id === operation.value?.instanceId)
    if (!row) return
    if (operation.value.kind === 'sync') selected.value = row
    else await runOperation(row, operation.value.kind)
  }
  function restoreOperation(rows) {
    if (operation.value) return
    operation.value =
      rows
        .map((row) => row.status?.lastOperation)
        .filter(Boolean)
        .sort((a, b) => b.startedAt.localeCompare(a.startedAt))[0] || null
  }
  async function poll() {
    if (polling || !alive) return
    polling = true
    try {
      const res = await request.get(`${base}/instances`)
      if (!alive) return
      instances.value = res.data
      restoreOperation(res.data)
      if (operation.value?.status === 'running') {
        const op = await request.get(`${base}/operations/${operation.value.id}`)
        if (!alive) return
        operation.value = op.data
        if (op.data.status === 'succeeded') ElMessage.success(t('common.success'))
      }
    } catch {
      /* Next tick can recover. */
    } finally {
      polling = false
    }
  }
  onMounted(async () => {
    await refresh()
    if (alive) timer = setInterval(poll, 3000)
  })
  onUnmounted(() => {
    alive = false
    clearInterval(timer)
    instanceForm.value.token = ''
    accountForm.value.token = ''
  })
  return {
    runtime,
    accounts,
    instances,
    sites,
    loading,
    saving,
    operation,
    instanceOpen,
    accountOpen,
    accountListOpen,
    instanceForm,
    accountForm,
    selected,
    refresh,
    editInstance,
    editAccount,
    saveInstance,
    saveAccount,
    testAccount,
    deleteAccount,
    control,
    removeInstance,
    runOperation,
    retryOperation
  }
}
