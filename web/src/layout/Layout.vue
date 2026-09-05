<template>
  <el-container class="layout">
    <el-aside class="sidebar desktop-sidebar" :width="collapsed ? '72px' : '230px'"><NavMenu :collapsed="collapsed" /></el-aside>
    <el-drawer v-model="drawer" direction="ltr" :with-header="false" size="230px" class="nav-drawer"><NavMenu @navigate="drawer = false" /></el-drawer>
    <el-container class="content-shell">
      <el-header class="header" height="64px">
        <div class="header-left">
          <el-button text circle :aria-label="$t('layout.toggleNav')" @click="toggleMenu"><el-icon><Menu /></el-icon></el-button>
          <div><div class="header-title">{{ pageTitle }}</div><div class="header-sub">{{ $t('layout.headerSub') }}</div></div>
        </div>
        <div class="header-actions">
          <el-tag :type="healthTagType" round>{{ healthText }}</el-tag>
          <el-button text circle :aria-label="$t('layout.refreshPage')" @click="refreshKey++"><el-icon><Refresh /></el-icon></el-button>
          <el-dropdown @command="setLocale">
            <button class="user-info"><span class="user-name">{{ locale === 'zh-CN' ? '中文' : 'English' }}</span><el-icon><ArrowDown /></el-icon></button>
            <template #dropdown><el-dropdown-menu><el-dropdown-item command="zh-CN" :disabled="locale === 'zh-CN'">简体中文</el-dropdown-item><el-dropdown-item command="en-US" :disabled="locale === 'en-US'">English</el-dropdown-item></el-dropdown-menu></template>
          </el-dropdown>
          <el-dropdown @command="handleCommand">
            <button class="user-info"><span class="avatar">{{ auth.username?.slice(0, 1)?.toUpperCase() || 'A' }}</span><span class="user-name">{{ auth.username }}</span><el-icon><ArrowDown /></el-icon></button>
            <template #dropdown><el-dropdown-menu><el-dropdown-item command="security">{{ $t('layout.accountSecurity') }}</el-dropdown-item><el-dropdown-item divided command="logout">{{ $t('layout.logout') }}</el-dropdown-item></el-dropdown-menu></template>
          </el-dropdown>
        </div>
      </el-header>
      <el-main class="main"><router-view :key="refreshKey" /></el-main>
    </el-container>

    <el-dialog v-model="securityOpen" :title="$t('layout.accountSecurity')" width="460px" :close-on-click-modal="!auth.needChangePassword" :close-on-press-escape="!auth.needChangePassword" :show-close="!auth.needChangePassword">
      <el-alert v-if="auth.needChangePassword" type="warning" :title="$t('layout.security.mustChangeAlert')" :closable="false" class="security-alert" />
      <el-form ref="securityFormRef" :model="security" :rules="securityRules" label-position="top">
        <el-form-item :label="$t('layout.security.username')" prop="username"><el-input v-model="security.username" autocomplete="username" /></el-form-item>
        <el-form-item :label="$t('layout.security.currentPassword')" prop="oldPassword"><el-input v-model="security.oldPassword" type="password" show-password autocomplete="current-password" /></el-form-item>
        <el-form-item :label="$t('layout.security.newPassword')" prop="newPassword"><el-input v-model="security.newPassword" type="password" show-password autocomplete="new-password" :placeholder="$t('layout.security.newPasswordPlaceholder')" /></el-form-item>
        <el-form-item :label="$t('layout.security.confirmPassword')" prop="confirmPassword"><el-input v-model="security.confirmPassword" type="password" show-password autocomplete="new-password" /></el-form-item>
      </el-form>
      <div class="security-section">
        <div class="security-section-head">
          <div><strong>Google Authenticator</strong><p>{{ $t('layout.totp.desc') }}</p></div>
          <el-tag :type="auth.totpEnabled ? 'success' : 'info'">{{ auth.totpEnabled ? $t('common.tagEnabled') : $t('common.tagDisabled') }}</el-tag>
        </div>
        <el-alert v-if="!secure" type="warning" :title="$t('layout.totp.httpsOnly')" :closable="false" />
        <div class="totp-actions" v-else-if="!auth.needChangePassword">
          <template v-if="auth.totpEnabled"><el-button @click="openTOTPManagement('regenerate')">{{ $t('layout.totp.regenerateRecovery') }}</el-button><el-button type="danger" plain @click="openTOTPManagement('disable')">{{ $t('layout.totp.disable2fa') }}</el-button></template>
          <el-button v-else type="primary" plain @click="openTOTPSetup">{{ $t('layout.totp.bindGa') }}</el-button>
        </div>
        <p v-else class="security-note">{{ $t('layout.totp.changePasswordFirst') }}</p>
      </div>
      <template #footer><el-button type="primary" :loading="savingPassword" @click="savePassword">{{ $t('layout.security.saveRelogin') }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="setupOpen" :title="$t('layout.totp.setupTitle')" width="460px" :close-on-click-modal="false" @closed="cancelTOTPSetup">
      <template v-if="setupStep === 'password'">
        <el-alert type="info" :title="$t('layout.totp.setupVerifyAlert')" :closable="false" />
        <el-form label-position="top" class="dialog-form"><input class="visually-hidden" type="text" autocomplete="username" :value="auth.username" tabindex="-1" aria-hidden="true" /><el-form-item :label="$t('layout.security.currentPassword')"><el-input v-model="setupPassword" type="password" show-password autocomplete="current-password" @keyup.enter="startTOTPSetup" /></el-form-item></el-form>
      </template>
      <template v-else>
        <div class="qr-wrap"><img v-if="setupId" :src="`/api/settings/totp/setup/${setupId}/qr`" :alt="$t('layout.totp.qrAlt')" /></div>
        <p class="setup-guide">{{ $t('layout.totp.setupGuide') }}</p>
        <div class="manual-key"><code>{{ setupManualKey }}</code><el-button link type="primary" @click="copyText(setupManualKey)">{{ $t('common.copy') }}</el-button></div>
        <el-alert type="warning" :title="$t('layout.totp.timeAccurate')" :closable="false" />
        <el-form label-position="top" class="dialog-form"><el-form-item :label="$t('layout.totp.codeLabel')"><el-input v-model="setupCode" maxlength="6" inputmode="numeric" autocomplete="one-time-code" @keyup.enter="enableTOTP" /></el-form-item></el-form>
      </template>
      <template #footer><el-button @click="setupOpen=false">{{ $t('common.cancel') }}</el-button><el-button v-if="setupStep === 'password'" type="primary" :loading="setupLoading" @click="startTOTPSetup">{{ $t('layout.totp.continue') }}</el-button><el-button v-else type="primary" :loading="setupLoading" @click="enableTOTP">{{ $t('layout.totp.verifyEnable') }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="factorOpen" :title="factorMode === 'disable' ? $t('layout.totp.factorDisableTitle') : $t('layout.totp.factorRegenerateTitle')" width="430px" :close-on-click-modal="false">
      <el-alert :type="factorMode === 'disable' ? 'warning' : 'info'" :title="factorMode === 'disable' ? $t('layout.totp.factorDisableAlert') : $t('layout.totp.factorRegenerateAlert')" :closable="false" />
      <el-form label-position="top" class="dialog-form"><input class="visually-hidden" type="text" autocomplete="username" :value="auth.username" tabindex="-1" aria-hidden="true" /><el-form-item :label="$t('layout.security.currentPassword')"><el-input v-model="factor.password" type="password" show-password autocomplete="current-password" /></el-form-item><el-form-item :label="$t('layout.totp.codeOrRecovery')"><el-input v-model="factor.code" autocomplete="one-time-code" @keyup.enter="submitTOTPManagement" /></el-form-item></el-form>
      <template #footer><el-button @click="factorOpen=false">{{ $t('common.cancel') }}</el-button><el-button :type="factorMode === 'disable' ? 'danger' : 'primary'" :loading="factorLoading" @click="submitTOTPManagement">{{ $t('common.confirm') }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="recoveryOpen" :title="$t('layout.recovery.title')" width="520px" :show-close="false" :close-on-click-modal="false" :close-on-press-escape="false">
      <el-alert type="warning" :title="$t('layout.recovery.alert')" :closable="false" />
      <div class="recovery-grid"><code v-for="code in recoveryCodes" :key="code">{{ code }}</code></div>
      <div class="recovery-tools"><el-button @click="copyText(recoveryCodes.join('\n'))">{{ $t('layout.recovery.copyAll') }}</el-button><el-button @click="downloadRecoveryCodes">{{ $t('layout.recovery.downloadFile') }}</el-button></div>
      <el-checkbox v-model="recoverySaved">{{ $t('layout.recovery.savedCheckbox') }}</el-checkbox>
      <template #footer><el-button type="primary" :disabled="!recoverySaved" @click="finishRecovery">{{ $t('layout.recovery.finishRelogin') }}</el-button></template>
    </el-dialog>
  </el-container>
