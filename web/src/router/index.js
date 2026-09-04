import { createRouter, createWebHistory } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '../store/auth'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('../views/Login.vue'),
    meta: { public: true }
  },
  {
    path: '/',
    component: () => import('../layout/Layout.vue'),
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: 'Dashboard', component: () => import('../views/Dashboard.vue'), meta: { title: '概览' } },
      { path: 'ddns', name: 'DDNS', component: () => import('../views/DDNS.vue'), meta: { title: '动态域名' } },
      { path: 'certs', name: 'Certs', component: () => import('../views/Certs.vue'), meta: { title: '证书管理' } },
      { path: 'web-service', name: 'WebService', component: () => import('../views/WebService.vue'), meta: { title: 'Web服务' } },
      { path: 'forward', name: 'Forward', component: () => import('../views/Forward.vue'), meta: { title: '端口转发' } },
      { path: 'settings', name: 'Settings', component: () => import('../views/Settings.vue'), meta: { title: '系统设置' } }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  const auth = useAuthStore()
  if (!auth.checked) {
    const ok = await auth.fetchMe()
    if (!ok) {
      return { path: '/login', query: { redirect: to.fullPath } }
    }
  } else if (!auth.username) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  if (auth.needChangePassword && to.path !== '/settings') {
    ElMessage.warning('正在使用默认密码，请先修改账号密码')
    return { path: '/settings' }
  }
  return true
})

export default router
