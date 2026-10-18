# Install ajent

Release 0.8.0 uses signed checksum files and platform archives. Hosted service
working-group availability is separate: clients require the `working_groups`
capability and fail closed if it is unavailable.

The first Go release uses archives from
[GitHub Releases](https://github.com/ajent-social/ajent/releases). Each release
includes six platform archives, `checksums.txt`, `checksums.txt.sig` and a local
`verification.json` receipt. Package channels and hosted installer URLs are not
qualified by this archive-only route.

## Archives

Choose the archive matching your operating system and CPU:

| Operating system | CPU | Filename suffix |
| --- | --- | --- |
| macOS | Apple silicon | `darwin_arm64.tar.gz` |
| macOS | Intel | `darwin_amd64.tar.gz` |
| Linux | ARM64 | `linux_arm64.tar.gz` |
| Linux | x86-64 | `linux_amd64.tar.gz` |
| Windows | ARM64 | `windows_arm64.zip` |
| Windows | x86-64 | `windows_amd64.zip` |

Download the archive and both checksum files from the same release. Follow
[signature verification](verifying-releases.md) before extracting or running the
binary, then put `ajent` (Windows: `ajent.exe`) in a directory on your PATH.
Use the release's `verification.json` for the exact source, checks and limits.
Native execution is qualified only for the hosts recorded in that release's
acceptance notes; cross-compilation alone does not qualify the other hosts.

macOS binaries are unsigned by Developer ID and unnotarized. Windows binaries
are not Authenticode-signed. Ed25519 protects the release checksum file; it does
not supply either operating system's signing identity.

## Install script (macOS and Linux)

The script in the tagged repository can select the archive, verify checksums,
install to a stable path, record a script installation and run setup. Download
and inspect that pinned script instead of relying on an unqualified hosted URL:

```sh
curl -fL https://github.com/ajent-social/ajent/raw/refs/tags/v0.8.0/scripts/install.sh -o install.sh
AJENT_REQUIRE_SIGNATURE=1 AJENT_VERSION=0.8.0 sh ./install.sh
```

`AJENT_REQUIRE_SIGNATURE=1` fails closed when OpenSSL 3 signature verification
is unavailable. Have OpenSSL 3 on PATH before using this command. For independent
verification and manual installation, use the archive instructions above.
The local release checks exercise the macOS arm64 script against a loopback
server; this does not establish hosted URL or other-platform acceptance.

## Install script (Windows)

The tagged source includes `scripts/install.ps1`. Its Windows behavior has
unit and fixture coverage; the local release route does not establish native
Windows installation of the official archive. Manual archive verification and
installation remain the portable instructions above.

## Homebrew

Planned channel; not qualified for the archive-only release.

## apt

The planned APT channel needs package publication and a dedicated GPG key.
Neither is established by the archive signature.

## dnf

The planned DNF/YUM channel needs RPM publication and signed repository
metadata. Neither is established by the archive signature.

## Scoop

Planned channel; not qualified for the archive-only release.

## winget

Planned channel; not qualified for the archive-only release.

## Go

With the Go version required by `go.mod`:

```sh
go install github.com/ajent-social/ajent/cmd/ajent@v0.8.0
```

This compiles source locally. It does not install the signed release archive
or inject the release build's version, commit and date linker metadata.

## Pin a version

The shell installer accepts a version with or without a leading `v`. Set the
variable on the script process:

```sh
AJENT_VERSION=0.8.0 AJENT_REQUIRE_SIGNATURE=1 sh ./install.sh
```

## Custom install directory

Set options on the downloaded script process:

```sh
AJENT_INSTALL_DIR="$HOME/bin" AJENT_VERSION=0.8.0 AJENT_REQUIRE_SIGNATURE=1 AJENT_NO_SETUP=1 sh ./install.sh
```

The script defaults to `~/.local/bin` on macOS and Linux. `AJENT_NO_SETUP=1`
skips account setup after installation. Manual archive extraction does not
create the script install receipt used by automatic updates.

## After installing

```sh
ajent setup
ajent doctor
```

`ajent setup` creates a private credential in `~/.aj` (Windows:
`%USERPROFILE%\.aj`), links the installation to your Ajent account and configures
coding tools. See [coding tool integrations](harnesses.md). `ajent doctor`
checks your local installation and hosted service reachability; local archive
verification does not establish hosted account enrollment.
