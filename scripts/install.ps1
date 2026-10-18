# SPDX-License-Identifier: Apache-2.0
#
# Installs ajent on Windows (docs/adr/003-distribution-channels.md).
# Downloads the release zip for this architecture, verifies its SHA-256
# against checksums.txt, checks the Ed25519 signature through the
# downloaded ajent binary's own "verify-release" command, installs it and
# records the install method in install.json. Runs on Windows PowerShell
# 5.1 and PowerShell 7. On macOS and Linux, use install.sh instead:
# curl -fsSL https://ajent.social/install.sh | sh
#
# Environment variables (same names as install.sh):
#   AJENT_VERSION              Version to install, e.g. "0.7.0" or "v0.7.0".
#                               Default: the latest stable release.
#   AJENT_INSTALL_DIR          Where to install ajent.exe.
#                               Default: $env:LOCALAPPDATA\Programs\ajent
#   AJENT_CONFIG_DIR           Where to write install.json.
#                               Default: $HOME\.aj
#   AJENT_NO_SETUP             When "1", skip running "ajent setup".
#   AJENT_REQUIRE_SIGNATURE    When "1" and openssl.exe 3.x is on PATH,
#                               also verify with OpenSSL against
#                               AJENT_RELEASE_PUBKEY_FILE, in addition to
#                               the always-run verify-release check below.
#   AJENT_DOWNLOAD_BASE        Base URL for release assets, for tests and
#                               mirrors. Default:
#                               https://github.com/ajent-social/ajent-client/releases/download
#                               AJENT_VERSION is required when this is set.
#   AJENT_RELEASE_PUBKEY_FILE  A PEM file used only for the additional
#                               OpenSSL check above. For tests only.

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function ajentInstallSay {
    param([string]$Message)
    [Console]::Error.WriteLine("ajent-install: $Message")
}

function ajentInstallStripLeadingV {
    param([string]$Value)
    if ($Value.Length -gt 0 -and $Value.Substring(0, 1) -eq 'v') {
        return $Value.Substring(1)
    }
    return $Value
}

function ajentInstallDetectArch {
    $raw = $env:PROCESSOR_ARCHITEW6432
    if (-not $raw) {
        $raw = $env:PROCESSOR_ARCHITECTURE
    }
    switch ($raw) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "unsupported architecture $raw" }
    }
}

function ajentInstallResolveVersion {
    $v = $env:AJENT_VERSION
    if ($v) {
        $v = ajentInstallStripLeadingV $v
    }

    if (-not $v) {
        if ($env:AJENT_DOWNLOAD_BASE) {
            throw "AJENT_VERSION is required when AJENT_DOWNLOAD_BASE is set"
        }
        $release = Invoke-RestMethod -Uri 'https://api.github.com/repos/ajent-social/ajent-client/releases/latest' -Headers @{ 'User-Agent' = 'ajent-install' }
        $v = ajentInstallStripLeadingV $release.tag_name
    }

    if ($v -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$') {
        throw "could not determine a valid version (got '$v')"
    }

    return $v
}

function ajentInstallVerifyChecksum {
    param([string]$FilePath, [string]$Name, [string]$ChecksumsPath)

    $want = $null
    foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
        $fields = $line -split '\s+', 2
        if ($fields.Length -lt 2) {
            continue
        }
        $entryName = $fields[1].TrimStart('*', ' ')
        if ($entryName -eq $Name) {
            $want = $fields[0].ToLower()
            break
        }
    }
    if (-not $want) {
        throw "checksum mismatch for $Name"
    }

    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $FilePath).Hash.ToLower()
    if ($want -ne $got) {
        throw "checksum mismatch for $Name"
    }
}

