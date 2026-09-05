<template>
  <div class="page-grid" v-loading="loading">
    <el-alert v-if="!data.adminHttps" type="error" title="管理后台正在使用明文 HTTP，请关闭兼容开关并改用 HTTPS。" :closable="false" show-icon />
    <section class="hero">
      <div><span class="eyebrow">系统健康</span><h1>{{ data.issues.length ? `${data.issues.length} 项需要处理` : '所有核心服务运行正常' }}</h1><p>版本 {{ data.version || '-' }} · {{ data.adminHttps ? 'HTTPS 管理已启用' : 'HTTP 兼容模式' }} · 最近更新 {{ updateSummary }}</p></div>
      <HealthBadge :issues="data.issues.length" />
    </section>
    <div v-if="data.issues.length" class="issues">
      <button v-for="item in data.issues" :key="item.module + item.id + item.message" @click="router.push(item.path)"><el-tag type="warning">{{ item.module }}</el-tag><span class="truncate">{{ item.message || '模块未正常运行' }}</span><el-icon><ArrowRight /></el-icon></button>
    </div>

    <div class="stats-grid">
      <button v-for="item in cards" :key="item.path" class="stat-card" @click="router.push(item.path)">
        <span class="stat-icon"><el-icon><component :is="item.icon" /></el-icon></span><span><strong>{{ item.value }}</strong><b>{{ item.label }}</b><small>{{ item.sub }}</small></span><el-icon class="stat-arrow"><ArrowRight /></el-icon>
      </button>
    </div>

    <div class="two-columns">
      <el-card>
        <template #header><div class="card-header"><span>系统运行信息</span><el-button text :icon="Refresh" @click="load">刷新</el-button></div></template>
        <div class="info-list"><div><span>平台架构</span><b>{{ sys.goos || '-' }} / {{ sys.goarch || '-' }}</b></div><div><span>管理协议</span><el-tag :type="data.adminHttps ? 'success':'danger'">{{ data.adminHttps ? 'HTTPS':'HTTP' }}</el-tag></div><div><span>账户安全</span><el-tag :type="data.mustChangePassword ? 'warning' : data.totpEnabled ? 'success' : 'info'">{{ data.mustChangePassword ? '需要修改密码' : data.totpEnabled ? '双重验证已启用' : '仅密码' }}</el-tag></div></div>
      </el-card>
      <el-card>
        <template #header><div class="card-header"><span>防火墙自动放行</span><el-tag :type="data.firewall.openwrt ? 'success':'info'">{{ data.firewall.openwrt ? 'OpenWrt':'非 OpenWrt' }}</el-tag></div></template>
        <div class="big-number">{{ data.firewall.rules.length }}</div><p class="muted">当前由程序维护的 WAN 放行规则</p>
        <div class="tag-row"><el-tag v-for="r in data.firewall.rules.slice(0,6)" :key="r.key" effect="plain">{{ r.port }}/{{ r.proto }}</el-tag><el-empty v-if="!data.firewall.rules.length" description="暂无自动放行规则" :image-size="48" /></div>
      </el-card>
    </div>

    <el-card>
      <template #header><div class="card-header"><span>最近错误</span><el-button text @click="router.push('/logs')">进入日志中心</el-button></div></template>
      <div v-if="data.recentErrors.length" class="recent-errors"><div v-for="e in data.recentErrors.slice(0,5)" :key="e.time+e.message"><el-tag type="danger" size="small">{{ e.source }}</el-tag><span class="truncate" :title="e.message">{{ e.message }}</span><time>{{ formatTime(e.time) }}</time></div></div><el-empty v-else description="最近没有错误" :image-size="54" />
    </el-card>

    <el-card>
      <template #header><div class="card-header"><span>上传包手动更新</span><el-tag type="success" effect="plain">不会主动联网</el-tag></div></template>
      <el-alert type="info" title="仅接受不超过 100 MiB、由项目 Ed25519 私钥签名且与当前架构匹配的 .run 包。包内脚本不会被执行。" :closable="false" />
      <div class="update-grid">
        <el-upload drag :auto-upload="false" :limit="1" accept=".run" :on-change="onFile" :on-remove="()=>uploadFile=null"><el-icon class="upload-icon"><UploadFilled /></el-icon><div>拖入签名 .run 包，或点击选择</div></el-upload>
        <div class="inspection">
          <template v-if="inspection"><div><span>版本</span><b>{{ inspection.version }}</b></div><div><span>架构</span><b>{{ inspection.goos }}/{{ inspection.goarch }}</b></div><div><span>大小</span><b>{{ formatBytes(inspection.size) }}</b></div><div><span>签名</span><el-tag type="success">验证通过</el-tag></div><div class="digest"><span>SHA256</span><code>{{ inspection.sha256 }}</code></div></template>
          <el-empty v-else description="上传检查后显示签名与兼容性结果" :image-size="54" />
        </div>
      </div>
      <div class="update-actions"><el-button type="primary" :loading="inspecting" :disabled="!uploadFile || !!inspection" @click="inspectPackage">上传并检查</el-button><el-button v-if="inspection" type="danger" plain @click="cancelPackage">取消</el-button><el-button v-if="inspection" type="warning" @click="installOpen=true">确认安装</el-button><el-tag v-if="updateStatus.state && updateStatus.state!=='idle'">{{ statusText }}</el-tag></div>
    </el-card>

    <el-dialog v-model="installOpen" title="确认安装更新" width="440px"><el-alert v-if="inspection?.downgrade" type="warning" title="这是降级包，需要明确允许降级。" :closable="false"/><el-form label-position="top"><el-form-item label="当前管理密码"><el-input v-model="installPassword" type="password" show-password /></el-form-item><el-form-item v-if="inspection?.downgrade"><el-checkbox v-model="allowDowngrade">我了解风险并允许降级</el-checkbox></el-form-item></el-form><template #footer><el-button @click="installOpen=false">取消</el-button><el-button type="warning" :loading="installing" @click="installPackage">安装并重启</el-button></template></el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowRight, Compass, Connection, Lock, Monitor, Refresh, UploadFilled } from '@element-plus/icons-vue'
