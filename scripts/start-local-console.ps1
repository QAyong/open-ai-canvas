[CmdletBinding()]
param([switch]$SkipBrowser)

# Single-terminal local dev launcher used by start-yingce-local.cmd.
# Backend and web share this console, so the desktop shortcut opens one window only.
# Upstream scripts/start-local.ps1 opens one window per service; that file stays untouched.

$ErrorActionPreference = "Stop"
# A double activation of the desktop shortcut starts two copies at once; both then race for
# ports 3000/8080 and the loser stops at "Startup failed" + pause in a second window. Hold a
# session-local mutex for the lifetime of the launcher so a duplicate activation exits instead.
$launcherMutex = New-Object System.Threading.Mutex($false, "YingceLocalServiceLauncher")
if (-not $launcherMutex.WaitOne(0)) {
    Write-Host "Yingce is already starting or running in another window; this window closes now." -ForegroundColor Yellow
    exit 0
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$backendDir = Join-Path $repoRoot "backend"
$webDir = Join-Path $repoRoot "web"
$dataDir = Join-Path $repoRoot ".local\project-workbench-debug"
$goBuildCache = Join-Path $repoRoot ".local\cache\go-build"
$goModuleCache = Join-Path $repoRoot ".local\cache\go-mod"
$statePath = Join-Path $repoRoot ".local\yingce-local-processes.json"
$appUrl = "http://localhost:3000"
# The readiness probe uses the IPv4 literal: localhost resolves to ::1 first on Windows
# while Vite listens on 127.0.0.1 only, and the refused IPv6 attempt can consume the whole
# timeout. The browser still opens $appUrl so its cookie and local data stay on one origin.
$probeUrl = "http://127.0.0.1:3000"

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
        throw "Local services are already running. Close the terminal that started them, then retry."
    }

    Remove-Item -LiteralPath $statePath -Force
}

foreach ($commandName in @("go", "bun")) {
    if (-not (Get-Command $commandName -ErrorAction SilentlyContinue)) {
        throw "Command not found: $commandName. Install the required runtime first."
    }
}

foreach ($directory in @($dataDir, $goBuildCache, $goModuleCache)) {
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
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

function Test-ListeningPort([int]$Port) {
    try {
        return $null -ne (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction Stop | Select-Object -First 1)
    } catch {
        return $false
    }
}

foreach ($port in @(3000, 8080)) {
    if (Test-ListeningPort $port) {
        throw "Port $port is already in use. Stop the process using it and retry."
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
Write-Host 'Backend: http://127.0.0.1:8080' -ForegroundColor Cyan
go run ./cmd/server
exit `$LASTEXITCODE
"@

$webCommand = @"
`$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $webDirLiteral
`$env:VITE_API_PROXY_TARGET = 'http://127.0.0.1:8080'
Write-Host 'Web app: http://localhost:3000' -ForegroundColor Cyan
bun run dev --host 127.0.0.1 --strictPort
exit `$LASTEXITCODE
"@

$trackedProcesses = @()
try {
    $backendEncoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($backendCommand))
    $backendProcess = Start-Process -FilePath $powerShellPath -NoNewWindow -WorkingDirectory $backendDir -PassThru -ArgumentList @("-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", $backendEncoded)
    $trackedProcesses += [ordered]@{
        role = "backend"
        pid = $backendProcess.Id
        startTime = Get-ProcessStartMarker $backendProcess
    }
    Save-LocalState $trackedProcesses

    $webEncoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($webCommand))
    $webProcess = Start-Process -FilePath $powerShellPath -NoNewWindow -WorkingDirectory $webDir -PassThru -ArgumentList @("-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", $webEncoded)
    $trackedProcesses += [ordered]@{
        role = "web"
        pid = $webProcess.Id
        startTime = Get-ProcessStartMarker $webProcess
    }
    Save-LocalState $trackedProcesses

    Write-Host "Waiting for backend and frontend (the first Go build may take a few minutes)..." -ForegroundColor Cyan
    $ready = $false
    $deadline = [DateTime]::UtcNow.AddMinutes(5)
    while ([DateTime]::UtcNow -lt $deadline) {
        if (@($trackedProcesses | Where-Object { Test-TrackedProcess $_ }).Count -ne $trackedProcesses.Count) {
            throw "A local service exited during startup. Check the output above."
        }
        try {
            $health = Invoke-RestMethod -Uri "$probeUrl/api/health" -TimeoutSec 5
            $page = Invoke-WebRequest -Uri $probeUrl -UseBasicParsing -TimeoutSec 5
            if ($health.code -eq 0 -and $page.StatusCode -eq 200) {
                $ready = $true
                break
            }
        } catch {
        }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) {
        throw "Startup timed out after 5 minutes. Check the service output above and $probeUrl/api/health."
    }

    Write-Host "Yingce is ready at $appUrl" -ForegroundColor Green
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
} finally {
    foreach ($process in $trackedProcesses) {
        if (Test-TrackedProcess $process) {
            Stop-ProcessTree ([int]$process.pid)
        }
    }
    Remove-Item -LiteralPath $statePath -Force -ErrorAction SilentlyContinue
}
