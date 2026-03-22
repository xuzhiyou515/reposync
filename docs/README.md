# RepoSync 开发文档

本文档用于把 [concept.md](/D:/Codes/RepoSync/concept.md) 中的产品设想沉淀为可执行的工程设计，方便后续分阶段开发、拆分任务和验收。

## 文档索引
- [系统总览](./architecture.md)
- [数据模型](./data-model.md)
- [API 设计](./api.md)
- [开发里程碑](./roadmap.md)
- [开发进度](./progress.md)

## 当前仓库状态
- 已初始化后端 `Go` 工程骨架：`backend/`
- 已初始化前端 `Vue + Vite` 工程骨架：`frontend/`
- 尚未完成核心业务实现，后续开发应以本文档为准逐步补齐

## 首版目标
- 支持多任务 Git 镜像同步
- 支持 SSH Key / HTTPS Token / API Token
- 支持 GitHub / Gogs Webhook 与定时触发
- 支持递归子模块同步
- 支持本地裸仓库镜像缓存
- 支持目标仓库缺失时自动建仓
- 提供单管理员使用的 Web 管理台

## 非目标
- 多租户
- RBAC
- 分布式执行器
- 自动缓存过期淘汰
- 高级仓库初始化能力，例如模板仓库、默认 README、分支保护
