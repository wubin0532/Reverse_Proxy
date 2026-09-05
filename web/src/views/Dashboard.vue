<template>
  <div class="page-grid" v-loading="loading">
    <el-alert v-if="!data.adminHttps" type="error" :title="$t('dashboard.adminHttpAlert')" :closable="false" show-icon />
    <section class="hero">
      <div><span class="eyebrow">{{ $t('dashboard.heroEyebrow') }}</span><h1>{{ data.issues.length ? $t('dashboard.issuesToHandle', { n: data.issues.length }) : $t('dashboard.allOk') }}</h1><p>{{ $t('dashboard.version') }} {{ data.version || '-' }} · {{ data.adminHttps ? $t('dashboard.httpsEnabled') : $t('dashboard.httpCompat') }} · {{ $t('dashboard.lastUpdate') }} {{ updateSummary }}</p></div>
      <HealthBadge :issues="data.issues.length" />
    </section>
    <div v-if="data.issues.length" class="issues">
      <button v-for="item in data.issues" :key="item.module + item.id + item.message" @click="router.push(item.path)"><el-tag type="warning">{{ item.module }}</el-tag><span class="truncate">{{ item.message || $t('dashboard.moduleNotRunning') }}</span><el-icon><ArrowRight /></el-icon></button>
    </div>

    <div class="stats-grid">
      <button v-for="item in cards" :key="item.path" class="stat-card" @click="router.push(item.path)">
        <span class="stat-icon"><el-icon><component :is="item.icon" /></el-icon></span><span><strong>{{ item.value }}</strong><b>{{ item.label }}</b><small>{{ item.sub }}</small></span><el-icon class="stat-arrow"><ArrowRight /></el-icon>
      </button>
    </div>

    <div class="two-columns">
      <el-card>
        <template #header><div class="card-header"><span>{{ $t('dashboard.systemInfo') }}</span><el-button text :icon="Refresh" @click="load">{{ $t('common.refresh') }}</el-button></div></template>
        <div class="info-list"><div><span>{{ $t('dashboard.platform') }}</span><b>{{ sys.goos || '-' }} / {{ sys.goarch || '-' }}</b></div><div><span>{{ $t('dashboard.adminProtocol') }}</span><el-tag :type="data.adminHttps ? 'success':'danger'">{{ data.adminHttps ? 'HTTPS':'HTTP' }}</el-tag></div><div><span>{{ $t('dashboard.accountSecurity') }}</span><el-tag :type="data.mustChangePassword ? 'warning' : data.totpEnabled ? 'success' : 'info'">{{ data.mustChangePassword ? $t('dashboard.needChangePassword') : data.totpEnabled ? $t('dashboard.totpEnabled') : $t('dashboard.passwordOnly') }}</el-tag></div></div>
      </el-card>
      <el-card>
        <template #header><div class="card-header"><span>{{ $t('dashboard.firewallTitle') }}</span><el-tag :type="data.firewall.openwrt ? 'success':'info'">{{ data.firewall.openwrt ? 'OpenWrt':$t('dashboard.nonOpenwrt') }}</el-tag></div></template>
        <div class="big-number">{{ data.firewall.rules.length }}</div><p class="muted">{{ $t('dashboard.firewallRules') }}</p>
        <div class="tag-row"><el-tag v-for="r in data.firewall.rules.slice(0,6)" :key="r.key" effect="plain">{{ r.port }}/{{ r.proto }}</el-tag><el-empty v-if="!data.firewall.rules.length" :description="$t('dashboard.noFirewallRules')" :image-size="48" /></div>
      </el-card>
    </div>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('dashboard.trafficTitle') }}</span><el-tag type="info" effect="plain">{{ $t('dashboard.memoryStats') }}</el-tag></div></template>
      <div v-if="data.sites.length" class="traffic-list">
        <div v-for="s in data.sites" :key="s.id">
          <span class="truncate" :title="s.name">{{ s.name || s.id }}</span>
          <span class="traffic-num">{{ $t('dashboard.requests') }} <b>{{ s.requests }}</b></span>
          <span class="traffic-num">{{ $t('dashboard.trafficIn') }} {{ fmtBytes(s.bytesIn) }}</span>
          <span class="traffic-num">{{ $t('dashboard.trafficOut') }} {{ fmtBytes(s.bytesOut) }}</span>
          <span class="tag-row">
            <el-tag v-if="s.status2xx" type="success" size="small" effect="plain">2xx {{ s.status2xx }}</el-tag>
            <el-tag v-if="s.status3xx" type="info" size="small" effect="plain">3xx {{ s.status3xx }}</el-tag>
            <el-tag v-if="s.status4xx" type="warning" size="small" effect="plain">4xx {{ s.status4xx }}</el-tag>
            <el-tag v-if="s.status5xx" type="danger" size="small" effect="plain">5xx {{ s.status5xx }}</el-tag>
            <el-tag v-if="s.status1xx" size="small" effect="plain">1xx {{ s.status1xx }}</el-tag>
            <span v-if="!s.requests" class="muted">{{ $t('dashboard.noRequests') }}</span>
          </span>
        </div>
      </div>
      <el-empty v-else :description="$t('dashboard.noTraffic')" :image-size="54" />
    </el-card>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('dashboard.recentErrors') }}</span><el-button text @click="router.push('/logs')">{{ $t('dashboard.goLogs') }}</el-button></div></template>
      <div v-if="data.recentErrors.length" class="recent-errors"><div v-for="e in data.recentErrors.slice(0,5)" :key="e.time+e.message"><el-tag type="danger" size="small">{{ e.source }}</el-tag><span class="truncate" :title="e.message">{{ e.message }}</span><time>{{ formatTime(e.time) }}</time></div></div><el-empty v-else :description="$t('dashboard.noErrors')" :image-size="54" />
    </el-card>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('dashboard.notifyTitle') }}</span><el-button text @click="openNotify">{{ $t('dashboard.notifySettings') }}</el-button></div></template>
      <div class="info-list">
        <div><span>Webhook</span><el-tag :type="notifySettings.url ? 'success' : 'info'">{{ notifySettings.url ? $t('common.tagEnabled') : $t('common.notConfigured') }}</el-tag></div>
        <div v-if="notifySettings.url"><span>{{ $t('dashboard.pushUrl') }}</span><b class="truncate" :title="notifySettings.url">{{ notifySettings.url }}</b></div>
        <div><span>{{ $t('dashboard.subscribedEvents') }}</span><b>{{ notifySettings.url ? (notifySettings.types.length ? notifySettings.types.map(typeLabel).join(joinSep) : $t('dashboard.allWarnError')) : '-' }}</b></div>
      </div>
      <div class="update-actions"><el-button type="primary" plain :disabled="!notifySettings.url" :loading="notifyTesting" @click="testNotify">{{ $t('dashboard.sendTest') }}</el-button></div>
    </el-card>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('dashboard.recentEvents') }}</span><el-tag type="info" effect="plain">{{ $t('dashboard.memoryBuffer') }}</el-tag></div></template>
      <div v-if="events.length" class="recent-errors"><div v-for="e in events" :key="e.time+e.type+e.message"><el-tag :type="levelTagType(e.level)" size="small">{{ typeLabel(e.type) }}</el-tag><span class="truncate" :title="e.message">{{ e.message }}</span><time>{{ formatTime(e.time) }}</time></div></div>
      <el-empty v-else :description="$t('dashboard.noEvents')" :image-size="54" />
    </el-card>

    <el-dialog v-model="notifyOpen" :title="$t('dashboard.notify.dialogTitle')" width="480px">
      <el-alert type="info" :title="$t('dashboard.notify.alert')" :closable="false" style="margin-bottom:12px" />
      <el-form label-position="top">
        <el-form-item :label="$t('dashboard.notify.webhookLabel')"><el-input v-model="notifyForm.url" placeholder="https://example.com/webhook" clearable /></el-form-item>
        <el-form-item :label="$t('dashboard.notify.subscribeLabel')">
          <el-checkbox-group v-model="notifyForm.types"><el-checkbox value="cert">{{ $t('dashboard.eventTypes.cert') }}</el-checkbox><el-checkbox value="ddns">{{ $t('dashboard.eventTypes.ddns') }}</el-checkbox><el-checkbox value="site">{{ $t('dashboard.eventTypes.site') }}</el-checkbox><el-checkbox value="forward">{{ $t('dashboard.eventTypes.forward') }}</el-checkbox></el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="notifyOpen=false">{{ $t('common.cancel') }}</el-button><el-button type="primary" :loading="notifySaving" @click="saveNotify">{{ $t('common.save') }}</el-button></template>
    </el-dialog>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('dashboard.update.title') }}</span><el-tag type="success" effect="plain">{{ $t('dashboard.update.noNetwork') }}</el-tag></div></template>
      <el-alert type="info" :title="$t('dashboard.update.alert')" :closable="false" />
      <div class="update-grid">
        <el-upload drag :auto-upload="false" :limit="1" accept=".run" :on-change="onFile" :on-remove="()=>uploadFile=null"><el-icon class="upload-icon"><UploadFilled /></el-icon><div>{{ $t('dashboard.update.uploadHint') }}</div></el-upload>
        <div class="inspection">
          <template v-if="inspection"><div><span>{{ $t('dashboard.update.version') }}</span><b>{{ inspection.version }}</b></div><div><span>{{ $t('dashboard.update.arch') }}</span><b>{{ inspection.goos }}/{{ inspection.goarch }}</b></div><div><span>{{ $t('dashboard.update.size') }}</span><b>{{ formatBytes(inspection.size) }}</b></div><div><span>{{ $t('dashboard.update.signature') }}</span><el-tag type="success">{{ $t('dashboard.update.signatureOk') }}</el-tag></div><div class="digest"><span>SHA256</span><code>{{ inspection.sha256 }}</code></div></template>
          <el-empty v-else :description="$t('dashboard.update.inspectPrompt')" :image-size="54" />
        </div>
      </div>
      <div class="update-actions"><el-button type="primary" :loading="inspecting" :disabled="!uploadFile || !!inspection" @click="inspectPackage">{{ $t('dashboard.update.uploadInspect') }}</el-button><el-button v-if="inspection" type="danger" plain @click="cancelPackage">{{ $t('common.cancel') }}</el-button><el-button v-if="inspection" type="warning" @click="installOpen=true">{{ $t('dashboard.update.confirmInstall') }}</el-button><el-tag v-if="updateStatus.state && updateStatus.state!=='idle'">{{ statusText }}</el-tag></div>
    </el-card>

    <el-card>
      <template #header><div class="card-header"><span>{{ $t('dashboard.backup.title') }}</span><el-tag type="info" effect="plain">{{ $t('dashboard.backup.crossDevice') }}</el-tag></div></template>
      <el-alert type="info" :title="$t('dashboard.backup.alert')" :closable="false" />
      <div class="update-actions"><el-button type="primary" @click="exportOpen=true">{{ $t('dashboard.backup.export') }}</el-button><el-button type="warning" plain @click="importOpen=true">{{ $t('dashboard.backup.import') }}</el-button></div>
    </el-card>

    <el-dialog v-model="exportOpen" :title="$t('dashboard.backup.exportTitle')" width="440px"><el-alert type="warning" :title="$t('dashboard.backup.exportAlert')" :closable="false" style="margin-bottom:12px" /><el-form label-position="top"><el-form-item :label="$t('dashboard.backup.currentAdminPassword')"><el-input v-model="exportForm.password" type="password" show-password /></el-form-item><el-form-item :label="$t('dashboard.backup.backupPassword')"><el-input v-model="exportForm.backupPassword" type="password" show-password /></el-form-item><el-form-item :label="$t('dashboard.backup.confirmBackupPassword')"><el-input v-model="exportForm.confirm" type="password" show-password /></el-form-item></el-form><template #footer><el-button @click="exportOpen=false">{{ $t('common.cancel') }}</el-button><el-button type="primary" :loading="exporting" @click="exportBackup">{{ $t('dashboard.backup.exportDownload') }}</el-button></template></el-dialog>

    <el-dialog v-model="importOpen" :title="$t('dashboard.backup.importTitle')" width="440px"><el-alert type="error" :title="$t('dashboard.backup.importAlert')" :closable="false" style="margin-bottom:12px" /><el-form label-position="top"><el-form-item :label="$t('dashboard.backup.backupFile')"><el-upload :auto-upload="false" :limit="1" accept=".json" :on-change="onBackupFile" :on-remove="()=>{importForm.backup='';importFileName=''}"><el-button>{{ $t('dashboard.backup.chooseFile') }}</el-button><template #tip><span class="muted" style="margin-left:8px">{{ importFileName || 'andey-proxy-backup-*.json' }}</span></template></el-upload></el-form-item><el-form-item :label="$t('dashboard.backup.currentAdminPassword')"><el-input v-model="importForm.password" type="password" show-password /></el-form-item><el-form-item :label="$t('dashboard.backup.backupPasswordLabel')"><el-input v-model="importForm.backupPassword" type="password" show-password /></el-form-item></el-form><template #footer><el-button @click="importOpen=false">{{ $t('common.cancel') }}</el-button><el-button type="danger" :loading="importing" @click="importBackup">{{ $t('dashboard.backup.confirmImport') }}</el-button></template></el-dialog>

    <el-dialog v-model="installOpen" :title="$t('dashboard.update.installTitle')" width="440px"><el-alert v-if="inspection?.downgrade" type="warning" :title="$t('dashboard.update.downgradeAlert')" :closable="false"/><el-form label-position="top"><el-form-item :label="$t('dashboard.backup.currentAdminPassword')"><el-input v-model="installPassword" type="password" show-password /></el-form-item><el-form-item v-if="inspection?.downgrade"><el-checkbox v-model="allowDowngrade">{{ $t('dashboard.update.allowDowngrade') }}</el-checkbox></el-form-item></el-form><template #footer><el-button @click="installOpen=false">{{ $t('common.cancel') }}</el-button><el-button type="warning" :loading="installing" @click="installPackage">{{ $t('dashboard.update.installRestart') }}</el-button></template></el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElNotification } from 'element-plus'
