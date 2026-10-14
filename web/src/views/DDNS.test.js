import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus, { ElMessageBox } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import DDNS from './DDNS.vue'

const requestMock = vi.hoisted(() => {
  if (!window.matchMedia)
    window.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} })
  if (!window.ResizeObserver)
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  return { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }
})
vi.mock('../api', () => ({ default: requestMock }))

const initialTask = () => ({
  id: 'task',
  name: 'Home DNS',
  enabled: false,
  providerId: 'cf',
  interval: 300,
  revision: 'version-1',
  records: [
    { id: 'a', name: '', domain: 'home.example.com', type: 'A', ttl: 300, enabled: false },
    { id: 'v6', name: '', domain: 'home.example.com', type: 'AAAA', ttl: 600, enabled: false }
  ],
  ipv4Source: { source: 'interface', interface: 'auto', apiUrl: '' },
  ipv6Source: { source: 'interface', interface: 'auto', apiUrl: '' }
})
let rows, wrappers
const mountPage = async () => {
  const wrapper = mount(DDNS, { global: { plugins: [ElementPlus, i18n] } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
const conflict = () => Object.assign(new Error('conflict'), { response: { status: 409, data: { code: 409 } } })

beforeEach(() => {
  rows = [initialTask()]
  wrappers = []
  requestMock.get.mockImplementation(async (path) => {
    if (path === '/api/ddns/tasks') return { data: structuredClone(rows) }
    if (path === '/api/providers')
      return { data: [{ id: 'cf', type: 'cloudflare', remark: 'Cloudflare', keyConfigured: true }] }
    if (path === '/api/ddns/interfaces') return { data: { interfaces: ['eth0'], wan: 'eth0' } }
    return { data: [] }
  })
  requestMock.post.mockResolvedValue({ data: { success: true } })
  requestMock.put.mockResolvedValue({ data: {} })
  requestMock.delete.mockResolvedValue({ data: {} })
  vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm')
})
afterEach(() => {
  wrappers.forEach((wrapper) => wrapper.unmount())
  vi.restoreAllMocks()
  vi.clearAllMocks()
})

describe('DDNS task workspace', () => {
  it('shows static types and preserves literal TXT values on record edits', async () => {
    rows[0].records.push({
      id: 'txt',
      domain: 'entry.example.com',
      type: 'TXT',
      value: '  Keep Case  ',
      ttl: 300,
      enabled: true
    })
    const wrapper = await mountPage()
    expect(wrapper.vm.familiesOf(wrapper.vm.tasks[0])).toContain('TXT')
    await wrapper.find('.expand-toggle').trigger('click')
    expect(wrapper.findAll('.record-row')[2].text()).toContain('Keep Case')
    wrapper.vm.openRecordDialog(wrapper.vm.tasks[0], wrapper.vm.tasks[0].records[2])
    await flushPromises()
    wrapper.vm.recordDialog.form.value = '  v=spf1 include:example.com ~all  '
    await wrapper.vm.saveRecord()
    expect(requestMock.put).toHaveBeenCalledWith(
      '/api/ddns/tasks/task/records/txt',
      expect.objectContaining({
        record: expect.objectContaining({ type: 'TXT', value: '  v=spf1 include:example.com ~all  ' })
      })
    )
  })

  it('prevents static records with missing values from being saved', async () => {
    const wrapper = await mountPage()
    wrapper.vm.openRecordDialog(wrapper.vm.tasks[0])
    Object.assign(wrapper.vm.recordDialog.form, { domain: 'entry.example.com', type: 'CNAME', value: '' })
    await wrapper.vm.saveRecord()
    expect(requestMock.post).not.toHaveBeenCalled()
    expect(wrapper.vm.recordDialog.visible).toBe(true)
  })
  it('uses compact task cards and preserves expansion across polling and filtering', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('.ddns-hero').exists()).toBe(false)
    expect(wrapper.find('.ddns-metrics').exists()).toBe(false)
    expect(wrapper.find('.task-table').exists()).toBe(false)
    expect(wrapper.find('.workspace-header h1').exists()).toBe(true)
    await wrapper.find('.expand-toggle').trigger('click')
    expect(wrapper.findAll('.record-row')).toHaveLength(2)
    rows[0].revision = 'version-2'
    await wrapper.vm.loadTasks()
    await flushPromises()
    expect(wrapper.find('.expand-toggle').attributes('aria-expanded')).toBe('true')
    expect(wrapper.findAll('.record-row')).toHaveLength(2)
    await wrapper.find('.search-input input').setValue('unmatched.example.com')
    expect(wrapper.findAll('.task-card')).toHaveLength(0)
    await wrapper.find('.search-input input').setValue('home.example.com')
    expect(wrapper.find('.expand-toggle').attributes('aria-expanded')).toBe('true')
  })

  it('omits revision on create and sends it when editing the same task', async () => {
    const wrapper = await mountPage()
    wrapper.vm.openTaskDialog()
    await flushPromises()
    Object.assign(wrapper.vm.taskDialog.form, { name: 'New task', providerId: 'cf' })
    wrapper.vm.taskDialog.form.records[0].domain = 'new.example.com'
    await wrapper.vm.saveTask()
    const [path, body] = requestMock.post.mock.calls.find(([path]) => path === '/api/ddns/tasks')
    expect(path).toBe('/api/ddns/tasks')
    expect(body).not.toHaveProperty('revision')
    expect(body.records[0]).toMatchObject({ domain: 'new.example.com', type: 'A' })
    wrapper.vm.openTaskDialog(wrapper.vm.tasks[0])
    await flushPromises()
    wrapper.vm.taskDialog.form.name = 'Edited task'
    await wrapper.vm.saveTask()
    expect(requestMock.put).toHaveBeenCalledWith(
      '/api/ddns/tasks/task',
      expect.objectContaining({ revision: 'version-1', name: 'Edited task' })
    )
  })

  it('confirms deletion and includes the current revision for task and record actions', async () => {
    const wrapper = await mountPage(),
      task = wrapper.vm.tasks[0],
      record = task.records[0]
    await wrapper.vm.toggleTask(task)
    expect(requestMock.post).toHaveBeenCalledWith('/api/ddns/tasks/task/toggle', { revision: 'version-1' })
    await wrapper.vm.taskCommand(task, 'delete')
    expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1)
    expect(requestMock.delete).toHaveBeenCalledWith('/api/ddns/tasks/task', { data: { revision: 'version-1' } })
    await wrapper.vm.recordCommand(task, record, 'delete')
    expect(ElMessageBox.confirm).toHaveBeenCalledTimes(2)
    expect(requestMock.delete).toHaveBeenCalledWith('/api/ddns/tasks/task/records/a', {
      data: { revision: 'version-1' }
    })
  })

  it('keeps a conflicting record draft and only adopts a new revision after explicit discard', async () => {
    const wrapper = await mountPage()
    wrapper.vm.openRecordDialog(wrapper.vm.tasks[0], wrapper.vm.tasks[0].records[0])
    wrapper.vm.recordDialog.form.domain = 'draft.example.com'
    rows[0].revision = 'version-2'
    rows[0].records[0].domain = 'server.example.com'
    requestMock.put.mockRejectedValueOnce(conflict())
    await wrapper.vm.saveRecord()
    await flushPromises()
    expect(wrapper.vm.recordDialog.conflict).toBe(true)
    expect(wrapper.vm.recordDialog.form.domain).toBe('draft.example.com')
    expect(wrapper.vm.recordDialog.task.revision).toBe('version-1')
    await wrapper.vm.saveRecord()
    expect(requestMock.put).toHaveBeenCalledTimes(1)
    ElMessageBox.confirm.mockRejectedValueOnce('cancel')
    await wrapper.vm.reloadRecordDraft()
    expect(wrapper.vm.recordDialog.form.domain).toBe('draft.example.com')
    await wrapper.vm.reloadRecordDraft()
    expect(wrapper.vm.recordDialog.conflict).toBe(false)
    expect(wrapper.vm.recordDialog.task.revision).toBe('version-2')
    expect(wrapper.vm.recordDialog.form.domain).toBe('server.example.com')
  })

  it('keeps a conflicting task draft and exposes an explicit reload path', async () => {
    const wrapper = await mountPage()
    wrapper.vm.openTaskDialog(wrapper.vm.tasks[0])
    await flushPromises()
    wrapper.vm.taskDialog.form.name = 'Draft task'
    rows[0].revision = 'version-2'
    rows[0].name = 'Server task'
    requestMock.put.mockRejectedValueOnce(conflict())
    await wrapper.vm.saveTask()
    expect(wrapper.vm.taskConflict).toBe(true)
    expect(wrapper.vm.taskDialog.form.name).toBe('Draft task')
    expect(wrapper.vm.taskDialog.form.revision).toBe('version-1')
    await wrapper.vm.reloadTaskDraft()
    expect(wrapper.vm.taskConflict).toBe(false)
    expect(wrapper.vm.taskDialog.form.name).toBe('Server task')
    expect(wrapper.vm.taskDialog.form.revision).toBe('version-2')
  })
})
