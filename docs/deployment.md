# RepoSync 部署说明

## 1. 运行要求

- Go 1.23+
- Node.js 20+
- Git 命令行可用，并且可被 `REPOSYNC_GIT_BIN` 找到

如果需要通过 SSH 同步私有仓库，部署机还需要具备基础 `ssh` 能力。

## 2. 前端静态资源加载策略

- 发布构建时会把前端 `dist/` 内嵌进后端二进制
- 运行时如果 `REPOSYNC_FRONTEND_DIST` 指向的目录存在且包含 `index.html`，则优先使用外部目录
- 否则自动回退到程序内嵌的前端资源

这意味着：
- 单文件发布时，后端程序本身已经能提供前端页面
- 如果你希望独立替换前端，也可以继续提供外部 `dist/` 目录覆盖

## 3. 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `REPOSYNC_HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `REPOSYNC_DB_PATH` | `data/reposync.db` | SQLite 数据库文件 |
| `REPOSYNC_CACHE_DIR` | `data/cache` | 默认缓存根目录 |
| `REPOSYNC_FRONTEND_DIST` | `../frontend/dist` | 前端静态产物目录 |
| `REPOSYNC_GIT_BIN` | `git` | Git 可执行文件 |
| `REPOSYNC_SECRET_KEY` | `reposync-dev-secret` | 凭证加密密钥，生产必须修改 |

建议至少在生产环境显式配置：
- `REPOSYNC_SECRET_KEY`
- `REPOSYNC_DB_PATH`
- `REPOSYNC_CACHE_DIR`
- `REPOSYNC_FRONTEND_DIST`

## 4. 本地构建发布包

### Windows PowerShell

```powershell
./scripts/build-release.ps1
```

### Linux / macOS

```bash
chmod +x ./scripts/build-release.sh
./scripts/build-release.sh
```

构建完成后会生成：

```text
release/
  backend/
    reposync(.exe)
  frontend/
    dist/
  config/
    reposync.env.example
  data/
  run.ps1
  run.sh
  windows-service.ps1
  DEPLOYMENT.md
```

## 5. 启动发布包

先复制环境变量模板：

### Windows PowerShell

```powershell
Copy-Item .\release\config\reposync.env.example .\release\config\reposync.env
```

### Linux / macOS

```bash
cp ./release/config/reposync.env.example ./release/config/reposync.env
```

然后修改 `reposync.env`，至少替换 `REPOSYNC_SECRET_KEY`。

启动命令：

### Windows PowerShell

```powershell
./release/run.ps1
```

### Linux / macOS

```bash
chmod +x ./release/run.sh
./release/run.sh
```

启动脚本会自动把以下路径指向发布包内部目录：
- `REPOSYNC_FRONTEND_DIST=./frontend/dist`
- `REPOSYNC_DB_PATH=./data/reposync.db`
- `REPOSYNC_CACHE_DIR=./data/cache`

如果你删除 `release/frontend/dist`，程序仍然可以使用二进制内嵌的前端页面启动。

## 8. 注册为 Windows 服务

发布包中包含 `release/windows-service.ps1`，可用于把 RepoSync 注册为 Windows 服务。

建议先进入 release 目录，再用管理员权限的 PowerShell 安装服务。

安装步骤：
- `release/config/reposync.env`
- `REPOSYNC_SECRET_KEY`
- `REPOSYNC_DB_PATH`
- `REPOSYNC_CACHE_DIR`

示例 PowerShell 命令：

```powershell
Set-Location .\release
.\windows-service.ps1 -Action install
.\windows-service.ps1 -Action start
```

常用操作：

```powershell
.\windows-service.ps1 -Action status
.\windows-service.ps1 -Action restart
.\windows-service.ps1 -Action stop
.\windows-service.ps1 -Action uninstall
```

说明：
- 默认服务名为 `RepoSync`
- 服务实际通过 `powershell.exe -> run.ps1 -> reposync.exe` 方式启动
- `run.ps1` 会自动加载 `config/reposync.env`
- 重建 release 包前建议先停止服务，避免文件占用
- 如需自定义名称，可在脚本参数中传入 `-ServiceName` 与 `-DisplayName`

## 6. 反向代理建议

RepoSync 自身会同时提供 API 和前端页面，最简单的部署方式是直接暴露应用端口。

如果需要走 Nginx / Caddy：
- 反向代理所有请求到 RepoSync HTTP 端口
- 保留 `/api/executions/:id/ws` 的 WebSocket Upgrade 头
- 不要拦截 `/api/executions/:id/stream` 的 SSE 长连接

## 7. 升级建议

升级时建议保留并备份：
- `data/reposync.db`
- `data/cache/`
- 自己维护的 `config/reposync.env`

替换新的二进制和前端静态文件后重启即可。


如果 Windows 服务启动失败，优先检查以下内容：

```powershell
Set-Location .\release
.\windows-service.ps1 -Action stop
```

也可以直接在前台执行发布脚本辅助排查：

```powershell
Set-Location .\release
.\windows-service.ps1 -Action start
```
