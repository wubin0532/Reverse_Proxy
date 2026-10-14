<template>
  <!-- 子规则编辑对话框 -->
  <el-dialog
    class="workspace-dialog"
    v-model="ruleDialog.visible"
    :title="ruleDialog.index >= 0 ? $t('webService.editSubRule') : $t('webService.addSubRule')"
    width="min(900px, 94vw)"
    append-to-body
    destroy-on-close
  >
    <el-form ref="ruleFormRef" :model="ruleDialog.form" :label-width="locale.startsWith('zh') ? '150px' : '200px'">
      <div v-if="ruleDialog.form.type === 'reverse' && !ruleDialog.healthReady" class="health-load-state">
        <el-alert
          v-if="!ruleDialog.healthReady"
          :title="ruleDialog.healthError ? labels.healthFailed : labels.healthPending"
          :type="ruleDialog.healthError ? 'error' : 'info'"
          :closable="false"
          show-icon
        />
        <el-button
          v-if="ruleDialog.healthError"
          class="health-retry"
          :loading="ruleDialog.healthLoading"
          @click="loadRuleHealth(ruleDialog.form.id)"
          >{{ labels.retry }}</el-button
        >
      </div>
      <el-tabs v-model="editorTab" class="rule-editor-tabs">
        <el-tab-pane :label="labels.basic" name="basic">
          <el-form-item :label="labels.group"
            ><el-input v-model="ruleDialog.form.group" maxlength="128"
          /></el-form-item>
          <el-form-item :label="$t('webService.ruleName')">
            <el-input v-model="ruleDialog.form.name" />
          </el-form-item>
          <el-form-item :label="locale.startsWith('zh') ? '启用规则' : 'Enable rule'"
            ><el-switch v-model="ruleDialog.form.enabled"
          /></el-form-item>
          <el-form-item :label="$t('webService.colType')" required>
            <el-radio-group v-model="ruleDialog.form.type">
              <el-radio-button value="reverse">{{ $t('webService.typeReverse') }}</el-radio-button>
              <el-radio-button value="redirect">{{ $t('webService.typeRedirect') }}</el-radio-button>
              <el-radio-button value="fileserver">{{ $t('webService.typeFileserver') }}</el-radio-button>
              <el-radio-button value="webdav">WebDAV</el-radio-button>
              <el-radio-button value="cgi">CGI</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item :label="$t('webService.frontendHost')">
            <el-input v-model="ruleDialog.form.frontendHost" :placeholder="$t('webService.frontendHostPlaceholder')" />
          </el-form-item>
          <el-form-item :label="$t('webService.frontendPath')">
            <el-input v-model="ruleDialog.form.frontendPath" :placeholder="$t('webService.frontendPathPlaceholder')" />
          </el-form-item>

          <template v-if="ruleDialog.form.type === 'reverse'">
            <el-form-item :label="locale.startsWith('zh') ? '后端池' : 'Backend pool'"
              ><template #label
                ><span>{{ locale.startsWith('zh') ? '后端池' : 'Backend pool' }}</span
                ><HelpHint
                  :label="locale.startsWith('zh') ? '后端池' : 'Backend pool'"
                  :text="helpText(locale, 'backendPool')"
              /></template>
              <el-select
                v-model="ruleDialog.form.backendPoolId"
                clearable
                style="width: 100%"
                :loading="poolsLoading"
                :placeholder="locale.startsWith('zh') ? '留空使用规则后端' : 'Empty uses rule backends'"
              >
                <el-option v-for="pool in pools" :key="pool.id" :label="pool.name" :value="pool.id" />
              </el-select>
            </el-form-item>
            <el-alert
              v-if="ruleDialog.form.backendPoolId"
              type="info"
              :closable="false"
              :title="
                locale.startsWith('zh')
                  ? '节点、权重、并发和健康检查由所选后端池管理。'
                  : 'Nodes, weights, capacity and health checks are managed by this pool.'
              "
            />
            <el-form-item v-else :label="$t('webService.backends')" required>
              <div class="backend-editor">
                <el-input v-model="ruleDialog.form.backendsText" :placeholder="$t('webService.backendsPlaceholder')" />
                <div class="backend-test-row">
                  <el-button size="small" :loading="backendTesting" @click="testBackend">{{
                    $t('webService.testFirstBackend')
                  }}</el-button>
                  <span v-if="backendTestResult" class="form-tip">{{ backendTestResult }}</span>
                </div>
              </div>
            </el-form-item>
          </template>
          <template v-if="ruleDialog.form.type === 'redirect'">
            <el-form-item :label="$t('webService.redirectTarget')" required>
              <el-input v-model="ruleDialog.form.redirectUrl" :placeholder="$t('webService.redirectUrlPlaceholder')" />
            </el-form-item>
            <el-form-item :label="$t('webService.statusCode')">
              <el-select v-model="ruleDialog.form.redirectCode" style="width: 200px">
                <el-option :value="301" :label="$t('webService.redirect301')" />
                <el-option :value="302" :label="$t('webService.redirect302')" />
                <el-option :value="307" :label="$t('webService.redirect307')" />
                <el-option :value="308" :label="$t('webService.redirect308')" />
              </el-select>
            </el-form-item>
          </template>

          <template v-if="['fileserver', 'webdav', 'cgi'].includes(ruleDialog.form.type)">
            <el-form-item :label="$t('webService.rootDir')" required>
              <el-input v-model="ruleDialog.form.rootDir" :placeholder="$t('webService.rootDirPlaceholder')" />
            </el-form-item>
          </template>
          <template v-if="ruleDialog.form.type === 'webdav'">
            <el-form-item :label="locale.startsWith('zh') ? '允许写入' : 'Allow writes'"
              ><el-switch v-model="ruleDialog.form.webdavWritable" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'webdavWritable')"
            /></el-form-item>
            <el-form-item
              v-if="ruleDialog.form.webdavWritable"
              :label="locale.startsWith('zh') ? '上传上限 MiB' : 'Upload limit MiB'"
              ><el-input-number
                v-model="ruleDialog.form.maxRequestBodyMiB"
                :min="0"
                :max="10240"
                controls-position="right"
            /></el-form-item>
            <p class="editor-note">
              {{
                locale.startsWith('zh')
                  ? '默认只读；允许写入须在认证设置中配置用户。上传上限0使用64MiB。目录查询支持Depth 0/1，每目录最多512项；不提供递归复制。'
                  : 'Read-only by default. Writing requires authentication. A zero upload limit uses 64 MiB. Listing supports Depth 0/1 and up to 512 entries per directory; recursive copying is unavailable.'
              }}
            </p>
          </template>
          <template v-if="ruleDialog.form.type === 'cgi'">
            <el-form-item :label="locale.startsWith('zh') ? 'CGI入口文件' : 'CGI entry file'" required
              ><el-input v-model="ruleDialog.form.cgiEntry" placeholder="app.cgi"
            /></el-form-item>
            <el-form-item :label="locale.startsWith('zh') ? '执行超时（秒）' : 'Execution timeout (s)'"
              ><el-input-number
                v-model="ruleDialog.form.cgiTimeoutSeconds"
                :min="1"
                :max="60"
                controls-position="right"
            /></el-form-item>
            <p class="editor-note">
              {{
                locale.startsWith('zh')
                  ? '须配置认证。仅执行指定目录内的固定可执行文件；以服务进程权限运行，请使用可信脚本。最多4个并行进程，输入/输出各8MiB，不支持CGI流式响应。'
                  : 'Authentication is required. Runs only the fixed executable inside the configured directory, with service-process privileges. Use trusted scripts. Up to 4 concurrent processes, 8 MiB input/output each; CGI streaming is unavailable.'
              }}
            </p>
          </template>
        </el-tab-pane>
        <el-tab-pane :label="locale.startsWith('zh') ? '认证设置' : 'Authentication'" name="auth">
          <el-form-item :label="locale.startsWith('zh') ? '认证来源' : 'Authentication source'"
            ><el-select v-model="ruleDialog.form.accessGroupId" style="width: 100%"
              ><el-option value="" :label="locale.startsWith('zh') ? '规则凭据' : 'Rule credentials'" /><el-option
                v-for="g in accessGroups"
                :key="g.id"
                :value="g.id"
                :label="g.name" /></el-select
          ></el-form-item>
          <el-alert
            v-if="ruleDialog.form.accessGroupId"
            type="info"
            :closable="false"
            :title="
              locale.startsWith('zh')
                ? '使用组内用户认证。会话绑定IP、站点Host与当前规则；转发到后端前移除登录凭据和会话Cookie。'
                : 'Authenticate with group users. Sessions bind to IP, Host and rule. Credentials and session cookies are removed before proxying.'
            "
          />
          <el-form-item v-if="!ruleDialog.form.accessGroupId" :label="$t('webService.basicAuth')">
            <el-switch v-model="ruleDialog.form.basicAuth" /><HelpHint
              :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
              :text="helpText(locale, 'basicAuth')"
            />
          </el-form-item>
          <template v-if="ruleDialog.form.basicAuth">
            <el-form-item :label="$t('webService.authUser')">
              <el-input v-model="ruleDialog.form.authUser" style="width: 240px" />
            </el-form-item>
            <el-form-item :label="$t('webService.authPass')">
              <el-input
                v-model="ruleDialog.form.authPass"
                type="password"
                show-password
                :placeholder="
                  ruleDialog.form.authPassConfigured ? $t('common.keepEmpty') : $t('webService.enterPassword')
                "
                style="width: 240px"
              />
            </el-form-item>
          </template>
        </el-tab-pane>
        <el-tab-pane :label="labels.access" name="access">
          <el-form-item :label="labels.policy"
            ><el-select v-model="ruleDialog.form.policyId" clearable style="width: 100%"
              ><el-option
                v-for="policy in policies"
                :key="policy.id"
                :label="policy.name"
                :value="policy.id" /></el-select
          ></el-form-item>
          <p class="editor-note">{{ labels.inherited }}</p>
          <el-form-item :label="$t('forward.ipAccess')">
            <el-select v-model="ruleDialog.form.ipListMode" style="width: 200px">
              <el-option value="" :label="$t('common.noLimit')" />
              <el-option value="whitelist" :label="$t('common.ipWhitelist')" />
              <el-option value="blacklist" :label="$t('common.ipBlacklist')" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="ruleDialog.form.ipListMode" :label="$t('common.ipList')">
            <el-input
              v-model="ruleDialog.ipListText"
              type="textarea"
              :rows="2"
              :placeholder="$t('common.ipListPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="$t('webService.uaAccess')">
            <el-select v-model="ruleDialog.form.uaListMode" style="width: 200px">
              <el-option value="" :label="$t('common.noLimit')" />
              <el-option value="whitelist" :label="$t('common.uaWhitelist')" />
              <el-option value="blacklist" :label="$t('common.uaBlacklist')" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="ruleDialog.form.uaListMode" :label="$t('webService.uaKeywords')">
            <el-input
              v-model="ruleDialog.uaListText"
              type="textarea"
              :rows="2"
              :placeholder="$t('webService.uaListPlaceholder')"
            />
          </el-form-item>
        </el-tab-pane>
        <el-tab-pane :label="locale.startsWith('zh') ? '日志设置' : 'Logging'" name="logging">
          <el-form-item :label="locale.startsWith('zh') ? '记录访问日志' : 'Record access log'"
            ><el-switch
              :model-value="!ruleDialog.form.disableAccessLog"
              @change="ruleDialog.form.disableAccessLog = !$event"
          /></el-form-item>
          <p class="editor-note">
            {{
              locale.startsWith('zh')
                ? '关闭后仍统计流量与请求；防护拦截事件继续记录。'
                : 'Traffic and request statistics remain active. Protection events are still recorded.'
            }}
          </p>
        </el-tab-pane>
        <el-tab-pane :label="labels.advanced" name="advanced">
          <template v-if="ruleDialog.form.type === 'reverse'">
            <el-form-item :label="$t('webService.stripPrefix')">
              <el-switch v-model="ruleDialog.form.stripPrefix" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'stripPrefix')"
              />
              <span class="form-tip">{{ $t('webService.stripPrefixTip') }}</span>
            </el-form-item>
            <el-form-item :label="$t('webService.preserveHost')">
              <el-switch v-model="ruleDialog.form.preserveHost" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'preserveHost')"
              />
              <span class="form-tip">{{ $t('webService.preserveHostTip') }}</span>
            </el-form-item>
            <el-form-item :label="$t('webService.autoProxyHeaders')">
              <el-switch v-model="ruleDialog.form.autoProxyHeaders" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'autoProxyHeaders')"
              />
              <span class="form-tip">{{ $t('webService.autoProxyHeadersTip') }}</span>
            </el-form-item>
            <el-form-item :label="$t('webService.skipBackendTls')">
              <el-switch v-model="ruleDialog.form.skipBackendTlsVerify" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'skipBackendTlsVerify')"
              />
              <span class="form-tip">{{ $t('webService.skipBackendTlsTip') }}</span>
            </el-form-item>
            <el-form-item :label="$t('webService.extraHeaders')">
              <div class="headers-editor">
                <div v-for="(h, i) in ruleDialog.headersList" :key="i" class="header-row">
                  <el-input v-model="h.key" :placeholder="$t('webService.headerKeyPlaceholder')" style="width: 220px" />
                  <el-input
                    v-model="h.value"
                    type="password"
                    show-password
                    :placeholder="h.configured ? $t('common.keepEmpty') : $t('webService.headerValuePlaceholder')"
                    style="width: 240px"
                  />
                  <el-button link type="danger" @click="ruleDialog.headersList.splice(i, 1)">{{
                    $t('common.delete')
                  }}</el-button>
                </div>
                <el-button size="small" plain @click="ruleDialog.headersList.push({ key: '', value: '' })">
                  {{ $t('webService.addHeader') }}
                </el-button>
              </div>
            </el-form-item>

            <h4 class="editor-section-title">{{ $t('webService.stabilityTitle') }}</h4>
            <div class="number-grid">
              <el-form-item :label="$t('webService.connectTimeout')">
                <el-input-number
                  v-model="ruleDialog.form.connectTimeoutSeconds"
                  :min="0"
                  :max="30"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.responseHeaderTimeout')">
                <el-input-number
                  v-model="ruleDialog.form.responseHeaderTimeoutSeconds"
                  :min="0"
                  :max="600"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.rateLimitRps')">
                <el-input-number
                  v-model="ruleDialog.form.rateLimitRPS"
                  :min="0"
                  :max="100000"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.rateLimitBurst')">
                <el-input-number
                  v-model="ruleDialog.form.rateLimitBurst"
                  :min="0"
                  :max="200000"
                  controls-position="right"
                />
              </el-form-item>
              <el-form-item :label="$t('webService.maxBody')">
                <el-input-number
                  v-model="ruleDialog.form.maxRequestBodyMiB"
                  :min="0"
                  :max="10240"
                  controls-position="right"
                />
              </el-form-item>
            </div>
            <div class="collapse-tip">{{ $t('webService.collapseTip') }}</div>
            <h4 class="editor-section-title">{{ $t('webService.responseRewrite') }}</h4>
            <el-form-item :label="$t('webService.rewriteLocation')">
              <el-switch v-model="ruleDialog.form.rewriteLocation" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'rewriteLocation')"
              />
              <span class="form-tip">{{ $t('webService.rewriteLocationTip') }}</span>
            </el-form-item>
            <el-form-item :label="$t('webService.cookieDomain')">
              <div class="rewrite-pair">
                <el-input
                  v-model="ruleDialog.form.cookieDomainFrom"
                  :placeholder="$t('webService.cookieDomainFromPlaceholder')"
                /><span>→</span
                ><el-input
                  v-model="ruleDialog.form.cookieDomainTo"
                  :placeholder="$t('webService.cookieDomainToPlaceholder')"
                />
              </div>
            </el-form-item>
            <el-form-item :label="$t('webService.cookiePath')">
              <div class="rewrite-pair">
                <el-input
                  v-model="ruleDialog.form.cookiePathFrom"
                  :placeholder="$t('webService.cookiePathFromPlaceholder')"
                /><span>→</span
                ><el-input
                  v-model="ruleDialog.form.cookiePathTo"
                  :placeholder="$t('webService.cookiePathToPlaceholder')"
                />
              </div>
            </el-form-item>
            <el-divider />
            <el-form-item :label="locale.startsWith('zh') ? '路径重写前缀' : 'Rewrite path prefix'"
              ><el-input v-model="ruleDialog.form.pathRewritePrefix" placeholder="/backend"
            /></el-form-item>
            <p class="editor-note">
              {{
                locale.startsWith('zh')
                  ? '替换命中的前端路径前缀，保留其后的路径和查询参数。留空沿用现有路径行为。'
                  : 'Replace the matched frontend prefix, preserving the remainder and query. Empty keeps existing behavior.'
              }}
            </p>
            <el-form-item :label="locale.startsWith('zh') ? '代理失败正文' : 'Proxy error body'"
              ><el-input v-model="ruleDialog.form.proxyErrorBody" type="textarea" :rows="2" maxlength="65536"
            /></el-form-item>
            <p class="editor-note">
              {{
                locale.startsWith('zh')
                  ? '用于程序产生的 502/503；后端已有响应仍透传。'
                  : 'Used for local proxy 502/503 errors. Backend responses are forwarded.'
              }}
            </p>
          </template>
          <el-form-item :label="locale.startsWith('zh') ? '响应头' : 'Response headers'">
            <div class="headers-editor">
              <div v-for="(header, i) in ruleDialog.responseHeadersList" :key="i" class="header-row">
                <el-input v-model="header.key" placeholder="X-Example" /><el-input
                  v-model="header.value"
                  :placeholder="locale.startsWith('zh') ? '空值删除响应头' : 'Empty removes header'"
                /><el-button type="danger" link @click="ruleDialog.responseHeadersList.splice(i, 1)">×</el-button>
              </div>
              <el-button @click="ruleDialog.responseHeadersList.push({ key: '', value: '' })">{{
                locale.startsWith('zh') ? '添加响应头' : 'Add response header'
              }}</el-button>
            </div>
          </el-form-item>
          <el-form-item label="CORS"
            ><el-switch v-model="ruleDialog.form.cors.enabled" /><HelpHint
              :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
              :text="helpText(locale, 'cors')"
          /></el-form-item>
          <template v-if="ruleDialog.form.cors.enabled">
            <el-form-item :label="locale.startsWith('zh') ? '允许来源' : 'Allowed origins'"
              ><el-input v-model="ruleDialog.corsOrigins" type="textarea" :rows="2" placeholder="https://example.com"
            /></el-form-item>
            <el-form-item :label="locale.startsWith('zh') ? '允许方法' : 'Allowed methods'"
              ><el-input v-model="ruleDialog.corsMethods" placeholder="GET, HEAD, OPTIONS"
            /></el-form-item>
            <el-form-item :label="locale.startsWith('zh') ? '允许请求头' : 'Allowed request headers'"
              ><el-input v-model="ruleDialog.corsHeaders" placeholder="Content-Type, Authorization"
            /></el-form-item>
            <el-form-item :label="locale.startsWith('zh') ? '允许凭据' : 'Allow credentials'"
              ><el-switch v-model="ruleDialog.form.cors.credentials" /><HelpHint
                :label="locale.startsWith('zh') ? '查看说明' : 'Help'"
                :text="helpText(locale, 'corsCredentials')"
            /></el-form-item>
            <el-form-item :label="locale.startsWith('zh') ? '预检缓存（秒）' : 'Preflight cache (s)'"
              ><el-input-number v-model="ruleDialog.form.cors.maxAge" :min="0" :max="86400"
            /></el-form-item>
          </template>
          <el-collapse v-if="ruleDialog.form.type === 'reverse' && !ruleDialog.form.backendPoolId"
            ><el-collapse-item :title="$t('webService.healthTitle')" name="health">
              <div v-if="ruleDialog.healthReady">
                <el-form-item :label="$t('webService.healthEnable')">
                  <el-switch v-model="ruleDialog.health.enabled" />
                  <span class="form-tip">{{ $t('webService.healthEnableTip') }}</span>
                </el-form-item>
                <template v-if="ruleDialog.health.enabled">
                  <el-form-item :label="$t('webService.healthType')">
                    <el-radio-group v-model="ruleDialog.health.type">
                      <el-radio-button value="tcp">TCP</el-radio-button>
                      <el-radio-button value="http">HTTP</el-radio-button>
                    </el-radio-group>
                  </el-form-item>
                  <el-form-item v-if="ruleDialog.health.type === 'http'" :label="$t('webService.healthPath')">
                    <el-input v-model="ruleDialog.health.path" placeholder="/" style="width: 240px" />
                  </el-form-item>
                  <div class="number-grid">
                    <el-form-item :label="$t('webService.healthInterval')">
                      <el-input-number
                        v-model="ruleDialog.health.intervalSeconds"
                        :min="2"
                        :max="300"
                        controls-position="right"
                      />
                    </el-form-item>
                    <el-form-item :label="$t('webService.healthTimeout')">
                      <el-input-number
                        v-model="ruleDialog.health.timeoutSeconds"
                        :min="1"
                        :max="60"
                        controls-position="right"
                      />
                    </el-form-item>
                    <el-form-item :label="$t('webService.healthRise')">
                      <el-input-number v-model="ruleDialog.health.rise" :min="1" :max="10" controls-position="right" />
                    </el-form-item>
                    <el-form-item :label="$t('webService.healthFall')">
                      <el-input-number v-model="ruleDialog.health.fall" :min="1" :max="10" controls-position="right" />
                    </el-form-item>
                  </div>
                  <div class="collapse-tip">{{ $t('webService.healthTip') }}</div>
                </template>
              </div>
            </el-collapse-item></el-collapse
          >
        </el-tab-pane>
      </el-tabs>
    </el-form>
    <template #footer>
      <el-button @click="ruleDialog.visible = false">{{ $t('common.cancel') }}</el-button>
      <el-button
        type="primary"
        :loading="ruleDialog.saving"
        :disabled="ruleDialog.form.type === 'reverse' && !ruleDialog.healthReady"
        @click="confirmRule"
        >{{ $t('webService.confirmOk') }}</el-button
      >
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, reactive, ref, watch, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import request from '../../api'
import HelpHint from '../HelpHint.vue'
import { helpText } from './help'
import { splitList } from '../../utils/list'
import { ipListError } from '../../utils/validate'

