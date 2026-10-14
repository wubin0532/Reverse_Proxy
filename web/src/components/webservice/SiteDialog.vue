<template>
  <!-- 站点编辑对话框 -->
  <el-dialog
    class="workspace-dialog site-dialog"
    v-model="dialog.visible"
    :title="dialog.isEdit ? $t('webService.editSite') : $t('webService.addSiteTitle')"
    width="min(720px, 94vw)"
    destroy-on-close
  >
    <el-form
      ref="formRef"
      :model="dialog.form"
      :rules="siteRules"
      :label-width="locale.startsWith('zh') ? '160px' : '220px'"
    >
      <el-form-item :label="$t('webService.siteName')" prop="name">
        <el-input v-model="dialog.form.name" :placeholder="$t('webService.siteNamePlaceholder')" />
      </el-form-item>
      <el-form-item :label="labels.address" prop="listenHost">
        <template #label
          ><span>{{ labels.address }}</span
          ><HelpHint :label="labels.address" :text="hints.address"
        /></template>
        <el-input
          v-model="dialog.form.listenHost"
          :aria-label="labels.address"
          placeholder="0.0.0.0 / :: / 127.0.0.1"
        />
      </el-form-item>
      <el-form-item :label="labels.port" prop="listenPort" required>
        <template #label
          ><span>{{ labels.port }}</span
          ><HelpHint :label="labels.port" :text="hints.port"
        /></template>
        <el-input-number
          v-model="dialog.form.listenPort"
          :aria-label="labels.port"
          :min="1"
          :max="65535"
          :precision="0"
          controls-position="right"
        />
      </el-form-item>
      <el-form-item :label="$t('webService.enableTls')">
        <template #label
          ><span>{{ $t('webService.enableTls') }}</span
          ><HelpHint :label="$t('webService.enableTls')" :text="hints.tls"
        /></template>
        <el-switch v-model="dialog.form.tls" />
      </el-form-item>
      <el-form-item v-if="dialog.form.tls" :label="$t('webService.cert')">
        <el-select
          v-model="dialog.form.certId"
          clearable
          :placeholder="$t('webService.certPlaceholder')"
          style="width: 100%"
        >
          <el-option
            v-for="c in certs"
            :key="c.id"
            :label="c.name + '（' + (c.domains || []).join(', ') + '）'"
            :value="c.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item v-if="dialog.form.tls" :label="$t('webService.forceHttps')">
        <template #label
          ><span>{{ $t('webService.forceHttps') }}</span
          ><HelpHint :label="$t('webService.forceHttps')" :text="hints.https"
        /></template>
        <el-switch v-model="dialog.form.forceHttps" :disabled="!dialog.form.certId" />
        <span class="form-tip">
          {{ dialog.form.certId ? $t('webService.forceHttpsTipOn') : $t('webService.forceHttpsTipOff') }}
        </span>
      </el-form-item>
      <el-form-item :label="labels.policy">
        <template #label
          ><span>{{ labels.policy }}</span
          ><HelpHint :label="labels.policy" :text="hints.policy" /></template
        ><el-select v-model="dialog.form.policyId" clearable style="width: 100%"
          ><el-option v-for="policy in policies" :key="policy.id" :label="policy.name" :value="policy.id" /></el-select
      ></el-form-item>
      <el-form-item :label="labels.custom">
        <template #label
          ><span>{{ labels.custom }}</span
          ><HelpHint :label="labels.custom" :text="hints.custom" /></template
        ><el-switch v-model="dialog.form.customDefault"
      /></el-form-item>
      <template v-if="dialog.form.customDefault"
        ><el-form-item :label="labels.status"
          ><el-input-number v-model="dialog.form.defaultStatus" :min="200" :max="599" /></el-form-item
        ><el-form-item :label="labels.body"
          ><el-input v-model="dialog.form.defaultBody" type="textarea" :rows="3" maxlength="65536" /></el-form-item
      ></template>
      <el-form-item :label="$t('webService.firewall')">
        <template #label
          ><span>{{ $t('webService.firewall') }}</span
          ><HelpHint :label="$t('webService.firewall')" :text="hints.firewall"
        /></template>
        <el-switch v-model="dialog.form.autoFw" />
        <span class="form-tip">{{ $t('webService.firewallTip') }}</span>
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="dialog.visible = false">{{ $t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="dialog.saving" @click="submit">{{ $t('common.save') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import request from '../../api'
import { isValidPort } from '../../utils/validate'
import { parseSiteListen, composeSiteListen, isValidListenHost } from '../../utils/site-listen'
import HelpHint from '../HelpHint.vue'
import { useCrudDialog } from '../../composables/useCrudDialog'

defineProps({ certs: { type: Array, default: () => [] }, policies: { type: Array, default: () => [] } })
const emit = defineEmits(['saved'])

const { t, locale } = useI18n()
const labels = computed(() =>
  locale.value.startsWith('zh')
    ? {
        address: '监听地址',
        port: '监听端口',
        policy: '共享防护策略',
        custom: '自定义默认响应',
        status: '状态码',
        body: '响应正文'
      }
    : {
        address: 'Listen address',
        port: 'Listen port',
        policy: 'Shared protection policy',
        custom: 'Custom default response',
        status: 'Status code',
        body: 'Response body'
      }
)

const hints = computed(() =>
  locale.value.startsWith('zh')
    ? {
        address:
          '填写 IP 或主机名，不要附带端口。127.0.0.1 仅供本机访问；0.0.0.0 监听所有 IPv4 网卡，:: 监听 IPv6。留空由系统选择监听网卡。',
        port: '范围 1–65535。不能与其他服务占用的地址和端口冲突。修改后通过新端口访问本站点。',
        tls: '使用 HTTPS 加密连接，需选择已导入或已签发的证书。证书域名应与访问域名匹配。',
        https: '需要选择证书。开启后，同一端口收到的 HTTP 明文请求会以 301 跳转到 HTTPS。',
        policy: '将防护中心的共享策略应用到本站点；仍同时检查全局策略和子规则策略。留空不额外添加站点策略。',
        custom: '当请求未命中任何子规则时，返回下面设置的状态码和正文；不会覆盖已匹配规则或后端的响应。',
        firewall:
          '仅在 OpenWrt 生效：自动维护 WAN 到路由器此监听端口的入站放行规则。用于需要从外网访问的服务；不会替你配置上级路由器端口映射。',
        invalidHost: '请输入地址，不要附带端口；IPv6 地址无需方括号。',
        invalidPort: '端口必须是 1 到 65535 之间的整数。'
      }
    : {
        address:
          'Enter an IP or hostname without a port. 127.0.0.1 is local only; 0.0.0.0 listens on all IPv4 interfaces, :: on IPv6. Empty lets the system choose interfaces.',
        port: 'An integer from 1 to 65535. Avoid an address and port already used by another service. Use the new port after changing it.',
        tls: 'Encrypt connections with HTTPS. Select an imported or issued certificate matching the access hostname.',
        https: 'Requires a certificate. Redirects plain HTTP on the same port to HTTPS with status 301.',
        policy:
          'Apply a shared protection policy from the protection center. Global and rule policies still apply. Empty adds no site policy.',
        custom:
          'Return this status and body when no subrule matches. Matched rules and backend responses are unaffected.',
        firewall:
          'OpenWrt only: maintain an inbound WAN firewall allowance for this listen port. Use for external access. Upstream router port forwarding must be configured separately.',
        invalidHost: 'Enter an address without a port. IPv6 needs no square brackets.',
        invalidPort: 'Port must be an integer from 1 to 65535.'
      }
)

const emptyForm = () => ({
  id: '',
  revision: 0,
  policyId: '',
  customDefault: false,
  defaultStatus: 404,
  defaultBody: '',
  name: '',
  listenHost: '0.0.0.0',
  listenPort: 8080,
  tls: false,
  certId: '',
  forceHttps: false,
  autoFw: false,
  enabled: true,
  rules: []
})

const siteRules = computed(() => ({
  name: [{ required: true, message: t('webService.nameRequired'), trigger: 'blur' }],
  listenHost: [
    {
      validator: (_rule, value, done) =>
        done(isValidListenHost(value) ? undefined : new Error(hints.value.invalidHost)),
      trigger: 'blur'
    }
  ],
  listenPort: [
    {
      validator: (_rule, value, done) => done(isValidPort(value) ? undefined : new Error(hints.value.invalidPort)),
      trigger: ['blur', 'change']
    }
  ]
}))

const { formRef, dialog, open, submit } = useCrudDialog({
  emptyForm,
  fromRow: (row) => ({
    id: row.id,
    revision: row.revision ?? 0,
    policyId: row.policyId || '',
    customDefault: !!row.defaultResponse,
    defaultStatus: row.defaultResponse?.statusCode ?? 404,
    defaultBody: row.defaultResponse?.body || '',
    name: row.name,
    ...parseSiteListen(row.listen),
    tls: row.tls,
    certId: row.certId || '',
    forceHttps: !!(row.forceHttps && row.certId),
    autoFw: !!row.autoFw,
    enabled: row.enabled,
    rules: row.rules || []
  }),
  onSubmit: async (form, d) => {
    const body = {
      name: form.name,
      policyId: form.policyId,
      ...(d.isEdit ? { expectedRevision: form.revision } : {}),
      defaultResponse: form.customDefault ? { statusCode: form.defaultStatus, body: form.defaultBody } : null,
      enabled: form.enabled,
      listen: composeSiteListen(form.listenHost, form.listenPort),
      tls: form.tls,
      certId: form.tls ? form.certId : '',
      forceHttps: !!(form.tls && form.certId && form.forceHttps),
      autoFw: form.autoFw,
      rules: d.isEdit ? form.rules : []
    }
    if (d.isEdit) {
      await request.put(`/api/sites/${form.id}`, body)
    } else {
      const res = await request.post('/api/sites', body)
      return res.data?.id || ''
    }
  },
  onSaved: (createdId) => emit('saved', createdId)
})

defineExpose({ open })
</script>

<style scoped>
:deep(.el-form-item__label) {
  align-items: center;
  white-space: nowrap;
}
:deep(.el-form-item__content) {
  gap: 8px;
}
.form-tip {
  margin-left: 0;
  flex: 1;
  min-width: 180px;
  color: var(--ap-muted);
  font-size: 12px;
}
@media (max-width: 700px) {
  :deep(.el-form-item) {
    display: block;
  }
  :deep(.el-form-item__label) {
    width: 100% !important;
    height: auto;
    justify-content: flex-start;
    margin-bottom: 6px;
  }
  :deep(.el-form-item__content) {
    margin-left: 0 !important;
  }
}
</style>
