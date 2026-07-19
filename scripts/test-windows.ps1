param(
    [switch]$CompileOnly,
    [string]$PrebuiltDir = ""
)

$ErrorActionPreference = "Stop"
$env:GORACE = "halt_on_error=1"
$root = Split-Path -Parent $PSScriptRoot
$core = Join-Path $root "modules\half-pi-core"
$gateway = Join-Path $root "modules\gateway-core"
$mind = Join-Path $root "modules\half-pi-mind"

if ($CompileOnly -and $PrebuiltDir) {
    throw "-CompileOnly and -PrebuiltDir are mutually exclusive"
}
if ($PrebuiltDir) {
    $PrebuiltDir = (Resolve-Path $PrebuiltDir).Path
}

function Invoke-MindCLI {
    param(
        [string]$Binary,
        [string]$WorkingDirectory,
        [string[]]$Arguments
    )
    $stdoutPath = Join-Path $env:TEMP "half-pi-cli-$([guid]::NewGuid()).out"
    $stderrPath = Join-Path $env:TEMP "half-pi-cli-$([guid]::NewGuid()).err"
    try {
        $process = Start-Process -FilePath $Binary -ArgumentList $Arguments -WorkingDirectory $WorkingDirectory `
            -Wait -PassThru -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Stdout = [IO.File]::ReadAllText($stdoutPath)
            Stderr = [IO.File]::ReadAllText($stderrPath)
        }
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $stdoutPath, $stderrPath
    }
}

function Assert-ExitCode {
    param($Result, [int]$Expected, [string]$Operation)
    if ($Result.ExitCode -ne $Expected) {
        throw "$Operation exit $($Result.ExitCode), expected $Expected`nstdout:`n$($Result.Stdout)`nstderr:`n$($Result.Stderr)"
    }
}

function Assert-RestrictedAcl {
    param([string]$Path)
    $acl = Get-Acl -LiteralPath $Path
    if (-not $acl.AreAccessRulesProtected) {
        throw "$Path DACL inherits from its parent"
    }
    $currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $allowedSIDs = @("S-1-5-18", $currentSID)
    $actualSIDs = @()
    foreach ($rule in $acl.Access) {
        $sid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        $actualSIDs += $sid
        if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or $allowedSIDs -notcontains $sid) {
            throw "$Path contains unexpected ACL rule for $sid"
        }
    }
    foreach ($expectedSID in $allowedSIDs) {
        if ($actualSIDs -notcontains $expectedSID) {
            throw "$Path is missing ACL rule for $expectedSID"
        }
    }
}

function Invoke-PrebuiltTest {
    param([string]$Name)
    $binary = Join-Path $PrebuiltDir "tests\$Name.test.exe"
    if (-not (Test-Path -LiteralPath $binary)) {
        throw "prebuilt test binary does not exist: $binary"
    }
    & $binary "-test.count=1" "-test.timeout=5m"
    if ($LASTEXITCODE -ne 0) {
        throw "$Name prebuilt race tests failed"
    }
}

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
            if ($LASTEXITCODE -ne 0) { throw "half-pi-core windows/$arch compile failed" }
            Remove-Item -Force $output
        }
    } else {
        if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
            throw "Native Windows mode must run on Windows. Use -CompileOnly elsewhere."
        }
        if ($PrebuiltDir) {
            Invoke-PrebuiltTest "tools"
        } else {
            go test -race -count=1 ./tools
            if ($LASTEXITCODE -ne 0) { throw "half-pi-core native race tests failed" }
        }
    }
} finally {
    Pop-Location
}

Push-Location $gateway
try {
    if ($CompileOnly) {
        $architectures = @("386", "amd64", "arm", "arm64")
        foreach ($arch in $architectures) {
            $env:GOOS = "windows"
            $env:GOARCH = $arch
            foreach ($target in @("protocol", "hub", "wss")) {
                $output = Join-Path $env:TEMP "half-pi-gateway-$target-windows-$arch.test.exe"
                Write-Host "Compiling gateway $target for windows/$arch"
                go test -c -o $output ".\$target"
                if ($LASTEXITCODE -ne 0) { throw "gateway $target windows/$arch compile failed" }
                Remove-Item -Force $output
            }
        }
    } else {
        if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
            throw "Native Windows mode must run on Windows. Use -CompileOnly elsewhere."
        }
        if ($PrebuiltDir) {
            @("protocol", "hub", "wss") | ForEach-Object {
                Invoke-PrebuiltTest $_
            }
        } else {
            go test -race -count=1 ./protocol ./hub ./wss
            if ($LASTEXITCODE -ne 0) { throw "gateway native race tests failed" }
        }
    }
} finally {
    Pop-Location
}

Push-Location $mind
try {
    if ($CompileOnly) {
        # modernc.org/sqlite does not publish a windows/arm (32-bit) libc target.
        $architectures = @("386", "amd64", "arm64")
        foreach ($arch in $architectures) {
            $env:GOOS = "windows"
            $env:GOARCH = $arch
            $targets = @(
                @("mind", ".\cmd\half-pi-mind"),
                @("adminipc", ".\internal\adminipc"),
                @("dispatcher", ".\internal\dispatcher"),
                @("statelock", ".\internal\statelock"),
                @("setup", ".\internal\setup")
            )
            foreach ($target in $targets) {
                $output = Join-Path $env:TEMP "half-pi-$($target[0])-windows-$arch.test.exe"
                Write-Host "Compiling $($target[0]) for windows/$arch"
                go test -c -o $output $target[1]
                if ($LASTEXITCODE -ne 0) { throw "$($target[0]) windows/$arch compile failed" }
                Remove-Item -Force $output
            }
        }
        return
    }

    if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
        throw "Native Windows mode must run on Windows. Use -CompileOnly elsewhere."
    }
    if ($PrebuiltDir) {
        @("half-pi-mind", "adminipc", "dispatcher", "management", "statelock", "setup", "store") | ForEach-Object {
            Invoke-PrebuiltTest $_
        }
    } else {
        go test -race -count=1 ./cmd/half-pi-mind ./internal/adminipc ./internal/dispatcher ./internal/management ./internal/statelock ./internal/setup ./internal/store
        if ($LASTEXITCODE -ne 0) { throw "half-pi-mind native race tests failed" }
    }

    $testRoot = Join-Path $env:TEMP "half-pi-management-$([guid]::NewGuid())"
    $appData = Join-Path $testRoot "appdata"
    $workDir = Join-Path $testRoot "work"
    $halfPiHome = Join-Path $appData "half-pi"
    $fixtureDir = Join-Path $halfPiHome "fixtures"
    $binary = Join-Path $testRoot "half-pi-mind-race.exe"
    $mindStdout = Join-Path $testRoot "mind.out"
    $mindStderr = Join-Path $testRoot "mind.err"
    $oldAppData = $env:APPDATA
    $mindProcess = $null
    try {
        New-Item -ItemType Directory -Force $fixtureDir, $workDir | Out-Null
        $env:APPDATA = $appData
        if ($PrebuiltDir) {
            Copy-Item -Force (Join-Path $PrebuiltDir "half-pi-mind-race.exe") $binary
        } else {
            go build -race -o $binary ./cmd/half-pi-mind
            if ($LASTEXITCODE -ne 0) { throw "half-pi-mind race binary build failed" }
        }

        $fixturePath = Join-Path $fixtureDir "management.json"
        [IO.File]::WriteAllText(
            $fixturePath,
            '{"version":1,"steps":[{"response":{"content":"ok"}}]}',
            [Text.UTF8Encoding]::new($false)
        )
        $tomlFixturePath = $fixturePath.Replace('\', '/')
        $config = @"
[server]
enabled = false
host = "127.0.0.1"
port = 0

[storage]
data_dir = ""
log_dir = ""

[llm]
default_provider = "fixture"
default_model = "fixture-model"

[[llm.providers]]
name = "fixture"
adapter = "scripted"
script_path = "$tomlFixturePath"

[[llm.models]]
id = "fixture-model"
provider = "fixture"
capabilities = []
max_tokens = 4096
temperature = 0
input_price_per_1k = 0
output_price_per_1k = 0
"@
        [IO.File]::WriteAllText(
            (Join-Path $halfPiHome "config.toml"),
            $config,
            [Text.UTF8Encoding]::new($false)
        )

        $offlineFace = Invoke-MindCLI $binary $workDir @("face", "add", "windows-face", "--profile", "observer", "--format", "json")
        Assert-ExitCode $offlineFace 0 "offline Face add"
        $offlineFaceJSON = $offlineFace.Stdout | ConvertFrom-Json
        if (-not $offlineFaceJSON.ok -or -not $offlineFaceJSON.result.token -or -not $offlineFaceJSON.result.application_key) {
            throw "offline Face add returned invalid JSON: $($offlineFace.Stdout)"
        }

        Assert-RestrictedAcl (Join-Path $halfPiHome "run")
        Assert-RestrictedAcl (Join-Path $halfPiHome "run\mind.lock")

        $mindProcess = Start-Process -FilePath $binary -WorkingDirectory $workDir -RedirectStandardOutput $mindStdout -RedirectStandardError $mindStderr -PassThru
        $running = $false
        for ($attempt = 0; $attempt -lt 100; $attempt++) {
            $status = Invoke-MindCLI $binary $workDir @("status", "--format", "json")
            if ($status.ExitCode -eq 0) {
                $statusJSON = $status.Stdout | ConvertFrom-Json
                if ($statusJSON.ok -and $statusJSON.result.state -eq "running" -and $statusJSON.result.pid -eq $mindProcess.Id) {
                    $running = $true
                    break
                }
            }
            Start-Sleep -Milliseconds 100
        }
        if (-not $running) {
            throw "Mind management IPC did not become ready`n$([IO.File]::ReadAllText($mindStderr))"
        }

        $faceList = Invoke-MindCLI $binary $workDir @("face", "list", "--format", "json")
        Assert-ExitCode $faceList 0 "online Face list"
        if ($faceList.Stdout -notmatch 'windows-face' -or $faceList.Stdout -match [regex]::Escape($offlineFaceJSON.result.token)) {
            throw "online Face list was inconsistent or leaked its token: $($faceList.Stdout)"
        }

        $onlineHand = Invoke-MindCLI $binary $workDir @("hand", "add", "windows-hand", "--format", "json")
        Assert-ExitCode $onlineHand 0 "online Hand add"
        $removeHand = Invoke-MindCLI $binary $workDir @("hand", "remove", "--label", "windows-hand", "--yes", "--format", "json")
        Assert-ExitCode $removeHand 0 "online Hand remove"
        $removeFace = Invoke-MindCLI $binary $workDir @("face", "remove", "--label", "windows-face", "--yes", "--format", "json")
        Assert-ExitCode $removeFace 0 "online Face remove"

        $peers = Invoke-MindCLI $binary $workDir @("peers", "--format", "json")
        Assert-ExitCode $peers 4 "Hub-disabled peers"
        $peersJSON = $peers.Stderr | ConvertFrom-Json
        if ($peersJSON.ok -or $peersJSON.error.code -ne "hub_disabled") {
            throw "Hub-disabled peers returned invalid error: $($peers.Stderr)"
        }
    } finally {
        if ($null -ne $mindProcess -and -not $mindProcess.HasExited) {
            Stop-Process -Id $mindProcess.Id -Force
            $mindProcess.WaitForExit()
        }
        $env:APPDATA = $oldAppData
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $testRoot
    }
} finally {
    Pop-Location
}
