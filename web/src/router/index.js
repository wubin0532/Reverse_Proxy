import { createRouter, createWebHistory } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '../store/auth'
import i18n from '../locales'

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
      { path: 'dashboard', name: 'Dashboard', component: () => import('../views/Dashboard.vue'), meta: { titleKey: 'nav.dashboard' } },
      { path: 'ddns', name: 'DDNS', component: () => import('../views/DDNS.vue'), meta: { titleKey: 'nav.ddns' } },
      { path: 'certs', name: 'Certs', component: () => import('../views/Certs.vue'), meta: { titleKey: 'nav.certs' } },
      { path: 'web-service', name: 'WebService', component: () => import('../views/WebService.vue'), meta: { titleKey: 'nav.webServiceTitle' } },
      { path: 'forward', name: 'Forward', component: () => import('../views/Forward.vue'), meta: { titleKey: 'nav.forward' } },
      { path: 'logs', name: 'Logs', component: () => import('../views/Logs.vue'), meta: { titleKey: 'nav.logs' } },
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
    ElMessage.warning(i18n.global.t('router.needChangePassword'))
    return '/dashboard'
  }
  return true
})

export default router
