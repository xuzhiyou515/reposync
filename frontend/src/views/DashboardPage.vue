<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import type { Credential, ExecutionDetail, RepoCache, SyncExecution, SyncExecutionNode, SyncTask } from '../types'

const tasks = ref<SyncTask[]>([])
const credentials = ref<Credential[]>([])
const caches = ref<RepoCache[]>([])
const executions = ref<SyncExecution[]>([])
const selectedExecution = ref<ExecutionDetail | null>(null)
const loading = ref(false)
const executionTaskId = ref<number | null>(null)
const expandedNodeIds = ref<Set<number>>(new Set())
const errorOnly = ref(false)
const selectedNodeId = ref<number | null>(null)
let executionPollTimer: number | null = null

const emptyTask = (): Partial<SyncTask> => ({
  name: '',
  sourceRepoUrl: '',
  targetRepoUrl: '',
  enabled: true,
  recursiveSubmodules: true,
  syncAllRefs: true,
  triggerConfig: {
    cron: '0 */30 * * * *',
    webhookSecret: '',
    enableSchedule: false,
    enableWebhook: false,
    branchReference: 'refs/heads/main',
  },
  providerConfig: {
    provider: 'github',
    namespace: '',
    visibility: 'private',
    descriptionTemplate: 'mirror for {{repo}}',
    baseApiUrl: '',
  },
})

const emptyCredential = (): Partial<Credential> => ({
  name: '',
  type: 'api_token',
  username: '',
  secret: '',
  scope: '',
})

const taskForm = reactive<Partial<SyncTask>>(emptyTask())
const credentialForm = reactive<Partial<Credential>>(emptyCredential())

const taskSummary = computed(() => ({
  total: tasks.value.length,
  enabled: tasks.value.filter((item) => item.enabled).length,
  recursive: tasks.value.filter((item) => item.recursiveSubmodules).length,
  scheduled: tasks.value.filter((item) => item.triggerConfig.enableSchedule).length,
}))

const taskFormTriggerCards = computed(() => [
  {
    label: 'Schedule',
    value: taskForm.triggerConfig?.enableSchedule ? taskForm.triggerConfig.cron || '已启用' : '未启用',
  },
  {
    label: 'Webhook',
    value: taskForm.triggerConfig?.enableWebhook ? (taskForm.triggerConfig.webhookSecret ? '已签名' : '未签名') : '未启用',
  },
  {
    label: 'Branch Filter',
    value: taskForm.triggerConfig?.branchReference || '未限制',
  },
  {
    label: 'Mirror Scope',
    value: taskForm.syncAllRefs ? 'all refs' : 'custom',
  },
])

const taskFormWebhookPreview = computed(() => {
  const provider = taskForm.providerConfig?.provider || 'github'
  const taskId = taskForm.id ?? ':taskId'
  return `/api/webhooks/${provider}/${taskId}`
})

type TreeRow = SyncExecutionNode & {
  label: string
  level: number
  hasChildren: boolean
  expanded: boolean
}

const executionTreeRows = computed<TreeRow[]>(() => {
  if (!selectedExecution.value) {
    return []
  }
  const childrenByParent = new Map<number | null, SyncExecutionNode[]>()
  for (const node of selectedExecution.value.nodes) {
    const parent = node.parentNodeId ?? null
    const list = childrenByParent.get(parent) ?? []
    list.push(node)
    childrenByParent.set(parent, list)
  }

  const result: TreeRow[] = []
  const visit = (parentId: number | null, level: number, visible: boolean) => {
    const children = (childrenByParent.get(parentId) ?? []).sort((left, right) => left.repoPath.localeCompare(right.repoPath))
    if (!visible) {
      return
    }
    children.forEach((node) => {
      const nodeChildren = childrenByParent.get(node.id) ?? []
      const hasChildren = nodeChildren.length > 0
      const expanded = expandedNodeIds.value.has(node.id)
      const matchesFilter = !errorOnly.value || node.status === 'failed' || !!node.errorMessage
      if (matchesFilter) {
        result.push({
          ...node,
          label: node.repoPath || '(root)',
          level,
          hasChildren,
          expanded,
        })
      }
      visit(node.id, level + 1, expanded)
    })
  }

  visit(null, 0, true)
  return result
})

