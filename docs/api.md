# RepoSync API 设计

所有接口统一返回 JSON。

## 1. 任务接口

### `GET /api/tasks`

返回任务列表，附带最近一次执行摘要。

### `POST /api/tasks`

创建任务。

请求示例：

```json
{
  "name": "sync-main-repo",
  "sourceRepoUrl": "git@github.com:source-org/main.git",
  "targetRepoUrl": "git@gogs.example.com:mirror-org/main.git",
  "cacheBasePath": "team-a",
  "sourceCredentialId": 1,
  "submoduleSourceCredentialId": 4,
  "targetCredentialId": 2,
  "submoduleTargetCredentialId": 5,
  "targetApiCredentialId": 3,
  "submoduleTargetApiCredentialId": 6,
  "submoduleRewriteProtocol": "ssh",
  "enabled": true,
  "recursiveSubmodules": true,
  "syncAllRefs": true,
  "triggerConfig": {
    "cron": "0 */30 * * * *",
    "enableSchedule": true,
    "enableWebhook": true,
    "branchReference": "refs/heads/main"
  },
  "providerConfig": {
    "provider": "gogs",
    "namespace": "mirror-org",
    "visibility": "private",
    "descriptionTemplate": "mirror for {{repo}}",
    "baseApiUrl": "https://gogs.example.com/api/v1"
  }
}
```

### `PUT /api/tasks/:id`

更新任务。

说明：
- `syncAllRefs` 首版固定为 `true`
- 服务端执行时始终采用 mirror 语义同步所有分支、标签和其他 refs
- `targetCredentialId` 用于主仓库目标 Git 推送
- `submoduleTargetCredentialId` 用于子模块目标 Git 推送；留空时回退到 `targetCredentialId`
- `targetApiCredentialId` 用于主仓库目标平台自动建仓；留空时回退到 `targetCredentialId`
- `submoduleTargetApiCredentialId` 用于子模块自动建仓；留空时回退到 `targetApiCredentialId`
- `submoduleRewriteProtocol` 控制重写 `.gitmodules` 时写回的子模块目标地址协议：
  - `inherit`：继承目标仓库 URL 风格
  - `http`：强制写为 HTTP/HTTPS
  - `ssh`：强制写为 SSH

### `DELETE /api/tasks/:id`

删除任务。

### `POST /api/tasks/:id/run`

手动触发任务执行。

### `GET /api/tasks/:id/executions`

查看任务执行历史。

### `GET /api/tasks/:id/webhook-events`

查看任务最近的 Webhook 历史。

### `POST /api/tasks/:id/webhook-events/:eventId/replay`

基于一条历史 Webhook 记录重新触发一次同步。

## 2. 缓存接口

### `GET /api/caches`

返回缓存列表。

返回项补充说明：
- `linkedTaskCount`：当前关联到该缓存的任务数量。

### `POST /api/caches/:id/move`

迁移缓存目录，并更新缓存记录中的 `cachePath`。任务后续执行会继续复用迁移后的目录。

请求示例：

```json
{
  "cachePath": "D:/Repos/cache/migrated/abcdef.git"
}
```

### `POST /api/caches/:id/cleanup`

清理指定缓存，同时删除缓存记录。


## 8. SVN 到 Git 任务规划

后续 `svn_import` 任务会复用现有任务接口，即继续使用 `POST /api/tasks` 与 `PUT /api/tasks/:id` 创建或更新任务。

约定如下：
- `taskType` 为 `svn_import` 时，`sourceRepoUrl` 不再表示 Git 源仓库，而是由 `svnConfig.svnUrl` 指定 SVN 地址
- `triggerConfig.enableWebhook` 对 `svn_import` 固定为禁用
- `providerConfig` 仍用于目标 Git 平台自动建仓
- `POST /api/tasks/:id/run` 对 `svn_import` 会触发一次 `git svn` 增量同步

示例请求：

```json
{
  "taskType": "svn_import",
  "name": "svn-main",
  "targetRepoUrl": "git@gogs.example.com:mirror/svn-main.git",
  "enabled": true,
  "triggerConfig": {
    "cron": "0 */30 * * * *",
    "enableSchedule": true,
    "enableWebhook": false,
    "branchReference": ""
  },
  "svnConfig": {
    "svnUrl": "https://svn.example.com/repos/project",
    "svnCredentialId": 10,
    "svnLayout": "standard",
    "authorsFilePath": "configs/authors.txt",
    "defaultAuthorDomain": "example.com",
    "gitSvnMetadataEnabled": true
  }
}
```

## 9. Current `svn_import` payload

The example above reflects an early planning shape. The current implementation uses the normal task fields directly:

- `sourceRepoUrl` is the SVN source address and supports `http://`, `https://`, and `svn://`
- `targetRepoUrl` is the Git destination and may use HTTP or SSH
- `svnConfig` currently supports:
  - `trunkPath`
  - `branchesPath`
  - `tagsPath`
  - `startRevision`
  - `authorsFilePath`
  - `authorDomain`

Current example:

```json
{
  "taskType": "svn_import",
  "name": "UICore",
  "sourceRepoUrl": "svn://svn.example.com/repos/project",
  "targetRepoUrl": "ssh://git@gogs.example.com:2222/mirror/uicore.git",
  "enabled": true,
  "triggerConfig": {
    "cron": "0 */30 * * * *",
    "enableSchedule": true,
    "enableWebhook": false,
    "branchReference": ""
  },
  "providerConfig": {
    "provider": "gogs",
    "namespace": "mirror",
    "visibility": "private",
    "baseApiUrl": "http://gogs.example.com:3000/api/v1"
  },
  "svnConfig": {
    "trunkPath": ".",
    "branchesPath": "",
    "tagsPath": "",
    "startRevision": "120000",
    "authorsFilePath": "",
    "authorDomain": "@example.com"
  }
}
```

Notes:

- `startRevision` is optional and only affects the initial `git svn clone` for a fresh cache
- when `targetRepoUrl` uses SSH, `providerConfig.baseApiUrl` must be set explicitly for repository existence checks and auto-create
- when `authorsFilePath` is empty, RepoSync auto-maps SVN authors to `name <name@domain>`
