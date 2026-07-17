param(
    [switch]$CompileOnly
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$core = Join-Path $root "modules\half-pi-core"

Push-Location $core
try {
    if ($CompileOnly) {
        $architectures = @("386", "amd64", "arm", "arm64")
        foreach ($arch in $architectures) {
            $output = Join-Path $env:TEMP "half-pi-core-windows-$arch.test.exe"
            Write-Host "Compiling half-pi-core tools for windows/$arch"
            $env:GOOS = "windows"
            $env:GOARCH = $arch
            go test -c -o $output ./tools
            Remove-Item -Force $output
        }
        return
    }

    if (-not $IsWindows) {
        throw "Native Windows mode must run on Windows. Use -CompileOnly elsewhere."
    }
    go test -race -count=1 ./tools
} finally {
    Pop-Location
}