const selectedExecutionStats = computed(() => {
  if (!selectedExecution.value) {
    return []
  }
  const nodes = selectedExecution.value.nodes
  return [
    { label: '缓存命中', value: String(nodes.filter((node) => node.cacheHit).length) },
    { label: '自动建仓', value: String(nodes.filter((node) => node.autoCreated).length) },
    { label: '失败节点', value: String(nodes.filter((node) => node.status === 'failed').length) },
    { label: '总节点', value: String(nodes.length) },
  ]
})

const selectedExecutionNode = computed(() => {
  if (!selectedExecution.value || selectedNodeId.value == null) {
    return null
  }
  return selectedExecution.value.nodes.find((node) => node.id === selectedNodeId.value) ?? null
})

const loadTasks = async () => {
  tasks.value = await api.listTasks()
}

const loadCredentials = async () => {
  credentials.value = await api.listCredentials()
}

const loadCaches = async () => {
  caches.value = await api.listCaches()
}

const loadExecutions = async (taskId: number) => {
  executionTaskId.value = taskId
  executions.value = await api.listExecutions(taskId)
}

const stopExecutionPolling = () => {
  if (executionPollTimer != null) {
    window.clearInterval(executionPollTimer)
    executionPollTimer = null
  }
}

const refreshSelectedExecution = async () => {
  if (!selectedExecution.value) {
    return
  }
  const detail = await api.executionDetail(selectedExecution.value.execution.id)
  selectedExecution.value = detail
  if (selectedNodeId.value == null) {
    selectedNodeId.value = detail.nodes[0]?.id ?? null
  }
  if (detail.execution.status !== 'running') {
    stopExecutionPolling()
    if (executionTaskId.value != null) {
      await loadExecutions(executionTaskId.value)
    }
    await loadTasks()
  }
}

const ensureExecutionPolling = () => {
  stopExecutionPolling()
  if (!selectedExecution.value || selectedExecution.value.execution.status !== 'running') {
    return
  }
  executionPollTimer = window.setInterval(() => {
    void refreshSelectedExecution()
  }, 2000)
}

const refreshAll = async () => {
  loading.value = true
  try {
    await Promise.all([loadTasks(), loadCredentials(), loadCaches()])
  } finally {
    loading.value = false
  }
}

const resetTaskForm = () => {
  Object.assign(taskForm, emptyTask())
}

const resetCredentialForm = () => {
  Object.assign(credentialForm, emptyCredential())
}

const saveTask = async () => {
  await api.saveTask(taskForm)
  ElMessage.success('任务已保存')
  resetTaskForm()
  await refreshAll()
}

const editTask = (task: SyncTask) => {
  Object.assign(taskForm, JSON.parse(JSON.stringify(task)))
}

const removeTask = async (task: SyncTask) => {
  await api.deleteTask(task.id)
  ElMessage.success('任务已删除')
  await refreshAll()
  if (executionTaskId.value === task.id) {
    executions.value = []
    selectedExecution.value = null
  }
}

const runTask = async (task: SyncTask) => {
  const execution = await api.runTask(task.id)
  ElMessage.success('任务已触发')
  await refreshAll()
  await loadExecutions(task.id)
  await openExecution(execution)
}

const saveCredential = async () => {
  await api.saveCredential(credentialForm)
  ElMessage.success('凭证已保存')
  resetCredentialForm()
  await loadCredentials()
}

const editCredential = (credential: Credential) => {
  Object.assign(credentialForm, { ...credential, secret: '' })
}

const removeCredential = async (credential: Credential) => {
  await api.deleteCredential(credential.id)
  ElMessage.success('凭证已删除')
  await loadCredentials()
}

