# RepoSync 开发文档

本文档用于把 [concept.md](/D:/Codes/RepoSync/concept.md) 中的产品设想沉淀为可执行的工程设计，方便后续分阶段开发、拆分任务和验收。

## 文档索引
- [系统总览](./architecture.md)
- [数据模型](./data-model.md)
- [API 设计](./api.md)
- [开发里程碑](./roadmap.md)
- [开发进度](./progress.md)
- [部署说明](./deployment.md)

## 当前仓库状态
- 后端 `Go` 服务、前端 `Vue + Vite` 管理台、SQLite 存储和发布脚本已落地。
- 首版 `Git mirror` 路线已完成，包含自动建仓、递归子模块、调度、Webhook、实时日志和缓存管理。
- `SVN -> Git` 正在持续补强，当前已具备任务模型、执行主链、作者映射、目标偏移保护、真实 E2E 回归夹具和基础管理台支持。

## 当前能力边界
- 支持多任务 `Git -> Git` 镜像同步。
- 支持 `SVN -> Git` 持续导入，范围限定为标准 `trunk / branches / tags` 布局。
- 支持 SSH 私钥、HTTP 用户名密码、平台 API Token 三类凭证。
- 支持 GitHub / Gogs、定时调度、Webhook、执行详情、缓存管理和单管理员管理台。

## 非目标
- 多租户
- RBAC
- 分布式执行器
- 自动缓存过期淘汰
- 高级仓库初始化能力，例如模板仓库、默认 README、分支保护
