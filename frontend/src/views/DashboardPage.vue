<script setup lang="ts">
import axios from 'axios'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import type { ElTree } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { api } from '../api'
import type { Credential, ExecutionDetail, RepoCache, SyncExecution, SyncExecutionNode, SyncTask, TaskType, WebhookEvent } from '../types'

const tasks = ref<SyncTask[]>([])
const credentials = ref<Credential[]>([])
const caches = ref<RepoCache[]>([])
const executions = ref<SyncExecution[]>([])
const webhookEvents = ref<WebhookEvent[]>([])
type WorkspaceTab = 'tasks' | 'credentials' | 'caches'
const activeWorkspaceTab = ref<WorkspaceTab>('tasks')
const selectedExecution = ref<ExecutionDetail | null>(null)
const loading = ref(false)
const taskDialogVisible = ref(false)
const credentialDialogVisible = ref(false)
const executionHistoryVisible = ref(false)
const executionDetailVisible = ref(false)
const executionDetailLoading = ref(false)
const executionTaskId = ref<number | null>(null)
const runningTaskId = ref<number | null>(null)
const expandedNodeIds = ref<Set<number>>(new Set())
const errorOnly = ref(false)
const selectedNodeId = ref<number | null>(null)
const webhookStatusFilter = ref<'all' | 'accepted' | 'ignored' | 'rejected' | 'failed' | 'blocked'>('all')
const TASK_LIST_REFRESH_MS = 15000
let executionStream: EventSource | null = null
let executionSocket: WebSocket | null = null
let taskListRefreshTimer: number | null = null
const executionTreeRef = ref<InstanceType<typeof ElTree>>()
const taskFormRef = ref<FormInstance>()
const credentialSecretMasked = ref('')
const credentialSecretOriginal = ref('')
const credentialSecretDirty = ref(false)
const workspaceScrollTop = reactive<Record<WorkspaceTab, number>>({
  tasks: 0,
  credentials: 0,
  caches: 0,
})

const emptyTask = (): Partial<SyncTask> => ({
  id: undefined,
  taskType: 'git_mirror',
  name: '',
  sourceRepoUrl: '',
  targetRepoUrl: '',
  cacheBasePath: '',
  enabled: true,
  recursiveSubmodules: true,
  syncAllRefs: true,
  submoduleSourceCredentialId: undefined,
  submoduleTargetCredentialId: undefined,
  targetApiCredentialId: undefined,
  submoduleTargetApiCredentialId: undefined,
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
  svnConfig: {
    trunkPath: 'trunk',
    branchesPath: 'branches',
    tagsPath: 'tags',
    authorsFilePath: '',
  },
})

const emptyCredential = (): Partial<Credential> => ({
  id: undefined,
  name: '',
  type: 'api_token',
  username: '',
  secret: '',
  scope: '',
})

const taskForm = reactive<Partial<SyncTask>>(emptyTask())
const credentialForm = reactive<Partial<Credential>>(emptyCredential())
const taskTypeOptions: Array<{ label: string; value: TaskType }> = [
  { label: 'Git Mirror', value: 'git_mirror' },
  { label: 'SVN Import', value: 'svn_import' },
]
const taskFormIsSVNImport = computed(() => taskForm.taskType === 'svn_import')

const repositoryValueValidator = (_rule: unknown, value: string, callback: (error?: Error) => void) => {
  const trimmed = value?.trim()
  if (!trimmed) {
    callback(new Error('该字段不能为空'))
    return
  }
  const looksLikeRepo =
    /^(https?:\/\/|ssh:\/\/|git@|file:\/\/)/.test(trimmed) ||
    /^[A-Za-z]:\\/.test(trimmed) ||
    trimmed.startsWith('/') ||
    trimmed.startsWith('./') ||
    trimmed.startsWith('../')
  if (!looksLikeRepo) {
    callback(new Error('请输入有效的仓库地址或本地路径'))
    return
  }
  callback()
}

const taskTargetValidator = (_rule: unknown, value: string, callback: (error?: Error) => void) => {
  repositoryValueValidator(_rule, value, (error) => {
    if (error) {
      callback(error)
      return
    }
    if (value?.trim() === taskForm.sourceRepoUrl?.trim()) {
      callback(new Error('目标仓库不能与源仓库相同'))
      return
    }
    callback()
  })
}

const conditionalCronValidator = (_rule: unknown, value: string, callback: (error?: Error) => void) => {
  if (taskForm.triggerConfig?.enableSchedule && !value?.trim()) {
    callback(new Error('启用定时后必须填写 Cron'))
    return
  }
  callback()
}

const conditionalWebhookSecretValidator = (_rule: unknown, value: string, callback: (error?: Error) => void) => {
  if (taskForm.triggerConfig?.enableWebhook && !value?.trim()) {
    callback(new Error('启用 Webhook 后必须填写 Secret'))
    return
  }
  callback()
}

const taskFormRules = reactive<FormRules>({
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
  sourceRepoUrl: [{ validator: repositoryValueValidator, trigger: 'blur' }],
  targetRepoUrl: [{ validator: taskTargetValidator, trigger: 'blur' }],
  'triggerConfig.cron': [{ validator: conditionalCronValidator, trigger: 'blur' }],
  'triggerConfig.webhookSecret': [{ validator: conditionalWebhookSecretValidator, trigger: 'blur' }],
})

const taskSummary = computed(() => ({
  total: tasks.value.length,
  enabled: tasks.value.filter((item) => item.enabled).length,
  recursive: tasks.value.filter((item) => item.recursiveSubmodules).length,
  scheduled: tasks.value.filter((item) => item.triggerConfig.enableSchedule).length,
  running: tasks.value.filter((item) => item.lastExecutionStatus === 'running').length,
  svnImports: tasks.value.filter((item) => item.taskType === 'svn_import').length,
}))