import request from '../api'
import HealthBadge from '../components/HealthBadge.vue'

const router=useRouter(), loading=ref(false), uploadFile=ref(null), inspecting=ref(false), inspection=ref(null), installOpen=ref(false), installPassword=ref(''), allowDowngrade=ref(false), installing=ref(false)
const data=reactive({version:'',adminHttps:true,mustChangePassword:false,totpEnabled:false,stats:{},issues:[],firewall:{openwrt:false,rules:[]},recentErrors:[],lastUpdate:{state:'idle'},lastUpdateEntries:[]}), sys=reactive({}), updateStatus=reactive({state:'idle'})
const cards=computed(()=>[[data.stats.ddns||0,'DDNS 任务',`启用 ${data.stats.ddnsEnabled||0} 个`,'/ddns',Compass],[data.stats.certs||0,'证书',`有效 ${data.stats.certsOk||0} 张`,'/certs',Lock],[data.stats.sites||0,'Web 站点',`监听 ${data.stats.sitesListening||0} 个`,'/web-service',Monitor],[data.stats.forwards||0,'转发规则',`启用 ${data.stats.forwardsEnabled||0} 条`,'/forward',Connection]].map(([value,label,sub,path,icon])=>({value,label,sub,path,icon})))
const statusText=computed(()=>({inspecting:'正在检查',inspected:'已检查，等待确认',installing:'正在安装',restarting:'正在重启',done:'更新完成',failed:'更新失败'}[updateStatus.state]||updateStatus.state))
const updateSummary=computed(()=>data.lastUpdateEntries?.[0]?.message||({idle:'无记录',inspecting:'检查中',inspected:'已检查待安装',installing:'安装中',restarting:'重启中',done:'成功',failed:'失败'}[data.lastUpdate?.state]||data.lastUpdate?.state||'无记录'))
let refreshTimer, statusTimer
async function load(){loading.value=true;try{const [dash,info]=await Promise.all([request.get('/api/dashboard'),request.get('/api/system/info')]);Object.assign(data,dash.data||{});Object.assign(sys,info.data||{})}finally{loading.value=false}}
function onFile(file){uploadFile.value=file.raw;inspection.value=null}
async function inspectPackage(){const form=new FormData();form.append('package',uploadFile.value);inspecting.value=true;try{inspection.value=(await request.post('/api/system/update/inspect',form,{timeout:120000})).data;ElMessage.success('签名和兼容性验证通过')}finally{inspecting.value=false}}
async function cancelPackage(){await request.delete(`/api/system/update/${inspection.value.uploadId}`);inspection.value=null;uploadFile.value=null}
async function installPackage(){if(!installPassword.value)return ElMessage.warning('请输入当前管理密码');installing.value=true;try{await request.post(`/api/system/update/${inspection.value.uploadId}/install`,{password:installPassword.value,allowDowngrade:allowDowngrade.value},{timeout:120000});installOpen.value=false;startStatusPoll()}finally{installing.value=false}}
async function pollStatus(){try{Object.assign(updateStatus,(await request.get('/api/system/update/status')).data||{});if(updateStatus.inspection&&!inspection.value)inspection.value=updateStatus.inspection;if(['done','failed','idle'].includes(updateStatus.state)){clearInterval(statusTimer);statusTimer=null}}catch{}}
function startStatusPoll(){clearInterval(statusTimer);pollStatus();statusTimer=setInterval(pollStatus,3000)}
function formatTime(v){return v?new Date(v).toLocaleString():'-'} function formatBytes(n){return n<1048576?`${(n/1024).toFixed(1)} KiB`:`${(n/1048576).toFixed(1)} MiB`}
watch(()=>updateStatus.state,s=>{if(['installing','restarting'].includes(s)&&!statusTimer)startStatusPoll()})
onMounted(()=>{load();refreshTimer=setInterval(load,30000);pollStatus()});onUnmounted(()=>{clearInterval(refreshTimer);clearInterval(statusTimer)})
</script>

