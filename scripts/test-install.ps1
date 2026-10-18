# SPDX-License-Identifier: Apache-2.0
#
# Exercises scripts/install.ps1 end to end against a loopback HTTP server
# serving a locally built ajent release for Windows
# (docs/tasks/T5.9.md step 5). Never downloads from a real host: every
# case below points install.ps1 at 127.0.0.1 only. Runs the same cases as
# scripts/test-install.sh (ok, bad-checksum) plus bad-signature, which
# goes through the "ajent verify-release" path built into the fixture
# binary itself.

$ErrorActionPreference = 'Stop'

$script:ajentInstallTestStatus = 0
$script:ajentInstallTestPsExe = if ($PSVersionTable.PSEdition -eq 'Core') { 'pwsh' } else { 'powershell' }

function ajentInstallTestPass {
    param([string]$TestCase)
    Write-Output "PASS $TestCase"
}

function ajentInstallTestFail {
    param([string]$TestCase, [string]$Reason)
    Write-Output "FAIL $TestCase`: $Reason"
    $script:ajentInstallTestStatus = 1
}

function ajentInstallTestModuleRoot {
    $dir = (Get-Location).Path
    while ($true) {
        if (Test-Path -LiteralPath (Join-Path $dir 'go.mod')) {
            return $dir
        }
        $parent = Split-Path -Path $dir -Parent
        if (-not $parent -or $parent -eq $dir) {
            throw "go.mod not found above $((Get-Location).Path)"
        }
        $dir = $parent
    }
}

function ajentInstallTestStartServer {
    param([string]$Root, [int]$Port)

    $serverRoot = $Root
    $job = Start-Job -ScriptBlock {
        python -m http.server $using:Port --bind 127.0.0.1 --directory $using:serverRoot
    }

    $ready = $false
    for ($i = 0; $i -lt 100; $i++) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$Port/" -TimeoutSec 1 | Out-Null
            $ready = $true
            break
        } catch {
            Start-Sleep -Milliseconds 100
        }
    }
    if (-not $ready) {
        throw "loopback test server did not start on port $Port"
    }
    return $job
}

function ajentInstallTestStopServer {
    param($Job)
    Stop-Job -Job $Job -ErrorAction SilentlyContinue | Out-Null
    Remove-Job -Job $Job -Force -ErrorAction SilentlyContinue
}

# ajentInstallTestRunInstall runs scripts/install.ps1 as a fresh child
# process (the same PowerShell flavor this test itself is running under),
# with the given environment, and returns @{ ExitCode; Output }.
function ajentInstallTestRunInstall {
    param([string]$InstallScript, [hashtable]$EnvVars)

    $envNames = @('AJENT_DOWNLOAD_BASE', 'AJENT_VERSION', 'AJENT_INSTALL_DIR', 'AJENT_CONFIG_DIR', 'AJENT_NO_SETUP', 'AJENT_REQUIRE_SIGNATURE', 'AJENT_RELEASE_PUBKEY_FILE')
    foreach ($n in $envNames) {
        Remove-Item -Path "Env:$n" -ErrorAction SilentlyContinue
    }
    foreach ($key in $EnvVars.Keys) {
        Set-Item -Path "Env:$key" -Value $EnvVars[$key]
    }

    $priorErrorPreference = $ErrorActionPreference
    try {
        # Windows PowerShell 5.1 wraps native stderr as ErrorRecord objects.
        # Capture diagnostics; the child's exit code still determines success.
        $ErrorActionPreference = 'Continue'
        $output = & $script:ajentInstallTestPsExe -NoProfile -File $InstallScript 2>&1 | Out-String
        $exit = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $priorErrorPreference
        foreach ($n in $envNames) {
            Remove-Item -Path "Env:$n" -ErrorAction SilentlyContinue
        }
    }

    return @{ ExitCode = $exit; Output = $output }
}

