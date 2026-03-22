# RepoSync

RepoSync 是一个面向单管理员的 Git 镜像同步管理台，首版目标支持：
- 多任务 Git 镜像同步
- GitHub / Gogs
- SSH Key / HTTPS Token / API Token
- Webhook + 定时触发
- 递归子模块同步
- 本地 bare mirror 缓存
- 目标仓库缺失时自动建仓
- 前端静态资源内嵌到后端程序，可由外部目录覆盖

## 仓库结构
- `concept.md`：最初的产品概念说明
- `docs/`：当前生效的开发文档
- `backend/`：Go 后端工程骨架
- `frontend/`：Vue 前端工程骨架

## 文档入口
从这里开始阅读开发文档：
- [开发文档总览](./docs/README.md)
- [系统总览](./docs/architecture.md)
- [数据模型](./docs/data-model.md)
- [API 设计](./docs/api.md)
- [开发里程碑](./docs/roadmap.md)
- [部署说明](./docs/deployment.md)
