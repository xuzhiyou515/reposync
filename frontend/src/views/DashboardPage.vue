<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import type { Credential, ExecutionDetail, RepoCache, SyncExecution, SyncTask } from '../types'

const tasks = ref<SyncTask[]>([])
const credentials = ref<Credential[]>([])
const caches = ref<RepoCache[]>([])
const executions = ref<SyncExecution[]>([])
const selectedExecution = ref<ExecutionDetail | null>(null)
const loading = ref(false)
const executionTaskId = ref<number | null>(null)

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
}))

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
  await api.runTask(task.id)
  ElMessage.success('任务已触发')
  await refreshAll()
  await loadExecutions(task.id)
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
}

const cleanupCache = async (cache: RepoCache) => {
  await api.cleanupCache(cache.id)
  ElMessage.success('缓存已清理')
  await loadCaches()
}

onMounted(async () => {
  await refreshAll()
})
</script>

<template>
  <div class="page-shell">
    <section class="hero-card">
      <div>
        <p class="eyebrow">RepoSync</p>
        <h1>Git 镜像同步控制台</h1>
        <p class="hero-copy">
          从 roadmap 的基础阶段开始，当前已经打通任务、凭证、缓存和手动执行记录的管理闭环。同步语义固定为镜像所有分支、标签和其他 refs。
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
          <span>递归同步</span>
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
              <el-form-item label="源仓库">
                <el-input v-model="taskForm.sourceRepoUrl" placeholder="git@github.com:org/repo.git" />
              </el-form-item>
              <el-form-item label="目标仓库">
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
                <el-form-item label="Provider">
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
                  <el-input v-model="taskForm.providerConfig!.baseApiUrl" />
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
            <el-table :data="tasks" height="560">
              <el-table-column prop="name" label="任务" min-width="180" />
              <el-table-column prop="providerConfig.provider" label="Provider" width="110" />
              <el-table-column label="状态" width="120">
                <template #default="{ row }">
                  <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="最近执行" min-width="160">
                <template #default="{ row }">
                  {{ row.lastExecutionStatus || '尚未执行' }}
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
            <el-table :data="credentials" height="560">
              <el-table-column prop="name" label="名称" min-width="180" />
              <el-table-column prop="type" label="类型" width="140" />
              <el-table-column prop="secretMasked" label="脱敏值" min-width="180" />
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
          <el-table :data="caches" height="560">
            <el-table-column prop="sourceRepoUrl" label="源仓库" min-width="280" />
            <el-table-column prop="cachePath" label="缓存路径" min-width="220" />
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
                <span class="muted-text">{{ executionTaskId ? `任务 #${executionTaskId}` : '先从任务页选择一个任务' }}</span>
              </div>
            </template>
            <el-table :data="executions" height="560">
              <el-table-column prop="status" label="状态" width="120" />
              <el-table-column prop="triggerType" label="触发方式" width="120" />
              <el-table-column prop="repoCount" label="仓库数" width="90" />
              <el-table-column prop="createdRepoCount" label="建仓数" width="90" />
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
                <p>{{ selectedExecution.execution.summaryLog }}</p>
              </div>
              <el-table :data="selectedExecution.nodes">
                <el-table-column prop="repoPath" label="路径" min-width="140" />
                <el-table-column prop="status" label="状态" width="100" />
                <el-table-column prop="cacheKey" label="缓存键" min-width="170" />
                <el-table-column prop="autoCreated" label="自动建仓" width="100" />
                <el-table-column prop="fetchDurationMs" label="Fetch(ms)" width="100" />
                <el-table-column prop="pushDurationMs" label="Push(ms)" width="100" />
              </el-table>
            </div>
            <el-empty v-else description="暂无执行详情" />
          </el-card>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>