import { ArrowRight, Compass, Connection, Lock, Monitor, Refresh, UploadFilled } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import request from '../api'
import { formatTime, formatBytes as fmtBytes } from '../utils/format'
import HealthBadge from '../components/HealthBadge.vue'

const { t, locale } = useI18n()

const router=useRouter(), loading=ref(false), uploadFile=ref(null), inspecting=ref(false), inspection=ref(null), installOpen=ref(false), installPassword=ref(''), allowDowngrade=ref(false), installing=ref(false)
const exportOpen=ref(false), exporting=ref(false), exportForm=reactive({password:'',backupPassword:'',confirm:''})
const importOpen=ref(false), importing=ref(false), importFileName=ref(''), importForm=reactive({password:'',backupPassword:'',backup:''})
const data=reactive({version:'',adminHttps:true,mustChangePassword:false,totpEnabled:false,stats:{},sites:[],issues:[],firewall:{openwrt:false,rules:[]},recentErrors:[],lastUpdate:{state:'idle'},lastUpdateEntries:[]}), sys=reactive({}), updateStatus=reactive({state:'idle'})
const events=ref([]), notifyOpen=ref(false), notifySaving=ref(false), notifyTesting=ref(false)
const notifySettings=reactive({url:'',types:[]}), notifyForm=reactive({url:'',types:[]})
function typeLabel(v){const k=String(v).split('.')[0];return ['cert','ddns','site','forward'].includes(k)?t(`dashboard.eventTypes.${k}`):v}
const joinSep=computed(()=>locale.value==='zh-CN'?'、':', ')
function levelTagType(l){return l==='error'?'danger':l==='warn'?'warning':'info'}
async function loadNotifySettings(){try{const res=await request.get('/api/notify/settings');notifySettings.url=res.data?.notifyWebhookURL||'';notifySettings.types=res.data?.notifyTypes||[]}catch{}}
function openNotify(){notifyForm.url=notifySettings.url;notifyForm.types=[...notifySettings.types];notifyOpen.value=true}
async function saveNotify(){notifySaving.value=true;try{const res=await request.put('/api/notify/settings',{notifyWebhookURL:notifyForm.url.trim(),notifyTypes:notifyForm.types});notifySettings.url=res.data.notifyWebhookURL||'';notifySettings.types=res.data.notifyTypes||[];notifyOpen.value=false;ElMessage.success(t('dashboard.notify.saved'))}finally{notifySaving.value=false}}
async function testNotify(){notifyTesting.value=true;try{await request.post('/api/notify/test');ElMessage.success(t('dashboard.notify.testSent'))}finally{notifyTesting.value=false}}
const cards=computed(()=>[[data.stats.ddns||0,t('dashboard.cardDdns'),t('dashboard.cardDdnsSub',{n:data.stats.ddnsEnabled||0}),'/ddns',Compass],[data.stats.certs||0,t('dashboard.cardCerts'),t('dashboard.cardCertsSub',{n:data.stats.certsOk||0}),'/certs',Lock],[data.stats.sites||0,t('dashboard.cardSites'),t('dashboard.cardSitesSub',{n:data.stats.sitesListening||0}),'/web-service',Monitor],[data.stats.forwards||0,t('dashboard.cardForwards'),t('dashboard.cardForwardsSub',{n:data.stats.forwardsEnabled||0}),'/forward',Connection]].map(([value,label,sub,path,icon])=>({value,label,sub,path,icon})))
const statusText=computed(()=>{const m={inspecting:t('dashboard.update.status.inspecting'),inspected:t('dashboard.update.status.inspected'),installing:t('dashboard.update.status.installing'),restarting:t('dashboard.update.status.restarting'),done:t('dashboard.update.status.done'),failed:t('dashboard.update.status.failed')};return m[updateStatus.state]||updateStatus.state})
const updateSummary=computed(()=>data.lastUpdateEntries?.[0]?.message||(({idle:t('dashboard.update.summary.idle'),inspecting:t('dashboard.update.summary.inspecting'),inspected:t('dashboard.update.summary.inspected'),installing:t('dashboard.update.summary.installing'),restarting:t('dashboard.update.summary.restarting'),done:t('dashboard.update.summary.done'),failed:t('dashboard.update.summary.failed')})[data.lastUpdate?.state]||data.lastUpdate?.state||t('dashboard.update.summary.idle')))
let refreshTimer, statusTimer
async function load(){loading.value=true;try{const [dash,info,ev]=await Promise.all([request.get('/api/dashboard'),request.get('/api/system/info'),request.get('/api/notify/events?limit=20')]);Object.assign(data,dash.data||{});Object.assign(sys,info.data||{});events.value=ev.data||[]}finally{loading.value=false}}
function onFile(file){uploadFile.value=file.raw;inspection.value=null}
async function inspectPackage(){const form=new FormData();form.append('package',uploadFile.value);inspecting.value=true;try{inspection.value=(await request.post('/api/system/update/inspect',form,{timeout:120000})).data;ElMessage.success(t('dashboard.update.signOk'))}finally{inspecting.value=false}}
async function cancelPackage(){await request.delete(`/api/system/update/${inspection.value.uploadId}`);inspection.value=null;uploadFile.value=null}
async function installPackage(){if(!installPassword.value)return ElMessage.warning(t('dashboard.update.enterAdminPassword'));installing.value=true;try{await request.post(`/api/system/update/${inspection.value.uploadId}/install`,{password:installPassword.value,allowDowngrade:allowDowngrade.value},{timeout:120000});installOpen.value=false;startStatusPoll()}finally{installing.value=false}}
async function pollStatus(){try{const prev=updateStatus.state;Object.assign(updateStatus,(await request.get('/api/system/update/status')).data||{});if(updateStatus.inspection&&!inspection.value)inspection.value=updateStatus.inspection;if(['done','failed','idle'].includes(updateStatus.state)){clearInterval(statusTimer);statusTimer=null;if(prev!==updateStatus.state&&updateStatus.state==='done'){ElNotification.success({title:t('dashboard.update.doneTitle'),message:t('dashboard.update.newVersion',{version:updateStatus.version||inspection.value?.version||'-'})});load()}else if(prev!==updateStatus.state&&updateStatus.state==='failed'){ElNotification.error({title:t('dashboard.update.failedTitle'),message:updateStatus.error||updateStatus.note||t('dashboard.update.installFailedLog')})}}}catch{}}
function startStatusPoll(){clearInterval(statusTimer);pollStatus();statusTimer=setInterval(pollStatus,3000)}
function formatBytes(n){return n<1048576?`${(n/1024).toFixed(1)} KiB`:`${(n/1048576).toFixed(1)} MiB`}
async function exportBackup(){
  if(!exportForm.password)return ElMessage.warning(t('dashboard.update.enterAdminPassword'))
  if(exportForm.backupPassword.length<8)return ElMessage.warning(t('dashboard.backup.passwordTooShort'))
  if(exportForm.backupPassword!==exportForm.confirm)return ElMessage.warning(t('dashboard.backup.passwordMismatch'))
  exporting.value=true
  try{
    const blob=await request.post('/api/system/backup/export',{password:exportForm.password,backupPassword:exportForm.backupPassword},{responseType:'blob',timeout:30000})
    const d=new Date(),p=n=>String(n).padStart(2,'0'),url=URL.createObjectURL(blob),a=document.createElement('a')
    a.href=url;a.download=`andey-proxy-backup-${d.getFullYear()}${p(d.getMonth()+1)}${p(d.getDate())}.json`;a.click();URL.revokeObjectURL(url)
    exportOpen.value=false;Object.assign(exportForm,{password:'',backupPassword:'',confirm:''})
    ElMessage.success(t('dashboard.backup.exported'))
  }catch{}finally{exporting.value=false}
}
function onBackupFile(file){importFileName.value=file.name;file.raw.text().then(t2=>{importForm.backup=t2}).catch(()=>ElMessage.error(t('dashboard.backup.readFailed')))}
async function importBackup(){
  if(!importForm.backup)return ElMessage.warning(t('dashboard.backup.chooseFileFirst'))
  if(!importForm.password||!importForm.backupPassword)return ElMessage.warning(t('dashboard.backup.enterPasswords'))
  importing.value=true
  try{
    await request.post('/api/system/backup/import',{password:importForm.password,backupPassword:importForm.backupPassword,backup:importForm.backup},{timeout:30000})
    importOpen.value=false
    ElMessage.success(t('dashboard.backup.importSuccess'))
    setTimeout(()=>{window.location.href='/login'},1500)
  }catch{}finally{importing.value=false}
}
watch(()=>updateStatus.state,s=>{if(['installing','restarting'].includes(s)&&!statusTimer)startStatusPoll()})
onMounted(()=>{load();loadNotifySettings();refreshTimer=setInterval(load,30000);pollStatus()});onUnmounted(()=>{clearInterval(refreshTimer);clearInterval(statusTimer)})
</script>

