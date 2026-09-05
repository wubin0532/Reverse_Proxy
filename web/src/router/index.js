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
      { path: 'dashboard', name: 'Dashboard', component: () => import('../views/Dashboard.vue'), meta: { title: '运维控制台' } },
      { path: 'ddns', name: 'DDNS', component: () => import('../views/DDNS.vue'), meta: { title: '动态域名' } },
      { path: 'certs', name: 'Certs', component: () => import('../views/Certs.vue'), meta: { title: '证书管理' } },
      { path: 'web-service', name: 'WebService', component: () => import('../views/WebService.vue'), meta: { title: 'Web服务' } },
      { path: 'forward', name: 'Forward', component: () => import('../views/Forward.vue'), meta: { title: '端口转发' } },
      { path: 'logs', name: 'Logs', component: () => import('../views/Logs.vue'), meta: { title: '日志中心' } },
      { path: 'settings', redirect: '/dashboard' }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' }
]

const router = createRouter({
  history: createWebHistory(),
	routes,
	scrollBehavior() {
		return { top: 0, left: 0 }
	}
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
  if (auth.needChangePassword && to.path !== '/dashboard') {
    ElMessage.warning('请先修改一次性初始密码')
    return '/dashboard'
  }
  return true
})

export default router
