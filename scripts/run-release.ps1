$ErrorActionPreference = "Stop"

$ReleaseRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$EnvFile = Join-Path $ReleaseRoot "config\reposync.env"
$BackendExe = Join-Path $ReleaseRoot "backend\reposync.exe"

if (-not (Test-Path $BackendExe)) {
  throw "Backend binary not found: $BackendExe"
}

if (Test-Path $EnvFile) {
  Get-Content $EnvFile | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#")) {
      return
    }
    $parts = $line -split "=", 2
    if ($parts.Length -eq 2) {
      [System.Environment]::SetEnvironmentVariable($parts[0].Trim(), $parts[1].Trim())
    }
  }
}

if (-not $env:REPOSYNC_FRONTEND_DIST) {
  $env:REPOSYNC_FRONTEND_DIST = (Join-Path $ReleaseRoot "frontend\dist")
}
if (-not $env:REPOSYNC_DB_PATH) {
  $env:REPOSYNC_DB_PATH = (Join-Path $ReleaseRoot "data\reposync.db")
}
if (-not $env:REPOSYNC_CACHE_DIR) {
  $env:REPOSYNC_CACHE_DIR = (Join-Path $ReleaseRoot "data\cache")
}
$env:NO_PROXY = "localhost,127.0.0.1,10.0.0.0/8,192.168.0.0/16,.tailbee4e7.ts.net"
& $BackendExe
