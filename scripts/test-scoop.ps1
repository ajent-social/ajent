# SPDX-License-Identifier: Apache-2.0
#
# Installs a locally rendered Scoop manifest for ajent against a local HTTP
# mirror of the GoReleaser snapshot output (dist/) and asserts that
# `ajent version --json` reports the version the manifest declares. Used by
# .github/workflows/windows-manifests.yml (T5.8) to validate the rendered
# Scoop bucket manifest without publishing to ajent-social/scoop-bucket.
#
# -CorruptHash is a genuine-red check: it flips the amd64 architecture hash
# in the temp manifest copy before installing, so `scoop install` must fail
# with a hash check error instead of succeeding silently. Requires
# PowerShell 7+.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Manifest,

    [Parameter(Mandatory = $true)]
    [string]$DistDir,

    [switch]$CorruptHash
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not (Test-Path -LiteralPath $Manifest)) {
    throw "Manifest not found: $Manifest"
}
if (-not (Test-Path -LiteralPath $DistDir)) {
    throw "DistDir not found: $DistDir"
}

$manifestObject = Get-Content -LiteralPath $Manifest -Raw | ConvertFrom-Json
$expectedVersion = $manifestObject.version
if ([string]::IsNullOrWhiteSpace($expectedVersion)) {
    throw "Manifest at $Manifest has no version field"
}

Write-Output "Expected version: $expectedVersion"

$distDirFull = (Resolve-Path -LiteralPath $DistDir).Path

# 1. Serve DistDir over HTTP so the manifest's GitHub release URLs resolve
#    against the local snapshot artifacts instead of the network.
$serverJob = Start-Job -ScriptBlock {
    python -m http.server 8765 --directory $using:distDirFull
}

$tempManifest = $null
try {
    # Give the server a moment to bind before the manifest is fetched.
    Start-Sleep -Seconds 2

    # 2. Rewrite the GitHub release download prefix to the local mirror.
    $content = Get-Content -LiteralPath $Manifest -Raw
    $content = $content -replace 'https://github\.com/ajent-social/ajent/releases/download/[^/]+/', 'http://127.0.0.1:8765/'

    if ($CorruptHash) {
        Write-Output 'CorruptHash: flipping the amd64 hash for the genuine-red check'
        $corruptTarget = $content | ConvertFrom-Json
        $originalHash = $corruptTarget.architecture.'64bit'.hash
        $flippedHash = $originalHash.Substring(1) + '0'
        $content = $content -replace [regex]::Escape($originalHash), $flippedHash
        Write-Output "Original amd64 hash:  $originalHash"
        Write-Output "Corrupted amd64 hash: $flippedHash"
    }

    $tempManifest = Join-Path ([System.IO.Path]::GetTempPath()) "ajent-scoop-test-$([Guid]::NewGuid()).json"
    Set-Content -LiteralPath $tempManifest -Value $content -NoNewline

    # 3. Install Scoop non-interactively when it is not already present.
    if (-not (Get-Command scoop -ErrorAction SilentlyContinue)) {
        Invoke-RestMethod -Uri 'https://get.scoop.sh' -OutFile 'install.ps1'
        & .\install.ps1 -RunAsAdmin
        Remove-Item -LiteralPath 'install.ps1' -Force -ErrorAction SilentlyContinue
    }

    # 4. Install from the temp manifest and assert the reported version.
    $installFailed = $false
    try {
        scoop install $tempManifest
        if ($LASTEXITCODE -ne 0) {
            $installFailed = $true
        }
    } catch {
        $installFailed = $true
        Write-Output "scoop install threw: $($_.Exception.Message)"
    }

    if ($CorruptHash) {
        if (-not $installFailed) {
            throw 'Expected scoop install to fail with a hash check error, but it succeeded'
        }
        Write-Output 'Genuine-red confirmed: scoop install failed on the corrupted hash, as expected.'
    } else {
        if ($installFailed) {
            throw 'scoop install failed'
        }

        $versionObject = ajent version --json | ConvertFrom-Json
        Write-Output "Installed version: $($versionObject.version)"
        if ($versionObject.version -ne $expectedVersion) {
            throw "Version mismatch: expected $expectedVersion, got $($versionObject.version)"
        }
        Write-Output "OK: ajent $($versionObject.version) matches the rendered manifest."
    }
} finally {
    # 5. Clean up: uninstall the package, drop the temp manifest and stop
    #    the local HTTP server.
    if (Get-Command scoop -ErrorAction SilentlyContinue) {
        scoop uninstall ajent 2>&1 | Out-Null
    }
    if ($tempManifest -and (Test-Path -LiteralPath $tempManifest)) {
        Remove-Item -LiteralPath $tempManifest -Force -ErrorAction SilentlyContinue
    }
    Stop-Job -Job $serverJob -ErrorAction SilentlyContinue | Out-Null
    Remove-Job -Job $serverJob -Force -ErrorAction SilentlyContinue
}