<style scoped>
.hero{display:flex;align-items:center;justify-content:space-between;padding:26px 30px;border-radius:18px;color:white;background:linear-gradient(120deg,#0b5360,#07918f);box-shadow:0 16px 38px rgba(8,96,107,.2)}.hero h1{margin:7px 0;font-size:26px}.hero p{margin:0;color:#cce8e8}.eyebrow{font-size:12px;letter-spacing:.15em;text-transform:uppercase}.health-orb{display:grid;place-items:center;width:68px;height:68px;border-radius:50%;background:rgba(255,255,255,.18);font-size:25px;font-weight:800}.health-orb.warning{background:rgba(255,185,83,.25)}.issues{display:grid;gap:8px}.issues button{display:flex;align-items:center;gap:10px;width:100%;padding:10px 12px;border:1px solid #efd8ac;border-radius:10px;background:#fffaf0;color:var(--ap-text);cursor:pointer}.issues button span:nth-child(2){flex:1;text-align:left}.stats-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.stat-card{position:relative;display:flex;align-items:center;gap:14px;padding:20px;border:1px solid var(--ap-border);border-radius:var(--ap-radius);background:white;box-shadow:var(--ap-shadow);color:var(--ap-text);text-align:left;cursor:pointer}.stat-card:hover{transform:translateY(-2px);border-color:#9bcdd0}.stat-icon{display:grid;place-items:center;width:46px;height:46px;border-radius:13px;background:#e9f6f5;color:var(--ap-primary);font-size:21px}.stat-card strong,.stat-card b,.stat-card small{display:block}.stat-card strong{font-size:26px}.stat-card b{margin-top:2px}.stat-card small{margin-top:3px;color:var(--ap-muted)}.stat-arrow{position:absolute;right:14px;color:#aac0c4}.two-columns{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.info-list>div{display:flex;justify-content:space-between;align-items:center;min-height:44px;border-bottom:1px solid #edf2f3}.big-number{font-size:36px;font-weight:750}.tag-row{display:flex;gap:8px;flex-wrap:wrap}.recent-errors>div{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:9px;padding:9px 0;border-bottom:1px solid #edf2f3}.recent-errors time{font-size:11px;color:var(--ap-muted)}.update-grid{display:grid;grid-template-columns:minmax(280px,1fr) minmax(300px,1fr);gap:18px;margin-top:16px}.upload-icon{font-size:40px;color:var(--ap-primary)}.inspection>div{display:grid;grid-template-columns:90px minmax(0,1fr);gap:8px;padding:7px 0}.inspection span{color:var(--ap-muted)}.inspection code{overflow:hidden;text-overflow:ellipsis}.update-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-top:16px}.traffic-list>div{display:grid;grid-template-columns:minmax(0,1.2fr) repeat(3,auto) minmax(0,2fr);align-items:center;gap:14px;padding:10px 0;border-bottom:1px solid #edf2f3}.traffic-list>div:last-child{border-bottom:none}.traffic-num{color:var(--ap-muted)}.traffic-num b{color:var(--ap-text)}@media(max-width:1100px){.stats-grid{grid-template-columns:repeat(2,1fr)}}@media(max-width:760px){.two-columns,.update-grid{grid-template-columns:1fr}.hero{padding:21px}.hero h1{font-size:21px}.health-orb{width:54px;height:54px}.recent-errors>div{grid-template-columns:auto minmax(0,1fr)}.recent-errors time{display:none}.traffic-list>div{grid-template-columns:minmax(0,1fr);gap:6px}}@media(max-width:480px){.stats-grid{grid-template-columns:1fr}}
</style>
