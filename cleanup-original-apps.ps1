$ErrorActionPreference='Stop'
$hubRoot=$PSScriptRoot
$hubCleanupLog=Join-Path $hubRoot 'data\original-apps-cleanup.json'
$hubInfoHub='C:\Program Files (x86)\ASUS InfoHub\ASUS InfoHub.exe'
$hubPortable='C:\Users\11274\Documents\Codex\2026-10-18\ai-z\work\aigo-app'
$hubResult=[ordered]@{success=$false;removedTasks=@();infoHubUninstalled=$false;aigoPortableRemoved=$false;temperatureServicePreserved=$false;error=$null}
try {
    foreach ($hubProcess in Get-CimInstance Win32_Process) {
        if ($hubProcess.ExecutablePath -and ($hubProcess.ExecutablePath.Equals($hubInfoHub,[StringComparison]::OrdinalIgnoreCase) -or $hubProcess.ExecutablePath.Equals((Join-Path $hubPortable 'AIGO.exe'),[StringComparison]::OrdinalIgnoreCase))) { Stop-Process -Id $hubProcess.ProcessId -Force -ErrorAction SilentlyContinue }
    }
    foreach ($hubTask in @('ASUS InfoHub1 StartupTask','ASUS InfoHub2 StartupTask','StartAIGO','Miku Screen Hub ASUS Initialization')) {
        if (Get-ScheduledTask -TaskName $hubTask -ErrorAction SilentlyContinue) { Unregister-ScheduledTask -TaskName $hubTask -Confirm:$false; $hubResult.removedTasks+= $hubTask }
    }
    $hubUninstaller='C:\Program Files (x86)\ASUS InfoHub\unins000.exe'
    if (Test-Path -LiteralPath $hubUninstaller) {
        $hubUninstallLog=Join-Path $hubRoot 'data\infohub-uninstall.log'
        $hubUninstall=Start-Process -FilePath $hubUninstaller -ArgumentList ('/VERYSILENT /SUPPRESSMSGBOXES /NORESTART /LOG="'+$hubUninstallLog+'"') -WindowStyle Hidden -PassThru
        $null=$hubUninstall.Handle
        $hubUninstall.WaitForExit()
        if ($null -ne $hubUninstall.ExitCode -and $hubUninstall.ExitCode -notin @(0,3010)) { throw ('InfoHub uninstaller exit code '+$hubUninstall.ExitCode) }
    }
    $hubResult.infoHubUninstalled=(-not (Test-Path -LiteralPath $hubInfoHub))
    if (-not $hubResult.infoHubUninstalled) { throw 'ASUS InfoHub executable remains after uninstall.' }
    if (Test-Path -LiteralPath $hubPortable) {
        $hubResolved=(Resolve-Path -LiteralPath $hubPortable).ProviderPath
        $hubAllowed='C:\Users\11274\Documents\Codex\2026-10-18\ai-z\work\'
        if (-not $hubResolved.StartsWith($hubAllowed,[StringComparison]::OrdinalIgnoreCase) -or $hubResolved -ne ($hubAllowed+'aigo-app')) { throw 'Portable removal target is outside the verified app directory.' }
        if ((Get-Item -LiteralPath $hubResolved).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Portable app directory is a reparse point.' }
        Remove-Item -LiteralPath $hubResolved -Recurse -Force
    }
    $hubResult.aigoPortableRemoved=(-not (Test-Path -LiteralPath $hubPortable))
    $hubService=Get-Service -Name 'asComSvc' -ErrorAction Stop
    $hubResult.temperatureServicePreserved=($hubService.Status -eq 'Running' -and (Test-Path -LiteralPath 'C:\Program Files (x86)\ASUS\AXSP\4.08.06\atkexComSvc.exe'))
    if (-not $hubResult.temperatureServicePreserved) { throw 'The required ASUS temperature service needs attention.' }
    & (Join-Path $hubRoot 'install-autostart.ps1')
    if (-not $?) { throw 'Failed to update native-only startup task.' }
    $hubResult.success=$true
} catch { $hubResult.error=$_.Exception.Message }
$hubResult | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $hubCleanupLog -Encoding UTF8
if (-not $hubResult.success) { exit 1 }
