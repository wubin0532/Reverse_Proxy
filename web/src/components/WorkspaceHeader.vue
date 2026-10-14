<template>
  <header class="workspace-chrome workspace-header">
    <div class="workspace-top">
      <div class="workspace-heading">
        <span class="workspace-heading-icon"><NavIcon :name="icon" /></span>
        <div>
          <h1 class="workspace-title">{{ title }}</h1>
          <p class="workspace-description">{{ description }}</p>
        </div>
      </div>
      <div v-if="$slots.actions" class="workspace-tools workspace-header-actions"><slot name="actions" /></div>
    </div>
  </header>
</template>
<script setup>
import NavIcon from './NavIcon.vue'
defineProps({
  title: { type: String, required: true },
  description: { type: String, required: true },
  icon: { type: String, required: true }
})
</script>
<style scoped>
.workspace-heading {
  display: flex;
  align-items: center;
  gap: 14px;
  min-width: 0;
  flex: 1;
}
.workspace-heading > div {
  min-width: 0;
}
.workspace-heading-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  border-radius: 12px;
  background: var(--ap-primary-soft);
  color: var(--ap-primary);
}
.workspace-description {
  margin: 5px 0 0;
  color: var(--ap-muted);
  font-size: 13px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.workspace-header-actions {
  justify-content: flex-end;
}
.workspace-header-actions :deep(.el-button) {
  margin-left: 0;
}
@media (max-width: 700px) {
  .workspace-heading {
    align-items: flex-start;
  }
  .workspace-header-actions {
    width: 100%;
    justify-content: flex-start;
    margin-top: 4px;
  }
  .workspace-header-actions :deep(.el-button) {
    flex: 1;
  }
}
</style>
