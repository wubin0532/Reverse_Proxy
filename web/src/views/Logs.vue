<template>
  <div class="page-grid">
    <el-card>
      <template #header><div class="card-header"><div><div>结构化日志</div><small class="muted">当前文件 1 MiB，保留 4 个轮转文件，总上限约 5 MiB</small></div><div class="actions"><el-button :icon="paused ? VideoPlay : VideoPause" @click="paused=!paused">{{ paused?'继续刷新':'暂停刷新' }}</el-button><el-button :icon="Download" @click="download">下载筛选结果</el-button><el-button type="danger" plain :icon="Delete" @click="clearOpen=true">清空</el-button></div></div></template>
      <div class="filters">
        <el-select v-model="filters.level" clearable placeholder="全部级别"><el-option label="Debug" value="debug"/><el-option label="Info" value="info"/><el-option label="Warn" value="warn"/><el-option label="Error" value="error"/></el-select>
        <el-input v-model="filters.source" clearable placeholder="模块，如 ddns / firewall" />
        <el-date-picker v-model="filters.range" type="datetimerange" range-separator="至" start-placeholder="开始时间" end-placeholder="结束时间" />
        <el-input v-model="filters.keyword" clearable placeholder="搜索消息关键字" @keyup.enter="reload" />
        <el-button type="primary" :icon="Search" @click="reload">筛选</el-button>
      </div>
      <el-table :data="entries" v-loading="loading" stripe class="log-table">
        <el-table-column label="时间" width="180"><template #default="{row}">{{ formatTime(row.time) }}</template></el-table-column>
        <el-table-column label="级别" width="90"><template #default="{row}"><el-tag :type="levelType(row.level)" size="small">{{ row.level }}</el-tag></template></el-table-column>
        <el-table-column prop="source" label="模块" width="130" show-overflow-tooltip />
        <el-table-column prop="entityId" label="关联 ID" width="140" show-overflow-tooltip />
        <el-table-column prop="clientIp" label="客户端 IP" width="145" show-overflow-tooltip />
        <el-table-column prop="message" label="消息" min-width="320" show-overflow-tooltip />
        <template #empty><el-empty description="没有符合条件的日志" :image-size="70" /></template>
      </el-table>
      <div class="pager"><span class="muted">本页 {{ entries.length }} 条</span><el-button :disabled="nextCursor<0" @click="loadMore">加载更多</el-button></div>
    </el-card>
    <el-dialog v-model="clearOpen" title="清空全部日志" width="420px"><el-alert type="warning" title="此操作会删除当前及全部轮转日志，无法恢复。" :closable="false"/><el-form label-position="top" class="clear-form"><el-form-item label="请输入当前管理密码"><el-input v-model="password" type="password" show-password @keyup.enter="clearLogs" /></el-form-item></el-form><template #footer><el-button @click="clearOpen=false">取消</el-button><el-button type="danger" :loading="clearing" @click="clearLogs">确认清空</el-button></template></el-dialog>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Delete, Download, Search, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import request from '../api'

const filters=reactive({level:'',source:'',keyword:'',range:null}), entries=ref([]), nextCursor=ref(-1), loading=ref(false), paused=ref(false), clearOpen=ref(false), password=ref(''), clearing=ref(false)
let timer
function params(cursor=0){return {level:filters.level||undefined,source:filters.source||undefined,q:filters.keyword||undefined,from:filters.range?.[0]?.toISOString(),to:filters.range?.[1]?.toISOString(),cursor,limit:100}}
async function reload(){loading.value=true;try{const d=(await request.get('/api/logs',{params:params()})).data||{};entries.value=d.entries||[];nextCursor.value=d.nextCursor??-1}finally{loading.value=false}}
async function loadMore(){if(nextCursor.value<0)return;const d=(await request.get('/api/logs',{params:params(nextCursor.value)})).data||{};entries.value.push(...(d.entries||[]));nextCursor.value=d.nextCursor??-1}
function download(){const q=new URLSearchParams();Object.entries(params()).forEach(([k,v])=>v!==undefined&&q.set(k,v));window.location.href=`/api/logs/download?${q}`}
async function clearLogs(){if(!password.value)return ElMessage.warning('请输入当前管理密码');clearing.value=true;try{await request.post('/api/logs/clear',{password:password.value});clearOpen.value=false;password.value='';ElMessage.success('日志已清空，并写入新的安全审计记录');await reload()}finally{clearing.value=false}}
function formatTime(v){return v?new Date(v).toLocaleString():'-'} function levelType(v){return {error:'danger',warn:'warning',info:'success',debug:'info'}[v]||'info'}
watch(paused,v=>{if(!v)reload()});onMounted(()=>{reload();timer=setInterval(()=>{if(!paused.value)reload()},5000)});onUnmounted(()=>clearInterval(timer))
</script>

<style scoped>
.filters{display:grid;grid-template-columns:140px 180px minmax(300px,1.3fr) minmax(220px,1fr) auto;gap:10px;margin-bottom:16px}.actions{display:flex;gap:8px;flex-wrap:wrap}.pager{display:flex;justify-content:space-between;align-items:center;margin-top:14px}.clear-form{margin-top:16px}@media(max-width:1100px){.filters{grid-template-columns:1fr 1fr}}@media(max-width:850px){.card-header{align-items:flex-start;flex-direction:column}.log-table{width:100%}}@media(max-width:520px){.filters{grid-template-columns:1fr}.actions{width:100%}.actions .el-button{flex:1;margin-left:0}}
</style>
