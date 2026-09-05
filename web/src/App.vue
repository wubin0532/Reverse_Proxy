<template>
  <el-config-provider :locale="elLocale"><router-view /></el-config-provider>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'

const { locale } = useI18n()
const elLocale = computed(() => (locale.value === 'zh-CN' ? zhCn : en))
</script>