const taskDialogTitle = computed(() => (taskForm.id ? '编辑任务' : '新增任务'))
const credentialDialogTitle = computed(() => (credentialForm.id ? '编辑凭证' : '新增凭证'))
const executionHistoryTitle = computed(() => {
  if (executionTaskId.value == null) {
    return '执行历史'
  }
  const task = tasks.value.find((item) => item.id === executionTaskId.value)
  return task ? `${task.name} · 执行历史` : `任务 #${executionTaskId.value} · 执行历史`
})

const taskFormTriggerCards = computed(() => [
  {
    label: '任务类型',
    value: taskFormIsSVNImport.value ? 'SVN Import' : 'Git Mirror',
  },
  {
    label: '定时计划',
    value: taskForm.triggerConfig?.enableSchedule ? taskForm.triggerConfig.cron || '已启用' : '未启用',
  },
  {
    label: 'Webhook',
    value: taskForm.triggerConfig?.enableWebhook ? (taskForm.triggerConfig.webhookSecret ? '已签名' : '未签名') : '未启用',
  },
  {
    label: '分支过滤',
    value: taskForm.triggerConfig?.branchReference || '未限制',
  },
  {
    label: taskFormIsSVNImport.value ? 'SVN 布局' : '镜像范围',
    value: taskFormIsSVNImport.value
      ? `${taskForm.svnConfig?.trunkPath || 'trunk'} / ${taskForm.svnConfig?.branchesPath || 'branches'} / ${taskForm.svnConfig?.tagsPath || 'tags'}`
      : taskForm.syncAllRefs ? '全部 refs' : '自定义',
  },
])

const taskFormWebhookPreview = computed(() => {
  if (taskFormIsSVNImport.value) {
    return 'SVN Import 不支持 Webhook'
  }
  const provider = taskForm.providerConfig?.provider || 'github'
  const taskId = taskForm.id ?? ':taskId'
  return `/api/webhooks/${provider}/${taskId}`
})

const taskTypeLabel = (task: Pick<SyncTask, 'taskType'>) => task.taskType === 'svn_import' ? 'SVN Import' : 'Git Mirror'

type ExecutionTreeNode = SyncExecutionNode & {
  label: string
  children: ExecutionTreeNode[]
}

const executionTreeData = computed<ExecutionTreeNode[]>(() => {
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

  const build = (parentId: number | null): ExecutionTreeNode[] => {
    const children = (childrenByParent.get(parentId) ?? []).sort((left, right) => left.repoPath.localeCompare(right.repoPath))
    const result: ExecutionTreeNode[] = []
    children.forEach((node) => {
      const descendants = build(node.id)
      const matchesSelf = node.status === 'failed' || !!node.errorMessage
      if (!errorOnly.value || matchesSelf || descendants.length > 0) {
        result.push({
          ...node,
          label: node.repoPath || '(root)',
          children: descendants,
        })
      }
    })
    return result
  }
  return build(null)
})

const selectedExecutionStats = computed(() => {
  if (!selectedExecution.value) {
    return []
  }
  const nodes = selectedExecution.value.nodes
  return [
    { label: '触发', value: String(selectedExecution.value.execution.triggerType) },
    { label: '仓库', value: String(selectedExecution.value.execution.repoCount) },
    { label: '建仓', value: String(selectedExecution.value.execution.createdRepoCount) },
    { label: '缓存命中', value: String(nodes.filter((node) => node.cacheHit).length) },
    { label: '总节点', value: String(nodes.length) },
    { label: '失败节点', value: String(nodes.filter((node) => node.status === 'failed').length) },
  ]
})

const selectedExecutionNode = computed(() => {
  if (!selectedExecution.value || selectedNodeId.value == null) {
    return null
  }
  return selectedExecution.value.nodes.find((node) => node.id === selectedNodeId.value) ?? null
})

const filteredWebhookEvents = computed(() => {
  if (webhookStatusFilter.value === 'all') {
    return webhookEvents.value
  }
  return webhookEvents.value.filter((item) => item.status === webhookStatusFilter.value)
})

const webhookStatusCards = computed(() => {
  const counts = {
    accepted: 0,
    ignored: 0,
    rejected: 0,
    failed: 0,
    blocked: 0,
  }
  for (const item of webhookEvents.value) {
    if (item.status in counts) {
      counts[item.status as keyof typeof counts] += 1
    }
  }
  return [
    { label: '已接受', value: String(counts.accepted), status: 'accepted' },
    { label: '已忽略', value: String(counts.ignored), status: 'ignored' },
    { label: '已拒绝', value: String(counts.rejected), status: 'rejected' },
    { label: '失败', value: String(counts.failed), status: 'failed' },
    { label: '已阻止', value: String(counts.blocked), status: 'blocked' },
  ]
})

const latestIgnoredWebhook = computed(() => webhookEvents.value.find((item) => item.status === 'ignored') ?? null)

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
  executions.value = (await api.listExecutions(taskId)) ?? []
}

const loadWebhookEvents = async (taskId: number) => {
  webhookEvents.value = (await api.listWebhookEvents(taskId)) ?? []
}

const getErrorMessage = (error: unknown, fallback: string) => {
  if (axios.isAxiosError(error)) {
    const payloadMessage = (error.response?.data as { error?: string; message?: string } | undefined)?.error
      || (error.response?.data as { error?: string; message?: string } | undefined)?.message
    return payloadMessage || error.message || fallback
  }
  if (error instanceof Error && error.message) {
    return error.message
  }
  return fallback
}

const stopExecutionStreaming = () => {
  if (executionSocket) {
    executionSocket.onclose = null
    executionSocket.onerror = null
    executionSocket.onmessage = null
    executionSocket.close()
    executionSocket = null
  }
  if (executionStream) {
    executionStream.onerror = null
    executionStream.close()
    executionStream = null
  }
}

const applyExecutionDetail = async (detail: ExecutionDetail) => {
  selectedExecution.value = detail
  if (selectedNodeId.value == null) {
    selectedNodeId.value = detail.nodes[0]?.id ?? null
  }
  await nextTick()
  syncTreeExpansion()
  if (detail.execution.status !== 'running') {
    stopExecutionStreaming()
    if (executionTaskId.value != null) {
      await loadExecutions(executionTaskId.value)
    }
    await Promise.all([loadTasks(), loadCaches()])
  }
}