function ajentInstallVerifySignature {
    param([string]$ExePath, [string]$ChecksumsPath, [string]$SigPath, [string]$ZipPath)

    & $ExePath verify-release --checksums $ChecksumsPath --sig $SigPath --file $ZipPath | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "release signature does not verify"
    }
    ajentInstallSay "signature checked with the downloaded ajent binary; for independent verification see docs/verifying-releases.md"

    if ($env:AJENT_REQUIRE_SIGNATURE -eq '1' -and $env:AJENT_RELEASE_PUBKEY_FILE) {
        $openssl = Get-Command -Name 'openssl.exe' -ErrorAction SilentlyContinue
        if ($openssl) {
            $help = & $openssl.Path pkeyutl -help 2>&1 | Out-String
            if ($help -match 'rawin') {
                $sigBin = Join-Path (Split-Path -Path $SigPath -Parent) 'sig.bin'
                & $openssl.Path base64 -d -A -in $SigPath -out $sigBin
                & $openssl.Path pkeyutl -verify -pubin -inkey $env:AJENT_RELEASE_PUBKEY_FILE -rawin -in $ChecksumsPath -sigfile $sigBin | Out-Null
                if ($LASTEXITCODE -ne 0) {
                    throw "release signature does not verify"
                }
            }
        }
    }
}

function ajentInstallPathContains {
    param([string]$PathList, [string]$Dir)

    if (-not $PathList) {
        return $false
    }
    $normalizedDir = $Dir.TrimEnd('\')
    foreach ($entry in ($PathList -split ';')) {
        if ($entry -and ($entry.TrimEnd('\') -ieq $normalizedDir)) {
            return $true
        }
    }
    return $false
}

function Install-Ajent {
    $arch = ajentInstallDetectArch
    $v = ajentInstallResolveVersion

    $base = $env:AJENT_DOWNLOAD_BASE
    if (-not $base) {
        $base = 'https://github.com/ajent-social/ajent-client/releases/download'
    }
    $name = "ajent_${v}_windows_${arch}.zip"

    $tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("ajent-" + [guid]::NewGuid()))
    try {
        $zip = Join-Path $tmp.FullName $name
        $checksums = Join-Path $tmp.FullName 'checksums.txt'
        $sig = Join-Path $tmp.FullName 'checksums.txt.sig'

        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$v/$name" -OutFile $zip
        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$v/checksums.txt" -OutFile $checksums
        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$v/checksums.txt.sig" -OutFile $sig

        ajentInstallVerifyChecksum -FilePath $zip -Name $name -ChecksumsPath $checksums

        $extractDir = Join-Path $tmp.FullName 'extracted'
        Expand-Archive -Path $zip -DestinationPath $extractDir -Force
        $newExe = Join-Path $extractDir 'ajent.exe'
        if (-not (Test-Path -LiteralPath $newExe)) {
            throw "ajent.exe was not found in the downloaded archive"
        }

        ajentInstallVerifySignature -ExePath $newExe -ChecksumsPath $checksums -SigPath $sig -ZipPath $zip

        $dir = $env:AJENT_INSTALL_DIR
        if (-not $dir) {
            $dir = Join-Path $env:LOCALAPPDATA 'Programs\ajent'
        }
        New-Item -ItemType Directory -Force -Path $dir | Out-Null

        $exe = Join-Path $dir 'ajent.exe'
        $oldExe = Join-Path $dir 'ajent.exe.old'
        if (Test-Path -LiteralPath $oldExe) {
            Remove-Item -LiteralPath $oldExe -Force
        }
        if (Test-Path -LiteralPath $exe) {
            Rename-Item -LiteralPath $exe -NewName 'ajent.exe.old'
        }
        Move-Item -LiteralPath $newExe -Destination $exe -Force

        $currentPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if (-not (ajentInstallPathContains -PathList $currentPath -Dir $dir)) {
            $newPath = $dir
            if ($currentPath) {
                $newPath = "$currentPath;$dir"
            }
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            $env:Path = "$dir;$env:Path"
        }

        $cfg = $env:AJENT_CONFIG_DIR
        if (-not $cfg) {
            $cfg = Join-Path $HOME '.aj'
        }
        New-Item -ItemType Directory -Force -Path $cfg | Out-Null

        $record = [ordered]@{
            method       = 'script'
            version      = $v
            path         = $exe
            installed_at = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'")
        }
        ($record | ConvertTo-Json -Compress) | Set-Content -LiteralPath (Join-Path $cfg 'install.json') -Encoding ascii -NoNewline

        ajentInstallSay "ajent $v installed to $exe"

        if ($env:AJENT_NO_SETUP -ne '1' -and [Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
            & $exe setup
        } else {
            ajentInstallSay 'Next: run "ajent setup" to connect your coding agents.'
        }
    }
    finally {
        Remove-Item -LiteralPath $tmp.FullName -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Install-Ajent
