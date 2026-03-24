# RepoSync 开发进度

更新时间：2026-03-23

## 已完成

### Phase 0
- 完成仓库初始化
- 完成前后端工程骨架
- 完成架构、数据模型、API、roadmap 文档

### Phase 1
- 完成 SQLite 数据层
- 完成任务、凭证、缓存、执行记录、Webhook 历史数据模型
- 完成任务 CRUD、凭证 CRUD、缓存查询、执行历史查询 API
- 完成基础管理台页面

### Phase 2
- 接入真实 Git mirror 流程
- 支持 `git clone --mirror`
- 支持 `git fetch --prune origin +refs/*:refs/*`
- 支持 `git push --mirror`
- 已通过本地集成测试验证镜像所有分支、标签和其他 refs
- 支持缓存命中记录与复用
- 支持任务级缓存保存路径
- 支持 Git HTTPS / SSH 凭证注入与命令日志脱敏

### Phase 3
- 接入 SCM Provider 抽象
- 实现 GitHub / Gogs 自动建仓
- 支持本地 bare 目标仓库缺失即初始化
- 接入目标仓库存在性检查与缺失即创建
- 自动建仓结果写入执行节点与执行汇总
- 覆盖“检查不存在、创建时已存在”的幂等竞争场景
- 补充 GitHub / Gogs 的鉴权与权限错误测试
- 拆分主仓库和子模块的 Git / API 凭证配置

### Phase 4
- 接入 `.gitmodules` 解析
- 接入子模块引用 commit 读取
- 接入主仓库到子模块的递归执行树
- 接入循环依赖检测
- 接入子模块目标仓库映射，并改为按 `.gitmodules` 原始仓库名派生目标仓库名
- 在主仓库推送前改写 `.gitmodules`
- 在父仓库中改写 gitlink 到同步后的子模块提交
- 稳定化改写提交，避免源分支不变时反复生成新 SHA
- 已通过递归 clone 集成测试验证目标仓库可直接使用镜像子模块
- 执行详情树已升级为组件化树控件

### Phase 5
- 接入 Cron 调度器
- 支持任务级调度注册 / 反注册
- 提供调度状态查询接口
- 接入 GitHub / Gogs Webhook 验签
- 接入事件类型过滤和按分支 ref 过滤
- 执行过程中持续写入阶段日志
- 接入底层 Git 命令 stdout / stderr 日志采集
- 提供 SSE 与 WebSocket 执行实时流
- 记录 accepted / ignored / rejected / failed / blocked Webhook 历史
- 支持 Webhook 重放
- 支持 Webhook 历史 CSV 导出

### Phase 6
- 完成任务状态、调度摘要、Webhook 摘要展示
- 完成独立的调度状态面板
- 完成执行详情中的递归树控件视图
- 完成缓存命中、自动建仓、耗时和错误信息展示
- 完成任务编辑页中的触发预览和 Webhook 路径预览
- 完成节点选中高亮与独立节点详情面板
- 完成执行日志面板与实时刷新
- 完成 Webhook 历史表格、状态筛选、执行跳转、重放与导出
- 完成前端构建验证

## 当前状态

首版 roadmap 已全部完成。

额外补充的交付物：
- 已提供 Windows / Linux 的 release 构建脚本
- 已提供发布包启动脚本和环境变量模板
- 已补充独立的部署说明文档
- 已支持前端静态资源内嵌到后端二进制，并保留外部目录覆盖能力

当前首版的明确设计语义：
- 递归子模块模式下，目标分支是“可直接使用的派生提交”，不是严格的源分支 bit-for-bit 镜像。
- 标签和其他非分支 refs 仍保持 mirror 语义。
- 实时日志通道默认使用 WebSocket，失败时回退到 SSE。

## 验证方式

当前代码已持续通过以下验证链路：
- `go test ./...`
- `go build ./...`
- `npm run build`

## 2026-03-22 增量更新

### 实时执行链路修复
- 修复执行详情实时日志不连续的问题
- 根因是 `executeTask()` 中后台执行 logger 未将 summary log 更新同步回运行中内存快照
- 修复后 WebSocket / SSE 订阅都可以持续收到执行过程中的日志和状态变化
- 为 `RunTask -> SubscribeExecution` 增加回归测试，确保后续执行日志会持续推送而不只停留在首条启动日志

### 管理台交互整理
- 任务表单和凭证表单改为从列表新增/编辑入口打开，不再常驻页面
- 执行历史改为从任务列表项进入，执行详情改为从执行历史项进入
- 点击“执行”后会直接打开执行详情
- 任务表单增加前端参数校验
- 任务列表的触发配置文案、操作列布局和空状态展示做了整理
- 任务删除后顶部统计和相关弹窗状态会同步刷新
- Element Plus 默认语言切换为中文，修复混杂英文和乱码显示问题

### 调度与时间显示整理
- 任务列表改为直接使用 `GET /api/tasks` 返回的 `scheduleCron` 与 `nextRunAt`
- 删除中间的 `ScheduleStatus` 暴露结构，调度器直接补全 `SyncTask` 调度字段
- 前后端对外展示时间统一为 `Asia/Shanghai` 时区
- 缓存大小改为记录真实 `sizeBytes` 并用于前端展示