function ajentInstallTestRunOk {
    param([string]$InstallScript, [string]$Base, [string]$Version, [string]$Tmp)

    $binDir = Join-Path $Tmp 'bin'
    $cfgDir = Join-Path $Tmp 'cfg'

    $result = ajentInstallTestRunInstall -InstallScript $InstallScript -EnvVars @{
        AJENT_DOWNLOAD_BASE = $Base
        AJENT_VERSION       = $Version
        AJENT_INSTALL_DIR   = $binDir
        AJENT_CONFIG_DIR    = $cfgDir
        AJENT_NO_SETUP      = '1'
    }

    if ($result.ExitCode -ne 0) {
        ajentInstallTestFail 'ok' "install.ps1 exited $($result.ExitCode): $($result.Output)"
        return
    }

    $exePath = Join-Path $binDir 'ajent.exe'
    if (-not (Test-Path -LiteralPath $exePath)) {
        ajentInstallTestFail 'ok' "ajent.exe was not installed"
        return
    }

    $versionOutput = & $exePath version --json
    $versionJson = $versionOutput | ConvertFrom-Json
    if ($versionJson.version -ne $Version) {
        ajentInstallTestFail 'ok' "unexpected version: got '$($versionJson.version)', want '$Version'"
        return
    }

    $installJsonPath = Join-Path $cfgDir 'install.json'
    if (-not (Test-Path -LiteralPath $installJsonPath)) {
        ajentInstallTestFail 'ok' "install.json was not written"
        return
    }
    $record = Get-Content -Raw -LiteralPath $installJsonPath | ConvertFrom-Json
    if ($record.method -ne 'script') {
        ajentInstallTestFail 'ok' "install.json method is '$($record.method)', want 'script'"
        return
    }

    $childPath = & $script:ajentInstallTestPsExe -NoProfile -Command "[Environment]::GetEnvironmentVariable('Path','User')"
    if ($childPath -notlike "*$binDir*") {
        ajentInstallTestFail 'ok' "install dir was not added to the persisted User PATH"
        return
    }

    ajentInstallTestPass 'ok'
}

function ajentInstallTestRunBadChecksum {
    param([string]$InstallScript, [string]$Base, [string]$Version, [string]$Tmp)

    $binDir = Join-Path $Tmp 'bin2'
    $cfgDir = Join-Path $Tmp 'cfg2'

    $result = ajentInstallTestRunInstall -InstallScript $InstallScript -EnvVars @{
        AJENT_DOWNLOAD_BASE = $Base
        AJENT_VERSION       = $Version
        AJENT_INSTALL_DIR   = $binDir
        AJENT_CONFIG_DIR    = $cfgDir
        AJENT_NO_SETUP      = '1'
    }

    if ($result.ExitCode -eq 0) {
        ajentInstallTestFail 'bad-checksum' "install.ps1 exited 0"
        return
    }
    if ($result.Output -notmatch 'checksum mismatch') {
        ajentInstallTestFail 'bad-checksum' "output does not contain 'checksum mismatch': $($result.Output)"
        return
    }
    if (Test-Path -LiteralPath (Join-Path $binDir 'ajent.exe')) {
        ajentInstallTestFail 'bad-checksum' "ajent.exe was installed despite a bad checksum"
        return
    }

    ajentInstallTestPass 'bad-checksum'
}

function ajentInstallTestRunBadSignature {
    param([string]$InstallScript, [string]$Base, [string]$Version, [string]$Tmp)

    $binDir = Join-Path $Tmp 'bin3'
    $cfgDir = Join-Path $Tmp 'cfg3'

    $result = ajentInstallTestRunInstall -InstallScript $InstallScript -EnvVars @{
        AJENT_DOWNLOAD_BASE = $Base
        AJENT_VERSION       = $Version
        AJENT_INSTALL_DIR   = $binDir
        AJENT_CONFIG_DIR    = $cfgDir
        AJENT_NO_SETUP      = '1'
    }

    if ($result.ExitCode -eq 0) {
        ajentInstallTestFail 'bad-signature' "install.ps1 exited 0"
        return
    }
    if ($result.Output -notmatch 'release signature does not verify') {
        ajentInstallTestFail 'bad-signature' "output does not contain 'release signature does not verify': $($result.Output)"
        return
    }

    ajentInstallTestPass 'bad-signature'
}

function ajentInstallTestSignChecksums {
    param([string]$Root, [string]$SeedFile, [string]$InPath, [string]$OutPath)

    $seed = (Get-Content -Raw -LiteralPath $SeedFile).Trim()
    Push-Location $Root
    try {
        $env:RELEASE_ED25519_KEY = $seed
        try {
            & go run ./internal/tools/signchecksums -in $InPath -out $OutPath
            if ($LASTEXITCODE -ne 0) {
                throw "signchecksums -in $InPath failed with exit $LASTEXITCODE"
            }
        } finally {
            Remove-Item -Path Env:\RELEASE_ED25519_KEY -ErrorAction SilentlyContinue
        }
    } finally {
        Pop-Location
    }
}