const refreshSelectedExecution = async () => {
  if (!selectedExecution.value) {
    return
  }
  await applyExecutionDetail(await api.executionDetail(selectedExecution.value.execution.id))
}

const startEventSourceExecutionStream = () => {
  if (!selectedExecution.value || selectedExecution.value.execution.status !== 'running') {
    return
  }
  executionStream = new EventSource(api.executionStreamUrl(selectedExecution.value.execution.id))
  executionStream.addEventListener('execution', (event) => {
    const message = event as MessageEvent<string>
    const payload = JSON.parse(message.data) as { detail?: ExecutionDetail }
    if (payload.detail) {
      void applyExecutionDetail(payload.detail)
      return
    }
    void applyExecutionDetail(JSON.parse(message.data) as ExecutionDetail)
  })
  executionStream.onerror = () => {
    stopExecutionStreaming()
    void refreshSelectedExecution()
  }
}

const ensureExecutionStreaming = () => {
  stopExecutionStreaming()
  if (!selectedExecution.value || selectedExecution.value.execution.status !== 'running') {
    return
  }
  let fallbackStarted = false
  const startFallback = () => {
    if (fallbackStarted || !selectedExecution.value || selectedExecution.value.execution.status !== 'running') {
      return
    }
    fallbackStarted = true
    if (executionSocket) {
      executionSocket.onclose = null
      executionSocket.onerror = null
      executionSocket.onmessage = null
      executionSocket.close()
      executionSocket = null
    }
    startEventSourceExecutionStream()
  }
  try {
    executionSocket = new WebSocket(api.executionWebSocketUrl(selectedExecution.value.execution.id))
    executionSocket.onmessage = (event) => {
      const payload = JSON.parse(event.data) as { detail?: ExecutionDetail }
      if (payload.detail) {
        void applyExecutionDetail(payload.detail)
      }
    }
    executionSocket.onerror = () => {
      startFallback()
    }
    executionSocket.onclose = () => {
      if (selectedExecution.value?.execution.status === 'running') {
        startFallback()
      }
    }
  } catch (_error) {
    startEventSourceExecutionStream()
  }
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
  for (const key of Object.keys(taskForm)) {
    delete (taskForm as Record<string, unknown>)[key]
  }
  Object.assign(taskForm, emptyTask())
}

const resetCredentialForm = () => {
  for (const key of Object.keys(credentialForm)) {
    delete (credentialForm as Record<string, unknown>)[key]
  }
  Object.assign(credentialForm, emptyCredential())
  credentialSecretMasked.value = ''
  credentialSecretOriginal.value = ''
  credentialSecretDirty.value = false
}

const openCreateTask = () => {
  resetTaskForm()
  taskDialogVisible.value = true
  void nextTick(() => taskFormRef.value?.clearValidate())
}

const openCreateCredential = () => {
  resetCredentialForm()
  credentialDialogVisible.value = true
}

const saveTask = async () => {
  const valid = await taskFormRef.value?.validate().catch(() => false)
  if (!valid) {
    ElMessage.warning('请先修正任务表单中的必填项')
    return
  }
  try {
    await api.saveTask(taskForm)
    ElMessage.success('任务已保存')
    resetTaskForm()
    taskDialogVisible.value = false
    taskFormRef.value?.clearValidate()
    await refreshAll()
  } catch (error) {
    ElMessage.error(`保存失败：${getErrorMessage(error, '无法保存任务')}`)
  }
}

const editTask = (task: SyncTask) => {
  resetTaskForm()
  Object.assign(taskForm, JSON.parse(JSON.stringify(task)))
  taskForm.taskType = taskForm.taskType || 'git_mirror'
  taskForm.svnConfig = {
    trunkPath: taskForm.svnConfig?.trunkPath || 'trunk',
    branchesPath: taskForm.svnConfig?.branchesPath || 'branches',
    tagsPath: taskForm.svnConfig?.tagsPath || 'tags',
    authorsFilePath: taskForm.svnConfig?.authorsFilePath || '',
  }
  taskDialogVisible.value = true
  void nextTick(() => taskFormRef.value?.clearValidate())
}

const removeTask = async (task: SyncTask) => {
  await api.deleteTask(task.id)
  tasks.value = tasks.value.filter((item) => item.id !== task.id)
  ElMessage.success('任务已删除')
  if (executionTaskId.value === task.id) {
    executionTaskId.value = null
    executions.value = []
    webhookEvents.value = []
    selectedExecution.value = null
    executionHistoryVisible.value = false
    executionDetailVisible.value = false
    stopExecutionStreaming()
  }
  await refreshAll()
}

const refreshTaskListIfVisible = async () => {
  if (document.visibilityState !== 'visible' || loading.value) {
    return
  }
  await loadTasks()
}

const startTaskListAutoRefresh = () => {
  if (taskListRefreshTimer != null) {
    window.clearInterval(taskListRefreshTimer)
  }
  taskListRefreshTimer = window.setInterval(() => {
    void refreshTaskListIfVisible()
  }, TASK_LIST_REFRESH_MS)
}

const stopTaskListAutoRefresh = () => {
  if (taskListRefreshTimer != null) {
    window.clearInterval(taskListRefreshTimer)
    taskListRefreshTimer = null
  }
}

const handlePageVisibilityChange = () => {
  if (document.visibilityState === 'visible') {
    void refreshTaskListIfVisible()
  }
}

watch(activeWorkspaceTab, async (nextTab, previousTab) => {
  if (previousTab) {
    workspaceScrollTop[previousTab] = window.scrollY
  }
  await nextTick()
  window.scrollTo({ top: workspaceScrollTop[nextTab] ?? 0, behavior: 'auto' })
})

