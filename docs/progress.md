# RepoSync 开发进度

更新时间：2026-03-22

## 已完成

### Phase 0
- 完成仓库初始化
- 完成前后端工程骨架
- 完成系统架构、数据模型、API、roadmap 文档

### Phase 1
- 已完成 SQLite 数据层
- 已完成 `SyncTask`、`Credential`、`SyncExecution`、`SyncExecutionNode`、`RepoCache` 的基础持久化
- 已完成任务 CRUD、凭证 CRUD、缓存查询、执行历史查询 API
- 已完成前端基础管理台页面

### Phase 2
- 已接入真实 Git mirror 流程
- 已支持 `git clone --mirror`
- 已支持缓存仓库上的 `git fetch --prune origin +refs/*:refs/*`
- 已支持目标仓库上的 `git push --mirror`
- 已通过本地集成测试验证镜像所有分支与标签
- 已支持缓存命中记录与第二次执行复用缓存

## 进行中
- Phase 3 自动建仓
- Phase 4 递归子模块同步

### Phase 3 当前进度
- 已接入 SCM Provider 抽象
- 已实现 GitHub Provider 基础流程
- 已实现 Gogs Provider 基础流程
- 已接入目标仓库存在性检查
- 已接入“缺失即自动建仓”的执行前置步骤
- 已将自动建仓结果写入执行节点与执行汇总
- 已通过 Provider 单元测试

当前限制：
- 目前自动建仓已覆盖主仓库
- 子模块递归建仓仍需等 Phase 4 递归执行树接入后补齐
- Git 凭证与 API 凭证仍共用任务层配置，尚未拆出更细粒度权限模型

### Phase 4 当前进度
- 已接入 `.gitmodules` 解析
- 已接入子模块引用 commit 读取
- 已接入主仓库到子模块的递归执行树
- 已接入按路径派生的子模块目标仓库映射
- 已接入循环依赖检测，避免无限递归
- 已通过本地集成测试验证主仓库和子模块递归镜像

当前限制：
- 当前子模块认证沿用主任务目标凭证
- 前端执行详情已能看到子模块节点，但还没有更清晰的树形可视化

## 下一步
- 把自动建仓进一步覆盖到远端子模块场景并补更多异常测试
- 在前端执行详情中强化递归树展示
- 开始推进 Phase 5 的定时调度与 Webhook 鉴权
