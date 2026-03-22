$ErrorActionPreference = "Stop"

param(
  [ValidateSet("install", "start", "stop", "restart", "uninstall", "status")]
  [string]$Action = "status",
  [string]$ServiceName = "RepoSync",
  [string]$DisplayName = "RepoSync",
  [string]$Description = "RepoSync Git mirror synchronization service"
)

function Test-IsAdministrator {
  $currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($currentIdentity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-ServiceCommandLine {
  param([string]$ReleaseRoot)

  $powershellExe = Join-Path $env:WINDIR "System32\WindowsPowerShell\v1.0\powershell.exe"
  $runScript = Join-Path $ReleaseRoot "run.ps1"
  if (-not (Test-Path $runScript)) {
    throw "Release run script not found: $runScript"
  }
  return "`"$powershellExe`" -NoProfile -ExecutionPolicy Bypass -File `"$runScript`""
}

function Get-ExistingService {
  param([string]$Name)
  return Get-Service -Name $Name -ErrorAction SilentlyContinue
}

if (-not (Test-IsAdministrator)) {
  throw "This script must be run as Administrator."
}

$ReleaseRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$service = Get-ExistingService -Name $ServiceName

switch ($Action) {
  "install" {
    if ($service) {
      throw "Service already exists: $ServiceName"
    }
    $binPath = Get-ServiceCommandLine -ReleaseRoot $ReleaseRoot
    sc.exe create $ServiceName binPath= $binPath start= auto DisplayName= $DisplayName | Out-Null
    sc.exe description $ServiceName $Description | Out-Null
    Write-Host "Installed Windows service: $ServiceName"
    Write-Host "Use '$PSCommandPath -Action start' to start it."
  }
  "start" {
    if (-not $service) {
      throw "Service not found: $ServiceName"
    }
    Start-Service -Name $ServiceName
    Write-Host "Started Windows service: $ServiceName"
  }
  "stop" {
    if (-not $service) {
      throw "Service not found: $ServiceName"
    }
    Stop-Service -Name $ServiceName
    Write-Host "Stopped Windows service: $ServiceName"
  }
  "restart" {
    if (-not $service) {
      throw "Service not found: $ServiceName"
    }
    Restart-Service -Name $ServiceName
    Write-Host "Restarted Windows service: $ServiceName"
  }
  "uninstall" {
    if (-not $service) {
      throw "Service not found: $ServiceName"
    }
    if ($service.Status -ne "Stopped") {
      Stop-Service -Name $ServiceName -Force
    }
    sc.exe delete $ServiceName | Out-Null
    Write-Host "Removed Windows service: $ServiceName"
  }
  "status" {
    if (-not $service) {
      Write-Host "Service not found: $ServiceName"
      exit 0
    }
    Get-Service -Name $ServiceName | Format-Table -AutoSize Name, Status, StartType
  }
}