</template>

<script setup>
import { computed, defineComponent, h, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElIcon, ElMenu, ElMenuItem, ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, Compass, Connection, Document, Lock, Menu, Monitor, Odometer, Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { useAuthStore } from '../store/auth'
import { setLocale } from '../locales'

const route = useRoute(), router = useRouter(), auth = useAuthStore()
const { t, locale } = useI18n()
const collapsed = ref(false), drawer = ref(false), refreshKey = ref(0), securityOpen = ref(false), health = ref('ok')
const healthTagType = computed(() => health.value === 'ok' ? 'success' : health.value === 'issue' ? 'warning' : 'info')
const healthText = computed(() => ({ ok: t('layout.healthOk'), issue: t('layout.healthIssue'), unknown: t('layout.healthUnknown') }[health.value]))
const securityFormRef = ref(), savingPassword = ref(false)
const secure = window.location.protocol === 'https:'
const setupOpen = ref(false), setupStep = ref('password'), setupPassword = ref(''), setupCode = ref(''), setupId = ref(''), setupManualKey = ref(''), setupLoading = ref(false)
const factorOpen = ref(false), factorMode = ref('disable'), factorLoading = ref(false), factor = reactive({ password: '', code: '' })
const recoveryOpen = ref(false), recoveryCodes = ref([]), recoverySaved = ref(false)
const security = reactive({ username: auth.username || 'admin', oldPassword: '', newPassword: '', confirmPassword: '' })
const securityRules = computed(() => ({
  username: [{ required: true, message: t('layout.security.usernameRequired'), trigger: 'blur' }], oldPassword: [{ required: true, message: t('layout.security.currentPasswordRequired'), trigger: 'blur' }],
  newPassword: [{ required: true, min: 10, max: 72, message: t('layout.security.passwordLength'), trigger: 'blur' }],
  confirmPassword: [{ validator: (_, value, done) => value === security.newPassword ? done() : done(new Error(t('layout.security.passwordMismatch'))), trigger: 'blur' }]
}))
const pageTitle = computed(() => route.meta.titleKey ? t(route.meta.titleKey) : '')
const menuItems = [['/dashboard','nav.dashboard',Odometer],['/ddns','nav.ddns',Compass],['/certs','nav.certs',Lock],['/web-service','nav.webService',Monitor],['/forward','nav.forward',Connection],['/logs','nav.logs',Document]]
const NavMenu = defineComponent({ props: { collapsed: Boolean }, emits: ['navigate'], setup(props, { emit }) { return () => {
  return h('div', { class:'nav-wrap' }, [h('div',{class:'logo'},[h('span',{class:'logo-mark'},'A'),!props.collapsed&&h('span','andey-proxy')]),h('div',{class:'nav-label'},props.collapsed?'':t('nav.group')),h(ElMenu,{defaultActive:route.path,collapse:props.collapsed,router:true,onSelect:()=>emit('navigate')},()=>menuItems.map(([path,label,icon])=>h(ElMenuItem,{index:path},{default:()=>[h(ElIcon,null,()=>h(icon)),h('span',t(label))]})))])
} } })
function toggleMenu() { if (window.innerWidth < 900) drawer.value = true; else collapsed.value = !collapsed.value }
async function handleCommand(cmd) { if (cmd === 'security') { await auth.fetchMe(); security.username = auth.username; securityOpen.value = true } else if (cmd === 'logout') { try { await ElMessageBox.confirm(t('layout.logoutConfirm'), t('layout.logout'), { type: 'warning', confirmButtonText: t('layout.logoutButton'), cancelButtonText: t('common.cancel') }) } catch { return } await auth.logout(); router.push('/login') } }
async function savePassword() { await securityFormRef.value.validate(); savingPassword.value = true; try { await request.post('/api/settings/password',{username:security.username,oldPassword:security.oldPassword,newPassword:security.newPassword}); clearInterval(healthTimer); ElMessage.success(t('layout.security.changedSuccess')); router.replace('/login') } finally { savingPassword.value = false } }
function openTOTPSetup() { setupStep.value = 'password'; setupPassword.value = ''; setupCode.value = ''; setupId.value = ''; setupManualKey.value = ''; setupOpen.value = true }
async function startTOTPSetup() { if (!setupPassword.value) return ElMessage.warning(t('common.enterCurrentPassword')); setupLoading.value = true; try { const res = await request.post('/api/settings/totp/setup', { password: setupPassword.value }); setupId.value = res.data.setupId; setupManualKey.value = res.data.manualKey; setupPassword.value = ''; setupStep.value = 'verify' } finally { setupLoading.value = false } }
async function cancelTOTPSetup() { const id = setupId.value; setupId.value = ''; setupManualKey.value = ''; setupCode.value = ''; if (id) { try { await request.delete(`/api/settings/totp/setup/${id}`) } catch {} } }
async function enableTOTP() { if (!/^\d{6}$/.test(setupCode.value.trim())) return ElMessage.warning(t('layout.totp.codeRequired')); setupLoading.value = true; try { const res = await request.post('/api/settings/totp/enable', { setupId: setupId.value, code: setupCode.value.trim() }); setupId.value = ''; setupOpen.value = false; clearInterval(healthTimer); showRecoveryCodes(res.data.recoveryCodes) } finally { setupLoading.value = false } }
function openTOTPManagement(mode) { factorMode.value = mode; factor.password = ''; factor.code = ''; factorOpen.value = true }
async function submitTOTPManagement() { if (!factor.password || !factor.code.trim()) return ElMessage.warning(t('layout.totp.enterPasswordAndCode')); factorLoading.value = true; try { const path = factorMode.value === 'disable' ? '/api/settings/totp/disable' : '/api/settings/totp/recovery/regenerate'; const res = await request.post(path, { password: factor.password, code: factor.code.trim() }); factorOpen.value = false; clearInterval(healthTimer); if (factorMode.value === 'disable') { ElMessage.success(t('layout.totp.disabled2fa')); router.replace('/login') } else showRecoveryCodes(res.data.recoveryCodes) } finally { factorLoading.value = false } }
function showRecoveryCodes(codes) { recoveryCodes.value = codes || []; recoverySaved.value = false; recoveryOpen.value = true }
async function copyText(text) { try { await navigator.clipboard.writeText(text); ElMessage.success(t('common.copied')) } catch { ElMessage.error(t('common.copyFailed')) } }
function downloadRecoveryCodes() { const blob = new Blob([`${t('layout.recovery.fileTitle')}\n\n${recoveryCodes.value.join('\n')}\n`], { type: 'text/plain;charset=utf-8' }); const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = 'andey-proxy-recovery-codes.txt'; a.click(); URL.revokeObjectURL(url) }
function finishRecovery() { recoveryCodes.value = []; recoveryOpen.value = false; router.replace('/login') }
let healthTimer
async function refreshHealth() { try { const res=await request.get('/api/dashboard'); health.value=(res.data?.issues?.length)?'issue':'ok' } catch { health.value='unknown' } }
onMounted(() => { securityOpen.value = auth.needChangePassword; refreshHealth(); healthTimer=setInterval(refreshHealth,30000) })
onUnmounted(() => clearInterval(healthTimer))
</script>

<style>
.visually-hidden{position:absolute!important;width:1px!important;height:1px!important;padding:0!important;margin:-1px!important;overflow:hidden!important;clip:rect(0,0,0,0)!important;white-space:nowrap!important;border:0!important}.layout{min-height:100vh}.sidebar{background:linear-gradient(180deg,#0b3e48,#092f38);transition:width .22s;overflow:hidden}.nav-wrap{height:100%}.logo{height:64px;display:flex;align-items:center;gap:10px;padding:0 17px;color:white;font-weight:700;font-size:18px;white-space:nowrap}.logo-mark,.avatar{display:inline-grid;place-items:center;width:38px;height:38px;border-radius:11px;background:linear-gradient(145deg,#16a6a2,#087f8c);color:white;box-shadow:0 6px 18px rgba(0,0,0,.18)}.nav-label{min-height:34px;padding:9px 20px;color:#7fa7ad;font-size:11px;letter-spacing:.12em}.sidebar :deep(.el-menu),.nav-drawer :deep(.el-menu){border:0;background:transparent}.sidebar :deep(.el-menu-item),.nav-drawer :deep(.el-menu-item){margin:5px 10px;border-radius:10px;color:#bfd0d3}.sidebar :deep(.el-menu-item:hover),.sidebar :deep(.el-menu-item.is-active),.nav-drawer :deep(.el-menu-item:hover),.nav-drawer :deep(.el-menu-item.is-active){background:rgba(34,181,176,.18);color:white}.content-shell{min-width:0}.header{position:sticky;top:0;z-index:20;display:flex;align-items:center;justify-content:space-between;padding:0 24px;background:rgba(255,255,255,.92);border-bottom:1px solid var(--ap-border);backdrop-filter:blur(10px)}.header-left,.header-actions,.user-info{display:flex;align-items:center;gap:10px}.header-title{font-size:18px;font-weight:700}.header-sub{margin-top:2px;font-size:12px;color:var(--ap-muted)}.user-info{border:0;background:transparent;color:var(--ap-text);cursor:pointer;min-height:44px}.avatar{width:32px;height:32px;border-radius:50%}.main{padding:22px;background:var(--ap-bg);overflow-x:hidden}.security-alert{margin-bottom:16px}.security-section{margin-top:18px;padding-top:18px;border-top:1px solid var(--ap-border)}.security-section-head{display:flex;justify-content:space-between;gap:16px;align-items:flex-start}.security-section-head p,.security-note,.setup-guide{margin:5px 0 14px;color:var(--ap-muted);font-size:13px}.totp-actions{display:flex;gap:10px;flex-wrap:wrap}.dialog-form{margin-top:16px}.qr-wrap{text-align:center}.qr-wrap img{width:220px;height:220px;border:8px solid white;border-radius:10px}.manual-key{display:flex;align-items:center;justify-content:space-between;padding:10px 12px;margin:10px 0 14px;border-radius:8px;background:#f2f6f6;word-break:break-all}.recovery-grid{display:grid;grid-template-columns:1fr 1fr;gap:9px;margin:16px 0}.recovery-grid code{padding:9px;text-align:center;border-radius:7px;background:#f2f6f6}.recovery-tools{display:flex;gap:8px;margin-bottom:14px}.nav-drawer :deep(.el-drawer__body){padding:0;background:#0b3e48}@media(max-width:899px){.desktop-sidebar{display:none}.main{padding:16px}.header{padding:0 14px}}@media(max-width:599px){.header-sub,.user-name,.header-actions>.el-tag{display:none}.main{padding:12px}.recovery-grid{grid-template-columns:1fr}.security-section-head{align-items:center}}
</style>
