# Self-updates

`ajent update` keeps your installation current. How it behaves depends on
how ajent got onto this machine. The first Go release supplies verified
archives and repository install scripts; package channels remain unqualified.
Manual archive extraction does not create a script install receipt.

## Script installs update themselves

If you installed with `install.sh` or `install.ps1` (see
[install](install.md)), ajent can update its own binary:

```sh
ajent update                    # check, then update if a newer stable release exists
ajent update --check            # report only; change nothing
ajent update --version 0.7.0    # pin a specific release
ajent update --pre              # allow prereleases
```

An update downloads the release archive for your platform, verifies its
checksum and Ed25519 signature exactly as described in
[verifying releases](verifying-releases.md), and only then replaces the
running binary. On Windows, where a running executable cannot be
overwritten directly, it renames the current binary aside first and rolls
back automatically if the replacement fails. A tampered checksum or
signature leaves your existing binary untouched.

## Automatic updates

Script installs check for updates automatically, at most once every 24
hours, in the background. Automatic updates:

- Only apply stable releases; a prerelease is never applied automatically.
- Always verify the checksum and signature first, the same as a manual
  `ajent update`.
- Are always off in CI, whenever the `CI` environment variable is set.
- Never run for a package-managed install (Homebrew, apt, dnf, Scoop or
  winget); those always defer to the package manager instead.

Turn automatic updates off:

```sh
ajent update --auto off
```

or set `AJENT_AUTO_UPDATE=0` in your environment. Check the current setting
with `ajent update --auto status`.

## Where the log is

Background update activity is appended to `~/.aj/update.log`
(`%USERPROFILE%\.aj\update.log` on Windows).

## Package-managed installs

When ajent detects that a package manager installed it, `ajent update`
never modifies the binary. It prints the exact upgrade command instead:

| Manager | Upgrade command |
| --- | --- |
| Homebrew | `brew upgrade ajent` |
| apt (Debian, Ubuntu) | `sudo apt update && sudo apt install --only-upgrade ajent` |
| dnf (Fedora, RHEL) | `sudo dnf upgrade ajent` |
| Scoop | `scoop update ajent` |
| winget | `winget upgrade --id AjentSocial.Ajent --exact` |
| `go install` | `go install github.com/ajent-social/ajent-client/cmd/ajent@latest` |

You can automate most of these the way you would any other package on your
system: Homebrew's own `brew autoupdate`, an `unattended-upgrades` origin
rule for `apt`, or a scheduled `winget upgrade`. See
[upgrading](upgrading.md) for the details of what changes between
versions.

## After an update

The first run of a new version refreshes ajent's bundled plugin assets
(the skill, the session-start hook, and tool manifests) and confirms every
coding-tool integration still points at your stable binary path, not a
versioned one. This runs automatically; you do not need to rerun
`ajent setup`.

The design behind this strategy is in
[ADR 005](adr/005-upgrade-strategy.md).