const openExecution = async (execution: SyncExecution) => {
  selectedExecution.value = await api.executionDetail(execution.id)
  expandedNodeIds.value = new Set(selectedExecution.value.nodes.map((node) => node.id))
  errorOnly.value = false
  selectedNodeId.value = selectedExecution.value.nodes[0]?.id ?? null
  ensureExecutionPolling()
}

const cleanupCache = async (cache: RepoCache) => {
  await api.cleanupCache(cache.id)
  ElMessage.success('缓存已清理')
  await loadCaches()
}

const taskTriggerSummary = (task: SyncTask) => {
  const bits: string[] = []
  if (task.triggerConfig.enableSchedule) {
    bits.push(`Schedule: ${task.triggerConfig.cron || 'unset'}`)
  }
  if (task.triggerConfig.enableWebhook) {
    bits.push(`Webhook: ${task.triggerConfig.webhookSecret ? 'signed' : 'unsigned'}`)
  }
  return bits.length ? bits.join(' | ') : 'Manual only'
}

const taskScheduleState = (task: SyncTask) => {
  if (!task.triggerConfig.enableSchedule) {
    return '未启用'
  }
  return task.triggerConfig.cron || '已启用'
}

const taskWebhookState = (task: SyncTask) => {
  if (!task.triggerConfig.enableWebhook) {
    return '未启用'
  }
  return task.triggerConfig.webhookSecret ? '已签名' : '未签名'
}

const taskBranchState = (task: SyncTask) => task.triggerConfig.branchReference || '未限制'

const taskStatusType = (status?: string) => {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'running') return 'warning'
  return 'info'
}

const toggleNode = (nodeId: number) => {
  const next = new Set(expandedNodeIds.value)
  if (next.has(nodeId)) {
    next.delete(nodeId)
  } else {
    next.add(nodeId)
  }
  expandedNodeIds.value = next
}

const expandAllNodes = () => {
  if (!selectedExecution.value) {
    return
  }
  expandedNodeIds.value = new Set(selectedExecution.value.nodes.map((node) => node.id))
}

const collapseAllNodes = () => {
  expandedNodeIds.value = new Set()
}

const selectNode = (nodeId: number) => {
  selectedNodeId.value = nodeId
}

onMounted(async () => {
  await refreshAll()
})

onBeforeUnmount(() => {
  stopExecutionPolling()
})
</script>

