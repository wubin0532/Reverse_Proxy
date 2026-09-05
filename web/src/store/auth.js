import { defineStore } from 'pinia'
import request from '../api'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    username: '',
    needChangePassword: false,
    totpEnabled: false,
    checked: false // 是否已尝试恢复登录态
  }),
  actions: {
    async fetchMe() {
      try {
        const res = await request.get('/api/me')
        this.username = res.data.username
        this.needChangePassword = res.data.needChangePassword
        this.totpEnabled = !!res.data.totpEnabled
        this.checked = true
        return true
      } catch {
        this.username = ''
        this.needChangePassword = false
        this.totpEnabled = false
        this.checked = true
        return false
      }
    },
    async login(username, password) {
      const res = await request.post('/api/login', { username, password })
    if (res.data.twoFactorRequired) return res.data
      this.username = username
      this.needChangePassword = res.data.needChangePassword
    this.totpEnabled = !!res.data.totpEnabled
      this.checked = true
    return res.data
    },
    async completeTwoFactor(username, challengeId, code) {
    const res = await request.post('/api/login/totp', { challengeId, code })
    this.username = username
    this.needChangePassword = res.data.needChangePassword
    this.totpEnabled = true
    this.checked = true
    return res.data
    },
    async logout() {
      try {
        await request.post('/api/logout')
      } finally {
        this.username = ''
        this.needChangePassword = false
    this.totpEnabled = false
      }
    }
  }
})
