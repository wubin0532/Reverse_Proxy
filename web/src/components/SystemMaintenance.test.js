import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, expect, it, vi } from 'vitest'
import i18n from '../locales'
import SystemMaintenance from './SystemMaintenance.vue'
import RestoreNotice from './RestoreNotice.vue'
import { restoreWarnings, setRestoreWarnings } from '../utils/restore-notice'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: api, deferUnauthorizedRedirects: () => () => {} }))
const navigation = vi.hoisted(() => ({ replace: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => navigation }))
let wrapper, notice
function create() {
  i18n.global.locale.value = 'zh-CN'
  api.get.mockResolvedValue({ data: { state: 'idle' } })
  wrapper = mount(SystemMaintenance, { global: { plugins: [ElementPlus, i18n] } })
}
afterEach(() => {
  wrapper?.unmount()
  notice?.unmount()
  setRestoreWarnings([])
  vi.clearAllMocks()
})
it('preserves restore warnings above the login route until acknowledged', async () => {
  create()
  const warning = 'Web 服务未生效: 端口占用 <script>test</script>'
  api.post.mockResolvedValue({ data: { loginRequired: true, warnings: [warning] } })
  Object.assign(wrapper.vm.importForm, { password: 'test-password', backupPassword: 'backup-only', backup: '{}' })
  await wrapper.vm.importBackup()
  expect(navigation.replace).toHaveBeenCalledWith('/login')
  expect(restoreWarnings.value).toEqual([warning])
  expect(sessionStorage.getItem('andey-proxy.restore-warnings')).toContain('端口占用')
  wrapper.unmount()
  wrapper = null
  notice = mount(RestoreNotice, { global: { plugins: [ElementPlus, i18n] } })
  expect(notice.text()).toContain('部分服务尚未生效')
  expect(notice.text()).toContain(warning)
  expect(notice.find('script').exists()).toBe(false)
  await notice.find('button').trigger('click')
  expect(notice.find('[role="alert"]').exists()).toBe(false)
  expect(sessionStorage.getItem('andey-proxy.restore-warnings')).toBeNull()
})
it('allows slow uploads and reports upload progress while awaiting inspection', async () => {
  create()
  wrapper.vm.uploadFile = new File(['test'], 'fixture.run')
  api.post.mockImplementation(async (_path, _data, options) => {
    expect(options.timeout).toBe(13 * 60 * 1000)
    options.onUploadProgress({ loaded: 50, total: 100 })
    expect(wrapper.vm.uploadProgress).toBe(50)
    return { data: { uploadId: 'verified' } }
  })
  await wrapper.vm.inspectPackage()
  await flushPromises()
  expect(wrapper.vm.inspection.uploadId).toBe('verified')
})
