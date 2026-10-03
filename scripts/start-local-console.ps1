[CmdletBinding()]
param([switch]$SkipBrowser)

# Single-terminal local dev launcher used by start-yingce-local.cmd.
# Backend and web share this console, so the desktop shortcut opens one window only.
# Upstream scripts/start-local.ps1 opens one window per service; that file stays untouched.

$ErrorActionPreference = "Stop"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$backendDir = Join-Path $repoRoot "backend"
$webDir = Join-Path $repoRoot "web"
$dataDir = Join-Path $repoRoot ".local\project-workbench-debug"
$goBuildCache = Join-Path $repoRoot ".local\cache\go-build"
$goModuleCache = Join-Path $repoRoot ".local\cache\go-mod"
$statePath = Join-Path $repoRoot ".local\yingce-local-processes.json"
# Every run overwrites these files. Service output goes here instead of the console so a
# window that is closed (or killed) mid-startup still leaves readable evidence behind.
$logDir = Join-Path $repoRoot ".local\logs"
$launcherLog = Join-Path $logDir "yingce-local-launcher.log"
$backendLog = Join-Path $logDir "yingce-backend.log"
$backendErrorLog = Join-Path $logDir "yingce-backend.error.log"
$webLog = Join-Path $logDir "yingce-web.log"
$webErrorLog = Join-Path $logDir "yingce-web.error.log"
$appUrl = "http://localhost:3000"
# Readiness talks to the backend directly and to the web root, never to http://127.0.0.1:3000/api/...:
# a proxied API request sent before the backend listens makes Vite print a burst of
# "http proxy error ... ECONNREFUSED 127.0.0.1:8080" lines that read like real failures.
$backendHealthUrl = "http://127.0.0.1:8080/api/health"
# The probe uses the IPv4 literal: localhost resolves to ::1 first on Windows while Vite
# listens on 127.0.0.1 only. The browser still opens $appUrl so its cookie and local data
# stay on one origin.
$webProbeUrl = "http://127.0.0.1:3000"

foreach ($directory in @($logDir, $dataDir, $goBuildCache, $goModuleCache)) {
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
}

function Show-ServiceLog([string]$Role, [string]$StandardLog, [string]$ErrorLog) {
    foreach ($path in @($StandardLog, $ErrorLog)) {
        if (-not (Test-Path -LiteralPath $path)) {
            continue
        }
        Write-Host "--- $Role output: $path (last 25 lines) ---" -ForegroundColor Yellow
        Get-Content -LiteralPath $path -Tail 25 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host $_ }
    }
}

function Show-ServiceLogs {
    Show-ServiceLog "backend" $backendLog $backendErrorLog
    Show-ServiceLog "web" $webLog $webErrorLog
    Write-Host "Launcher log: $launcherLog" -ForegroundColor Yellow
}

# A double activation of the desktop shortcut starts two copies at once; both then race for
# ports 3000/8080 and the loser stops at "Startup failed" + pause in a second window. Hold a
# session-local mutex for the lifetime of the launcher so a duplicate activation exits instead.
$launcherMutex = New-Object System.Threading.Mutex($false, "YingceLocalServiceLauncher")
$ownsLauncherMutex = $false
try {
    $ownsLauncherMutex = $launcherMutex.WaitOne(0)
} catch {
    # A launcher killed mid-run (closed window, reboot) leaves an abandoned mutex; the wait
    # throws, yet the OS still hands ownership over, so startup must continue instead of
    # failing with a .NET error that hides the real state.
    if ($_.Exception -is [System.Threading.AbandonedMutexException] -or $_.Exception.InnerException -is [System.Threading.AbandonedMutexException]) {
        $ownsLauncherMutex = $true
    } else {
        throw
    }
}

if (-not $ownsLauncherMutex) {
    # Exiting immediately hides this message; keep the window readable for a few seconds.
    Write-Host "Yingce is already starting or running in another window; this window closes in 8 seconds." -ForegroundColor Yellow
    Start-Sleep -Seconds 8
    exit 0
}

# Started only after the single-instance check: the running launcher holds this transcript
# open, and a duplicate activation has nothing worth logging.
try {
    Start-Transcript -Path $launcherLog -Force | Out-Null
} catch {
    Write-Host "Cannot write the launcher log at $launcherLog" -ForegroundColor Yellow
}

Add-Type -Path (Join-Path $PSScriptRoot "local-process-job.cs")
[YingceLocalProcessJob]::AttachCurrentProcess()
try {
    $Host.UI.RawUI.WindowTitle = "Yingce - local services"
} catch {
    # Non-interactive hosts have no window title; startup must not fail because of it.
}

function Get-ProcessStartMarker([System.Diagnostics.Process]$Process) {
    try {
        return $Process.StartTime.ToUniversalTime().ToString("o")
    } catch {
        return ""
    }
}

