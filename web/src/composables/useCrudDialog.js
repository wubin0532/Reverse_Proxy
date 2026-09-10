// CRUD 编辑对话框的通用脚手架：visible/isEdit/saving 状态、open(row?) 打开、
// submit() 校验并提交。请求错误由响应拦截器统一提示，此处只需保持对话框打开。
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

export function useCrudDialog({ emptyForm, fromRow = (row) => ({ ...row }), onSubmit, onSaved }) {
  const { t } = useI18n()
  const formRef = ref()
  const dialog = reactive({ visible: false, isEdit: false, saving: false, form: emptyForm() })

  function open(row) {
    dialog.isEdit = !!row
    dialog.form = row ? fromRow(row) : emptyForm()
    dialog.visible = true
  }

  async function submit() {
    await formRef.value.validate()
    dialog.saving = true
    try {
      const result = await onSubmit(dialog.form, dialog)
      ElMessage.success(t('common.saveSuccess'))
      dialog.visible = false
      onSaved?.(result)
    } catch {
      // 拦截器已提示
    } finally {
      dialog.saving = false
    }
  }

  return { formRef, dialog, open, submit }
}
