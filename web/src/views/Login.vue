<template>
  <div class="login-page">
    <el-card class="login-card">
      <div class="brand-mark">A</div>
      <div class="login-title">andey-proxy</div>
      <p class="login-subtitle">安全管理反向代理与网络入口</p>
      <el-alert :type="secure ? 'success' : 'error'" :title="secure ? 'HTTPS 安全连接' : '当前为明文 HTTP 连接'" :closable="false" class="https-state" />
      <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="onSubmit">
        <template v-if="step === 'password'">
          <el-form-item prop="username">
            <el-input v-model="form.username" placeholder="账号" :prefix-icon="User" />
          </el-form-item>
          <el-form-item prop="password">
            <el-input v-model="form.password" type="password" placeholder="密码" show-password :prefix-icon="Lock" />
          </el-form-item>
        </template>
        <template v-else>
          <el-alert type="info" title="密码验证成功，请输入 Google Authenticator 动态验证码。" :closable="false" class="totp-hint" />
          <el-form-item prop="code">
            <el-input v-model="form.code" :placeholder="useRecovery ? '输入一次性恢复码' : '输入 6 位动态验证码'" :inputmode="useRecovery ? 'text' : 'numeric'" autocomplete="one-time-code" :prefix-icon="Key" />
          </el-form-item>
          <div class="login-options">
            <el-button link type="primary" @click="useRecovery = !useRecovery">{{ useRecovery ? '使用动态验证码' : '使用恢复码' }}</el-button>
            <el-button link @click="backToPassword">返回账号密码</el-button>
          </div>
        </template>
        <el-form-item>
          <el-button type="primary" class="login-btn" :loading="loading" @click="onSubmit">
            {{ step === 'password' ? '登 录' : '验证并登录' }}
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock, Key } from '@element-plus/icons-vue'
import { useAuthStore } from '../store/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const formRef = ref()
const loading = ref(false)
const form = reactive({ username: 'admin', password: '', code: '' })
const step = ref('password')
const challengeId = ref('')
const useRecovery = ref(false)
const secure = window.location.protocol === 'https:'

const rules = {
  username: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
  code: [{ validator: (_, value, done) => step.value === 'totp' && !value.trim() ? done(new Error('请输入动态验证码或恢复码')) : done(), trigger: 'blur' }]
}

function backToPassword() {
  step.value = 'password'
  challengeId.value = ''
  form.code = ''
  useRecovery.value = false
}

async function onSubmit() {
  await formRef.value.validate()
  loading.value = true
  try {
	if (step.value === 'password') {
	  const data = await auth.login(form.username, form.password)
	  if (data.twoFactorRequired) {
		challengeId.value = data.challengeId
		step.value = 'totp'
		form.password = ''
		ElMessage.info('请输入双重验证码')
		return
	  }
	} else {
	  await auth.completeTwoFactor(form.username, challengeId.value, form.code.trim())
	}
    ElMessage.success('登录成功')
    if (auth.needChangePassword) {
      ElMessage.warning('请立即修改首次启动生成的一次性密码')
      router.push('/dashboard')
    } else {
      router.push(route.query.redirect || '/dashboard')
    }
  } catch {
    // 错误提示已由响应拦截器统一处理
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  background: radial-gradient(circle at 20% 10%, #1f8f93 0, transparent 35%), linear-gradient(140deg, #082f39 0%, #0a5965 100%);
}
.login-card {
  width: min(420px, 100%);
  padding: 24px 18px;
}
.login-title {
  font-size: 22px;
  font-weight: 600;
  text-align: center;
  margin-top: 12px;
}
.brand-mark { display:grid; place-items:center; width:52px; height:52px; margin:auto; border-radius:15px; color:white; font-size:25px; font-weight:750; background:linear-gradient(145deg,#13aaa5,#087f8c); box-shadow:0 10px 24px rgba(8,127,140,.25); }
.login-subtitle { margin:6px 0 18px; text-align:center; color:var(--ap-muted); }
.https-state { margin-bottom:18px; }
.totp-hint { margin-bottom: 16px; }
.login-options { display:flex; justify-content:space-between; margin:-8px 0 12px; }
.login-btn {
  width: 100%;
}
</style>