<template>
  <div class="page-shell">
    <section class="hero-card">
      <div>
        <p class="eyebrow">RepoSync</p>
        <h1>Git 镜像同步控制台</h1>
        <p class="hero-copy">
          当前版本已经覆盖任务管理、真实 mirror 执行、自动建仓、递归子模块同步、定时调度和 Webhook 鉴权。
          这一版页面重点把调度状态和递归执行树展示出来，方便判断同步链路是否健康。
        </p>
      </div>
      <div class="stats-grid">
        <div class="stat-card">
          <strong>{{ taskSummary.total }}</strong>
          <span>任务总数</span>
        </div>
        <div class="stat-card">
          <strong>{{ taskSummary.enabled }}</strong>
          <span>启用任务</span>
        </div>
        <div class="stat-card">
          <strong>{{ taskSummary.recursive }}</strong>
          <span>递归任务</span>
        </div>
        <div class="stat-card">
          <strong>{{ taskSummary.scheduled }}</strong>
          <span>启用调度</span>
        </div>
      </div>
    </section>

    <el-tabs class="workspace-tabs">
      <el-tab-pane label="任务">
        <div class="content-grid">
          <el-card shadow="never" class="panel-card">
            <template #header>
              <div class="panel-header">
                <span>任务表单</span>
                <el-button text @click="resetTaskForm">清空</el-button>
              </div>
            </template>
            <el-form label-position="top">
              <el-form-item label="任务名称">
                <el-input v-model="taskForm.name" />
              </el-form-item>
              <el-form-item label="源仓库 URL">
                <el-input v-model="taskForm.sourceRepoUrl" placeholder="git@github.com:org/repo.git" />
              </el-form-item>
              <el-form-item label="目标仓库 URL">
                <el-input v-model="taskForm.targetRepoUrl" placeholder="git@gogs.example.com:mirror/repo.git" />
              </el-form-item>
              <div class="two-column">
                <el-form-item label="源凭证">
                  <el-select v-model="taskForm.sourceCredentialId" clearable>
                    <el-option v-for="item in credentials" :key="item.id" :label="item.name" :value="item.id" />
                  </el-select>
                </el-form-item>
                <el-form-item label="目标凭证">
                  <el-select v-model="taskForm.targetCredentialId" clearable>
                    <el-option v-for="item in credentials" :key="item.id" :label="item.name" :value="item.id" />
                  </el-select>
                </el-form-item>
              </div>
              <div class="two-column">
                <el-form-item label="目标平台">
                  <el-select v-model="taskForm.providerConfig!.provider">
                    <el-option label="GitHub" value="github" />
                    <el-option label="Gogs" value="gogs" />
                  </el-select>
                </el-form-item>
                <el-form-item label="命名空间">
                  <el-input v-model="taskForm.providerConfig!.namespace" />
                </el-form-item>
              </div>
              <div class="two-column">
                <el-form-item label="可见性">
                  <el-select v-model="taskForm.providerConfig!.visibility">
                    <el-option label="Private" value="private" />
                    <el-option label="Public" value="public" />
                  </el-select>
                </el-form-item>
                <el-form-item label="API Base URL">
                  <el-input v-model="taskForm.providerConfig!.baseApiUrl" placeholder="Optional" />
                </el-form-item>
              </div>
              <el-form-item label="描述模板">
                <el-input v-model="taskForm.providerConfig!.descriptionTemplate" />
              </el-form-item>
              <div class="two-column">
                <el-form-item label="Cron">
                  <el-input v-model="taskForm.triggerConfig!.cron" />
                </el-form-item>
                <el-form-item label="Branch Reference">
                  <el-input v-model="taskForm.triggerConfig!.branchReference" />
                </el-form-item>
              </div>
              <el-form-item label="Webhook Secret">
                <el-input v-model="taskForm.triggerConfig!.webhookSecret" />
              </el-form-item>
              <div class="flag-row">
                <el-switch v-model="taskForm.enabled" active-text="启用任务" />
                <el-switch v-model="taskForm.recursiveSubmodules" active-text="递归子模块" />
                <el-switch v-model="taskForm.syncAllRefs" active-text="镜像全部 refs" />
                <el-switch v-model="taskForm.triggerConfig!.enableSchedule" active-text="启用定时" />
                <el-switch v-model="taskForm.triggerConfig!.enableWebhook" active-text="启用 Webhook" />
              </div>
              <div class="task-trigger-preview">
                <div class="panel-header">
                  <strong>触发配置预览</strong>
                  <span class="muted-text mono">{{ taskFormWebhookPreview }}</span>
                </div>
                <div class="trigger-grid">
                  <div v-for="item in taskFormTriggerCards" :key="item.label" class="trigger-card">
                    <span>{{ item.label }}</span>
                    <strong class="mono">{{ item.value }}</strong>
                  </div>
                </div>
                <p class="preview-copy">
                  Webhook 当前只处理 push 事件；如果配置了 Branch Reference，则仅接受匹配该 ref 的请求。
                </p>
              </div>
              <el-button type="primary" @click="saveTask">保存任务</el-button>
            </el-form>
          </el-card>

          <el-card shadow="never" class="panel-card">
            <template #header>
              <div class="panel-header">
                <span>任务列表</span>
                <el-button text :loading="loading" @click="refreshAll">刷新</el-button>
              </div>
            </template>
            <el-table :data="tasks" height="620">
              <el-table-column prop="name" label="任务" min-width="180" />
              <el-table-column label="同步" width="120">
                <template #default="{ row }">
                  <el-tag :type="row.recursiveSubmodules ? 'success' : 'info'">
                    {{ row.recursiveSubmodules ? '递归' : '单仓库' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="调度 / Webhook" min-width="220">
                <template #default="{ row }">
                  <div class="trigger-stack">
                    <span class="mono muted-text">{{ taskTriggerSummary(row) }}</span>
                    <div class="trigger-tags">
                      <el-tag size="small" effect="plain">{{ taskScheduleState(row) }}</el-tag>
                      <el-tag size="small" effect="plain">{{ taskWebhookState(row) }}</el-tag>
                      <el-tag size="small" effect="plain">ref {{ taskBranchState(row) }}</el-tag>
                    </div>
                  </div>
                </template>
              </el-table-column>
              <el-table-column label="最近执行" width="130">
                <template #default="{ row }">
                  <el-tag :type="taskStatusType(row.lastExecutionStatus)">
                    {{ row.lastExecutionStatus || 'none' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="仓库 / 建仓" width="120">
                <template #default="{ row }">
                  {{ row.lastExecutionRepoCount || 0 }} / {{ row.lastCreatedRepoCount || 0 }}
                </template>
              </el-table-column>
              <el-table-column label="操作" width="260">
                <template #default="{ row }">
                  <div class="action-row">
                    <el-button size="small" @click="editTask(row)">编辑</el-button>
                    <el-button size="small" type="primary" @click="runTask(row)">执行</el-button>
                    <el-button size="small" @click="loadExecutions(row.id)">历史</el-button>
                    <el-button size="small" type="danger" @click="removeTask(row)">删除</el-button>
                  </div>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="凭证">
        <div class="content-grid">
          <el-card shadow="never" class="panel-card">
            <template #header>
              <div class="panel-header">
                <span>凭证表单</span>
                <el-button text @click="resetCredentialForm">清空</el-button>
              </div>
            </template>
            <el-form label-position="top">
              <el-form-item label="名称">
                <el-input v-model="credentialForm.name" />
              </el-form-item>
              <div class="two-column">
                <el-form-item label="类型">
                  <el-select v-model="credentialForm.type">
                    <el-option label="API Token" value="api_token" />
                    <el-option label="HTTPS Token" value="https_token" />
                    <el-option label="SSH Key" value="ssh_key" />
                  </el-select>
                </el-form-item>
                <el-form-item label="用户名">
                  <el-input v-model="credentialForm.username" />
                </el-form-item>
              </div>
              <el-form-item label="Secret">
                <el-input v-model="credentialForm.secret" type="textarea" :rows="4" />
              </el-form-item>
              <el-form-item label="Scope">
                <el-input v-model="credentialForm.scope" />
              </el-form-item>
              <el-button type="primary" @click="saveCredential">保存凭证</el-button>
            </el-form>
          </el-card>

          <el-card shadow="never" class="panel-card">
            <template #header>
              <span>凭证列表</span>
            </template>
            <el-table :data="credentials" height="620">
              <el-table-column prop="name" label="名称" min-width="180" />
              <el-table-column prop="type" label="类型" width="140" />
              <el-table-column prop="scope" label="用途" min-width="180" />
              <el-table-column prop="secretMasked" label="脱敏值" min-width="160" />
              <el-table-column label="操作" width="180">
                <template #default="{ row }">
                  <div class="action-row">
                    <el-button size="small" @click="editCredential(row)">编辑</el-button>
                    <el-button size="small" type="danger" @click="removeCredential(row)">删除</el-button>
                  </div>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
        </div>
      </el-tab-pane>

      <el-tab-pane label="缓存">
        <el-card shadow="never" class="panel-card">
          <template #header>
            <div class="panel-header">
              <span>缓存列表</span>
              <el-button text @click="loadCaches">刷新</el-button>
            </div>
          </template>
          <el-table :data="caches" height="620">
            <el-table-column prop="sourceRepoUrl" label="源仓库" min-width="280" />
            <el-table-column prop="cachePath" label="缓存路径" min-width="260" />
            <el-table-column prop="healthStatus" label="健康状态" width="120" />
            <el-table-column prop="hitCount" label="命中次数" width="100" />
            <el-table-column label="操作" width="120">
              <template #default="{ row }">
                <el-button size="small" type="danger" @click="cleanupCache(row)">清理</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="执行记录">
        <div class="content-grid">
          <el-card shadow="never" class="panel-card">
            <template #header>
              <div class="panel-header">
                <span>执行历史</span>
                <span class="muted-text">
                  {{ executionTaskId ? `任务 #${executionTaskId}` : '先从任务页选择一个任务' }}
                </span>
              </div>
            </template>
            <el-table :data="executions" height="620">
              <el-table-column label="状态" width="100">
                <template #default="{ row }">
                  <el-tag :type="taskStatusType(row.status)">{{ row.status }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="triggerType" label="触发方式" width="110" />
              <el-table-column prop="repoCount" label="仓库数" width="90" />
              <el-table-column prop="createdRepoCount" label="建仓数" width="90" />
              <el-table-column prop="failedNodeCount" label="失败节点" width="100" />
              <el-table-column prop="startedAt" label="开始时间" min-width="180" />
              <el-table-column label="操作" width="120">
                <template #default="{ row }">
                  <el-button size="small" @click="openExecution(row)">详情</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-card>

          <el-card shadow="never" class="panel-card">
            <template #header>
              <span>执行详情</span>
            </template>
            <div v-if="selectedExecution" class="detail-stack">
              <div class="detail-block">
                <strong>{{ selectedExecution.task.name }}</strong>
                <p>
                  {{ selectedExecution.execution.status === 'running' ? '执行中，日志每 2 秒自动刷新。' : '执行结束，以下为本次摘要日志。' }}
                </p>
                <div class="summary-tags">
                  <el-tag size="small">trigger: {{ selectedExecution.execution.triggerType }}</el-tag>
                  <el-tag size="small">repos: {{ selectedExecution.execution.repoCount }}</el-tag>
                  <el-tag size="small">created: {{ selectedExecution.execution.createdRepoCount }}</el-tag>
                </div>
              </div>

              <div class="detail-block log-panel">
                <div class="panel-header">
                  <strong>执行日志</strong>
                  <el-tag size="small" :type="taskStatusType(selectedExecution.execution.status)">
                    {{ selectedExecution.execution.status }}
                  </el-tag>
                </div>
                <pre class="log-output">{{ selectedExecution.execution.summaryLog || '等待日志输出...' }}</pre>
              </div>

              <div class="status-grid">
                <div v-for="item in selectedExecutionStats" :key="item.label" class="status-card">
                  <span>{{ item.label }}</span>
                  <strong>{{ item.value }}</strong>
                </div>
              </div>

              <div class="detail-block trigger-panel">
                <div class="panel-header">
                  <strong>触发状态面板</strong>
                  <div class="action-row">
                    <el-button size="small" @click="expandAllNodes">展开全部</el-button>
                    <el-button size="small" @click="collapseAllNodes">收起全部</el-button>
                    <el-switch v-model="errorOnly" active-text="只看失败节点" />
                  </div>
                </div>
                <div class="trigger-grid">
                  <div class="trigger-card">
                    <span>Schedule</span>
                    <strong>{{ taskScheduleState(selectedExecution.task) }}</strong>
                  </div>
                  <div class="trigger-card">
                    <span>Webhook</span>
                    <strong>{{ taskWebhookState(selectedExecution.task) }}</strong>
                  </div>
                  <div class="trigger-card">
                    <span>Branch Filter</span>
                    <strong class="mono">{{ taskBranchState(selectedExecution.task) }}</strong>
                  </div>
                  <div class="trigger-card">
                    <span>Mirror Scope</span>
                    <strong>{{ selectedExecution.task.syncAllRefs ? 'all refs' : 'custom' }}</strong>
                  </div>
                </div>
              </div>

              <div class="execution-layout">
                <div class="tree-list">
                  <div
                    v-for="node in executionTreeRows"
                    :key="node.id"
                    class="tree-row"
                    :class="{ 'tree-row-active': selectedNodeId === node.id }"
                    @click="selectNode(node.id)"
                  >
                    <div class="tree-left">
                      <div class="tree-indent" :style="{ '--tree-level': String(node.level) }"></div>
                      <div class="tree-node">
                        <div class="tree-title">
                          <button
                            v-if="node.hasChildren"
                            class="tree-toggle"
                            type="button"
                            @click.stop="toggleNode(node.id)"
                          >
                            {{ node.expanded ? '−' : '+' }}
                          </button>
                          <span v-else class="tree-toggle tree-toggle-placeholder">·</span>
                          <span class="mono">{{ node.label }}</span>
                          <el-tag size="small" :type="taskStatusType(node.status)">{{ node.status }}</el-tag>
                        </div>
                        <div class="tree-meta">
                          <span>depth {{ node.depth }}</span>
                          <span>cache {{ node.cacheHit ? 'hit' : 'miss' }}</span>
                          <span>auto-create {{ node.autoCreated ? 'yes' : 'no' }}</span>
                          <span>create {{ node.createDurationMs }}ms</span>
                          <span>fetch {{ node.fetchDurationMs }}ms</span>
                          <span>push {{ node.pushDurationMs }}ms</span>
                        </div>
                        <div class="tree-paths mono">
                          <div>src: {{ node.sourceRepoUrl }}</div>
                          <div>dst: {{ node.targetRepoUrl }}</div>
                          <div v-if="node.referenceCommit">ref: {{ node.referenceCommit }}</div>
                          <div v-if="node.errorMessage" class="error-text">error: {{ node.errorMessage }}</div>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
                <div v-if="selectedExecutionNode" class="node-detail-card">
                  <div class="panel-header">
                    <strong>节点详情</strong>
                    <el-tag size="small" :type="taskStatusType(selectedExecutionNode.status)">
                      {{ selectedExecutionNode.status }}
                    </el-tag>
                  </div>
                  <div class="node-detail-grid">
                    <div class="node-detail-item">
                      <span>路径</span>
                      <strong class="mono">{{ selectedExecutionNode.repoPath || '(root)' }}</strong>
                    </div>
                    <div class="node-detail-item">
                      <span>深度</span>
                      <strong>{{ selectedExecutionNode.depth }}</strong>
                    </div>
                    <div class="node-detail-item">
                      <span>缓存</span>
                      <strong>{{ selectedExecutionNode.cacheHit ? 'hit' : 'miss' }}</strong>
                    </div>
                    <div class="node-detail-item">
                      <span>自动建仓</span>
                      <strong>{{ selectedExecutionNode.autoCreated ? 'yes' : 'no' }}</strong>
                    </div>
                  </div>
                  <div class="tree-paths mono">
                    <div>src: {{ selectedExecutionNode.sourceRepoUrl }}</div>
                    <div>dst: {{ selectedExecutionNode.targetRepoUrl }}</div>
                    <div v-if="selectedExecutionNode.referenceCommit">ref: {{ selectedExecutionNode.referenceCommit }}</div>
                    <div>create: {{ selectedExecutionNode.createDurationMs }}ms</div>
                    <div>fetch: {{ selectedExecutionNode.fetchDurationMs }}ms</div>
                    <div>push: {{ selectedExecutionNode.pushDurationMs }}ms</div>
                    <div v-if="selectedExecutionNode.errorMessage" class="error-text">
                      error: {{ selectedExecutionNode.errorMessage }}
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <el-empty v-else description="暂无执行详情" />
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>
