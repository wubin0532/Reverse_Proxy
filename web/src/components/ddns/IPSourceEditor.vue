<template>
  <div class="ip-source-editor">
    <el-form-item :label="zh ? '获取方式' : 'Source type'">
      <template #label
        ><span>{{ zh ? '获取方式' : 'Source type' }}</span
        ><HelpHint
          :label="zh ? '获取方式' : 'Source type'"
          :text="
            zh
              ? '网卡读取本机地址；HTTP(S) 查询外部接口；STUN 查询 NAT 映射地址；DNS 从指定记录提取 IP。此处只获取 IP，未修改云端解析。'
              : 'Interface reads a local address; HTTP(S) queries an endpoint; STUN obtains a NAT-mapped address; DNS extracts an IP from a record. This only obtains an IP, without changing cloud DNS.'
          " /></template
      ><el-select :model-value="modelValue.source" @update:model-value="set('source', $event)"
        ><el-option :label="zh ? '网卡地址' : 'Interface'" value="interface" /><el-option
          label="HTTP(S)"
          value="api" /><el-option label="STUN" value="stun" /><el-option label="DNS" value="dns" /></el-select
    ></el-form-item>
    <el-form-item v-if="modelValue.source === 'interface'" :label="zh ? '网卡' : 'Interface'"
      ><el-select
        :model-value="modelValue.interface"
        filterable
        allow-create
        default-first-option
        @update:model-value="set('interface', $event)"
        ><el-option :label="zh ? '自动识别' : 'Automatic'" value="auto" /><el-option
          v-for="name in interfaces"
          :key="name"
          :label="name"
          :value="name" /></el-select
    ></el-form-item>
    <el-form-item v-else :label="zh ? '查询地址' : 'Query URL'"
      ><el-input
        :model-value="modelValue.apiUrl"
        :placeholder="placeholder"
        @update:model-value="set('apiUrl', $event)"
    /></el-form-item>
    <el-form-item :label="zh ? '失败回退来源' : 'Fallback sources'">
      <template #label
        ><span>{{ zh ? '失败回退来源' : 'Fallback sources' }}</span
        ><HelpHint
          :label="zh ? '失败回退来源' : 'Fallback sources'"
          :text="
            zh
              ? '主来源失败或返回的地址类型不符时，按填写顺序尝试备用来源，最多 3 个，每个最多等待 5 秒。'
              : 'When the primary source fails or returns the wrong IP family, try up to three fallback sources in order, each with a five-second deadline.'
          " /></template
      ><el-input
        :model-value="fallbackText"
        type="textarea"
        :rows="3"
        :placeholder="zh ? '每行一个来源，最多3行；按顺序尝试' : 'One source per line, up to 3; tried in order'"
        @update:model-value="fallbackText = $event"
    /></el-form-item>
    <p class="source-help">
      {{
        zh
          ? '支持 https://、stun://、dns://server/domain/A（IPv6 用 AAAA，也可 TXT）、interface://网卡名。每个来源最多等待5秒；失败或地址族不符时尝试下一项。'
          : 'Supports https://, stun://, dns://server/domain/A (AAAA for IPv6, or TXT), interface://name. Each source waits up to 5 seconds; failure or wrong address family tries the next.'
      }}
    </p>
  </div>
</template>
<script setup>
import { computed } from 'vue'
import HelpHint from '../HelpHint.vue'
import { useI18n } from 'vue-i18n'
const props = defineProps({
  modelValue: { type: Object, required: true },
  interfaces: { type: Array, default: () => [] },
  family: { type: String, default: 'ipv4' }
})
const emit = defineEmits(['update:modelValue'])
const { locale } = useI18n(),
  zh = computed(() => locale.value.startsWith('zh'))
const placeholder = computed(() =>
  props.modelValue.source === 'stun'
    ? 'stun://server:3478'
    : props.modelValue.source === 'dns'
      ? `dns://server/domain/${props.family === 'ipv6' ? 'AAAA' : 'A'}`
      : 'https://…'
)
function set(key, value) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
const fallbackText = computed({
  get: () =>
    (props.modelValue.fallbacks || [])
      .map((s) => (s.source === 'interface' ? `interface://${s.interface || 'auto'}` : s.apiUrl))
      .join('\n'),
  set: (value) => {
    const fallbacks = value
      .split('\n')
      .filter((line) => line.trim())
      .map((line) => {
        const url = line.trim()
        if (url.startsWith('interface://')) return { source: 'interface', interface: url.slice(12) }
        return { source: url.startsWith('stun://') ? 'stun' : url.startsWith('dns://') ? 'dns' : 'api', apiUrl: url }
      })
    set('fallbacks', fallbacks)
  }
})
</script>
<style scoped>
.ip-source-editor {
  min-width: 0;
}
.ip-source-editor :deep(.el-select) {
  width: 100%;
}
.source-help {
  font-size: 12px;
  color: var(--ap-muted);
  margin: 0 0 16px;
  line-height: 1.6;
}
</style>
