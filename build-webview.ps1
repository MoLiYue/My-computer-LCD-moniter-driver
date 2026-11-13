param([string]$OutputDirectory = (Join-Path $PSScriptRoot 'data/webview-bin'))
$ErrorActionPreference = 'Stop'
$version = '1.0.4022.49'
$cache = Join-Path $PSScriptRoot 'data/webview-sdk'
$sdk = Join-Path $cache $version
$library = Join-Path $sdk 'lib/net462/Microsoft.Web.WebView2.Core.dll'
$loader = Join-Path $sdk 'runtimes/win-x64/native/WebView2Loader.dll'
if (-not (Test-Path -LiteralPath $library) -or -not (Test-Path -LiteralPath $loader)) {
    New-Item -ItemType Directory -Force -Path $cache | Out-Null
    $archive = Join-Path $cache ("webview2-$version.zip")
    $uri = "https://api.nuget.org/v3-flatcontainer/microsoft.web.webview2/$version/microsoft.web.webview2.$version.nupkg"
    & curl.exe --fail --silent --show-error --location --max-time 120 $uri -o $archive
    if ($LASTEXITCODE -ne 0) { throw 'WebView2 SDK download failed' }
    Expand-Archive -LiteralPath $archive -DestinationPath $sdk -Force
}
$compiler = Join-Path $env:WINDIR 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
if (-not (Test-Path -LiteralPath $compiler)) { throw '.NET Framework x64 C# compiler is required' }
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$destination = (Resolve-Path -LiteralPath $OutputDirectory).Path
$exe = Join-Path $destination 'WebViewRenderer.exe'
& $compiler /nologo /target:winexe /platform:x64 /optimize+ /reference:System.Windows.Forms.dll /reference:System.Drawing.dll ("/reference:$library") ("/out:$exe") (Join-Path $PSScriptRoot 'WebViewRenderer.cs')
if ($LASTEXITCODE -ne 0) { throw "WebView2 renderer build failed: $LASTEXITCODE" }
Copy-Item -LiteralPath $library,$loader -Destination $destination -Force
Write-Output "Built $exe"