### SVN -> Git 规划
- 已确认 `SVN -> Git` 会作为独立任务类型推进
- 首版仅支持标准 `trunk / branches / tags`
- 目标 Git 仓库必须作为只读镜像使用
- 首版 SVN 认证范围限定为 `HTTP / HTTPS`
- 执行实现基于 `git svn`
- 计划支持可选 `authors.txt`、真实 Git tag 映射和手动/定时触发

## 2026-03-23 增量更新

### 凭据与任务配置防错
- 保存任务时新增源仓库凭据类型校验：`sourceRepoUrl` 为 `http/https` 时，不允许绑定 `ssh_key` 源凭据
- 管理台“保存任务”新增错误提示，后端返回 4xx 时会明确展示失败原因，不再出现“点击无反应”

### Git/SSH 执行稳定性
- 调整 SSH 注入参数，移除 `-F NUL`，避免部分 Windows 环境报 `Can't open user config file NUL`
- 兼容仅提供 `ssh-rsa` host key 的旧服务端
- SSH 命令改为非交互并增加连接超时，避免长时间挂起
- 所有 git 命令默认注入 `GIT_TERMINAL_PROMPT=0`、`GCM_INTERACTIVE=never`
- push 增加 HTTP 稳定参数（`http.postBuffer`、`http.version=HTTP/1.1`、禁用交互凭据）
- clone/fetch/push 增加 `--progress`，并新增执行心跳日志（定时输出 elapsed）

### 递归子模块同步适配
- 子模块源 URL 会按主模块源凭据类型进行协议适配（SSH/HTTP）
- 支持基于主模块 URL 解析子模块相对路径 URL
- 子模块转 SSH 时继承主模块 SSH 的 host/port 与用户名，避免端口和账号不一致

### 缓存自愈与发布脚本
- 镜像缓存目录存在但不是有效 mirror 仓库时，执行前自动清理并重建，避免 `already exists and is not an empty directory`
- `build-release.ps1/.sh` 改为覆盖更新策略，不再删除整个 `release/`，保留 `release/data` 和 `release/config/reposync.env`

## 2026-03-24 Incremental Update

### Phase 7: SVN -> Git scaffold
- Added task type `svn_import` across the task domain model, SQLite storage, and frontend types.
- Added `svnConfig` with `trunkPath`, `branchesPath`, `tagsPath`, and optional `authorsFilePath`.
- Added backend validation for `svn_import`: source must use `http/https`, source credential cannot be `ssh_key`, recursive submodules are disabled, and webhook trigger is rejected.
- Reserved a dedicated execution branch for `svn_import`; for now it fails fast with a clear "not implemented yet" message instead of entering the Git mirror pipeline.
- Updated the task editor so the UI can switch between `Git Mirror` and `SVN Import` and show the relevant fields.
- Added store/service tests covering SVN task round-trip, validation, and guarded execution behavior.

## 2026-03-24 Incremental Update (2)

### Phase 7: SVN execution backbone
- Added `git svn clone/fetch` support in the Git client layer, including cache reuse for SVN import worktrees.
- Added SVN remote ref promotion so imported SVN refs can be materialized as Git branches and real Git tags before push.
- Added `refs/heads/*` and `refs/tags/*` push flow for `svn_import`, reusing the existing target repository auto-create pipeline.
- Upgraded `svn_import` from placeholder rejection to real asynchronous execution in the service layer, including execution nodes, cache records, logs, and status updates.
- Added Git client tests covering SVN clone argument generation and SVN remote ref classification.

## 2026-03-24 Incremental Update (3)

### Phase 7: SVN author mapping fallback
- Added `svnConfig.authorDomain` so SVN import can control the default email suffix used for synthesized Git authors.
- When `authors.txt` is not provided, `git svn` now uses a generated `authors-prog` helper and maps SVN authors to `name <name@domain>`.
- Default author domain now falls back to the SVN source host name in the service layer, with storage-level fallback to `svn.local`.
- Updated the management UI to explain the fallback behavior and allow overriding the default author domain.
- Added tests covering `authors-file` vs `authors-prog` argument selection and default author domain behavior.

## 2026-03-24 Incremental Update (4)

### Phase 7: target drift protection
- Changed `svn_import` target push semantics from force-update to normal branch/tag push so target refs must remain a read-only mirror.
- Added drift-aware error wrapping for `svn_import`: non-fast-forward branch updates and tag collisions now surface as explicit target drift failures.
- This aligns the implementation with the roadmap restriction that manually modified target Git repositories must fail instead of being auto-merged or overwritten.

## 2026-03-24 Incremental Update (5)

### Phase 7: cache namespace isolation
- Namespaced repository cache keys by task type so `git_mirror` and `svn_import` no longer collide when they point at the same source/target pair.
- Added a service-layer regression test to lock in distinct cache keys for different task types.