const props = defineProps({ site: { type: Object, default: null }, policies: { type: Array, default: () => [] } })
const emit = defineEmits(['saved'])

const { t, locale } = useI18n()
const labels = computed(() =>
  locale.value.startsWith('zh')
    ? {
        basic: '基础设置',
        access: '安全设置',
        advanced: '高级设置',
        retry: '重新读取',
        healthFailed: '健康检查配置读取失败。请重试后再保存。',
        inherited: '站点及全局策略继续生效；此处可增加规则专用的策略和认证。',
        group: '分组',
        policy: '共享防护策略',
        healthPending: '正在读取健康检查配置。读取成功后才能保存，避免覆盖原配置。',
        conflict: '配置已发生变化，请重新打开编辑器。'
      }
    : {
        basic: 'Basic',
        access: 'Access control',
        advanced: 'Advanced',
        retry: 'Retry',
        healthFailed: 'Failed to load health settings. Retry before saving.',
        inherited: 'Site and global policies remain active; add rule-specific controls here.',
        group: 'Group',
        policy: 'Shared protection policy',
        healthPending: 'Health settings must load before saving to preserve the current configuration.',
        conflict: 'Configuration changed. Reopen the editor before saving.'
      }
)

const emptyRule = () => ({
  id: '',
  revision: 0,
  group: '',
  policyId: '',
  backendPoolId: '',
  accessGroupId: '',
  pathRewritePrefix: '',
  proxyErrorBody: '',
  disableAccessLog: false,
  cors: { enabled: false, credentials: false, maxAge: 600 },
  name: '',
  type: 'reverse',
  enabled: true,
  frontendHost: '',
  frontendPath: '',
  backendsText: '',
  redirectUrl: '',
  redirectCode: 302,
  rootDir: '',
  webdavWritable: false,
  cgiEntry: '',
  cgiTimeoutSeconds: 15,
  preserveHost: false,
  autoProxyHeaders: true,
  skipBackendTlsVerify: false,
  stripPrefix: false,
  connectTimeoutSeconds: 5,
  responseHeaderTimeoutSeconds: 60,
  rateLimitRPS: 0,
  rateLimitBurst: 0,
  maxRequestBodyMiB: 0,
  rewriteLocation: false,
  cookieDomainFrom: '',
  cookieDomainTo: '',
  cookiePathFrom: '',
  cookiePathTo: '',
  basicAuth: false,
  authUser: '',
  authPass: '',
  authPassConfigured: false,
  ipListMode: '',
  uaListMode: ''
})

