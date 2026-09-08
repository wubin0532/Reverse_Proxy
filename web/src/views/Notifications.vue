<template>
  <div class="notify-page" v-loading="loading">
    <section class="notify-hero">
      <div>
        <span class="eyebrow">{{ $t('notifications.eyebrow') }}</span>
        <h1>{{ $t('notifications.title') }}</h1>
        <p>{{ $t('notifications.subtitle') }}</p>
      </div>
      <div class="hero-icon"><el-icon><Bell /></el-icon></div>
    </section>

    <div class="channel-grid">
      <el-card class="channel-card">
        <template #header>
          <div class="card-header">
            <div class="channel-name"><span class="telegram-logo">T</span><div><b>Telegram</b><small>Bot API</small></div></div>
            <el-tag :type="telegramActive ? 'success' : 'info'">{{ telegramActive ? $t('common.tagEnabled') : $t('common.tagDisabled') }}</el-tag>
          </div>
        </template>
        <p class="channel-desc">{{ $t('notifications.telegram.description') }}</p>
        <div class="channel-details">
          <div><span>{{ $t('notifications.telegram.botToken') }}</span><b>{{ settings.telegram.botTokenConfigured ? $t('common.keyConfigured') : $t('common.notConfigured') }}</b></div>
          <div><span>Chat ID</span><b>{{ settings.telegram.chatId || '-' }}</b></div>
          <div><span>{{ $t('notifications.telegram.topic') }}</span><b>{{ settings.telegram.messageThreadId || '-' }}</b></div>
        </div>
        <div class="actions">
          <el-button type="primary" @click="openTelegram">{{ $t('notifications.configure') }}</el-button>
          <el-button :disabled="!telegramActive" :loading="testing" @click="testTelegram">{{ $t('notifications.sendTest') }}</el-button>
          <el-popconfirm
            v-if="settings.telegram.configured"
            :title="$t('notifications.deleteConfirm')"
            :confirm-button-text="$t('notifications.deleteChannel')"
            :cancel-button-text="$t('common.cancel')"
            confirm-button-type="danger"
            @confirm="deleteTelegram"
          >
            <template #reference><el-button type="danger" plain :loading="deleting">{{ $t('notifications.deleteChannel') }}</el-button></template>
          </el-popconfirm>
        </div>
      </el-card>

      <el-card>
        <template #header><div class="card-header"><span>{{ $t('notifications.subscription.title') }}</span><el-tag type="info" effect="plain">{{ subscriptionSummary }}</el-tag></div></template>
        <p class="channel-desc">{{ $t('notifications.subscription.description') }}</p>
        <div class="event-tags">
          <el-tag v-for="type in displayedTypes" :key="type" effect="plain">{{ typeLabel(type) }}</el-tag>
        </div>
      </el-card>
    </div>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('notifications.events.title') }}</span><el-button text :icon="Refresh" @click="loadEvents">{{ $t('common.refresh') }}</el-button></div></template>
      <div v-if="events.length" class="event-list">
        <div v-for="event in events" :key="event.time+event.type+event.message">
          <el-tag :type="levelTagType(event.level)" size="small">{{ typeLabel(event.type) }}</el-tag>
          <div><b>{{ event.message }}</b><small v-if="event.entity">{{ event.entity }}</small></div>
          <time>{{ formatTime(event.time) }}</time>
        </div>
      </div>
      <el-empty v-else :description="$t('notifications.events.empty')" :image-size="58" />
    </el-card>

    <el-dialog v-model="dialogOpen" :title="$t('notifications.telegram.dialogTitle')" width="520px">
      <el-alert type="info" :title="$t('notifications.telegram.guide')" :closable="false" />
      <el-form label-position="top" class="notify-form">
        <el-form-item :label="$t('common.enabled')"><el-switch v-model="form.telegram.enabled" /></el-form-item>
        <el-form-item :label="$t('notifications.telegram.botToken')">
          <el-input v-model="form.telegram.botToken" type="password" show-password autocomplete="new-password" :placeholder="settings.telegram.botTokenConfigured ? $t('common.keepEmpty') : '123456789:AA...'" />
        </el-form-item>
        <el-form-item v-if="settings.telegram.botTokenConfigured">
          <el-checkbox v-model="form.telegram.clearBotToken">{{ $t('notifications.telegram.clearToken') }}</el-checkbox>
        </el-form-item>
        <el-form-item label="Chat ID"><el-input v-model="form.telegram.chatId" placeholder="123456789 / -100... / @channel" /></el-form-item>
        <el-form-item :label="$t('notifications.telegram.topic')"><el-input-number v-model="form.telegram.messageThreadId" :min="0" :controls="false" /></el-form-item>
        <el-form-item :label="$t('notifications.subscription.selectLabel')">
          <el-checkbox-group v-model="form.types">
            <el-checkbox value="cert">{{ $t('notifications.eventTypes.cert') }}</el-checkbox>
            <el-checkbox value="ddns">{{ $t('notifications.eventTypes.ddns') }}</el-checkbox>
            <el-checkbox value="site">{{ $t('notifications.eventTypes.site') }}</el-checkbox>
            <el-checkbox value="forward">{{ $t('notifications.eventTypes.forward') }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="dialogOpen=false">{{ $t('common.cancel') }}</el-button><el-button type="primary" :loading="saving" @click="save">{{ $t('common.save') }}</el-button></template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { Bell, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime } from '../utils/format'

const { t } = useI18n()
const loading=ref(false), saving=ref(false), testing=ref(false), deleting=ref(false), dialogOpen=ref(false), events=ref([])
const settings=reactive({types:[],telegram:{enabled:false,configured:false,botTokenConfigured:false,chatId:'',messageThreadId:0}})
const form=reactive({types:[],telegram:{enabled:false,botToken:'',clearBotToken:false,chatId:'',messageThreadId:0}})
const allTypes=['cert','ddns','site','forward']
const telegramActive=computed(()=>settings.telegram.enabled&&settings.telegram.configured)
const displayedTypes=computed(()=>settings.types.length?settings.types:allTypes)
const subscriptionSummary=computed(()=>settings.types.length?t('notifications.subscription.selected',{n:settings.types.length}):t('notifications.subscription.allWarnError'))
function typeLabel(value){const key=String(value).split('.')[0];return allTypes.includes(key)?t(`notifications.eventTypes.${key}`):value}
function levelTagType(level){return level==='error'?'danger':level==='warn'?'warning':'info'}
function applySettings(payload={}){settings.types=Array.isArray(payload.types)?payload.types:[];Object.assign(settings.telegram,payload.telegram||{})}
async function loadSettings(){const res=await request.get('/api/notifications/settings');applySettings(res.data)}
async function loadEvents(){const res=await request.get('/api/notifications/events?limit=30');events.value=res.data||[]}
async function load(){loading.value=true;try{await Promise.all([loadSettings(),loadEvents()])}finally{loading.value=false}}
function openTelegram(){
  form.types=[...settings.types]
  Object.assign(form.telegram,{enabled:settings.telegram.enabled,botToken:'',clearBotToken:false,chatId:settings.telegram.chatId||'',messageThreadId:settings.telegram.messageThreadId||0})
  dialogOpen.value=true
}
async function save(){
  saving.value=true
  try{
    const res=await request.put('/api/notifications/settings',{types:form.types,telegram:{...form.telegram,messageThreadId:Number(form.telegram.messageThreadId)||0}})
    applySettings(res.data)
    dialogOpen.value=false
    ElMessage.success(t('notifications.saved'))
  }finally{saving.value=false}
}
async function testTelegram(){testing.value=true;try{await request.post('/api/notifications/test/telegram');ElMessage.success(t('notifications.testSent'))}finally{testing.value=false}}
async function deleteTelegram(){deleting.value=true;try{const res=await request.delete('/api/notifications/channels/telegram');applySettings(res.data);ElMessage.success(t('notifications.deleted'))}finally{deleting.value=false}}
let timer
onMounted(()=>{load();timer=setInterval(loadEvents,30000)})
onUnmounted(()=>clearInterval(timer))
</script>

<style scoped>
.notify-page{display:grid;gap:18px}.notify-hero{display:flex;align-items:center;justify-content:space-between;padding:26px 30px;border-radius:18px;color:white;background:linear-gradient(125deg,#173d64,#1686aa);box-shadow:0 16px 38px rgba(17,82,115,.2)}.notify-hero h1{margin:6px 0;font-size:26px}.notify-hero p{margin:0;color:#d8eef6}.eyebrow{font-size:12px;letter-spacing:.15em}.hero-icon{display:grid;place-items:center;width:68px;height:68px;border-radius:20px;background:rgba(255,255,255,.16);font-size:30px}.channel-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.card-header,.channel-name,.actions{display:flex;align-items:center}.card-header{justify-content:space-between;gap:12px}.channel-name{gap:12px}.channel-name small,.channel-name b{display:block}.channel-name small{margin-top:2px;color:var(--ap-muted)}.telegram-logo{display:grid;place-items:center;width:42px;height:42px;border-radius:13px;background:#229ed9;color:white;font-weight:800;font-size:20px}.channel-desc{margin:0 0 16px;color:var(--ap-muted);line-height:1.7}.channel-details>div{display:flex;justify-content:space-between;gap:14px;padding:9px 0;border-bottom:1px solid #edf2f3}.channel-details span{color:var(--ap-muted)}.channel-details b{max-width:65%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.actions{gap:10px;margin-top:18px}.event-tags{display:flex;gap:9px;flex-wrap:wrap}.event-list>div{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:12px;padding:11px 0;border-bottom:1px solid #edf2f3}.event-list b,.event-list small{display:block}.event-list small,.event-list time{margin-top:3px;color:var(--ap-muted);font-size:12px}.notify-form{margin-top:16px}.notify-form :deep(.el-input-number){width:100%}.notify-form :deep(.el-input-number .el-input__inner){text-align:left}@media(max-width:800px){.channel-grid{grid-template-columns:1fr}.notify-hero{padding:22px}.event-list>div{grid-template-columns:auto minmax(0,1fr)}.event-list time{display:none}}@media(max-width:480px){.hero-icon{display:none}}
</style>
