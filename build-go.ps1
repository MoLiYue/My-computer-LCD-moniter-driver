param([string]$GoPath = 'go', [string]$OutputPath = (Join-Path $PSScriptRoot 'MikuScreenHub.exe'), [switch]$Test)
$ErrorActionPreference = 'Stop'
$output = [IO.Path]::GetFullPath($OutputPath)
$savedEnvironment = @{ GOOS=$env:GOOS; GOARCH=$env:GOARCH; CGO_ENABLED=$env:CGO_ENABLED }
Push-Location -LiteralPath $PSScriptRoot
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    # vendor/ belongs to the legacy Node version, not to Go modules.
    if ($Test) {
        & $GoPath test -mod=readonly .
        if ($LASTEXITCODE -ne 0) { throw "Go tests failed: $LASTEXITCODE" }
    }
    & $GoPath build -mod=readonly -trimpath -ldflags '-s -w' -o $output .
    if ($LASTEXITCODE -ne 0) { throw "Go build failed: $LASTEXITCODE" }
    Write-Output "Built $output"
} finally {
    Pop-Location
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
}
