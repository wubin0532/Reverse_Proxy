<template>
  <!-- 凭据对话框 -->
  <el-dialog
    v-model="dialog.visible"
    :title="dialog.isEdit ? $t('ddns.editProvider') : $t('ddns.addProviderTitle')"
    width="480px"
    destroy-on-close
  >
    <el-form ref="formRef" :model="dialog.form" :rules="providerRules" label-width="110px">
      <el-form-item :label="$t('ddns.colProvider')" prop="type">
        <el-select v-model="dialog.form.type" :disabled="dialog.isEdit" style="width: 100%">
          <el-option :label="$t('ddns.providerTypes.aliyun') + ' (aliyun)'" value="aliyun" />
          <el-option label="Cloudflare" value="cloudflare" />
          <el-option label="DNSPod (dnspod)" value="dnspod" />
          <el-option :label="$t('ddns.providerTypes.tencentcloud') + ' (tencentcloud)'" value="tencentcloud" />
          <el-option :label="$t('ddns.providerTypes.huaweicloud') + ' (huaweicloud)'" value="huaweicloud" />
          <el-option
            :label="$t('ddns.providerTypes.godaddy') + ' (godaddy) · ' + $t('ddns.acmeOnly')"
            value="godaddy"
          />
          <el-option
            :label="$t('ddns.providerTypes.route53') + ' (route53) · ' + $t('ddns.acmeOnly')"
            value="route53"
          />
        </el-select>
      </el-form-item>
      <el-form-item :label="$t('ddns.remark')" prop="remark">
        <el-input v-model="dialog.form.remark" :placeholder="$t('ddns.remarkPlaceholder')" />
      </el-form-item>
      <el-form-item :label="keyLabel" prop="key">
        <el-input
          v-model="dialog.form.key"
          type="password"
          show-password
          :placeholder="dialog.isEdit && dialog.form.keyConfigured ? $t('common.keepEmpty') : keyPlaceholder"
        />
      </el-form-item>
      <el-form-item v-if="dialog.form.type !== 'cloudflare'" :label="secretLabel" prop="secret">
        <el-input
          v-model="dialog.form.secret"
          type="password"
          show-password
          :placeholder="dialog.isEdit && dialog.form.secretConfigured ? $t('common.keepEmpty') : secretPlaceholder"
        />
      </el-form-item>
      <el-form-item :label="$t('ddns.customEndpoint')">
        <el-input v-model="dialog.form.endpoint" :placeholder="$t('ddns.endpointPlaceholder')" />
      </el-form-item>
      <el-form-item :label="$t('ddns.testDomain')">
        <el-input v-model="testDomain" :placeholder="$t('ddns.testDomainPlaceholder')" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button :loading="testing" @click="testProvider">{{ $t('ddns.test') }}</el-button>
      <el-button @click="dialog.visible = false">{{ $t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="dialog.saving" @click="submit">{{ $t('common.save') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../../api'
import { useCrudDialog } from '../../composables/useCrudDialog'

const emit = defineEmits(['saved'])

const { t } = useI18n()

const KEY_LABELS = {
  aliyun: 'AccessKey ID',
  cloudflare: 'API Token',
  tencentcloud: 'SecretId',
  huaweicloud: 'Access Key ID',
  godaddy: 'API Key',
  route53: 'Access Key ID'
}
const SECRET_LABELS = {
  dnspod: 'Token',
  tencentcloud: 'SecretKey',
  huaweicloud: 'Secret Access Key',
  godaddy: 'API Secret',
  route53: 'Secret Access Key'
}
const KEY_PLACEHOLDERS = {
  cloudflare: 'cfTokenPlaceholder',
  dnspod: 'dnspodTokenIdPlaceholder',
  tencentcloud: 'tencentSecretIdPlaceholder',
  huaweicloud: 'huaweiAkPlaceholder',
  godaddy: 'godaddyKeyPlaceholder',
  route53: 'route53AkPlaceholder'
}
const SECRET_PLACEHOLDERS = {
  dnspod: 'dnspodTokenPlaceholder',
  tencentcloud: 'tencentSecretKeyPlaceholder',
  huaweicloud: 'huaweiSkPlaceholder',
  godaddy: 'godaddySecretPlaceholder',
  route53: 'route53SkPlaceholder'
}

const emptyForm = () => ({ id: '', type: 'aliyun', remark: '', key: '', secret: '', endpoint: '' })

const testing = ref(false)
const testDomain = ref('')

const {
  formRef,
  dialog,
  open: openDialog,
  submit
} = useCrudDialog({
  emptyForm,
  fromRow: (row) => ({
    id: row.id,
    type: row.type,
    remark: row.remark || '',
    key: '',
    secret: '',
    endpoint: row.endpoint || '',
    keyConfigured: !!row.keyConfigured,
    secretConfigured: !!row.secretConfigured,
    endpointConfigured: !!row.endpoint
  }),
  onSubmit: async (form, d) => {
    const body = {
      type: form.type,
      remark: form.remark,
      key: form.key,
      secret: form.secret,
      endpoint: form.endpoint
    }
    // clearEndpoint 仅编辑接口（PUT）接受；创建接口带此字段会被后端拒绝
    if (d.isEdit) {
      body.clearEndpoint = form.endpointConfigured && !form.endpoint
      await request.put(`/api/providers/${form.id}`, body)
    } else {
      await request.post('/api/providers', body)
    }
  },
  onSaved: () => emit('saved')
})

const keyLabel = computed(() => KEY_LABELS[dialog.form.type] || 'Token ID')
const keyPlaceholder = computed(() => t(`ddns.${KEY_PLACEHOLDERS[dialog.form.type] || 'aliyunKeyIdPlaceholder'}`))
const secretLabel = computed(() => SECRET_LABELS[dialog.form.type] || 'AccessKey Secret')
const secretPlaceholder = computed(() =>
  t(`ddns.${SECRET_PLACEHOLDERS[dialog.form.type] || 'aliyunSecretPlaceholder'}`)
)

const providerRules = computed(() => ({
  type: [{ required: true, message: t('ddns.providerRequired'), trigger: 'change' }],
  key: [
    {
      validator: (_, v, done) =>
        v || (dialog.isEdit && dialog.form.keyConfigured) ? done() : done(new Error(t('ddns.keyRequired'))),
      trigger: 'blur'
    }
  ],
  secret: [
    {
      validator: (_, v, done) =>
        dialog.form.type === 'cloudflare' || v || (dialog.isEdit && dialog.form.secretConfigured)
          ? done()
          : done(new Error(t('ddns.secretRequired'))),
      trigger: 'blur'
    }
  ]
}))

function open(row) {
  testDomain.value = ''
  openDialog(row)
}

async function testProvider() {
  if (!testDomain.value) {
    ElMessage.warning(t('ddns.testDomainRequired'))
    return
  }
  testing.value = true
  try {
    const res = await request.post('/api/providers/test', {
      id: dialog.form.id,
      type: dialog.form.type,
      key: dialog.form.key,
      secret: dialog.form.secret,
      endpoint: dialog.form.endpoint,
      domain: testDomain.value
    })
    ElMessage.success(res.data?.message || t('ddns.credValid'))
  } catch {
    // 拦截器已提示
  } finally {
    testing.value = false
  }
}

defineExpose({ open })
</script>
