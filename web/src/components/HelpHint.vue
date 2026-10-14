<template>
  <el-tooltip
    :content="text"
    :trigger="['hover', 'focus', 'click']"
    placement="top"
    popper-class="field-help-popper"
    :show-after="150"
    :hide-after="0"
  >
    <button
      type="button"
      class="field-help"
      :aria-label="`${label}${/^[\x00-\x7F]+$/.test(label) ? ' help' : '说明'}`"
      :aria-description="text"
      @click.stop
    >
      ?
    </button>
  </el-tooltip>
</template>
<script setup>
defineProps({ label: { type: String, required: true }, text: { type: String, required: true } })
</script>
<style scoped>
.field-help {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  padding: 0;
  margin-left: 5px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--ap-muted);
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: help;
}
.field-help::before {
  position: absolute;
  width: 16px;
  height: 16px;
  border: 1px solid currentColor;
  border-radius: 50%;
  content: '';
}
.field-help:hover,
.field-help:focus-visible {
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.field-help:focus-visible {
  outline: 2px solid var(--el-color-primary);
  outline-offset: 2px;
}
:global(.field-help-popper) {
  max-width: min(320px, calc(100vw - 32px));
  line-height: 1.65;
  white-space: normal;
  overflow-wrap: anywhere;
}
@media (max-width: 767px), (pointer: coarse) {
  .field-help {
    width: 44px;
    height: 44px;
  }
}
</style>
