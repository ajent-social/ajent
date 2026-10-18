# ADR 003: Distribution channels and release automation

## Status

Accepted (GitHub Pages hosting confirmed by the maintainer on 2026-09-23).

## Date

2026-09-23

## Context

Adoption depends on a one-line install on each operating system, and on users getting upgrades through the tool they already use. Release work has to be fully automated so that a single merged release PR publishes to every channel.

## Decision

- **Versioning and changelog:** release-please drives Conventional Commits to semantic versions, `CHANGELOG.md` and tags.
- **Build and publish:** GoReleaser v2 runs on the release tag in GitHub Actions. It produces archives (`tar.gz` for macOS and Linux, `zip` for Windows), `checksums.txt`, SBOMs, and the channel artifacts below.
- **macOS and Linux one-liner:** `curl -fsSL https://ajent.social/install.sh | sh`. The script lives in this repository (`scripts/install.sh`). It checks SHA-256 against the signed checksums, installs to `~/.local/bin` (or `AJENT_INSTALL_DIR`), then runs `ajent setup`. A mirror is served at `https://pkg.ajent.social/install.sh`.
- **Windows one-liner:** `irm https://ajent.social/install.ps1 | iex`, from `scripts/install.ps1`. It installs to `%LOCALAPPDATA%\Programs\ajent` and adds that folder to the user's PATH.
- **Homebrew:** a GoReleaser `homebrew_casks` entry publishes to `ajent-social/homebrew-tap`: `brew install ajent-social/tap/ajent`. Homebrew core is a later goal, once the project meets its notability rules.
- **Debian, Ubuntu, Fedora and RHEL:** nFPM builds `.deb` and `.rpm` packages. A GPG-signed APT repository and a DNF/YUM repository are rebuilt by CI in the public `ajent-social/packages` repository. GitHub Pages serves them at `https://pkg.ajent.social` (a CNAME record at the DNS provider). The repositories keep the latest five versions.
- **Windows packages:** a Scoop bucket at `ajent-social/scoop-bucket`, and winget manifests submitted to `microsoft/winget-pkgs` by GoReleaser with package ID `AjentSocial.Ajent`.
- **Verification as production:** after each release, a workflow installs through every channel on clean runners. It asserts that `ajent version` matches the tag and that `ajent doctor` reaches the hosted service. A release is done only when this workflow is green.

## Consequences

- One tag feeds seven channels with no manual steps once the one-time secrets are in place: tap token, bucket token, winget fork token, GPG key, and signing keys.
- GitHub Pages hosting is free and publicly auditable. Its limits are soft bandwidth caps and no per-file analytics. Moving to object storage later only changes DNS, because clients use `pkg.ajent.social`.
- The first winget submission waits on Microsoft review. Later version bumps are automated pull requests.
- The hosted `ajent.social/install.*` URLs need a change on the service side (a redirect or proxy to the scripts in this repository). Until then, `pkg.ajent.social` is the canonical URL.
