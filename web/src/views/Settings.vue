<template>
  <el-card class="settings-card">
    <template #header>
      <span>修改账号密码</span>
    </template>
    <el-alert
      v-if="auth.needChangePassword"
      type="warning"
      title="正在使用默认账号密码（666 / 666），请立即修改"
      :closable="false"
      class="tip"
    />
    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-width="100px"
      class="settings-form"
    >
      <el-form-item label="账号" prop="username">
        <el-input v-model="form.username" placeholder="登录账号" />
      </el-form-item>
      <el-form-item v-if="!auth.needChangePassword" label="原密码" prop="oldPassword">
        <el-input v-model="form.oldPassword" type="password" show-password placeholder="当前密码" />
      </el-form-item>
      <el-form-item label="新密码" prop="newPassword">
        <el-input v-model="form.newPassword" type="password" show-password placeholder="新密码" />
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirmPassword">
        <el-input v-model="form.confirmPassword" type="password" show-password placeholder="再次输入新密码" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="loading" @click="onSubmit">保存</el-button>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../api'
import { useAuthStore } from '../store/auth'

const router = useRouter()
const auth = useAuthStore()

const formRef = ref()
const loading = ref(false)
const form = reactive({
  username: auth.username,
  oldPassword: '',
  newPassword: '',
  confirmPassword: ''
})

const rules = {
  username: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  oldPassword: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  newPassword: [{ required: true, min: 6, message: '新密码至少 6 位', trigger: 'blur' }],
  confirmPassword: [
    {
      validator: (rule, value, callback) => {
        if (!value) {
          callback(new Error('请再次输入新密码'))
        } else if (value !== form.newPassword) {
          callback(new Error('两次输入的密码不一致'))
        } else {
          callback()
        }
      },
      trigger: 'blur'
    }
  ]
}

async function onSubmit() {
  await formRef.value.validate()
  loading.value = true
  try {
    await request.post('/api/settings/password', {
      username: form.username,
      oldPassword: auth.needChangePassword ? '' : form.oldPassword,
      newPassword: form.newPassword
    })
    auth.username = form.username
    auth.needChangePassword = false
    ElMessage.success('账号密码修改成功')
    form.oldPassword = ''
    form.newPassword = ''
    form.confirmPassword = ''
    router.push('/dashboard')
  } catch {
    // 错误提示已由响应拦截器统一处理
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.settings-card {
  max-width: 560px;
}
.tip {
  margin-bottom: 20px;
}
.settings-form {
  margin-top: 8px;
}
</style>