function Test-TrackedProcess([object]$Record) {
    $processId = 0
    if (-not [int]::TryParse([string]$Record.pid, [ref]$processId)) {
        return $false
    }

    $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        return $false
    }

    $expectedStartTime = [string]$Record.startTime
    if ([string]::IsNullOrWhiteSpace($expectedStartTime)) {
        return $false
    }
    return (Get-ProcessStartMarker $process) -eq $expectedStartTime
}

function Test-TcpListener([string]$TargetHost, [int]$Port, [int]$TimeoutMs = 500) {
    # A refused connection faults the connect task; checking the task is cheaper and quieter
    # than catching the exception of an HTTP request per polling round.
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $connect = $client.ConnectAsync($TargetHost, $Port)
        if (-not $connect.AsyncWaitHandle.WaitOne($TimeoutMs)) {
            return $false
        }
        return $client.Connected
    } catch {
        return $false
    } finally {
        $client.Close()
    }
}

function Stop-ProcessTree([int]$ProcessId) {
    if ($null -eq (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
        return
    }

    $taskKill = Get-Command taskkill.exe -ErrorAction SilentlyContinue
    if ($null -ne $taskKill) {
        & $taskKill.Source /PID $ProcessId /T /F *> $null
        return
    }

    Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
}

if (Test-Path -LiteralPath $statePath) {
    try {
        $previousState = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    } catch {
        throw "Cannot read local service state file: $statePath"
    }

    $activePreviousProcesses = @($previousState.processes | Where-Object { Test-TrackedProcess $_ })
    if ($activePreviousProcesses.Count -gt 0) {
        $activePids = ($activePreviousProcesses | ForEach-Object { [string]$_.pid }) -join ", "
        throw "Local services are already running (pid $activePids). Close the window that started them, or stop them with: taskkill /PID $($activePreviousProcesses[0].pid) /T /F"
    }

    Remove-Item -LiteralPath $statePath -Force
}

foreach ($commandName in @("go", "bun")) {
    if (-not (Get-Command $commandName -ErrorAction SilentlyContinue)) {
        throw "Command not found: $commandName. Install the required runtime first."
    }
}

$viteBinary = Join-Path $webDir "node_modules\.bin\vite"
if (-not (Test-Path -LiteralPath $viteBinary)) {
    Write-Host "web/node_modules is missing; running bun install --frozen-lockfile..." -ForegroundColor Yellow
    Push-Location $webDir
    try {
        & bun install --frozen-lockfile
        if ($LASTEXITCODE -ne 0) {
            throw "bun install failed; the web app cannot start."
        }
    } finally {
        Pop-Location
    }
}

foreach ($port in @(3000, 8080)) {
    $portOwner = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $portOwner) {
        throw "Port $port is already in use by pid $($portOwner.OwningProcess). Stop that process and retry."
    }
}

$powerShellPath = (Get-Command pwsh -ErrorAction SilentlyContinue).Source
if (-not $powerShellPath) {
    $powerShellPath = (Get-Command powershell.exe -ErrorAction SilentlyContinue).Source
}
if (-not $powerShellPath) {
    throw "PowerShell was not found; cannot start the local services."
}

function ConvertTo-PowerShellLiteral([string]$Value) {
    return "'" + $Value.Replace("'", "''") + "'"
}

function Save-LocalState([object[]]$Processes) {
    $state = [ordered]@{
        version = 1
        createdAt = [DateTime]::UtcNow.ToString("o")
        processes = $Processes
    }
    $state | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $statePath -Encoding UTF8
}

$backendDirLiteral = ConvertTo-PowerShellLiteral $backendDir
$webDirLiteral = ConvertTo-PowerShellLiteral $webDir
$dataDirLiteral = ConvertTo-PowerShellLiteral $dataDir

$backendCommand = @"
`$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $backendDirLiteral
`$env:CANVAS_BACKEND_ADDR = '127.0.0.1:8080'
`$env:CANVAS_BACKEND_DATA_DIR = $dataDirLiteral
`$env:GOCACHE = $(ConvertTo-PowerShellLiteral $goBuildCache)
`$env:GOMODCACHE = $(ConvertTo-PowerShellLiteral $goModuleCache)
go run ./cmd/server
exit `$LASTEXITCODE
"@