const emptyHealth = () => ({
  enabled: false,
  type: 'tcp',
  path: '/',
  intervalSeconds: 10,
  timeoutSeconds: 3,
  rise: 1,
  fall: 2
})

const ruleFormRef = ref()
const ruleDialog = reactive({
  visible: false,
  saving: false,
  index: -1,
  siteID: '',
  siteRevision: 0,
  healthReady: true,
  healthLoading: false,
  healthError: false,
  form: emptyRule(),
  headersList: [],
  responseHeadersList: [],
  corsOrigins: '',
  corsMethods: '',
  corsHeaders: '',
  ipListText: '',
  uaListText: '',
  health: emptyHealth(),
  healthWasEnabled: false
})
const editorTab = ref('basic')
let openGeneration = 0,
  healthRequest = 0
watch(
  () => ruleDialog.visible,
  (visible) => {
    if (!visible) {
      openGeneration++
      healthRequest++
      ruleDialog.form.authPass = ''
      for (const header of ruleDialog.headersList) header.value = ''
    }
  }
)
watch(
  () => ruleDialog.form.type,
  (type) => {
    if (type !== 'reverse' && ['advanced', 'health'].includes(editorTab.value)) editorTab.value = 'basic'
  }
)
onBeforeUnmount(() => {
  openGeneration++
  healthRequest++
})
const backendTesting = ref(false)
const backendTestResult = ref('')

