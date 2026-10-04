<template>
  <div class="tunnel-page" v-loading="loading">
    <div class="heading">
      <div>
        <h2>{{ $t('tunnel.title') }}</h2>
        <p>{{ $t('tunnel.subtitle') }}</p>
      </div>
      <div class="actions">
        <el-button @click="accountListOpen = true">{{ $t('tunnel.accounts') }}</el-button>
        <el-button @click="refresh">{{ $t('common.refresh') }}</el-button>
        <el-button type="primary" @click="editInstance()">{{ $t('tunnel.addInstance') }}</el-button>
      </div>
    </div>
    <TunnelRuntime
      :runtime="runtime"
      :instances="instances"
      :busy="busy"
      @installed="refresh"
      @busy="runtimeBusy = $event"
    />
    <el-card v-if="operation">
      <div class="heading">
        <b>{{ $t('tunnel.operation') }} · {{ $t(`tunnel.kinds.${operation.kind}`) }}</b
        ><el-tag :type="operation.status === 'failed' ? 'danger' : 'info'">{{ statusText(operation.status) }}</el-tag>
      </div>
      <p>{{ $t(`tunnel.stages.${operation.stage}`) }}</p>
      <el-alert v-if="operation.error" type="error" :title="operation.error" :closable="false" />
      <el-button v-if="operation.status === 'failed' || operation.status === 'interrupted'" @click="retryOperation">{{
        $t('tunnel.retry')
      }}</el-button>
    </el-card>
    <el-card>
      <el-table class="desktop-instances" :data="instances" row-key="id">
        <el-table-column :label="$t('common.name')" min-width="190"
          ><template #default="{ row }"
            ><b>{{ row.name }}</b
            ><small class="id">{{
              row.tunnelId || $t(row.source === 'managed' ? 'tunnel.notCreated' : 'tunnel.noTunnelId')
            }}</small
            ><el-tag size="small">{{ $t(`tunnel.${row.source}`) }}</el-tag></template
          ></el-table-column
        >
        <el-table-column :label="$t('tunnel.process')" min-width="150"
          ><template #default="{ row }"
            ><el-tag :type="row.status.ready ? 'success' : 'info'">{{ statusText(row.status.process) }}</el-tag
            ><small class="id">{{ $t(row.status.ready ? 'tunnel.ready' : 'tunnel.waiting') }}</small
            ><small v-if="row.status.error" class="error">{{ row.status.error }}</small></template
          ></el-table-column
        >
        <el-table-column :label="$t('tunnel.cloudSync')" min-width="120"
          ><template #default="{ row }"
            >{{ statusText(row.status.sync)
            }}<small v-for="message in row.status.targetErrors" :key="message" class="error">{{
              message
            }}</small></template
          ></el-table-column
        >
        <el-table-column :label="$t('common.actions')" min-width="240">
          <template #default="{ row }"
            ><TunnelActions :instance="row" :busy="busy" @control="control" @details="selected = $event" @more="more"
          /></template>
        </el-table-column>
      </el-table>
      <div class="mobile-instances">
        <article v-for="instance in instances" :key="instance.id">
          <b>{{ instance.name }}</b
          ><small class="id">{{ instance.tunnelId || $t('tunnel.notCreated') }}</small>
          <div class="actions">
            <el-tag>{{ statusText(instance.status.process) }}</el-tag
            ><el-tag :type="instance.status.ready ? 'success' : 'warning'">{{
              $t(instance.status.ready ? 'tunnel.ready' : 'tunnel.waiting')
            }}</el-tag
            ><el-tag>{{ statusText(instance.status.sync) }}</el-tag>
          </div>
          <p v-for="error in instance.status.targetErrors" :key="error" class="error">{{ error }}</p>
          <TunnelActions
            :instance="instance"
            :busy="busy"
            @control="control"
            @details="selected = $event"
            @more="more"
          />
        </article>
      </div>
      <el-empty v-if="!instances.length" :description="$t('tunnel.noInstances')" />
    </el-card>
    <el-dialog
      v-model="instanceOpen"
      :title="$t(instanceForm.id ? 'tunnel.editInstance' : 'tunnel.addInstance')"
      width="min(560px, 95vw)"
      @closed="instanceForm.token = ''"
    >
      <el-form label-position="top">
        <el-form-item :label="$t('common.name')"><el-input v-model="instanceForm.name" maxlength="128" /></el-form-item>
        <el-form-item :label="$t('tunnel.source')"
          ><el-radio-group v-model="instanceForm.source" :disabled="!!instanceForm.id"
            ><el-radio value="managed">{{ $t('tunnel.managed') }}</el-radio
            ><el-radio value="imported">{{ $t('tunnel.imported') }}</el-radio></el-radio-group
          ></el-form-item
        >
        <el-form-item :label="$t('tunnel.account')"
          ><el-select
            v-model="instanceForm.accountRef"
            clearable
            :placeholder="$t(instanceForm.source === 'managed' ? 'tunnel.requiredAccount' : 'tunnel.optionalAccount')"
            :disabled="!!instanceForm.id && instanceForm.source === 'managed'"
            ><el-option
              v-for="account in accounts"
              :key="account.id"
              :label="account.name"
              :value="account.id" /></el-select
        ></el-form-item>
        <el-alert
          v-if="instanceForm.source === 'imported'"
          :title="$t('tunnel.importedTip')"
          type="info"
          :closable="false"
        />
        <el-form-item v-if="instanceForm.source === 'imported'" :label="$t('tunnel.tunnelId')"
          ><el-input v-model="instanceForm.tunnelId"
        /></el-form-item>
        <el-form-item v-if="instanceForm.source === 'imported' || instanceForm.id" :label="$t('tunnel.token')"
          ><el-input
            v-model="instanceForm.token"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="instanceForm.tokenConfigured ? $t('common.keepEmpty') : ''"
        /></el-form-item>
        <el-form-item :label="$t('tunnel.protocol')"
          ><el-select v-model="instanceForm.protocol"
            ><el-option
              v-for="value in ['auto', 'http2', 'quic']"
              :key="value"
              :label="value"
              :value="value" /></el-select
        ></el-form-item>
        <el-form-item :label="$t('tunnel.ipVersion')"
          ><el-select v-model="instanceForm.ipVersion"
            ><el-option v-for="value in ['auto', '4', '6']" :key="value" :label="value" :value="value" /></el-select
        ></el-form-item>
      </el-form>
      <template #footer
        ><el-button @click="instanceOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="saving" :disabled="busy" @click="saveInstance">{{
          $t('common.save')
        }}</el-button></template
      >
    </el-dialog>
    <el-dialog v-model="accountListOpen" :title="$t('tunnel.accounts')" width="min(760px, 95vw)">
      <el-button type="primary" @click="editAccount()">{{ $t('tunnel.addAccount') }}</el-button>
      <el-table :data="accounts"
        ><el-table-column prop="name" :label="$t('common.name')" /><el-table-column
          prop="accountId"
          :label="$t('tunnel.accountId')"
        /><el-table-column :label="$t('common.actions')" min-width="220"
          ><template #default="{ row }"
            ><el-button size="small" @click="testAccount(row)">{{ $t('tunnel.test') }}</el-button
            ><el-button size="small" @click="editAccount(row)">{{ $t('common.edit') }}</el-button
            ><el-popconfirm :title="$t('common.delete') + '?'" @confirm="deleteAccount(row)"
              ><template #reference
                ><el-button size="small" type="danger" :disabled="busy">{{ $t('common.delete') }}</el-button></template
              ></el-popconfirm
            ></template
          ></el-table-column
        ></el-table
      >
    </el-dialog>
    <el-dialog
      v-model="accountOpen"
      :title="$t(accountForm.id ? 'tunnel.editAccount' : 'tunnel.addAccount')"
      width="min(560px, 95vw)"
      @closed="accountForm.token = ''"
    >
      <el-alert :title="$t('tunnel.accountTip')" type="info" :closable="false" />
      <el-form label-position="top"
        ><el-form-item :label="$t('common.name')"><el-input v-model="accountForm.name" maxlength="128" /></el-form-item
        ><el-form-item :label="$t('tunnel.accountId')"><el-input v-model="accountForm.accountId" /></el-form-item
        ><el-form-item :label="$t('tunnel.apiToken')"
          ><el-input
            v-model="accountForm.token"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="accountForm.tokenConfigured ? $t('common.keepEmpty') : ''" /></el-form-item
      ></el-form>
      <template #footer
        ><el-button @click="accountOpen = false">{{ $t('common.cancel') }}</el-button
        ><el-button type="primary" :loading="saving" :disabled="busy" @click="saveAccount">{{
          $t('common.save')
        }}</el-button></template
      >
    </el-dialog>
    <TunnelDetails
      v-if="selected"
      :key="selected.id"
      :instance="instances.find((item) => item.id === selected.id) || selected"
      :sites="sites"
      :busy="busy"
      @close="selected = null"
      @edit="editInstance(selected)"
      @saved="refresh"
      @sync="(digest) => runOperation(selected, 'sync', digest)"
    />
  </div>
