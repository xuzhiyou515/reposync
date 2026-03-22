# RepoSync 数据模型

## 1. SyncTask

用于描述一条“源仓库 -> 目标仓库”的同步任务。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `name` | string | 任务名称 |
| `sourceRepoUrl` | string | 源仓库地址 |
| `targetRepoUrl` | string | 目标仓库地址 |
| `cacheBasePath` | string | 任务级缓存根路径；留空时使用系统默认缓存目录 |
| `sourceCredentialId` | integer nullable | 主仓库源 Git 凭证 |
| `submoduleSourceCredentialId` | integer nullable | 子模块源 Git 凭证；留空时回退到 `sourceCredentialId` |
| `targetCredentialId` | integer nullable | 主仓库目标 Git 凭证 |
| `submoduleTargetCredentialId` | integer nullable | 子模块目标 Git 凭证；留空时回退到 `targetCredentialId` |
| `targetApiCredentialId` | integer nullable | 主仓库目标平台 API 凭证；留空时回退到 `targetCredentialId` |
| `submoduleTargetApiCredentialId` | integer nullable | 子模块目标平台 API 凭证；留空时回退到 `targetApiCredentialId` |
| `enabled` | boolean | 是否启用 |
| `recursiveSubmodules` | boolean | 是否递归同步子模块 |
| `syncAllRefs` | boolean | 是否镜像所有分支、标签和其他 refs；首版固定为 `true` |
| `triggerConfig` | json | 定时/Webhook 配置 |
| `providerConfig` | json | 目标平台与自动建仓配置 |
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
| `name` | string | 展示名称 |
| `type` | enum | `ssh_key` / `https_token` / `api_token` |
| `username` | string | 可选用户名；HTTPS Git 访问时可参与 basic auth |
| `secret` | string | 密文存储，API 输出时不回显 |
| `secretMasked` | string | 脱敏后的展示值 |
| `scope` | string | 用途说明 |
| `createdAt` | datetime | 创建时间 |
| `updatedAt` | datetime | 更新时间 |

补充说明：
- `https_token` / `api_token` 会在执行 Git HTTPS 命令或平台 API 请求时注入认证信息。
- `ssh_key` 会在执行时临时写入密钥文件，并通过 `GIT_SSH_COMMAND` 注入，不在日志中明文输出。

## 3. SyncExecution

表示一次完整的同步执行。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `taskId` | integer | 所属任务 |
| `triggerType` | enum | `manual` / `schedule` / `webhook` |
| `status` | enum | `running` / `success` / `failed` |
| `startedAt` | datetime | 开始时间 |
| `finishedAt` | datetime nullable | 结束时间 |
| `repoCount` | integer | 本次同步仓库总数 |
| `createdRepoCount` | integer | 自动创建目标仓库数 |
| `failedNodeCount` | integer | 失败节点数 |
| `summaryLog` | text | 阶段日志和底层 Git 命令输出 |

## 4. SyncExecutionNode

表示执行树中的一个节点，可对应主仓库或某个子模块。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `executionId` | integer | 所属执行 |
| `parentNodeId` | integer nullable | 父节点 ID |
| `repoPath` | string | 仓库路径；主仓库为空字符串 |
| `sourceRepoUrl` | string | 源仓库地址 |
| `targetRepoUrl` | string | 目标仓库地址 |
| `referenceCommit` | string | 当前节点引用或最终有效提交 |
| `depth` | integer | 递归深度 |
| `cacheKey` | string | 缓存键 |
| `cacheHit` | boolean | 是否命中缓存 |
| `autoCreated` | boolean | 是否自动建仓 |
| `createDurationMs` | integer | 自动建仓耗时 |
| `fetchDurationMs` | integer | 缓存刷新耗时 |
| `pushDurationMs` | integer | 推送或改写耗时 |
| `status` | enum | `running` / `success` / `failed` |
| `errorMessage` | string | 失败信息 |

## 5. RepoCache

描述本地裸仓库缓存。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `cacheKey` | string | 缓存键 |
| `sourceRepoUrl` | string | 源仓库地址 |
| `authContext` | string | 认证上下文摘要 |
| `cachePath` | string | 本地缓存路径 |
| `lastFetchAt` | datetime nullable | 最近一次 fetch 时间 |
| `lastUsedAt` | datetime nullable | 最近一次使用时间 |
| `hitCount` | integer | 命中次数 |
| `sizeBytes` | integer | 缓存大小 |
| `healthStatus` | string | `ready` / `broken` 等 |
| `lastErrorMessage` | string | 最近一次错误 |

## 6. WebhookEvent

记录 Webhook 历史。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 主键 |
| `taskId` | integer | 所属任务 |
| `provider` | string | `github` / `gogs` |
| `eventType` | string | 事件类型 |
| `ref` | string | 推送 ref |
| `status` | string | `accepted` / `ignored` / `rejected` / `failed` / `blocked` |
| `reason` | string | 状态原因 |
| `executionId` | integer nullable | 关联执行 |
| `createdAt` | datetime | 记录时间 |

## 7. ????????

?????????????????????????????

| ?? | ?? | ?? |
| --- | --- | --- |
| `scheduleCron` | string | ????? Cron ?????????? |
| `nextRunAt` | datetime nullable | ??????????????????????? |


## 9. SVNImport ????

??? `svn_import` ?????`SyncTask` ????????? SVN ????????? Git mirror ?????

?????

| ?? | ?? | ?? |
| --- | --- | --- |
| `taskType` | enum | `git_mirror` / `svn_import` |
| `svnUrl` | string | SVN ????? |
| `svnCredentialId` | integer nullable | SVN HTTP/HTTPS ?? |
| `svnLayout` | enum | ????? `standard` |
| `authorsFilePath` | string nullable | ??? `authors.txt` ?? |
| `defaultAuthorDomain` | string nullable | ???????? email ?? |
| `gitSvnMetadataEnabled` | boolean | ???? `git-svn-id` ??? |

???
- `svn_import` ????? `trunk / branches / tags`?
- `svn_import` ??? Git ???????????
- `svn_import` ???????????
