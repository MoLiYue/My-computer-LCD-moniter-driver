param([switch]$LegacyNode)
$ErrorActionPreference = 'Stop'
$hubRoot = $PSScriptRoot
$hubData = Join-Path $hubRoot 'data'
New-Item -ItemType Directory -Path $hubData -Force | Out-Null
$hubLog = Join-Path $hubData 'autostart.log'
function Write-HubLog([string]$message) { Add-Content -LiteralPath $hubLog -Value ((Get-Date -Format 'yyyy-MM-dd HH:mm:ss')+' '+$message) -Encoding UTF8 }
function Get-HubStatus { try { $state=Invoke-RestMethod -Uri 'http://127.0.0.1:19330/api/status' -TimeoutSec 3; if ($state.name -eq 'Miku Screen Hub') { return $state } } catch {} return $null }
$hubMutex = New-Object Threading.Mutex($false,'Local\MikuScreenHub.Autostart')
$hubOwnsMutex = $false
try {
    try { $hubOwnsMutex=$hubMutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $hubOwnsMutex=$true }
    if (-not $hubOwnsMutex) { Write-HubLog 'Another autostart instance is active.'; return }
    if (Get-HubStatus) { Write-HubLog 'Already running; no duplicate instance launched.'; return }
    if ($LegacyNode) {
        $hubExe = 'C:\Program Files\nodejs\node.exe'
        $hubArgs = @('app.js','--start')
    } else {
        $hubExe = Join-Path $hubRoot 'MikuScreenHub.exe'
        $hubArgs = @('--start')
        if (-not (Test-Path -LiteralPath $hubExe -PathType Leaf)) { throw 'MikuScreenHub.exe not found; run build-go.ps1.' }
    }
    $hubProcess = Start-Process -FilePath $hubExe -ArgumentList $hubArgs -WorkingDirectory $hubRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $hubData 'hub-stdout.log') -RedirectStandardError (Join-Path $hubData 'hub-stderr.log') -PassThru
    $null=$hubProcess.Handle
    Write-HubLog ('Started '+[IO.Path]::GetFileName($hubExe)+' PID '+$hubProcess.Id+'.')
    $hubDeadline=(Get-Date).AddSeconds(120)
    $hubReady=$false
    do {
        Start-Sleep -Seconds 2
        $hubProcess.Refresh(); if ($hubProcess.HasExited) { throw 'Unified program exited during startup.' }
        $hubState=Get-HubStatus
        if ($hubState) {
            if ($hubState.screens.aigo.running -and $hubState.screens.aigo.sent -gt 0 -and $hubState.screens.asus.running -and $hubState.screens.asus.sent -gt 0) { $hubReady=$true; break }
        }
    } while ((Get-Date) -lt $hubDeadline)
    Write-HubLog $(if ($hubReady) { 'Both displays are sending frames.' } else { 'Startup wait expired; check the control panel for display errors.' })
    $hubProcess.WaitForExit()
    if ($null -ne $hubProcess.ExitCode -and $hubProcess.ExitCode -ne 0) { throw ('Unified program exited with code '+$hubProcess.ExitCode) }
    Write-HubLog 'Unified program exited normally.'
} catch {
    Write-HubLog ('ERROR: '+$_.Exception.Message)
    exit 1
} finally {
    if ($hubOwnsMutex) { $hubMutex.ReleaseMutex() }
    $hubMutex.Dispose()
}