</template>
<script setup>
import { computed, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useTunnels } from '../composables/useTunnels'
import TunnelDetails from '../components/TunnelDetails.vue'
import TunnelRuntime from '../components/TunnelRuntime.vue'
import TunnelActions from '../components/TunnelActions.vue'
const router = useRouter(),
  { t } = useI18n()
const {
  runtime,
  accounts,
  instances,
  sites,
  loading,
  saving,
  operation,
  instanceOpen,
  accountOpen,
  accountListOpen,
  instanceForm,
  accountForm,
  selected,
  refresh,
  editInstance,
  editAccount,
  saveInstance,
  saveAccount,
  testAccount,
  deleteAccount,
  control,
  removeInstance,
  runOperation,
  retryOperation
} = useTunnels()
const runtimeBusy = ref(false)
const busy = computed(() => runtimeBusy.value || operation.value?.status === 'running')
function statusText(value) {
  return t(`tunnel.status.${value}`)
}
async function more(row, command) {
  if (command === 'edit') return editInstance(row)
  if (command === 'restart') return control(row, 'restart')
  if (command === 'create') return runOperation(row, 'create')
  try {
    await ElMessageBox.confirm(
      t(command === 'delete' ? 'tunnel.deleteCloudConfirm' : 'tunnel.removeConfirm'),
      t(command === 'delete' ? 'tunnel.deleteCloud' : 'tunnel.remove'),
      { type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel') }
    )
    if (command === 'delete') await runOperation(row, 'delete')
    else await removeInstance(row)
  } catch {
    /* Cancel keeps the instance. */
  }
}
</script>
<style scoped>
.tunnel-page {
  display: grid;
  gap: 16px;
}
.heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
h2 {
  margin: 0;
}
p,
small {
  color: var(--el-text-color-secondary);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.actions .el-button {
  margin: 0;
}
.id,
.error {
  display: block;
  margin: 6px 0;
  word-break: break-all;
}
.error {
  color: var(--el-color-danger);
}
.el-select {
  width: 100%;
}
.el-alert {
  margin: 12px 0;
}
.mobile-instances {
  display: none;
}
@media (max-width: 700px) {
  .desktop-instances {
    display: none;
  }
  .mobile-instances {
    display: grid;
    gap: 16px;
  }
  .mobile-instances article {
    padding: 16px 0;
    border-bottom: 1px solid var(--ap-border);
  }
  .mobile-instances article .actions {
    margin: 12px 0;
  }
}
</style>
