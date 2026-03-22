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
  "sourceCredentialId": 1,
  "targetCredentialId": 2,
  "targetApiCredentialId": 3,
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
- `syncAllRefs` 首版必须为 `true`
- 服务端执行时应采用 mirror 语义同步所有分支、标签和其他 refs
- `targetCredentialId` 用于目标 Git 仓库访问
- `targetApiCredentialId` 用于目标平台自动建仓；留空时兼容回退到 `targetCredentialId`

### `DELETE /api/tasks/:id`
删除任务。

### `POST /api/tasks/:id/run`
手动触发任务执行。

返回示例：
```json
{
  "id": 101,
  "taskId": 10,
  "triggerType": "manual",
  "status": "running",
  "startedAt": "2026-03-22T14:00:00Z"
}
```

### `GET /api/tasks/:id/executions`
查看某个任务的执行历史。

### `GET /api/tasks/:id/schedule-status`
查看单个任务当前的调度注册状态、下一次执行时间和未注册原因。

## 2. 调度状态接口

### `GET /api/schedules`
返回所有任务的调度状态列表。

返回示例：
```json
[
  {
    "taskId": 10,
    "taskName": "sync-main-repo",
    "enabled": true,
    "registered": true,
    "cron": "0 */30 * * * *",
    "nextRunAt": "2026-03-22T14:30:00Z",
    "previousRunAt": "2026-03-22T14:00:00Z",
    "reason": ""
  }
]
```

## 3. 执行详情接口

### `GET /api/executions/:id`
返回执行摘要、所属任务、执行树节点。

返回结构：
```json
{
  "execution": {},
  "task": {},
  "nodes": []
}
```

## 4. 凭证接口

### `GET /api/credentials`
返回脱敏后的凭证列表。

### `POST /api/credentials`
创建凭证。

请求示例：
```json
{
  "name": "gogs-admin-token",
  "type": "api_token",
  "username": "",
  "secret": "token-value",
  "scope": "gogs admin create repo"
}
```

### `PUT /api/credentials/:id`
更新凭证。

### `DELETE /api/credentials/:id`
删除凭证。

## 5. 缓存接口

### `GET /api/caches`
获取缓存列表。

### `POST /api/caches/:id/cleanup`
手动清理指定缓存。

返回示例：
```json
{
  "cleaned": true
}
```

## 6. Webhook 接口

### `POST /api/webhooks/github/:taskId`
接收 GitHub Webhook，按任务配置触发同步。

### `POST /api/webhooks/gogs/:taskId`
接收 Gogs Webhook，按任务配置触发同步。

### `POST /api/tasks/:id/webhook-events/:eventId/replay`
基于一条历史 Webhook 记录重新触发一次同步执行。

首版建议校验：
- 任务已启用
- Webhook 已启用
- Webhook Secret 匹配

## 7. 错误响应

统一错误结构：
```json
{
  "error": "human readable message"
}
```

常见状态码：
- `400` 请求参数错误
- `404` 资源不存在
- `409` 并发冲突或重复创建
- `500` 服务内部错误