watch(
  () => taskForm.taskType,
  (taskType) => {
    if (taskType === 'svn_import') {
      taskForm.recursiveSubmodules = false
      taskForm.triggerConfig!.enableWebhook = false
      taskForm.syncAllRefs = true
      taskForm.svnConfig = {
        trunkPath: taskForm.svnConfig?.trunkPath || 'trunk',
        branchesPath: taskForm.svnConfig?.branchesPath || 'branches',
        tagsPath: taskForm.svnConfig?.tagsPath || 'tags',
        authorsFilePath: taskForm.svnConfig?.authorsFilePath || '',
      }
      return
    }
    taskForm.svnConfig = {
      trunkPath: taskForm.svnConfig?.trunkPath || 'trunk',
      branchesPath: taskForm.svnConfig?.branchesPath || 'branches',
      tagsPath: taskForm.svnConfig?.tagsPath || 'tags',
      authorsFilePath: taskForm.svnConfig?.authorsFilePath || '',
    }
  },
)

const runTask = async (task: SyncTask) => {
  runningTaskId.value = task.id
  executionDetailVisible.value = true
  executionDetailLoading.value = true
  selectedExecution.value = null
  selectedNodeId.value = null
  expandedNodeIds.value = new Set()
  try {
    const execution = await api.runTask(task.id)
    ElMessage.success('任务已触发')
    await openExecutionLive(task, execution)
    await Promise.all([
      refreshAll(),
      loadExecutions(task.id),
      loadWebhookEvents(task.id),
    ])
  } catch (error) {
    executionDetailVisible.value = false
    ElMessage.error(`执行失败：${getErrorMessage(error, '无法启动任务')}`)
  } finally {
    runningTaskId.value = null
  }
}

const openTaskHistory = async (taskId: number) => {
  await Promise.all([loadExecutions(taskId), loadWebhookEvents(taskId)])
  executionHistoryVisible.value = true
}

const openExecutionByID = async (executionId?: number) => {
  if (!executionId) {
    return
  }
  const execution = executions.value.find((item) => item.id === executionId)
  if (execution) {
    await openExecution(execution)
    return
  }
  await openExecution({
    id: executionId,
    taskId: executionTaskId.value ?? 0,
    triggerType: 'webhook',
    status: 'running',
    startedAt: new Date().toISOString(),
    repoCount: 0,
    createdRepoCount: 0,
    failedNodeCount: 0,
    summaryLog: '',
  })
}

const saveCredential = async () => {
  const payload: Partial<Credential> = { ...credentialForm }
  if (payload.id && !credentialSecretDirty.value) {
    payload.secret = credentialSecretOriginal.value
  }
  await api.saveCredential(payload)
  ElMessage.success('凭证已保存')
  resetCredentialForm()
  credentialDialogVisible.value = false
  await loadCredentials()
}

const editCredential = (credential: Credential) => {
  credentialSecretMasked.value = credential.secretMasked || ''
  credentialSecretOriginal.value = credential.secret || ''
  credentialSecretDirty.value = false
  Object.assign(credentialForm, { ...credential, secret: credential.secretMasked || '' })
  credentialDialogVisible.value = true
}

const openExecutionLive = async (task: SyncTask, execution: SyncExecution) => {
  selectedExecution.value = {
    execution,
    task,
    nodes: [],
  }
  expandedNodeIds.value = new Set()
  errorOnly.value = false
  selectedNodeId.value = null
  executionDetailVisible.value = true
  executionDetailLoading.value = false
  await nextTick()
  ensureExecutionStreaming()
  void refreshSelectedExecution()
}

const prepareCredentialSecretEdit = () => {
  if (!credentialForm.id || credentialSecretDirty.value) {
    return
  }
  credentialSecretDirty.value = true
  credentialForm.secret = ''
}

const onCredentialSecretInput = () => {
  if (credentialForm.id && !credentialSecretDirty.value) {
    credentialSecretDirty.value = true
    if (credentialForm.secret === credentialSecretMasked.value) {
      credentialForm.secret = ''
    }
  }
}

const removeCredential = async (credential: Credential) => {
  await api.deleteCredential(credential.id)
  ElMessage.success('凭证已删除')
  await loadCredentials()
}

const openExecution = async (execution: SyncExecution) => {
  executionDetailLoading.value = true
  selectedExecution.value = await api.executionDetail(execution.id)
  expandedNodeIds.value = new Set(selectedExecution.value.nodes.map((node) => node.id))
  errorOnly.value = false
  selectedNodeId.value = selectedExecution.value.nodes[0]?.id ?? null
  executionDetailVisible.value = true
  await nextTick()
  syncTreeExpansion()
  ensureExecutionStreaming()
  executionDetailLoading.value = false
}

const cleanupCache = async (cache: RepoCache) => {
  await api.cleanupCache(cache.id)
  ElMessage.success('缓存已清理')
  await loadCaches()
}

const replayWebhookEvent = async (event: WebhookEvent) => {
  if (executionTaskId.value == null) {
    return
  }
  const execution = await api.replayWebhookEvent(executionTaskId.value, event.id)
  ElMessage.success('Webhook 记录已重放')
  await loadWebhookEvents(executionTaskId.value)
  await loadExecutions(executionTaskId.value)
  await openExecution(execution)
}

const closeExecutionDetail = () => {
  executionDetailVisible.value = false
  executionDetailLoading.value = false
  selectedExecution.value = null
  selectedNodeId.value = null
  expandedNodeIds.value = new Set()
  stopExecutionStreaming()
}

