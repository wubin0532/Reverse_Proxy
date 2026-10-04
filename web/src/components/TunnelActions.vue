<template>
  <div class="actions">
    <el-button size="small" :disabled="busy" @click="$emit('control', instance, instance.enabled ? 'stop' : 'start')">{{
      $t(instance.enabled ? 'tunnel.stop' : 'tunnel.start')
    }}</el-button>
    <el-button size="small" type="primary" plain @click="$emit('details', instance)">{{
      $t('tunnel.details')
    }}</el-button>
    <el-dropdown trigger="click" @command="(command) => $emit('more', instance, command)"
      ><el-button size="small" :disabled="busy">{{ $t('tunnel.more') }}</el-button>
      <template #dropdown
        ><el-dropdown-menu>
          <el-dropdown-item command="edit">{{ $t('common.edit') }}</el-dropdown-item>
          <el-dropdown-item command="restart" :disabled="!instance.enabled">{{
            $t('tunnel.restart')
          }}</el-dropdown-item>
          <el-dropdown-item v-if="instance.source === 'managed' && !instance.tokenConfigured" command="create">{{
            $t('tunnel.create')
          }}</el-dropdown-item>
          <el-dropdown-item divided command="remove">{{ $t('tunnel.remove') }}</el-dropdown-item>
          <el-dropdown-item v-if="instance.source === 'managed' && instance.tunnelId" command="delete">{{
            $t('tunnel.deleteCloud')
          }}</el-dropdown-item>
        </el-dropdown-menu></template
      >
    </el-dropdown>
  </div>
</template>
<script setup>
defineProps({ instance: { type: Object, required: true }, busy: Boolean })
defineEmits(['control', 'details', 'more'])
</script>
<style scoped>
.actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.actions .el-button {
  margin: 0;
}
</style>
