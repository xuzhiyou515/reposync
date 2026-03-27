# RepoSync

RepoSync 是一个面向单管理员的仓库同步管理台，当前能力覆盖两条主线：
- 多任务 `Git -> Git` 镜像同步
- 标准 `trunk / branches / tags` 布局的 `SVN -> Git` 持续导入

首版已支持：
- GitHub / Gogs
- SSH 私钥 / HTTP 用户名密码 / API Token
- Webhook + 定时触发
- 递归子模块同步
- 子模块 `.gitmodules` 重写协议可配置为继承目标仓库、强制 HTTP/HTTPS 或强制 SSH
- 本地 bare mirror / SVN 导入缓存
- 目标仓库缺失时自动建仓
- 前端静态资源内嵌到后端程序，也可由外部目录覆盖

## 仓库结构
- `concept.md`：最初的产品概念说明
- `docs/`：当前生效的开发文档
- `backend/`：Go 后端工程
- `frontend/`：Vue 前端工程

## 文档入口
- [开发文档总览](./docs/README.md)
- [系统总览](./docs/architecture.md)
- [数据模型](./docs/data-model.md)
- [API 设计](./docs/api.md)
- [开发里程碑](./docs/roadmap.md)
- [开发进度](./docs/progress.md)
- [部署说明](./docs/deployment.md)
