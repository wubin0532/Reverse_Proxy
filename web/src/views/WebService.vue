<template>
  <el-card>
    <template #header>
      <div class="card-header">
        <span>Web 服务站点</span>
        <el-button type="primary" size="small" @click="openSiteDialog()">新增站点</el-button>
      </div>
    </template>
    <el-table :data="sites" v-loading="loading">
      <el-table-column prop="name" label="名称" min-width="110" />
      <el-table-column prop="listen" label="监听地址" width="120" />
      <el-table-column label="TLS" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.tls" type="success" size="small">HTTPS</el-tag>
          <el-tag v-else type="info" size="small">HTTP</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="子规则" width="80">
        <template #default="{ row }">{{ (row.rules || []).length }} 条</template>
      </el-table-column>
      <el-table-column label="状态" min-width="140">
        <template #default="{ row }">
          <el-tag :type="siteStatusType(row)" size="small">{{ siteStatusText(row) }}</el-tag>
          <el-tooltip v-if="row.error" :content="row.error" placement="top">
            <span class="error-text">{{ row.error }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleSite(row)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openLogs(row)">日志</el-button>
          <el-button link type="primary" @click="openSiteDialog(row)">编辑</el-button>
          <el-popconfirm title="确定删除该站点？" @confirm="deleteSite(row)">
            <template #reference>
              <el-button link type="danger">删除</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
      <template #empty><el-empty description="暂无站点，点击右上角新增" :image-size="60" /></template>
    </el-table>

    <!-- 站点编辑对话框 -->
    <el-dialog
      v-model="siteDialog.visible"
      :title="siteDialog.isEdit ? '编辑站点' : '新增站点'"
      width="720px"
      destroy-on-close
    >
      <el-form ref="siteFormRef" :model="siteDialog.form" :rules="siteRules" label-width="100px">
        <el-form-item label="站点名称" prop="name">
          <el-input v-model="siteDialog.form.name" placeholder="如 内网服务" />
        </el-form-item>
        <el-form-item label="监听地址" prop="listen">
          <el-input v-model="siteDialog.form.listen" placeholder="如 :8080 或 0.0.0.0:443" style="width: 260px" />
        </el-form-item>
        <el-form-item label="启用 TLS">
          <el-switch v-model="siteDialog.form.tls" />
        </el-form-item>
        <el-form-item v-if="siteDialog.form.tls" label="证书">
          <el-select v-model="siteDialog.form.certId" clearable placeholder="留空使用自签名证书" style="width: 100%">
            <el-option
              v-for="c in certs"
              :key="c.id"
              :label="c.name + '（' + (c.domains || []).join(', ') + '）'"
              :value="c.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="防火墙">
          <el-switch v-model="siteDialog.form.autoFw" />
          <span class="form-tip">仅 OpenWrt 生效：自动在防火墙放行 WAN 侧该端口</span>
        </el-form-item>
      </el-form>

      <div class="rules-header">
        <span class="rules-title">子规则（按顺序匹配）</span>
        <el-button size="small" type="primary" plain @click="openRuleDialog()">添加规则</el-button>
      </div>
      <el-table :data="siteDialog.rules" size="small" border>
        <el-table-column label="#" width="46">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="90">
          <template #default="{ row }">{{ row.name || '-' }}</template>
        </el-table-column>
        <el-table-column label="类型" width="90">
          <template #default="{ row }">
            <el-tag size="small">{{ ruleTypeText(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="匹配" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.frontendHost || '*' }}{{ row.frontendPath || '/' }}
          </template>
        </el-table-column>
        <el-table-column label="目标" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ ruleTarget(row) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150">
          <template #default="{ row, $index }">
            <el-button link type="primary" :disabled="$index === 0" @click="moveRule($index, -1)">上移</el-button>
            <el-button link type="primary" @click="openRuleDialog(row, $index)">编辑</el-button>
            <el-button link type="danger" @click="siteDialog.rules.splice($index, 1)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty><span class="rules-empty">暂无子规则</span></template>
      </el-table>

      <template #footer>
        <el-button @click="siteDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="siteDialog.saving" @click="saveSite">保存</el-button>
      </template>
    </el-dialog>

    <!-- 子规则编辑对话框 -->
    <el-dialog
      v-model="ruleDialog.visible"
      :title="ruleDialog.index >= 0 ? '编辑子规则' : '添加子规则'"
      width="640px"
      append-to-body
      destroy-on-close
    >
      <el-form ref="ruleFormRef" :model="ruleDialog.form" label-width="110px">
        <el-form-item label="规则名称">
          <el-input v-model="ruleDialog.form.name" placeholder="可选" />
        </el-form-item>
        <el-form-item label="类型" required>
          <el-radio-group v-model="ruleDialog.form.type">
            <el-radio-button value="reverse">反向代理</el-radio-button>
            <el-radio-button value="redirect">重定向</el-radio-button>
            <el-radio-button value="fileserver">静态文件</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="前端域名">
          <el-input v-model="ruleDialog.form.frontendHost" placeholder="留空匹配任意域名，如 home.example.com" />
        </el-form-item>
        <el-form-item label="前端路径">
          <el-input v-model="ruleDialog.form.frontendPath" placeholder="路径前缀，留空为 /，如 /api" />
        </el-form-item>

        <template v-if="ruleDialog.form.type === 'reverse'">
          <el-form-item label="后端地址" required>
            <div class="backend-editor">
              <el-input
                v-model="ruleDialog.form.backendsText"
                placeholder="多个用英文逗号分隔，如 http://10.0.0.2:80, http://10.0.0.3:80"
              />
              <div class="backend-test-row">
                <el-button size="small" :loading="backendTesting" @click="testBackend">测试第一个后端</el-button>
                <span v-if="backendTestResult" class="form-tip">{{ backendTestResult }}</span>
              </div>
            </div>
          </el-form-item>
          <el-form-item label="移除路径前缀">
            <el-switch v-model="ruleDialog.form.stripPrefix" />
            <span class="form-tip">例如 /app/api 转发为 /api，并设置 X-Forwarded-Prefix</span>
          </el-form-item>
          <el-form-item label="透传 Host">
            <el-switch v-model="ruleDialog.form.preserveHost" />
            <span class="form-tip">开启后将原始 Host 头传给后端</span>
          </el-form-item>
          <el-form-item label="自动代理头">
            <el-switch v-model="ruleDialog.form.autoProxyHeaders" />
            <span class="form-tip">清除外部伪造值并生成 Host、X-Forwarded-*、X-Real-*</span>
          </el-form-item>
          <el-form-item label="忽略后端 TLS">
            <el-switch v-model="ruleDialog.form.skipBackendTlsVerify" />
            <span class="form-tip">仅当前子规则生效，用于内网自签证书</span>
          </el-form-item>
          <el-form-item label="附加请求头">
            <div class="headers-editor">
              <div v-for="(h, i) in ruleDialog.headersList" :key="i" class="header-row">
                <el-input v-model="h.key" placeholder="Header 名，如 X-Real-IP" style="width: 220px" />
                <el-input v-model="h.value" type="password" show-password :placeholder="h.configured ? '已配置，留空保持不变' : '值（只写）'" style="width: 240px" />
                <el-button link type="danger" @click="ruleDialog.headersList.splice(i, 1)">删除</el-button>
              </div>
              <el-button size="small" plain @click="ruleDialog.headersList.push({ key: '', value: '' })">
                添加请求头
              </el-button>
            </div>
          </el-form-item>

          <el-collapse class="security-collapse">
            <el-collapse-item title="稳定性与公网保护" name="stability">
              <div class="number-grid">
                <el-form-item label="连接超时">
                  <el-input-number v-model="ruleDialog.form.connectTimeoutSeconds" :min="0" :max="30" controls-position="right" />
                </el-form-item>
                <el-form-item label="响应头超时">
                  <el-input-number v-model="ruleDialog.form.responseHeaderTimeoutSeconds" :min="0" :max="600" controls-position="right" />
                </el-form-item>
                <el-form-item label="每 IP 每秒请求">
                  <el-input-number v-model="ruleDialog.form.rateLimitRPS" :min="0" :max="100000" controls-position="right" />
                </el-form-item>
                <el-form-item label="允许突发请求">
                  <el-input-number v-model="ruleDialog.form.rateLimitBurst" :min="0" :max="200000" controls-position="right" />
                </el-form-item>
                <el-form-item label="请求体上限（MiB）">
                  <el-input-number v-model="ruleDialog.form.maxRequestBodyMiB" :min="0" :max="10240" controls-position="right" />
                </el-form-item>
              </div>
              <div class="collapse-tip">超时单位为秒；0 表示保持系统默认或关闭限制。新规则建议连接 5 秒、响应头 60 秒。</div>
            </el-collapse-item>
            <el-collapse-item title="响应重写（可选）" name="response">
              <el-form-item label="修正 Location">
                <el-switch v-model="ruleDialog.form.rewriteLocation" />
                <span class="form-tip">仅改写指向当前后端的绝对重定向地址</span>
              </el-form-item>
              <el-form-item label="Cookie Domain">
                <div class="rewrite-pair"><el-input v-model="ruleDialog.form.cookieDomainFrom" placeholder="原域名" /><span>→</span><el-input v-model="ruleDialog.form.cookieDomainTo" placeholder="新域名，空为删除" /></div>
              </el-form-item>
              <el-form-item label="Cookie Path">
                <div class="rewrite-pair"><el-input v-model="ruleDialog.form.cookiePathFrom" placeholder="原路径，如 /app" /><span>→</span><el-input v-model="ruleDialog.form.cookiePathTo" placeholder="新路径，空为删除" /></div>
              </el-form-item>
            </el-collapse-item>
          </el-collapse>
        </template>

        <template v-if="ruleDialog.form.type === 'redirect'">
          <el-form-item label="目标地址" required>
            <el-input
              v-model="ruleDialog.form.redirectUrl"
              placeholder="如 https://example.com{path}，支持 {path} {query} 占位符"
            />
          </el-form-item>
          <el-form-item label="状态码">
            <el-select v-model="ruleDialog.form.redirectCode" style="width: 200px">
              <el-option :value="301" label="301 永久重定向" />
              <el-option :value="302" label="302 临时重定向" />
              <el-option :value="307" label="307 临时重定向（保持方法）" />
              <el-option :value="308" label="308 永久重定向（保持方法）" />
            </el-select>
          </el-form-item>
        </template>

        <template v-if="ruleDialog.form.type === 'fileserver'">
          <el-form-item label="文件目录" required>
            <el-input v-model="ruleDialog.form.rootDir" placeholder="服务器上的绝对路径，如 /mnt/share/www" />
          </el-form-item>
        </template>

        <el-collapse class="security-collapse">
          <el-collapse-item title="安全选项（Basic 认证 / IP 与 UA 访问控制）" name="sec">
            <el-form-item label="Basic 认证">
              <el-switch v-model="ruleDialog.form.basicAuth" />
            </el-form-item>
            <template v-if="ruleDialog.form.basicAuth">
              <el-form-item label="认证账号">
                <el-input v-model="ruleDialog.form.authUser" style="width: 240px" />
              </el-form-item>
              <el-form-item label="认证密码">
                <el-input v-model="ruleDialog.form.authPass" type="password" show-password :placeholder="ruleDialog.form.authPassConfigured ? '已配置，留空保持不变' : '请输入密码'" style="width: 240px" />
              </el-form-item>
            </template>
            <el-form-item label="IP 访问控制">
              <el-select v-model="ruleDialog.form.ipListMode" style="width: 200px">
                <el-option value="" label="不限制" />
                <el-option value="whitelist" label="白名单（仅允许列表内 IP）" />
                <el-option value="blacklist" label="黑名单（拦截列表内 IP）" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="ruleDialog.form.ipListMode" label="IP 列表">
              <el-input
                v-model="ruleDialog.ipListText"
                type="textarea"
                :rows="2"
                placeholder="英文逗号分隔，支持 CIDR，如 192.168.1.0/24, 10.0.0.5"
              />
            </el-form-item>
            <el-form-item label="UA 访问控制">
              <el-select v-model="ruleDialog.form.uaListMode" style="width: 200px">
                <el-option value="" label="不限制" />
                <el-option value="whitelist" label="白名单（仅允许列表内 UA）" />
                <el-option value="blacklist" label="黑名单（拦截列表内 UA）" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="ruleDialog.form.uaListMode" label="UA 关键字">
              <el-input
                v-model="ruleDialog.uaListText"
                type="textarea"
                :rows="2"
                placeholder="User-Agent 关键字，英文逗号分隔，如 curl, bot"
              />
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="ruleDialog.visible = false">取消</el-button>
        <el-button type="primary" @click="confirmRule">确定</el-button>
      </template>
    </el-dialog>

    <!-- 日志抽屉 -->
    <el-drawer v-model="logsDrawer.visible" :title="`站点日志 - ${logsDrawer.name}`" size="560px">
      <div class="log-toolbar">
        <el-button size="small" @click="loadLogs">刷新</el-button>
      </div>
      <div class="log-list">
        <el-empty v-if="!logsDrawer.logs.length" description="暂无日志" :image-size="60" />
        <div v-for="(line, i) in logsDrawer.logs" :key="i" class="log-line">{{ line }}</div>
      </div>
    </el-drawer>
  </el-card>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import request from '../api'

const sites = ref([])
const certs = ref([])
const loading = ref(false)

function siteStatusText(row) {
  if (!row.enabled) return '已停用'
  const map = { listening: '监听中', error: '错误', stopped: '已停止' }
  return map[row.status] || row.status || '-'
}
function siteStatusType(row) {
  if (!row.enabled) return 'info'
  return row.status === 'listening' ? 'success' : row.status === 'error' ? 'danger' : 'info'
}

const ruleTypeTexts = { reverse: '反向代理', redirect: '重定向', fileserver: '静态文件' }
function ruleTypeText(t) {
  return ruleTypeTexts[t] || t
}
function ruleTarget(rule) {
  if (rule.type === 'reverse') return (rule.backends || []).join(', ')
  if (rule.type === 'redirect') return `${rule.redirectUrl || '-'} (${rule.redirectCode || 302})`
  return rule.rootDir || '-'
}

// ---------- 站点对话框 ----------
const siteFormRef = ref()
const siteDialog = reactive({
  visible: false,
  isEdit: false,
  saving: false,
  form: { id: '', name: '', listen: '', tls: false, certId: '', autoFw: false, enabled: true },
  rules: []
})

const siteRules = {
  name: [{ required: true, message: '请输入站点名称', trigger: 'blur' }],
  listen: [{ required: true, message: '请输入监听地址', trigger: 'blur' }]
}

function openSiteDialog(row) {
  siteDialog.isEdit = !!row
  siteDialog.form = row
    ? { id: row.id, name: row.name, listen: row.listen, tls: row.tls, certId: row.certId || '', autoFw: !!row.autoFw, enabled: row.enabled }
    : { id: '', name: '', listen: '', tls: false, certId: '', autoFw: false, enabled: true }
  siteDialog.rules = row ? JSON.parse(JSON.stringify(row.rules || [])) : []
  siteDialog.visible = true
}

function moveRule(index, delta) {
  const target = index + delta
  if (target < 0 || target >= siteDialog.rules.length) return
  const arr = siteDialog.rules
  ;[arr[index], arr[target]] = [arr[target], arr[index]]
}

async function saveSite() {
  await siteFormRef.value.validate()
  const f = siteDialog.form
  const body = {
    name: f.name,
    enabled: f.enabled,
    listen: f.listen,
    tls: f.tls,
    certId: f.tls ? f.certId : '',
    autoFw: f.autoFw,
    rules: siteDialog.rules
  }
  siteDialog.saving = true
  try {
    if (siteDialog.isEdit) {
      await request.put(`/api/sites/${f.id}`, body)
    } else {
      await request.post('/api/sites', body)
    }
    ElMessage.success('保存成功')
    siteDialog.visible = false
    load()
  } catch {
    // 拦截器已提示
  } finally {
    siteDialog.saving = false
  }
}

async function toggleSite(row) {
  try {
    await request.post(`/api/sites/${row.id}/toggle`)
    load()
  } catch {
    // 拦截器已提示
  }
}

async function deleteSite(row) {
  try {
    await request.delete(`/api/sites/${row.id}`)
    ElMessage.success('已删除')
    load()
  } catch {
    // 拦截器已提示
  }
}

// ---------- 子规则对话框 ----------
const emptyRule = () => ({
  id: '', name: '', type: 'reverse', enabled: true,
  frontendHost: '', frontendPath: '',
  backendsText: '', redirectUrl: '', redirectCode: 302, rootDir: '',
  preserveHost: false, autoProxyHeaders: true, skipBackendTlsVerify: false,
	stripPrefix: false, connectTimeoutSeconds: 5, responseHeaderTimeoutSeconds: 60,
	rateLimitRPS: 0, rateLimitBurst: 0, maxRequestBodyMiB: 0,
	rewriteLocation: false, cookieDomainFrom: '', cookieDomainTo: '', cookiePathFrom: '', cookiePathTo: '',
  basicAuth: false, authUser: '', authPass: '', authPassConfigured: false,
  ipListMode: '', uaListMode: ''
})

const ruleDialog = reactive({
  visible: false,
  index: -1,
  form: emptyRule(),
  headersList: [],
  ipListText: '',
  uaListText: ''
})
const backendTesting = ref(false)
const backendTestResult = ref('')

function openRuleDialog(row, index = -1) {
  ruleDialog.index = index
  ruleDialog.form = row
    ? {
        id: row.id || '',
        name: row.name || '',
        type: row.type || 'reverse',
        enabled: row.enabled !== false,
        frontendHost: row.frontendHost || '',
        frontendPath: row.frontendPath || '',
        backendsText: (row.backends || []).join(', '),
        redirectUrl: row.redirectUrl || '',
        redirectCode: row.redirectCode || 302,
        rootDir: row.rootDir || '',
        preserveHost: !!row.preserveHost,
        autoProxyHeaders: row.autoProxyHeaders !== false,
        skipBackendTlsVerify: !!row.skipBackendTlsVerify,
		stripPrefix: !!row.stripPrefix,
		connectTimeoutSeconds: row.connectTimeoutSeconds ?? 0,
		responseHeaderTimeoutSeconds: row.responseHeaderTimeoutSeconds ?? 0,
		rateLimitRPS: row.rateLimitRPS ?? 0,
		rateLimitBurst: row.rateLimitBurst ?? 0,
		maxRequestBodyMiB: row.maxRequestBodyMiB ?? 0,
		rewriteLocation: !!row.rewriteLocation,
		cookieDomainFrom: row.cookieDomainFrom || '',
		cookieDomainTo: row.cookieDomainTo || '',
		cookiePathFrom: row.cookiePathFrom || '',
		cookiePathTo: row.cookiePathTo || '',
        basicAuth: !!row.basicAuth,
        authUser: row.authUser || '',
        authPass: '',
        authPassConfigured: !!row.basicAuth,
        ipListMode: row.ipListMode || '',
        uaListMode: row.uaListMode || ''
      }
    : emptyRule()
  ruleDialog.headersList = row
    ? Object.entries(row.headers || {}).map(([key]) => ({ key, value: '', configured: true }))
    : []
  ruleDialog.ipListText = row ? (row.ipList || []).join(', ') : ''
  ruleDialog.uaListText = row ? (row.uaList || []).join(', ') : ''
	backendTestResult.value = ''
  ruleDialog.visible = true
}

async function testBackend() {
	const backend = splitList(ruleDialog.form.backendsText)[0]
	if (!backend) {
		ElMessage.warning('请先填写后端地址')
		return
	}
	backendTesting.value = true
	backendTestResult.value = ''
	try {
		const res = await request.post('/api/sites/backend-test', {
			url: backend,
			connectTimeoutSeconds: ruleDialog.form.connectTimeoutSeconds || 5,
			skipBackendTlsVerify: ruleDialog.form.skipBackendTlsVerify
		})
		const data = res.data || {}
		backendTestResult.value = `连接成功 · ${data.latencyMs ?? 0} ms${data.tls ? ' · TLS 正常' : ''}`
		ElMessage.success('后端连接测试通过')
	} catch {
		backendTestResult.value = '连接失败'
	} finally {
		backendTesting.value = false
	}
}

function splitList(text) {
  return (text || '').split(',').map((s) => s.trim()).filter(Boolean)
}

function confirmRule() {
  const f = ruleDialog.form
  if (f.type === 'reverse' && !splitList(f.backendsText).length) {
    ElMessage.warning('请填写后端地址')
    return
  }
  if (f.type === 'redirect' && !f.redirectUrl) {
    ElMessage.warning('请填写目标地址')
    return
  }
  if (f.type === 'fileserver' && !f.rootDir) {
    ElMessage.warning('请填写文件目录')
    return
  }
  const headers = {}
  for (const h of ruleDialog.headersList) {
    if (h.key.trim()) headers[h.key.trim()] = h.value
  }
  const rule = {
    id: f.id,
    name: f.name,
    type: f.type,
    enabled: f.enabled,
    frontendHost: f.frontendHost.trim(),
    frontendPath: f.frontendPath.trim(),
    backends: f.type === 'reverse' ? splitList(f.backendsText) : [],
    redirectUrl: f.type === 'redirect' ? f.redirectUrl.trim() : '',
    redirectCode: f.type === 'redirect' ? f.redirectCode : 0,
    rootDir: f.type === 'fileserver' ? f.rootDir.trim() : '',
    headers,
    preserveHost: f.preserveHost,
    autoProxyHeaders: f.autoProxyHeaders,
    skipBackendTlsVerify: f.skipBackendTlsVerify,
	stripPrefix: f.stripPrefix,
	connectTimeoutSeconds: f.connectTimeoutSeconds || 0,
	responseHeaderTimeoutSeconds: f.responseHeaderTimeoutSeconds || 0,
	rateLimitRPS: f.rateLimitRPS || 0,
	rateLimitBurst: f.rateLimitRPS ? (f.rateLimitBurst || f.rateLimitRPS * 2) : 0,
	maxRequestBodyMiB: f.maxRequestBodyMiB || 0,
	rewriteLocation: f.rewriteLocation,
	cookieDomainFrom: f.cookieDomainFrom.trim(),
	cookieDomainTo: f.cookieDomainTo.trim(),
	cookiePathFrom: f.cookiePathFrom.trim(),
	cookiePathTo: f.cookiePathTo.trim(),
    basicAuth: f.basicAuth,
    authUser: f.basicAuth ? f.authUser : '',
    authPass: f.basicAuth ? f.authPass : '',
    ipListMode: f.ipListMode,
    ipList: f.ipListMode ? splitList(ruleDialog.ipListText) : [],
    uaListMode: f.uaListMode,
    uaList: f.uaListMode ? splitList(ruleDialog.uaListText) : []
  }
  if (ruleDialog.index >= 0) {
    siteDialog.rules[ruleDialog.index] = rule
  } else {
    siteDialog.rules.push(rule)
  }
  ruleDialog.visible = false
}

// ---------- 日志抽屉 ----------
const logsDrawer = reactive({ visible: false, name: '', id: '', logs: [] })

function openLogs(row) {
  logsDrawer.id = row.id
  logsDrawer.name = row.name
  logsDrawer.visible = true
  loadLogs()
}

async function loadLogs() {
  try {
	const res = await request.get('/api/logs', { params: { entityId: logsDrawer.id, limit: 200 } })
	logsDrawer.logs = (res.data?.entries || []).map((entry) => `${new Date(entry.time).toLocaleString()} [${entry.level}] ${entry.message}`)
  } catch {
    logsDrawer.logs = []
  }
}

async function load() {
  loading.value = true
  try {
    const res = await request.get('/api/sites')
    sites.value = res.data || []
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

async function loadCerts() {
  try {
    const res = await request.get('/api/certs')
    certs.value = res.data || []
  } catch {
    // 拦截器已提示
  }
}

onMounted(() => {
  load()
  loadCerts()
})
</script>

<style scoped>
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.error-text {
  margin-left: 6px;
  color: #f56c6c;
  font-size: 12px;
  max-width: 120px;
  display: inline-block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
.rules-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 8px 0 10px;
}
.rules-title {
  font-weight: 600;
  font-size: 14px;
}
.rules-empty {
  color: #999;
  font-size: 13px;
}
.form-tip {
  margin-left: 10px;
  color: #999;
  font-size: 12px;
}
.headers-editor {
  width: 100%;
}
.backend-editor { width: 100%; }
.backend-test-row { display: flex; align-items: center; margin-top: 8px; }
.number-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
.number-grid :deep(.el-input-number) { width: 100%; }
.rewrite-pair { width: 100%; display: grid; grid-template-columns: 1fr auto 1fr; gap: 8px; align-items: center; }
.collapse-tip { margin: -4px 0 12px 110px; color: #8a9698; font-size: 12px; }
@media (max-width: 599px) {
  .number-grid { grid-template-columns: 1fr; }
  .rewrite-pair { grid-template-columns: 1fr; }
  .rewrite-pair > span { display: none; }
  .collapse-tip { margin-left: 0; }
  :deep(.el-form-item) { display: block; }
  :deep(.el-form-item__label) {
    width: 100% !important;
    height: auto;
    justify-content: flex-start;
    margin-bottom: 6px;
  }
  :deep(.el-form-item__content) { margin-left: 0 !important; }
}
.header-row {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
  align-items: center;
}
.security-collapse {
  width: 100%;
  margin-top: 4px;
}
.log-toolbar {
  margin-bottom: 10px;
}
.log-list {
  font-family: Menlo, Consolas, monospace;
  font-size: 12px;
}
.log-line {
  padding: 3px 0;
  border-bottom: 1px solid #f0f0f0;
  word-break: break-all;
  color: #333;
}
</style>
