$ErrorActionPreference = "Stop"

function Invoke-NativeCommand {
  param(
    [string]$Command,
    [string[]]$Arguments
  )

  & $Command @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "Command failed: $Command $($Arguments -join ' ')"
  }
}

function Invoke-CmdCommand {
  param(
    [string]$CommandLine
  )

  cmd.exe /c $CommandLine
  if ($LASTEXITCODE -ne 0) {
    throw "Command failed: $CommandLine"
  }
}

$RepoRoot = Split-Path -Parent $PSScriptRoot
$ReleaseDir = Join-Path $RepoRoot "release"
$BackendDir = Join-Path $RepoRoot "backend"
$FrontendDir = Join-Path $RepoRoot "frontend"
$ReleaseBackendDir = Join-Path $ReleaseDir "backend"
$ReleaseFrontendDir = Join-Path $ReleaseDir "frontend"
$ReleaseConfigDir = Join-Path $ReleaseDir "config"
$ReleaseDataDir = Join-Path $ReleaseDir "data"

Write-Host "Building frontend..."
Push-Location $FrontendDir
if (-not (Test-Path (Join-Path $FrontendDir "node_modules\vite\bin\vite.js"))) {
  Invoke-CmdCommand "npm.cmd ci"
}
Invoke-NativeCommand "node.exe" @((Join-Path $FrontendDir "node_modules\vite\bin\vite.js"), "build")
Pop-Location

Write-Host "Building backend..."
if (Test-Path $ReleaseDir) {
  Remove-Item -Recurse -Force $ReleaseDir
}

New-Item -ItemType Directory -Force -Path $ReleaseBackendDir | Out-Null
New-Item -ItemType Directory -Force -Path $ReleaseFrontendDir | Out-Null
New-Item -ItemType Directory -Force -Path $ReleaseConfigDir | Out-Null
New-Item -ItemType Directory -Force -Path $ReleaseDataDir | Out-Null

Push-Location $BackendDir
Invoke-NativeCommand "go.exe" @("build", "-o", (Join-Path $ReleaseBackendDir "reposync.exe"), "./cmd/server")
Pop-Location

Copy-Item -Recurse -Force (Join-Path $FrontendDir "dist") $ReleaseFrontendDir
Copy-Item -Force (Join-Path $RepoRoot "scripts\reposync.env.example") (Join-Path $ReleaseConfigDir "reposync.env.example")
Copy-Item -Force (Join-Path $RepoRoot "scripts\run-release.ps1") (Join-Path $ReleaseDir "run.ps1")
Copy-Item -Force (Join-Path $RepoRoot "scripts\run-release.sh") (Join-Path $ReleaseDir "run.sh")
Copy-Item -Force (Join-Path $RepoRoot "docs\deployment.md") (Join-Path $ReleaseDir "DEPLOYMENT.md")

Write-Host "Release bundle created at $ReleaseDir"