const accessGroups = ref([])
const pools = ref([]),
  poolsLoading = ref(false)
async function loadPools(generation) {
  poolsLoading.value = true
  try {
    const res = await request.get('/api/backend-pools')
    if (generation === openGeneration) pools.value = Array.isArray(res.data) ? res.data : []
  } catch {
    if (generation === openGeneration) pools.value = []
  } finally {
    if (generation === openGeneration) poolsLoading.value = false
  }
}
async function loadAccessGroups(generation) {
  try {
    const res = await request.get('/api/protection/access/groups')
    if (generation === openGeneration) accessGroups.value = Array.isArray(res.data) ? res.data : []
  } catch {}
}
watch(
  () => ruleDialog.form.accessGroupId,
  (id) => {
    if (id) ruleDialog.form.basicAuth = false
  }
)
function open(row, index = -1) {
  if (!props.site) return
  openGeneration++
  loadPools(openGeneration)
  loadAccessGroups(openGeneration)
  healthRequest++
  editorTab.value = 'basic'
  ruleDialog.healthError = false
  ruleDialog.healthLoading = false
  ruleDialog.saving = false
  ruleDialog.index = index
  ruleDialog.siteID = props.site.id
  ruleDialog.siteRevision = props.site.revision ?? 0
  ruleDialog.healthReady = !(row?.id && (row.type || 'reverse') === 'reverse')
  ruleDialog.form = row
    ? {
        id: row.id || '',
        revision: row.revision ?? 0,
        group: row.group || '',
        policyId: row.policyId || '',
        backendPoolId: row.backendPoolId || '',
        accessGroupId: row.accessGroupId || '',
        pathRewritePrefix: row.pathRewritePrefix || '',
        proxyErrorBody: row.proxyErrorBody || '',
        disableAccessLog: !!row.disableAccessLog,
        cors: { enabled: false, credentials: false, maxAge: 600, ...(row.cors || {}) },
        name: row.name || '',
        type: row.type || 'reverse',
        enabled: row.enabled !== false,
        frontendHost: row.frontendHost || '',
        frontendPath: row.frontendPath || '',
        backendsText: (row.backends || []).join(', '),
        redirectUrl: row.redirectUrl || '',
        redirectCode: row.redirectCode || 302,
        rootDir: row.rootDir || '',
        webdavWritable: !!row.webdavWritable,
        cgiEntry: row.cgiEntry || '',
        cgiTimeoutSeconds: row.cgiTimeoutSeconds || 15,
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
  ruleDialog.responseHeadersList = Object.entries(row?.responseHeaders || {}).map(([key, value]) => ({ key, value }))
  ruleDialog.corsOrigins = (row?.cors?.origins || []).join('\n')
  ruleDialog.corsMethods = (row?.cors?.methods || ['GET', 'HEAD', 'OPTIONS']).join(', ')
  ruleDialog.corsHeaders = (row?.cors?.headers || []).join(', ')
  ruleDialog.headersList = row
    ? Object.entries(row.headers || {}).map(([key]) => ({ key, value: '', configured: true }))
    : []
  ruleDialog.ipListText = row ? (row.ipList || []).join(', ') : ''
  ruleDialog.uaListText = row ? (row.uaList || []).join(', ') : ''
  ruleDialog.health = emptyHealth()
  ruleDialog.healthWasEnabled = false
  backendTestResult.value = ''
  backendTesting.value = false
  ruleDialog.visible = true
  if (row?.id && (row.type || 'reverse') === 'reverse') loadRuleHealth(row.id)
}

async function loadRuleHealth(ruleID) {
  const generation = openGeneration,
    requestID = ++healthRequest,
    siteID = ruleDialog.siteID
  ruleDialog.healthLoading = true
  ruleDialog.healthError = false
  const current = () =>
    generation === openGeneration &&
    requestID === healthRequest &&
    ruleDialog.visible &&
    ruleDialog.siteID === siteID &&
    ruleDialog.form.id === ruleID
  try {
    const res = await request.get(`/api/sites/${siteID}/rules/${ruleID}/health`)
    const conf = res.data?.conf
    if (!current()) return
    if (!conf) throw new Error('Missing health configuration')
    ruleDialog.health = { ...emptyHealth(), ...conf, enabled: !!conf.enabled }
    ruleDialog.healthWasEnabled = !!conf.enabled
    ruleDialog.healthReady = true
  } catch {
    if (current()) {
      ruleDialog.healthError = true
      ruleDialog.healthReady = false
    }
  } finally {
    if (current()) ruleDialog.healthLoading = false
  }
}

function healthError() {
  const h = ruleDialog.health
  if (!h.enabled) return ''
  if (h.type === 'http' && !(h.path || '').startsWith('/')) return t('webService.healthPathError')
  if (h.timeoutSeconds > h.intervalSeconds) return t('webService.healthTimeoutError')
  return ''
}

async function testBackend() {
  const backend = splitList(ruleDialog.form.backendsText)[0]
  if (!backend) {
    ElMessage.warning(t('webService.fillBackendFirst'))
    return
  }
  const generation = openGeneration
  backendTesting.value = true
  backendTestResult.value = ''
  try {
    const res = await request.post('/api/sites/backend-test', {
      url: backend,
      connectTimeoutSeconds: ruleDialog.form.connectTimeoutSeconds || 5,
      skipBackendTlsVerify: ruleDialog.form.skipBackendTlsVerify
    })
    if (generation !== openGeneration || !ruleDialog.visible) return
    const data = res.data || {}
    backendTestResult.value = `${t('webService.backendOk', { ms: data.latencyMs ?? 0 })}${data.tls ? t('webService.tlsOk') : ''}`
    ElMessage.success(t('webService.backendTestPassed'))
  } catch {
    if (generation === openGeneration) backendTestResult.value = t('webService.backendFailed')
  } finally {
    if (generation === openGeneration) backendTesting.value = false
  }
}

async function confirmRule() {
  const f = ruleDialog.form
  if (f.type === 'reverse' && !ruleDialog.healthReady) {
    ElMessage.warning(labels.value.healthPending)
    return
  }
  if (!f.name.trim()) {
    ElMessage.warning(t('webService.ruleNameRequired'))
    return
  }
  if (f.type === 'reverse' && !f.backendPoolId && !splitList(f.backendsText).length) {
    ElMessage.warning(t('webService.fillBackend'))
    return
  }
  if (f.type === 'redirect' && !f.redirectUrl) {
    ElMessage.warning(t('webService.fillTarget'))
    return
  }
  if (['fileserver', 'webdav', 'cgi'].includes(f.type) && !f.rootDir) {
    ElMessage.warning(t('webService.fillRootDir'))
    return
  }
  if (f.type === 'reverse' && !f.backendPoolId) {
    const hErr = healthError()
    if (hErr) {
      ElMessage.warning(hErr)
      return
    }
  }
  if (f.ipListMode) {
    const ipErr = ipListError(t, ruleDialog.ipListText)
    if (ipErr) {
      ElMessage.warning(ipErr)
      return
    }
  }
  const headers = {}
  for (const h of ruleDialog.headersList) {
    // 空值保留提交：后端 mergeRuleSecrets（internal/webproxy/api.go）会把空值与原有值合并，实现"留空保持不变"
    if (h.key.trim()) headers[h.key.trim()] = h.value
  }
  const rule = {
    id: f.id,
    group: f.group.trim(),
    policyId: f.policyId,
    pathRewritePrefix: f.type === 'reverse' ? f.pathRewritePrefix.trim() : '',
    proxyErrorBody: f.type === 'reverse' ? f.proxyErrorBody : '',
    disableAccessLog: f.disableAccessLog,
    responseHeaders: Object.fromEntries(
      ruleDialog.responseHeadersList.filter((h) => h.key.trim()).map((h) => [h.key.trim(), h.value])
    ),
    cors: {
      ...f.cors,
      origins: splitList(ruleDialog.corsOrigins),
      methods: splitList(ruleDialog.corsMethods),
      headers: splitList(ruleDialog.corsHeaders)
    },
    backendPoolId: f.type === 'reverse' ? f.backendPoolId : '',
    accessGroupId: f.accessGroupId,
    expectedRevision: ruleDialog.index >= 0 ? f.revision : ruleDialog.siteRevision,
    ...(f.type === 'reverse' ? { healthCheck: { ...ruleDialog.health } } : {}),
    name: f.name,
    type: f.type,
    enabled: f.enabled,
    frontendHost: f.frontendHost.trim(),
    frontendPath: f.frontendPath.trim(),
    backends: f.type === 'reverse' && !f.backendPoolId ? splitList(f.backendsText) : [],
    redirectUrl: f.type === 'redirect' ? f.redirectUrl.trim() : '',
    redirectCode: f.type === 'redirect' ? f.redirectCode : 0,
    rootDir: ['fileserver', 'webdav', 'cgi'].includes(f.type) ? f.rootDir.trim() : '',
    webdavWritable: f.type === 'webdav' && f.webdavWritable,
    cgiEntry: f.type === 'cgi' ? f.cgiEntry.trim() : '',
    cgiTimeoutSeconds: f.type === 'cgi' ? f.cgiTimeoutSeconds : 0,
    headers,
    preserveHost: f.preserveHost,
    autoProxyHeaders: f.autoProxyHeaders,
    skipBackendTlsVerify: f.skipBackendTlsVerify,
    stripPrefix: f.stripPrefix,
    connectTimeoutSeconds: f.connectTimeoutSeconds || 0,
    responseHeaderTimeoutSeconds: f.responseHeaderTimeoutSeconds || 0,
    rateLimitRPS: f.rateLimitRPS || 0,
    rateLimitBurst: f.rateLimitRPS ? f.rateLimitBurst || f.rateLimitRPS * 2 : 0,
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
  const generation = openGeneration
  ruleDialog.saving = true
  try {
    if (ruleDialog.index >= 0) {
      await request.put(`/api/sites/${ruleDialog.siteID}/rules/${rule.id}`, rule)
    } else {
      await request.post(`/api/sites/${ruleDialog.siteID}/rules`, rule)
    }
    emit('saved')
    if (generation === openGeneration) {
      ElMessage.success(t('common.saveSuccess'))
      ruleDialog.visible = false
    }
  } catch (error) {
    if (generation === openGeneration && error.response?.status === 409) ElMessage.warning(labels.value.conflict)
  } finally {
    if (generation === openGeneration) ruleDialog.saving = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.rule-editor-tabs :deep(.el-tabs__header) {
  margin: 0 0 20px;
}
.editor-section-title {
  font-size: 13px;
  margin: 22px 0 14px;
  padding-top: 14px;
  border-top: 1px solid var(--ap-border);
}
.editor-note {
  color: var(--ap-muted);
  font-size: 12px;
  margin: 0 0 18px;
}
.health-retry {
  margin: 12px 0;
}

.form-tip {
  margin-left: 10px;
  color: var(--ap-muted);
  font-size: 12px;
}
.headers-editor,
.backend-editor {
  width: 100%;
}
.backend-test-row {
  display: flex;
  align-items: center;
  margin-top: 8px;
}
.number-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}
.number-grid :deep(.el-input-number) {
  width: 100%;
}
.rewrite-pair {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 8px;
  width: 100%;
}
.collapse-tip {
  margin: -4px 0 12px 110px;
  color: var(--ap-muted);
  font-size: 12px;
}
.header-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.security-collapse {
  width: 100%;
  margin-top: 4px;
}
@media (max-width: 700px) {
  :deep(.el-radio-button__inner) {
    min-height: 44px;
    display: inline-flex;
    align-items: center;
    border-left: 1px solid var(--ap-border);
    border-radius: 6px;
  }
  .header-row {
    flex-wrap: wrap;
  }
  .header-row :deep(.el-input) {
    width: 100% !important;
  }
  :deep(.el-radio-group) {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }
  .number-grid,
  .rewrite-pair {
    grid-template-columns: 1fr;
  }
  .rewrite-pair > span {
    display: none;
  }
  .collapse-tip {
    margin-left: 0;
  }
  :deep(.el-form-item) {
    display: block;
  }
  :deep(.el-form-item__label) {
    width: 100% !important;
    height: auto;
    justify-content: flex-start;
    margin-bottom: 6px;
  }
  :deep(.el-form-item__content) {
    margin-left: 0 !important;
  }
}
</style>
