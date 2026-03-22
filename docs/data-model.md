# RepoSync 数据模型

## 1. SyncTask

用于描述一条“源仓库 -> 目标仓库”的同步任务。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `name` | string | 任务名称 |
| `sourceRepoUrl` | string | 源仓库地址 |
| `targetRepoUrl` | string | 目标仓库地址 |
| `cacheBasePath` | string | 任务级缓存保存根路径；留空时使用默认缓存目录 |
| `sourceCredentialId` | integer nullable | 源仓库凭证 |
| `targetCredentialId` | integer nullable | 目标 Git 仓库凭证 |
| `targetApiCredentialId` | integer nullable | 目标平台 API 凭证；用于自动建仓，留空时回退到 `targetCredentialId` |
| `enabled` | boolean | 是否启用 |
| `recursiveSubmodules` | boolean | 是否递归同步子模块 |
| `triggerConfig` | json | 定时/Webhook 配置 |
| `providerConfig` | json | 目标平台与自动建仓配置 |
| `syncAllRefs` | boolean | 是否镜像所有分支、标签及其他 refs，首版固定为 `true` |
| `createdAt` | datetime | 创建时间 |
| `updatedAt` | datetime | 更新时间 |

### triggerConfig
```json
{
  "cron": "0 */30 * * * *",
  "webhookSecret": "optional-secret",
  "enableSchedule": true,
  "enableWebhook": true,
  "branchReference": "refs/heads/main"
}
```

### providerConfig
```json
{
  "provider": "github",
  "namespace": "team-a",
  "visibility": "private",
  "descriptionTemplate": "mirror for {{repo}}",
  "baseApiUrl": "https://api.github.com"
}
```

## 2. Credential

用于保存 Git 或平台 API 凭证。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `name` | string | 凭证名称 |
| `type` | enum | `ssh_key` / `https_token` / `api_token` |
| `username` | string nullable | HTTPS 账号名，可选 |
| `secret` | encrypted string | 加密后的密钥或 Token |
| `secretMasked` | string | 脱敏展示值 |
| `scope` | string | 适用说明 |
| `createdAt` | datetime | 创建时间 |
| `updatedAt` | datetime | 更新时间 |

## 3. SyncExecution

记录一次任务执行。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `taskId` | integer | 对应任务 |
| `triggerType` | enum | `manual` / `schedule` / `webhook` |
| `status` | enum | `pending` / `running` / `success` / `failed` |
| `startedAt` | datetime | 开始时间 |
| `finishedAt` | datetime nullable | 结束时间 |
| `repoCount` | integer | 成功同步仓库数 |
| `createdRepoCount` | integer | 自动创建仓库数 |
| `failedNodeCount` | integer | 失败节点数 |
| `summaryLog` | text | 执行摘要 |

## 4. SyncExecutionNode

表示执行树中的一个节点，可对应主仓库或某个子模块。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `executionId` | integer | 所属执行 |
| `parentNodeId` | integer nullable | 父节点 |
| `repoPath` | string | 在主任务中的相对路径，主仓库为空 |
| `sourceRepoUrl` | string | 源仓库地址 |
| `targetRepoUrl` | string | 目标仓库地址 |
| `referenceCommit` | string | 引用 commit |
| `depth` | integer | 递归深度 |
| `cacheKey` | string | 命中的缓存键 |
| `cacheHit` | boolean | 是否命中已有缓存 |
| `autoCreated` | boolean | 是否自动建仓 |
| `createDurationMs` | integer | 建仓耗时 |
| `fetchDurationMs` | integer | fetch 耗时 |
| `pushDurationMs` | integer | push 耗时 |
| `status` | enum | 节点状态 |
| `errorMessage` | text | 错误信息 |

## 5. RepoCache

用于管理本地缓存仓库元数据。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `cacheKey` | string | 唯一键 |
| `sourceRepoUrl` | string | 源仓库地址 |
| `authContext` | string | 认证上下文摘要 |
| `cachePath` | string | 本地缓存路径 |
| `lastFetchAt` | datetime nullable | 最近 fetch 时间 |
| `lastUsedAt` | datetime nullable | 最近使用时间 |
| `hitCount` | integer | 命中次数 |
| `sizeBytes` | integer | 缓存占用大小 |
| `healthStatus` | string | `ready` / `broken` |
| `lastErrorMessage` | text | 最近错误 |

## 6. 关系说明
- 一个 `SyncTask` 有多条 `SyncExecution`
- 一条 `SyncExecution` 有多条 `SyncExecutionNode`
- 一个 `SyncTask` 可关联多个 `Credential`
- 多个任务可共享同一 `Credential`
- 多次执行可共享同一 `RepoCache`
