<template>
  <!-- 站点编辑对话框 -->
  <el-dialog
    v-model="dialog.visible"
    :title="dialog.isEdit ? $t('webService.editSite') : $t('webService.addSiteTitle')"
    width="620px"
    destroy-on-close
  >
    <el-form ref="formRef" :model="dialog.form" :rules="siteRules" label-width="100px">
      <el-form-item :label="$t('webService.siteName')" prop="name">
        <el-input v-model="dialog.form.name" :placeholder="$t('webService.siteNamePlaceholder')" />
      </el-form-item>
      <el-form-item :label="$t('webService.colListen')" prop="listen">
        <el-input v-model="dialog.form.listen" :placeholder="$t('webService.listenPlaceholder')" style="width: 260px" />
      </el-form-item>
      <el-form-item :label="$t('webService.enableTls')">
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
        <el-switch v-model="dialog.form.forceHttps" :disabled="!dialog.form.certId" />
        <span class="form-tip">
          {{ dialog.form.certId ? $t('webService.forceHttpsTipOn') : $t('webService.forceHttpsTipOff') }}
        </span>
      </el-form-item>
      <el-form-item :label="$t('webService.firewall')">
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
import { listenValidator } from '../../utils/validate'
import { useCrudDialog } from '../../composables/useCrudDialog'

defineProps({ certs: { type: Array, default: () => [] } })
const emit = defineEmits(['saved'])

const { t } = useI18n()

const emptyForm = () => ({
  id: '',
  name: '',
  listen: '',
  tls: false,
  certId: '',
  forceHttps: false,
  autoFw: false,
  enabled: true,
  rules: []
})

const siteRules = computed(() => ({
  name: [{ required: true, message: t('webService.nameRequired'), trigger: 'blur' }],
  listen: [
    { required: true, message: t('webService.listenRequired'), trigger: 'blur' },
    { validator: listenValidator(t), trigger: 'blur' }
  ]
}))

const { formRef, dialog, open, submit } = useCrudDialog({
  emptyForm,
  fromRow: (row) => ({
    id: row.id,
    name: row.name,
    listen: row.listen,
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
      enabled: form.enabled,
      listen: form.listen,
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
.form-tip {
  margin-left: 10px;
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
