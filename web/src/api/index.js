import axios from 'axios'
import { ElMessage } from 'element-plus'
import i18n from '../locales'

const { t } = i18n.global

const request = axios.create({
  baseURL: '/',
  timeout: 15000
})

request.interceptors.response.use(
  (response) => {
    const res = response.data
    if (res && typeof res === 'object' && 'code' in res) {
      if (res.code === 0) {
        return res
      }
      if (res.code === 401) {
        redirectToLogin()
        return Promise.reject(new Error(res.msg || t('api.notLoggedIn')))
      }
      ElMessage.error(res.msg || t('api.requestFailed'))
      return Promise.reject(new Error(res.msg || t('api.requestFailed')))
    }
    return res
  },
  async (error) => {
    if (error.response && error.response.status === 401) {
      redirectToLogin()
      return Promise.reject(error)
    }
    // responseType: 'blob' 的接口出错时响应体是 Blob 包装的 JSON，需解析出具体错误信息
    let msg = error.response?.data?.msg
    const data = error.response?.data
    if (!msg && data instanceof Blob && data.type.includes('json')) {
      try {
        msg = JSON.parse(await data.text())?.msg
      } catch {
        // 解析失败则回退到通用提示
      }
    }
    ElMessage.error(msg || error.message || t('api.networkError'))
    return Promise.reject(error)
  }
)

function redirectToLogin() {
  if (window.location.pathname !== '/login') {
    const redirect = encodeURIComponent(window.location.pathname + window.location.search)
    window.location.href = `/login?redirect=${redirect}`
  }
}

export default request