$webCommand = @"
`$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $webDirLiteral
`$env:VITE_API_PROXY_TARGET = 'http://127.0.0.1:8080'
bun run dev --host 127.0.0.1 --strictPort
exit `$LASTEXITCODE
"@

$trackedProcesses = @()
$servicesStarted = $false
try {
    $backendEncoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($backendCommand))
    $backendProcess = Start-Process -FilePath $powerShellPath -NoNewWindow -WorkingDirectory $backendDir -PassThru -RedirectStandardOutput $backendLog -RedirectStandardError $backendErrorLog -ArgumentList @("-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", $backendEncoded)
    $trackedProcesses += [ordered]@{
        role = "backend"
        pid = $backendProcess.Id
        startTime = Get-ProcessStartMarker $backendProcess
    }
    Save-LocalState $trackedProcesses

    $webEncoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($webCommand))
    $webProcess = Start-Process -FilePath $powerShellPath -NoNewWindow -WorkingDirectory $webDir -PassThru -RedirectStandardOutput $webLog -RedirectStandardError $webErrorLog -ArgumentList @("-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", $webEncoded)
    $trackedProcesses += [ordered]@{
        role = "web"
        pid = $webProcess.Id
        startTime = Get-ProcessStartMarker $webProcess
    }
    Save-LocalState $trackedProcesses
    $servicesStarted = $true

    Write-Host "Backend (127.0.0.1:8080) and web ($appUrl) are starting." -ForegroundColor Cyan
    Write-Host "Service output: $backendLog , $webLog" -ForegroundColor DarkGray
    Write-Host "The first Go build can take a few minutes; progress is printed below." -ForegroundColor Cyan

    $startedAt = [DateTime]::UtcNow
    $deadline = $startedAt.AddMinutes(10)
    $nextHeartbeat = $startedAt.AddSeconds(20)
    $ready = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        if (@($trackedProcesses | Where-Object { Test-TrackedProcess $_ }).Count -ne $trackedProcesses.Count) {
            throw "A local service exited during startup. Check the service output below."
        }

        $backendState = "starting"
        $webState = "waiting"
        # Probe the socket before the URL: a refused HTTP request is expected while the backend
        # compiles, and PowerShell records every failed request in the transcript as an error.
        if (Test-TcpListener "127.0.0.1" 8080) {
            try {
                $health = Invoke-RestMethod -Uri $backendHealthUrl -TimeoutSec 5
                if ($health.code -eq 0) {
                    $backendState = "up"
                }
            } catch {
            }
        }
        if ($backendState -eq "up" -and (Test-TcpListener "127.0.0.1" 3000)) {
            try {
                $page = Invoke-WebRequest -Uri $webProbeUrl -UseBasicParsing -TimeoutSec 5
                if ($page.StatusCode -eq 200) {
                    $webState = "up"
                }
            } catch {
            }
        }

        if ($backendState -eq "up" -and $webState -eq "up") {
            $ready = $true
            break
        }

        if ([DateTime]::UtcNow -ge $nextHeartbeat) {
            $elapsedSeconds = [int]([DateTime]::UtcNow - $startedAt).TotalSeconds
            Write-Host ("Waiting for local services... {0}s (backend: {1}, web: {2})" -f $elapsedSeconds, $backendState, $webState) -ForegroundColor DarkGray
            $nextHeartbeat = [DateTime]::UtcNow.AddSeconds(20)
        }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) {
        throw "Startup timed out after 10 minutes. Check the service output below."
    }

    Write-Host "Yingce is ready at $appUrl" -ForegroundColor Green
    Write-Host "Logs: $backendLog , $webLog , $launcherLog" -ForegroundColor DarkGray
    Write-Host "Keep this terminal open; closing it stops the local services." -ForegroundColor Yellow
    if (-not $SkipBrowser) {
        # Open the project with the user's default browser.
        Start-Process -FilePath $appUrl
        Write-Host "If no browser window appeared, open $appUrl manually." -ForegroundColor Yellow
    }

    while ($true) {
        $activeProcesses = @($trackedProcesses | Where-Object { Test-TrackedProcess $_ })
        if ($activeProcesses.Count -eq 0) {
            break
        }
        if ($activeProcesses.Count -lt $trackedProcesses.Count) {
            Write-Host "A local service exited; stopping the remaining service." -ForegroundColor Yellow
            break
        }
        Start-Sleep -Seconds 1
    }
} catch {
    Write-Host ""
    Write-Host "Startup failed: $($_.Exception.Message)" -ForegroundColor Red
    if ($servicesStarted) {
        # Only meaningful once the services ran; earlier failures leave the previous run's logs on disk.
        Show-ServiceLogs
    }
    throw
} finally {
    foreach ($process in $trackedProcesses) {
        if (Test-TrackedProcess $process) {
            Stop-ProcessTree ([int]$process.pid)
        }
    }
    Remove-Item -LiteralPath $statePath -Force -ErrorAction SilentlyContinue

    if ($ownsLauncherMutex) {
        try {
            [void]$launcherMutex.ReleaseMutex()
        } catch {
        }
    }
    try {
        Stop-Transcript | Out-Null
    } catch {
    }
}
