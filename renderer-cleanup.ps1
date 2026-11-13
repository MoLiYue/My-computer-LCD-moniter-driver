param([Parameter(Mandatory=$true)][string]$Profile)
$ErrorActionPreference = 'Stop'
$profilePath = [IO.Path]::GetFullPath($Profile)
$escaped = [regex]::Escape($profilePath)
# Chromium may quote the entire switch or just its value.
$profileSwitch = '(?:^|\s)(?:"--user-data-dir=' + $escaped + '"|--user-data-dir="' + $escaped + '"|--user-data-dir=' + $escaped + ')(?=\s|$)'
$browsers = @(Get-CimInstance Win32_Process -Filter "Name='chrome.exe' OR Name='msedge.exe'" |
    Where-Object { $_.CommandLine -match $profileSwitch })
if ($browsers.Count -eq 0) { return }

$byId = @{}
foreach ($browser in $browsers) { $byId[[int]$browser.ProcessId] = $browser }
# Verify all owners before touching anything. A PID alone is not an identity.
foreach ($browser in $browsers) {
    $ownerId = [int]$browser.ParentProcessId
    $owner = $byId[$ownerId]
    if ($owner -and $owner.CreationDate -le $browser.CreationDate) { continue }
    $owner = Get-CimInstance Win32_Process -Filter "ProcessId=$ownerId"
    if ($owner -and $owner.CreationDate -le $browser.CreationDate) {
        throw "LCD browser profile is still in use by a live process (PID $ownerId)"
    }
}

$stopped = 0
foreach ($browser in $browsers | Sort-Object CreationDate) {
    $browserId = [int]$browser.ProcessId
    $process = Get-Process -Id $browserId -ErrorAction SilentlyContinue
    if (-not $process) { continue }
    try {
        # Hold an OS handle while rechecking the PID to avoid stopping a reused one.
        $null = $process.Handle
        $current = Get-CimInstance Win32_Process -Filter "ProcessId=$browserId"
        if ($current -and $current.CreationDate -eq $browser.CreationDate -and
            $current.Name -eq $browser.Name -and $current.CommandLine -match $profileSwitch) {
            $process.Kill()
            if (-not $process.WaitForExit(1200)) { throw "Orphaned LCD browser did not exit (PID $browserId)" }
            $stopped++
        }
    } catch {
        if (-not $process.HasExited) { throw }
    } finally { $process.Dispose() }
}
if ($stopped -gt 0) { Write-Output "Stopped $stopped orphaned LCD browser process(es)." }
