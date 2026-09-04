<template>
  <el-container class="layout">
    <el-aside width="220px" class="sidebar">
      <div class="logo">andey-proxy</div>
      <el-menu
        :default-active="activeMenu"
        background-color="#001529"
        text-color="#a6adb4"
        active-text-color="#ffffff"
        router
      >
        <el-menu-item index="/dashboard">
          <el-icon><Odometer /></el-icon>
          <span>概览</span>
        </el-menu-item>
        <el-menu-item index="/ddns">
          <el-icon><Compass /></el-icon>
          <span>动态域名</span>
        </el-menu-item>
        <el-menu-item index="/certs">
          <el-icon><Lock /></el-icon>
          <span>证书管理</span>
        </el-menu-item>
        <el-menu-item index="/web-service">
          <el-icon><Monitor /></el-icon>
          <span>Web服务</span>
        </el-menu-item>
        <el-menu-item index="/forward">
          <el-icon><Connection /></el-icon>
          <span>端口转发</span>
        </el-menu-item>
        <el-menu-item index="/settings">
          <el-icon><Setting /></el-icon>
          <span>系统设置</span>
        </el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="header" height="56px">
        <div class="header-title">{{ pageTitle }}</div>
        <el-dropdown @command="handleCommand">
          <span class="user-info">
            <el-icon><User /></el-icon>
            {{ auth.username }}
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="logout">退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </el-header>
      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Odometer, Compass, Lock, Monitor, Connection, Setting, User, ArrowDown
} from '@element-plus/icons-vue'
import { useAuthStore } from '../store/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const activeMenu = computed(() => route.path)
const pageTitle = computed(() => route.meta.title || '')

async function handleCommand(cmd) {
  if (cmd === 'logout') {
    await auth.logout()
    ElMessage.success('已退出登录')
    router.push('/login')
  }
}
</script>

<style scoped>
.layout {
  height: 100vh;
}
.sidebar {
  background-color: #001529;
}
.logo {
  height: 56px;
  line-height: 56px;
  text-align: center;
  color: #fff;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: 1px;
}
.sidebar :deep(.el-menu) {
  border-right: none;
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #e8e8e8;
}
.header-title {
  font-size: 16px;
  font-weight: 600;
}
.user-info {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  color: #333;
}
.main {
  background: #f0f2f5;
}
</style>
