@echo off
setlocal
cd /d "%~dp0"

rem Single-window launcher: backend and web run inside this terminal, so the
rem desktop shortcut opens exactly one window. Closing it stops every service.
rem Upstream scripts\start-local.ps1 opens one window per service; keep it as is.
rem Set YINGCE_NO_BROWSER=1 to start the services without opening a browser tab.

where pwsh.exe >nul 2>nul
if errorlevel 1 (
    set "YINGCE_PS=powershell.exe"
) else (
    set "YINGCE_PS=pwsh.exe"
)
where %YINGCE_PS% >nul 2>nul
if errorlevel 1 (
    echo PowerShell was not found. Install PowerShell 7 from https://aka.ms/powershell and retry.
    pause
    exit /b 1
)

set "YINGCE_BROWSER_ARG="
if defined YINGCE_NO_BROWSER set "YINGCE_BROWSER_ARG=-SkipBrowser"

%YINGCE_PS% -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-local-console.ps1" %YINGCE_BROWSER_ARG%
if errorlevel 1 (
    echo.
    echo Startup failed. Review the error above; the full launcher log is at:
    echo   %~dp0.local\logs\yingce-local-launcher.log
    pause
)
endlocal
