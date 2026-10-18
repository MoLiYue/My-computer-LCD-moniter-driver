param([switch]$Remove)
$ErrorActionPreference='Stop'
$hubRoot=$PSScriptRoot
$hubInstallLog=Join-Path $hubRoot 'data\autostart-install.log'
$hubTaskName='Miku Screen Hub'
$hubInitTaskName='Miku Screen Hub ASUS Initialization'
$hubAccount='MLYUE-GAMING-MA\11274'
$hubSid='S-1-5-21-3523813906-1649781413-1406611198-1001'
$hubOldTasks=@('ASUS InfoHub1 StartupTask','ASUS InfoHub2 StartupTask','StartAIGO')
try {
    if ($Remove) {
        Unregister-ScheduledTask -TaskName $hubTaskName -Confirm:$false -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $hubInitTaskName -Confirm:$false -ErrorAction SilentlyContinue
        Set-Content -LiteralPath $hubInstallLog -Value 'REMOVED: Unified autostart removed. Original software tasks are not recreated.' -Encoding UTF8
        exit 0
    }
    $hubPowerShell=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $hubAction=New-ScheduledTaskAction -Execute $hubPowerShell -Argument ('-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -File "'+(Join-Path $hubRoot 'autostart.ps1')+'"') -WorkingDirectory $hubRoot
    $hubTrigger=New-ScheduledTaskTrigger -AtLogOn -User $hubAccount
    $hubTrigger.Delay='PT20S'
    $hubPrincipal=New-ScheduledTaskPrincipal -UserId $hubSid -LogonType Interactive -RunLevel Limited
    $hubSettings=New-ScheduledTaskSettingsSet -Hidden -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $hubTaskName -Action $hubAction -Trigger $hubTrigger -Principal $hubPrincipal -Settings $hubSettings -Description 'Start both custom LCD displays after this user logs in, using native HID initialization. Runs hidden at normal user privilege.' -Force | Out-Null
    foreach ($hubOldTask in ($hubOldTasks+@($hubInitTaskName))) { if (Get-ScheduledTask -TaskName $hubOldTask -ErrorAction SilentlyContinue) { Unregister-ScheduledTask -TaskName $hubOldTask -Confirm:$false } }
    Set-Content -LiteralPath $hubInstallLog -Value 'SUCCESS: At logon +20 seconds, limited user, hidden. Native LCD initialization; no InfoHub dependency. Original AIGO/InfoHub startup tasks and temporary initialization task removed.' -Encoding UTF8
} catch {
    Set-Content -LiteralPath $hubInstallLog -Value ('ERROR: '+$_.Exception.Message) -Encoding UTF8
    exit 1
}