<style scoped>
.hero{display:flex;align-items:center;justify-content:space-between;padding:26px 30px;border-radius:18px;color:white;background:linear-gradient(120deg,#0b5360,#07918f);box-shadow:0 16px 38px rgba(8,96,107,.2)}.hero h1{margin:7px 0;font-size:26px}.hero p{margin:0;color:#cce8e8}.eyebrow{font-size:12px;letter-spacing:.15em;text-transform:uppercase}.health-orb{display:grid;place-items:center;width:68px;height:68px;border-radius:50%;background:rgba(255,255,255,.18);font-size:25px;font-weight:800}.health-orb.warning{background:rgba(255,185,83,.25)}.issues{display:grid;gap:8px}.issues button{display:flex;align-items:center;gap:10px;width:100%;padding:10px 12px;border:1px solid #efd8ac;border-radius:10px;background:#fffaf0;color:var(--ap-text);cursor:pointer}.issues button span:nth-child(2){flex:1;text-align:left}.stats-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.stat-card{position:relative;display:flex;align-items:center;gap:14px;padding:20px;border:1px solid var(--ap-border);border-radius:var(--ap-radius);background:white;box-shadow:var(--ap-shadow);color:var(--ap-text);text-align:left;cursor:pointer}.stat-card:hover{transform:translateY(-2px);border-color:#9bcdd0}.stat-icon{display:grid;place-items:center;width:46px;height:46px;border-radius:13px;background:#e9f6f5;color:var(--ap-primary);font-size:21px}.stat-card strong,.stat-card b,.stat-card small{display:block}.stat-card strong{font-size:26px}.stat-card b{margin-top:2px}.stat-card small{margin-top:3px;color:var(--ap-muted)}.stat-arrow{position:absolute;right:14px;color:#aac0c4}.two-columns{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.info-list>div{display:flex;justify-content:space-between;align-items:center;min-height:44px;border-bottom:1px solid #edf2f3}.big-number{font-size:36px;font-weight:750}.tag-row{display:flex;gap:8px;flex-wrap:wrap}.recent-errors>div{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:9px;padding:9px 0;border-bottom:1px solid #edf2f3}.recent-errors time{font-size:11px;color:var(--ap-muted)}.update-grid{display:grid;grid-template-columns:minmax(280px,1fr) minmax(300px,1fr);gap:18px;margin-top:16px}.upload-icon{font-size:40px;color:var(--ap-primary)}.inspection>div{display:grid;grid-template-columns:90px minmax(0,1fr);gap:8px;padding:7px 0}.inspection span{color:var(--ap-muted)}.inspection code{overflow:hidden;text-overflow:ellipsis}.update-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-top:16px}@media(max-width:1100px){.stats-grid{grid-template-columns:repeat(2,1fr)}}@media(max-width:760px){.two-columns,.update-grid{grid-template-columns:1fr}.hero{padding:21px}.hero h1{font-size:21px}.health-orb{width:54px;height:54px}.recent-errors>div{grid-template-columns:auto minmax(0,1fr)}.recent-errors time{display:none}}@media(max-width:480px){.stats-grid{grid-template-columns:1fr}}
</style>
