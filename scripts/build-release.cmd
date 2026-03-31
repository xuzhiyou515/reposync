@echo off
setlocal

set "SCRIPT_DIR=%~dp0"
if /I "%SCRIPT_DIR:~0,4%"=="\\?\" set "SCRIPT_DIR=%SCRIPT_DIR:~4%"
cd /d "%SCRIPT_DIR%"
%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%build-release.ps1"
exit /b %ERRORLEVEL%
