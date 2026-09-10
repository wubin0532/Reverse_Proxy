<template>
  <div class="page-grid" v-loading="loading">
    <section class="hero">
      <div>
        <span class="eyebrow">{{ $t('nav.group') }}</span>
        <h1>{{ $t('settings.title') }}</h1>
        <p>{{ $t('settings.subtitle') }}</p>
      </div>
    </section>

    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('dashboard.systemInfo') }}</span
          ><el-button text :icon="Refresh" @click="load">{{ $t('common.refresh') }}</el-button>
        </div></template
      >
      <div class="info-list">
        <div>
          <span>{{ $t('dashboard.version') }}</span
          ><b>{{ sys.version || data.version || '-' }}</b>
        </div>
        <div>
          <span>{{ $t('dashboard.platform') }}</span
          ><b>{{ sys.goos || '-' }} / {{ sys.goarch || '-' }}</b>
        </div>
        <div>
          <span>{{ $t('settings.uptime') }}</span
          ><b>{{ uptimeText }}</b>
        </div>
        <div>
          <span>{{ $t('dashboard.adminProtocol') }}</span
          ><el-tag :type="data.adminHttps ? 'success' : 'danger'">{{ data.adminHttps ? 'HTTPS' : 'HTTP' }}</el-tag>
        </div>
        <div>
          <span>{{ $t('dashboard.accountSecurity') }}</span
          ><el-tag :type="data.mustChangePassword ? 'warning' : data.totpEnabled ? 'success' : 'info'">{{
            data.mustChangePassword
              ? $t('dashboard.needChangePassword')
              : data.totpEnabled
                ? $t('dashboard.totpEnabled')
                : $t('dashboard.passwordOnly')
          }}</el-tag>
        </div>
      </div>
    </el-card>

    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('dashboard.firewallTitle') }}</span
          ><el-tag :type="data.firewall.openwrt ? 'success' : 'info'">{{
            data.firewall.openwrt ? 'OpenWrt' : $t('dashboard.nonOpenwrt')
          }}</el-tag>
        </div></template
      >
      <div class="big-number">{{ data.firewall.rules.length }}</div>
      <p class="muted">{{ $t('dashboard.firewallRules') }}</p>
      <div class="tag-row">
        <el-tag v-for="r in data.firewall.rules" :key="r.key" effect="plain">{{ r.port }}/{{ r.proto }}</el-tag
        ><el-empty v-if="!data.firewall.rules.length" :description="$t('dashboard.noFirewallRules')" :image-size="48" />
      </div>
    </el-card>

    <el-card>
      <template #header
        ><div class="card-header">
          <span>{{ $t('settings.quickActions') }}</span>
        </div></template
      >
      <div class="info-list">
        <div>
          <span>{{ $t('layout.accountSecurity') }}</span>
          <p class="muted action-desc">{{ $t('settings.accountSecurityDesc') }}</p>
          <el-button type="primary" plain @click="openAccountSecurity">{{
            $t('settings.openAccountSecurity')
          }}</el-button>
        </div>
        <div>
          <span>{{ $t('nav.notifications') }}</span>
          <p class="muted action-desc">{{ $t('settings.notificationsDesc') }}</p>
          <el-button plain @click="router.push('/notifications')">{{ $t('settings.goNotifications') }}</el-button>
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { computed, inject, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'

const { t } = useI18n()
const router = useRouter()
const openAccountSecurity = inject('openAccountSecurity', () => {})

const loading = ref(false)
const data = reactive({
  version: '',
  adminHttps: true,
  mustChangePassword: false,
  totpEnabled: false,
  firewall: { openwrt: false, rules: [] }
})
const sys = reactive({})

const uptimeText = computed(() => {
  const s = Number(sys.uptime) || 0
  const d = Math.floor(s / 86400),
    h = Math.floor((s % 86400) / 3600),
    m = Math.floor((s % 3600) / 60)
  return d > 0 ? t('settings.uptimeDays', { d, h }) : t('settings.uptimeHours', { h, m })
})

async function load() {
  loading.value = true
  try {
    const [dash, info] = await Promise.all([request.get('/api/dashboard'), request.get('/api/system/info')])
    Object.assign(data, dash.data || {})
    Object.assign(sys, info.data || {})
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 26px 30px;
  border-radius: 18px;
  color: white;
  background: linear-gradient(120deg, var(--ap-hero-from), var(--ap-hero-to));
  box-shadow: 0 16px 38px var(--ap-hero-shadow);
}
.hero h1 {
  margin: 7px 0;
  font-size: 26px;
}
.hero p {
  margin: 0;
  color: var(--ap-hero-text-soft);
}
.eyebrow {
  font-size: 12px;
  letter-spacing: 0.15em;
  text-transform: uppercase;
}
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.info-list > div {
  display: flex;
  justify-content: space-between;
  align-items: center;
  min-height: 44px;
  border-bottom: 1px solid var(--ap-border-soft);
  gap: 14px;
}
.big-number {
  font-size: 36px;
  font-weight: 750;
}
.tag-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.muted {
  color: var(--ap-muted);
}
.action-desc {
  flex: 1;
  margin: 0;
  font-size: 13px;
}
@media (max-width: 760px) {
  .hero {
    padding: 21px;
  }
  .hero h1 {
    font-size: 21px;
  }
  .info-list > div {
    flex-wrap: wrap;
    padding: 8px 0;
  }
}
</style>