const exportWebhookEvents = () => {
  if (!filteredWebhookEvents.value.length) {
    ElMessage.warning('当前没有可导出的 Webhook 记录')
    return
  }
  const header = ['id', 'provider', 'eventType', 'ref', 'status', 'reason', 'executionId', 'createdAt']
  const rows = filteredWebhookEvents.value.map((item) => [
    item.id,
    item.provider,
    item.eventType,
    item.ref,
    item.status,
    (item.reason || '').replaceAll('"', '""'),
    item.executionId ?? '',
    item.createdAt,
  ])
  const csv = [header, ...rows]
    .map((row) => row.map((value) => `"${String(value ?? '')}"`).join(','))
    .join('\n')
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `reposync-webhook-events-${executionTaskId.value ?? 'task'}.csv`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

const taskCronDisplay = (task: SyncTask) => {
  if (!task.triggerConfig.enableSchedule) {
    return '-'
  }
  return task.scheduleCron || task.triggerConfig.cron || '-'
}

const taskNextRunDisplay = (task: SyncTask) => {
  if (!task.triggerConfig.enableSchedule) {
    return '-'
  }
  return formatEast8DateTime(task.nextRunAt)
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

const formatBytes = (value?: number) => {
  if (!value || value <= 0) {
    return '0 B'
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  const precision = unitIndex === 0 ? 0 : size >= 10 ? 1 : 2
  return `${size.toFixed(precision)} ${units[unitIndex]}`
}

const formatTaskListTime = (value?: string) => {
  if (!value) return '未执行'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

const formatEast8DateTime = (value?: string, empty = '-') => {
  if (!value) return empty
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

const webhookEventStatusType = (status?: string) => {
  if (status === 'accepted') return 'success'
  if (status === 'ignored') return 'warning'
  if (status === 'blocked') return 'info'
  return 'danger'
}

const webhookReasonLabel = (reason?: string) => {
  if (!reason) return '-'
  if (reason === 'branch does not match trigger config') return '分支与任务过滤条件不匹配'
  if (reason === 'unsupported github event' || reason === 'unsupported gogs event') return 'Webhook 事件类型未启用'
  if (reason === 'webhook is disabled for this task') return '任务未启用 Webhook'
  return reason
}

const expandAllNodes = () => {
  if (!selectedExecution.value) {
    return
  }
  expandedNodeIds.value = new Set(selectedExecution.value.nodes.map((node) => node.id))
  void nextTick().then(syncTreeExpansion)
}

const collapseAllNodes = () => {
  expandedNodeIds.value = new Set()
  void nextTick().then(syncTreeExpansion)
}

const selectNode = (node: SyncExecutionNode | number) => {
  selectedNodeId.value = typeof node === 'number' ? node : node.id
}

const syncTreeExpansion = () => {
  const tree = executionTreeRef.value
  if (!tree || !selectedExecution.value) {
    return
  }
  selectedExecution.value.nodes.forEach((node) => {
    tree.store.nodesMap[node.id]?.expand()
    if (!expandedNodeIds.value.has(node.id)) {
      tree.store.nodesMap[node.id]?.collapse()
    }
  })
}

const onTreeNodeClick = (node: ExecutionTreeNode) => {
  selectNode(node)
}

const onTreeNodeExpand = (node: ExecutionTreeNode) => {
  expandedNodeIds.value = new Set(expandedNodeIds.value).add(node.id)
}

const onTreeNodeCollapse = (node: ExecutionTreeNode) => {
  const next = new Set(expandedNodeIds.value)
  next.delete(node.id)
  expandedNodeIds.value = next
}

onMounted(async () => {
  await refreshAll()
  startTaskListAutoRefresh()
  document.addEventListener('visibilitychange', handlePageVisibilityChange)
})

onBeforeUnmount(() => {
  stopTaskListAutoRefresh()
  document.removeEventListener('visibilitychange', handlePageVisibilityChange)
  stopExecutionStreaming()
})
</script>

<template>
  <div class="page-shell">
    <section class="hero-card">
      <div>
        <p class="eyebrow">RepoSync</p>
        <h1>Git 镜像同步控制台</h1>
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
        <div class="stat-card">
          <strong>{{ taskSummary.running }}</strong>
          <span>正在执行</span>
        </div>
        <div class="stat-card">
          <strong>{{ taskSummary.svnImports }}</strong>
          <span>SVN 导入</span>
        </div>
      </div>
    </section>

    <el-tabs v-model="activeWorkspaceTab" class="workspace-tabs">
      <el-tab-pane label="任务" name="tasks">
        <div class="stack-layout">
          <div class="full-width-layout">
            <el-card shadow="never" class="panel-card panel-card-wide">
              <template #header>
                <div class="panel-header">
                  <span>任务列表</span>
                  <div class="action-row">
                    <el-button type="primary" @click="openCreateTask">新增任务</el-button>
                    <el-button text :loading="loading" @click="refreshAll">刷新</el-button>
                  </div>
                </div>
              </template>
              <el-table :data="tasks" height="620">
                <el-table-column prop="name" label="任务" min-width="220" />
                <el-table-column label="类型" width="120">
                  <template #default="{ row }">
                    <el-tag :type="row.taskType === 'svn_import' ? 'warning' : 'success'">
                      {{ taskTypeLabel(row) }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column label="同步" width="130">
                  <template #default="{ row }">
                    <el-tag :type="row.taskType === 'svn_import' ? 'warning' : row.recursiveSubmodules ? 'success' : 'info'">
                      {{ row.taskType === 'svn_import' ? 'SVN 标准布局' : row.recursiveSubmodules ? '递归子模块' : '单仓库' }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column label="Cron" min-width="170">
                  <template #default="{ row }">
                    <span class="task-trigger-value">{{ taskCronDisplay(row) }}</span>
                  </template>
                </el-table-column>
                <el-table-column label="下次执行" min-width="180">
                  <template #default="{ row }">
                    <span class="mono">{{ taskNextRunDisplay(row) }}</span>
                  </template>
                </el-table-column>
                <el-table-column label="最近执行" width="190">
                  <template #default="{ row }">
                    <div class="recent-execution-cell">
                      <span class="recent-execution-time">{{ formatTaskListTime(row.lastExecutionAt) }}</span>
                      <el-button
                        v-if="row.lastExecutionId"
                        size="small"
                        text
                        class="recent-execution-button"
                        @click="openExecutionByID(row.lastExecutionId)"
                      >
                        <el-tag :type="taskStatusType(row.lastExecutionStatus)">
                          {{ row.lastExecutionStatus || '未执行' }}
                        </el-tag>
                      </el-button>
                      <el-tag v-else :type="taskStatusType(row.lastExecutionStatus)">
                        {{ row.lastExecutionStatus || '未执行' }}
                      </el-tag>
                    </div>
                  </template>
                </el-table-column>
                <el-table-column label="总仓库数" width="100">
                  <template #default="{ row }">
                    {{ row.lastExecutionRepoCount || 0 }}
                  </template>
                </el-table-column>
                <el-table-column label="操作" width="280" align="center">
                  <template #default="{ row }">
                    <div class="action-row action-row-inline">
                      <el-button size="small" @click="editTask(row)">编辑</el-button>
                      <el-button size="small" type="primary" @click="runTask(row)">执行</el-button>
                      <el-button size="small" @click="openTaskHistory(row.id)">历史</el-button>
                      <el-button size="small" type="danger" @click="removeTask(row)">删除</el-button>
                    </div>
                  </template>
                </el-table-column>
              </el-table>
            </el-card>
          </div>

        </div>
      </el-tab-pane>

      <el-tab-pane label="凭证" name="credentials">
        <div class="full-width-layout">
          <el-card shadow="never" class="panel-card panel-card-wide">
            <template #header>
              <div class="panel-header">
                <span>凭证列表</span>
                <el-button type="primary" @click="openCreateCredential">新增凭证</el-button>
              </div>
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

      <el-tab-pane label="缓存" name="caches">
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
            <el-table-column label="占用空间" width="120">
              <template #default="{ row }">
                {{ formatBytes(row.sizeBytes) }}
              </template>
            </el-table-column>
            <el-table-column label="操作" width="120">
              <template #default="{ row }">
                <el-button size="small" type="danger" @click="cleanupCache(row)">清理</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>


    <el-dialog v-model="executionHistoryVisible" :title="executionHistoryTitle" width="1240px" destroy-on-close>
      <div class="history-dialog-body">
        <el-table :data="executions" max-height="420">
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="taskStatusType(row.status)">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="triggerType" label="触发方式" width="110" />
          <el-table-column prop="repoCount" label="仓库数" width="90" />
          <el-table-column prop="createdRepoCount" label="建仓数" width="90" />
          <el-table-column prop="failedNodeCount" label="失败节点" width="100" />
          <el-table-column label="开始时间" min-width="180">
            <template #default="{ row }">
              <span class="mono">{{ formatEast8DateTime(row.startedAt) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="结束时间" min-width="180">
            <template #default="{ row }">
              <span class="mono">{{ formatEast8DateTime(row.finishedAt) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-button size="small" @click="openExecution(row)">详情</el-button>
            </template>
          </el-table-column>
        </el-table>

        <div class="webhook-history">
          <div class="panel-header">
            <strong>最近 Webhook 记录</strong>
            <div class="history-toolbar">
              <span class="muted-text">{{ executionTaskId ? `任务 #${executionTaskId}` : '先选择任务' }}</span>
              <el-button size="small" @click="exportWebhookEvents">导出 CSV</el-button>
              <el-select v-model="webhookStatusFilter" size="small" class="history-filter">
                <el-option label="全部" value="all" />
                <el-option label="已接受" value="accepted" />
                <el-option label="已忽略" value="ignored" />
                <el-option label="已拒绝" value="rejected" />
                <el-option label="失败" value="failed" />
                <el-option label="已阻止" value="blocked" />
              </el-select>
            </div>
          </div>
          <div class="webhook-status-grid">
            <div
              v-for="item in webhookStatusCards"
              :key="item.label"
              class="webhook-status-card"
              :data-status="item.status"
            >
              <span>{{ item.label }}</span>
              <strong>{{ item.value }}</strong>
            </div>
          </div>
          <div v-if="latestIgnoredWebhook" class="webhook-reason-banner">
            <div class="panel-header">
              <strong>最近一次忽略原因</strong>
              <el-tag size="small" type="warning">{{ latestIgnoredWebhook.eventType || 'push' }}</el-tag>
            </div>
            <p>{{ webhookReasonLabel(latestIgnoredWebhook.reason) }}</p>
            <span class="muted-text mono">
              {{ latestIgnoredWebhook.ref || '无 ref' }} · {{ formatEast8DateTime(latestIgnoredWebhook.createdAt) }}
            </span>
          </div>
          <el-table :data="filteredWebhookEvents" max-height="280" empty-text="暂无 Webhook 记录">
            <el-table-column prop="status" label="状态" width="100">
              <template #default="{ row }">
                <el-tag size="small" :type="webhookEventStatusType(row.status)">
                  {{ row.status }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="eventType" label="事件" width="90" />
            <el-table-column prop="ref" label="Ref" min-width="180" />
            <el-table-column label="时间" width="180">
              <template #default="{ row }">
                <span class="mono">{{ formatEast8DateTime(row.createdAt) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="结果" min-width="260">
              <template #default="{ row }">
                <span :class="{ 'warning-text': row.status === 'ignored' }">{{ webhookReasonLabel(row.reason) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="160">
              <template #default="{ row }">
                <div class="action-row">
                  <el-button v-if="row.executionId" size="small" @click="openExecutionByID(row.executionId)">跳转</el-button>
                  <el-button size="small" type="primary" plain @click="replayWebhookEvent(row)">重放</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </div>
    </el-dialog>

    <el-dialog
      v-model="executionDetailVisible"
      width="1280px"
      destroy-on-close
      @closed="closeExecutionDetail"
    >
      <template #header>
        <div class="execution-dialog-header">
          <strong>执行详情</strong>
          <el-tag
            v-if="selectedExecution"
            class="execution-dialog-status"
            :type="taskStatusType(selectedExecution.execution.status)"
          >
            {{ selectedExecution.execution.status }}
          </el-tag>
        </div>
      </template>
      <div v-if="executionDetailLoading" class="execution-loading-state">
        <el-skeleton :rows="8" animated />
        <p class="muted-text">正在启动任务并加载执行详情...</p>
      </div>
      <div v-if="selectedExecution" class="detail-stack">
        <div class="detail-block">
          <strong>{{ selectedExecution.task.name }}</strong>
        </div>

        <div class="execution-summary-strip">
          <div v-for="item in selectedExecutionStats" :key="item.label" class="status-card">
            <span>{{ item.label }}</span>
            <strong>{{ item.value }}</strong>
          </div>
        </div>

        <div class="detail-block log-panel">
          <div class="panel-header">
            <strong>执行日志</strong>
          </div>
          <pre class="log-output">{{ selectedExecution.execution.summaryLog || '等待日志输出...' }}</pre>
        </div>

        <div class="execution-layout">
          <div class="tree-panel">
            <div class="execution-tree-toolbar">
              <div class="action-row">
                <el-button size="small" @click="expandAllNodes">展开全部</el-button>
                <el-button size="small" @click="collapseAllNodes">收起全部</el-button>
              </div>
              <el-switch v-model="errorOnly" active-text="只看失败节点" />
            </div>
            <el-tree
              ref="executionTreeRef"
              :data="executionTreeData"
              node-key="id"
              :current-node-key="selectedNodeId ?? undefined"
              :expand-on-click-node="false"
              highlight-current
              empty-text="暂无执行节点"
              @node-click="onTreeNodeClick"
              @node-expand="onTreeNodeExpand"
              @node-collapse="onTreeNodeCollapse"
            >
              <template #default="{ data }">
                <div class="tree-node-card" :class="{ 'tree-node-card-active': selectedNodeId === data.id }">
                  <div class="tree-title">
                    <span class="mono">{{ data.label }}</span>
                    <el-tag size="small" :type="taskStatusType(data.status)">{{ data.status }}</el-tag>
                  </div>
                  <div class="tree-meta">
                    <span>深度 {{ data.depth }}</span>
                    <span>缓存 {{ data.cacheHit ? '命中' : '未命中' }}</span>
                    <span>建仓 {{ data.autoCreated ? '已创建' : '未创建' }}</span>
                    <span>创建 {{ data.createDurationMs }}ms</span>
                    <span>拉取 {{ data.fetchDurationMs }}ms</span>
                    <span>推送 {{ data.pushDurationMs }}ms</span>
                  </div>
                  <div class="tree-paths mono">
                    <div>源: {{ data.sourceRepoUrl }}</div>
                    <div>目标: {{ data.targetRepoUrl }}</div>
                    <div v-if="data.referenceCommit">引用: {{ data.referenceCommit }}</div>
                    <div v-if="data.errorMessage" class="error-text">错误: {{ data.errorMessage }}</div>
                  </div>
                </div>
              </template>
            </el-tree>
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
                <strong>{{ selectedExecutionNode.cacheHit ? '命中' : '未命中' }}</strong>
              </div>
              <div class="node-detail-item">
                <span>自动建仓</span>
                <strong>{{ selectedExecutionNode.autoCreated ? '是' : '否' }}</strong>
              </div>
            </div>
            <div class="tree-paths mono">
              <div>源: {{ selectedExecutionNode.sourceRepoUrl }}</div>
              <div>目标: {{ selectedExecutionNode.targetRepoUrl }}</div>
              <div v-if="selectedExecutionNode.referenceCommit">引用: {{ selectedExecutionNode.referenceCommit }}</div>
              <div>创建: {{ selectedExecutionNode.createDurationMs }}ms</div>
              <div>拉取: {{ selectedExecutionNode.fetchDurationMs }}ms</div>
              <div>推送: {{ selectedExecutionNode.pushDurationMs }}ms</div>
              <div v-if="selectedExecutionNode.errorMessage" class="error-text">
                错误: {{ selectedExecutionNode.errorMessage }}
              </div>
            </div>
          </div>
        </div>
      </div>
      <el-empty v-else-if="!executionDetailLoading" description="暂无执行详情" />
    </el-dialog>

    <el-dialog v-model="taskDialogVisible" :title="taskDialogTitle" width="1080px" destroy-on-close>
      <el-form ref="taskFormRef" :model="taskForm" :rules="taskFormRules" label-position="top" class="dialog-form">
        <section class="form-section">
          <div class="form-section-header">
            <strong>基础信息</strong>
            <span>定义任务名称、源目标仓库和缓存位置。</span>
          </div>
          <div class="form-section-body">
            <el-form-item label="任务名称" prop="name">
              <el-input v-model="taskForm.name" />
            </el-form-item>
            <el-form-item label="任务类型">
              <el-segmented v-model="taskForm.taskType" :options="taskTypeOptions" />
            </el-form-item>
            <div class="two-column form-grid-wide">
              <el-form-item label="源仓库 URL" prop="sourceRepoUrl">
                <el-input
                  v-model="taskForm.sourceRepoUrl"
                  :placeholder="taskFormIsSVNImport ? 'https://svn.example.com/repos/project' : 'git@github.com:org/repo.git'"
                />
              </el-form-item>
              <el-form-item label="目标仓库 URL" prop="targetRepoUrl">
                <el-input v-model="taskForm.targetRepoUrl" placeholder="git@gogs.example.com:mirror/repo.git" />
              </el-form-item>
            </div>
            <el-form-item label="缓存保存路径">
              <el-input v-model="taskForm.cacheBasePath" placeholder="留空时使用默认目录，也可以填写绝对或相对路径" />
            </el-form-item>
          </div>
        </section>

        <section v-if="taskFormIsSVNImport" class="form-section">
          <div class="form-section-header">
            <strong>SVN 导入配置</strong>
            <span>当前只支持标准 `trunk / branches / tags` 布局，后续执行器会直接复用这些路径。</span>
          </div>
          <div class="form-section-body">
            <div class="two-column form-grid-wide">
              <el-form-item label="Trunk 路径">
                <el-input v-model="taskForm.svnConfig!.trunkPath" />
              </el-form-item>
              <el-form-item label="Branches 路径">
                <el-input v-model="taskForm.svnConfig!.branchesPath" />
              </el-form-item>
            </div>
            <div class="two-column form-grid-wide">
              <el-form-item label="Tags 路径">
                <el-input v-model="taskForm.svnConfig!.tagsPath" />
              </el-form-item>
              <el-form-item label="authors.txt 文件">
                <el-input v-model="taskForm.svnConfig!.authorsFilePath" placeholder="可选，本地文件路径" />
              </el-form-item>
            </div>
          </div>
        </section>

        <section class="form-section">
          <div class="form-section-header">
            <strong>凭证配置</strong>
            <span>{{ taskFormIsSVNImport ? 'SVN 源和目标 Git/平台访问凭证。' : '分别控制主仓库、子模块和平台 API 的访问凭证。' }}</span>
          </div>
          <div class="form-section-body">
            <div class="two-column form-grid-wide">
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
            <div v-if="!taskFormIsSVNImport" class="two-column form-grid-wide">
              <el-form-item label="子模块源凭证">
                <el-select v-model="taskForm.submoduleSourceCredentialId" clearable>
                  <el-option v-for="item in credentials" :key="`sub-src-${item.id}`" :label="item.name" :value="item.id" />
                </el-select>
                <div class="field-help">留空时会回退使用主仓库的源凭证。</div>
              </el-form-item>
              <el-form-item label="子模块目标凭证">
                <el-select v-model="taskForm.submoduleTargetCredentialId" clearable>
                  <el-option v-for="item in credentials" :key="`sub-dst-${item.id}`" :label="item.name" :value="item.id" />
                </el-select>
                <div class="field-help">留空时会回退使用主仓库的目标凭证。</div>
              </el-form-item>
            </div>
            <div class="two-column form-grid-wide">
              <el-form-item label="目标平台 API 凭证">
                <el-select v-model="taskForm.targetApiCredentialId" clearable>
                  <el-option v-for="item in credentials" :key="`api-${item.id}`" :label="item.name" :value="item.id" />
                </el-select>
                <div class="field-help">用于自动建仓、检查仓库存在性等平台 API 调用。</div>
              </el-form-item>
              <el-form-item v-if="!taskFormIsSVNImport" label="子模块目标平台 API 凭证">
                <el-select v-model="taskForm.submoduleTargetApiCredentialId" clearable>
                  <el-option v-for="item in credentials" :key="`sub-api-${item.id}`" :label="item.name" :value="item.id" />
                </el-select>
                <div class="field-help">留空时会继承主仓库的目标平台 API 凭证。</div>
              </el-form-item>
            </div>
          </div>
        </section>

        <section class="form-section">
          <div class="form-section-header">
            <strong>平台与建仓</strong>
            <span>配置自动建仓时使用的平台、命名空间和仓库描述。</span>
          </div>
          <div class="form-section-body">
            <div class="two-column form-grid-wide">
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
            <div class="two-column form-grid-wide">
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
          </div>
        </section>

        <section class="form-section">
          <div class="form-section-header">
            <strong>触发与同步策略</strong>
            <span>设置定时、Webhook、分支过滤以及镜像行为。</span>
          </div>
          <div class="form-section-body">
            <div class="flag-row">
              <el-switch v-model="taskForm.enabled" active-text="启用任务" />
              <el-switch v-if="!taskFormIsSVNImport" v-model="taskForm.recursiveSubmodules" active-text="递归子模块" />
              <el-switch v-model="taskForm.syncAllRefs" active-text="镜像全部 refs" />
              <el-switch v-model="taskForm.triggerConfig!.enableSchedule" active-text="启用定时" />
              <el-switch v-if="!taskFormIsSVNImport" v-model="taskForm.triggerConfig!.enableWebhook" active-text="启用 Webhook" />
            </div>
            <div class="two-column form-grid-wide">
              <el-form-item label="Cron" prop="triggerConfig.cron">
                <el-input v-model="taskForm.triggerConfig!.cron" />
              </el-form-item>
              <el-form-item :label="taskFormIsSVNImport ? '导入分支标识' : 'Branch Reference'">
                <el-input v-model="taskForm.triggerConfig!.branchReference" />
              </el-form-item>
            </div>
            <el-form-item v-if="!taskFormIsSVNImport" label="Webhook Secret" prop="triggerConfig.webhookSecret">
              <el-input v-model="taskForm.triggerConfig!.webhookSecret" />
            </el-form-item>
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
            </div>
          </div>
        </section>
      </el-form>
      <template #footer>
        <div class="action-row dialog-footer">
          <el-button @click="taskDialogVisible = false">取消</el-button>
          <el-button type="primary" @click="saveTask">保存任务</el-button>
        </div>
      </template>
    </el-dialog>

    <el-dialog v-model="credentialDialogVisible" :title="credentialDialogTitle" width="760px" destroy-on-close>
      <el-form label-position="top" class="dialog-form">
        <section class="form-section">
          <div class="form-section-header">
            <strong>基本信息</strong>
            <span>定义凭证名称、类型和用途范围。</span>
          </div>
          <div class="form-section-body">
            <el-form-item label="名称">
              <el-input v-model="credentialForm.name" />
            </el-form-item>
            <div class="two-column form-grid-wide">
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
            <el-form-item label="用途">
              <el-input v-model="credentialForm.scope" />
            </el-form-item>
          </div>
        </section>

        <section class="form-section">
          <div class="form-section-header">
            <strong>密钥内容</strong>
            <span>按当前凭证类型填写 Token 或 SSH 私钥内容。</span>
          </div>
          <div class="form-section-body">
            <el-form-item label="Secret">
              <el-input
                v-model="credentialForm.secret"
                type="textarea"
                :rows="6"
                @focus="prepareCredentialSecretEdit"
                @input="onCredentialSecretInput"
              />
              <div v-if="credentialForm.id && !credentialSecretDirty" class="field-help">
                当前显示的是脱敏值；如果不修改这个字段，保存时会保留原始密钥。
              </div>
              <div v-else-if="credentialForm.id" class="field-help">
                已进入重新输入模式，请填写新的 Secret。
              </div>
            </el-form-item>
          </div>
        </section>
      </el-form>
      <template #footer>
        <div class="action-row dialog-footer">
          <el-button @click="credentialDialogVisible = false">取消</el-button>
          <el-button type="primary" @click="saveCredential">保存凭证</el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>
