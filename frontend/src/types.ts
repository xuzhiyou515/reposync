export type ProviderType = 'github' | 'gogs'
export type Visibility = 'private' | 'public'
export type CredentialType = 'ssh_key' | 'https_token' | 'api_token'
export type TaskType = 'git_mirror' | 'svn_import'

export interface TriggerConfig {
  cron: string
  webhookSecret: string
  enableSchedule: boolean
  enableWebhook: boolean
  branchReference: string
}

export interface ProviderConfig {
  provider: ProviderType
  namespace: string
  visibility: Visibility
  descriptionTemplate: string
  baseApiUrl: string
}

export interface SVNConfig {
  trunkPath: string
  branchesPath: string
  tagsPath: string
  authorsFilePath: string
}

export interface SyncTask {
  id: number
  taskType: TaskType
  name: string
  sourceRepoUrl: string
  targetRepoUrl: string
  cacheBasePath: string
  sourceCredentialId?: number | null
  submoduleSourceCredentialId?: number | null
  targetCredentialId?: number | null
  submoduleTargetCredentialId?: number | null
  targetApiCredentialId?: number | null
  submoduleTargetApiCredentialId?: number | null
  enabled: boolean
  recursiveSubmodules: boolean
  syncAllRefs: boolean
  triggerConfig: TriggerConfig
  providerConfig: ProviderConfig
  svnConfig: SVNConfig
  scheduleCron?: string
  nextRunAt?: string
  lastExecutionId?: number
  lastExecutionStatus?: string
  lastExecutionAt?: string
  lastExecutionRepoCount?: number
  lastCreatedRepoCount?: number
}

export interface Credential {
  id: number
  name: string
  type: CredentialType
  username?: string
  secret?: string
  secretMasked?: string
  scope?: string
}

export interface RepoCache {
  id: number
  cacheKey: string
  sourceRepoUrl: string
  authContext: string
  cachePath: string
  lastFetchAt?: string
  lastUsedAt?: string
  hitCount: number
  sizeBytes: number
  healthStatus: string
  lastErrorMessage?: string
}

export interface SyncExecution {
  id: number
  taskId: number
  triggerType: string
  status: string
  startedAt: string
  finishedAt?: string
  repoCount: number
  createdRepoCount: number
  failedNodeCount: number
  summaryLog: string
}

export interface SyncExecutionNode {
  id: number
  executionId: number
  parentNodeId?: number
  repoPath: string
  sourceRepoUrl: string
  targetRepoUrl: string
  referenceCommit: string
  depth: number
  cacheKey: string
  cacheHit: boolean
  autoCreated: boolean
  createDurationMs: number
  fetchDurationMs: number
  pushDurationMs: number
  status: string
  errorMessage?: string
}

export interface ExecutionDetail {
  execution: SyncExecution
  task: SyncTask
  nodes: SyncExecutionNode[]
}

export interface WebhookEvent {
  id: number
  taskId: number
  provider: string
  eventType: string
  ref: string
  status: string
  reason: string
  executionId?: number
  createdAt: string
}