function ajentInstallTestRun {
    $root = ajentInstallTestModuleRoot
    $installScript = Join-Path $root 'scripts\install.ps1'

    $tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("ajent-test-install-" + [guid]::NewGuid()))
    $originalUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $server = $null

    try {
        Push-Location $root
        try {
            # Throwaway signing key; the fixture binary below trusts only this one.
            $k = Join-Path $tmp.FullName 'k'
            & go run ./internal/tools/signchecksums -keygen $k
            if ($LASTEXITCODE -ne 0) {
                throw "signchecksums -keygen failed with exit $LASTEXITCODE"
            }
            $kPub = (Get-Content -Raw -LiteralPath "$k.pub").Trim()

            $version = '0.0.0-TEST'
            $ldflags = "-X github.com/ajent-social/ajent/internal/version.Version=$version -X github.com/ajent-social/ajent/internal/update.extraKeyB64=$kPub"
            $exePath = Join-Path $tmp.FullName 'ajent.exe'
            & go build -ldflags $ldflags -o $exePath ./cmd/ajent
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed with exit $LASTEXITCODE"
            }
        } finally {
            Pop-Location
        }

        $zipName = "ajent_${version}_windows_amd64.zip"
        $zipPath = Join-Path $tmp.FullName $zipName
        Compress-Archive -Path $exePath -DestinationPath $zipPath -Force

        $checksumsPath = Join-Path $tmp.FullName 'checksums.txt'
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash.ToLower()
        Set-Content -LiteralPath $checksumsPath -Value "$hash  $zipName" -Encoding ascii -NoNewline

        $sigPath = Join-Path $tmp.FullName 'checksums.txt.sig'
        ajentInstallTestSignChecksums -Root $root -SeedFile "$k.seed" -InPath $checksumsPath -OutPath $sigPath

        $www = Join-Path $tmp.FullName 'www'

        $okDir = Join-Path $www "ok\v$version"
        New-Item -ItemType Directory -Force -Path $okDir | Out-Null
        Copy-Item -LiteralPath $zipPath -Destination (Join-Path $okDir $zipName)
        Copy-Item -LiteralPath $checksumsPath -Destination (Join-Path $okDir 'checksums.txt')
        Copy-Item -LiteralPath $sigPath -Destination (Join-Path $okDir 'checksums.txt.sig')

        $badChecksumDir = Join-Path $www "bad-checksum\v$version"
        New-Item -ItemType Directory -Force -Path $badChecksumDir | Out-Null
        Copy-Item -LiteralPath $checksumsPath -Destination (Join-Path $badChecksumDir 'checksums.txt')
        Copy-Item -LiteralPath $sigPath -Destination (Join-Path $badChecksumDir 'checksums.txt.sig')
        $badZip = Join-Path $badChecksumDir $zipName
        Copy-Item -LiteralPath $zipPath -Destination $badZip
        Add-Content -LiteralPath $badZip -Value 'x' -Encoding ascii -NoNewline

        $badSigDir = Join-Path $www "bad-signature\v$version"
        New-Item -ItemType Directory -Force -Path $badSigDir | Out-Null
        Copy-Item -LiteralPath $zipPath -Destination (Join-Path $badSigDir $zipName)
        $badSigChecksums = Join-Path $badSigDir 'checksums.txt'
        Copy-Item -LiteralPath $checksumsPath -Destination $badSigChecksums

        $k2 = Join-Path $tmp.FullName 'k2'
        Push-Location $root
        try {
            & go run ./internal/tools/signchecksums -keygen $k2
            if ($LASTEXITCODE -ne 0) {
                throw "signchecksums -keygen (k2) failed with exit $LASTEXITCODE"
            }
        } finally {
            Pop-Location
        }
        $badSig = Join-Path $badSigDir 'checksums.txt.sig'
        ajentInstallTestSignChecksums -Root $root -SeedFile "$k2.seed" -InPath $badSigChecksums -OutPath $badSig

        $port = 8767
        $server = ajentInstallTestStartServer -Root $www -Port $port

        $base = "http://127.0.0.1:$port"
        ajentInstallTestRunOk -InstallScript $installScript -Base "$base/ok" -Version $version -Tmp $tmp.FullName
        ajentInstallTestRunBadChecksum -InstallScript $installScript -Base "$base/bad-checksum" -Version $version -Tmp $tmp.FullName
        ajentInstallTestRunBadSignature -InstallScript $installScript -Base "$base/bad-signature" -Version $version -Tmp $tmp.FullName
    }
    finally {
        if ($server) {
            ajentInstallTestStopServer -Job $server
        }
        if ($null -eq $originalUserPath) {
            [Environment]::SetEnvironmentVariable('Path', $null, 'User')
        } else {
            [Environment]::SetEnvironmentVariable('Path', $originalUserPath, 'User')
        }
        Remove-Item -LiteralPath $tmp.FullName -Recurse -Force -ErrorAction SilentlyContinue
    }
}

ajentInstallTestRun
exit $script:ajentInstallTestStatus
