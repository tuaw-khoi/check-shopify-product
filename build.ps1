$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$goExecutable = $null
$pathGo = Get-Command go -ErrorAction SilentlyContinue
if ($pathGo) {
    $goExecutable = $pathGo.Source
}

if (-not $goExecutable) {
    $portableGo = 'D:\Coding\.tools\go1.27.1\go\bin\go.exe'
    if (Test-Path -LiteralPath $portableGo) {
        $goExecutable = $portableGo
    }
}

if (-not $goExecutable) {
    throw 'Go 1.27+ was not found. Install Go or update the portable path in build.ps1.'
}

$cacheRoot = 'D:\Coding\.cache'
$env:GOTMPDIR = Join-Path $cacheRoot 'go-tmp'
$env:GOCACHE = Join-Path $cacheRoot 'go-build'
$env:GOMODCACHE = Join-Path $cacheRoot 'go-mod'
New-Item -ItemType Directory -Force -Path $env:GOTMPDIR, $env:GOCACHE, $env:GOMODCACHE | Out-Null

Push-Location $projectRoot
try {
    & $goExecutable test -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed; executable was not built.' }

    & $goExecutable build -trimpath -ldflags '-s -w' -o 'shopify-price-checker.exe' .
    if ($LASTEXITCODE -ne 0) { throw 'Executable build failed.' }

    Get-Item -LiteralPath (Join-Path $projectRoot 'shopify-price-checker.exe') | Select-Object FullName, Length, LastWriteTime
}
finally {
    Pop-Location
}
