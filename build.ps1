param([string]$OutputPath)
$ErrorActionPreference = 'Stop'
$hubFramework = Join-Path $env:WINDIR 'Microsoft.NET/Framework64/v4.0.30319'
$hubCompiler = Join-Path $hubFramework 'csc.exe'
if (-not (Test-Path -LiteralPath $hubCompiler)) {
    throw '.NET Framework 4.x C# compiler was not found. Windows x64 is required.'
}
$hubSource = Join-Path $PSScriptRoot 'ShutdownWatcher.cs'
$hubOutput = if ($OutputPath) { [System.IO.Path]::GetFullPath($OutputPath) } else { Join-Path $PSScriptRoot 'ShutdownWatcher.exe' }
& $hubCompiler /nologo /target:winexe /optimize+ '/reference:System.Windows.Forms.dll' '/reference:System.Drawing.dll' "/out:$hubOutput" $hubSource
if ($LASTEXITCODE -ne 0) { throw "ShutdownWatcher build failed: $LASTEXITCODE" }
Write-Output "Built $hubOutput"
