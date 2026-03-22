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
$EmbeddedFrontendDir = Join-Path $BackendDir "internal\app\embedded"

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

Write-Host "Embedding frontend assets into backend binary..."
$EmbeddedBackupDir = Join-Path ([System.IO.Path]::GetTempPath()) ("reposync-embedded-backup-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $EmbeddedBackupDir | Out-Null
Copy-Item -Force (Join-Path $EmbeddedFrontendDir "*") $EmbeddedBackupDir
Remove-Item -Recurse -Force (Join-Path $EmbeddedFrontendDir "*")
Copy-Item -Recurse -Force (Join-Path $FrontendDir "dist\*") $EmbeddedFrontendDir

$pushedBackend = $false
try {
  Push-Location $BackendDir
  $pushedBackend = $true
  Invoke-NativeCommand "go.exe" @("build", "-o", (Join-Path $ReleaseBackendDir "reposync.exe"), "./cmd/server")
} finally {
  if ($pushedBackend) {
    Pop-Location
  }
  Remove-Item -Recurse -Force (Join-Path $EmbeddedFrontendDir "*")
  Copy-Item -Recurse -Force (Join-Path $EmbeddedBackupDir "*") $EmbeddedFrontendDir
  Remove-Item -Recurse -Force $EmbeddedBackupDir
}

Copy-Item -Recurse -Force (Join-Path $FrontendDir "dist") $ReleaseFrontendDir
Copy-Item -Force (Join-Path $RepoRoot "scripts\reposync.env.example") (Join-Path $ReleaseConfigDir "reposync.env.example")
Copy-Item -Force (Join-Path $RepoRoot "scripts\run-release.ps1") (Join-Path $ReleaseDir "run.ps1")
Copy-Item -Force (Join-Path $RepoRoot "scripts\run-release.sh") (Join-Path $ReleaseDir "run.sh")
Copy-Item -Force (Join-Path $RepoRoot "scripts\manage-windows-service.ps1") (Join-Path $ReleaseDir "windows-service.ps1")
Copy-Item -Force (Join-Path $RepoRoot "docs\deployment.md") (Join-Path $ReleaseDir "DEPLOYMENT.md")

Write-Host "Release bundle created at $ReleaseDir"
