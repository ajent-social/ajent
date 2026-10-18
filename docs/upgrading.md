# Upgrading ajent

The hosted service adds capabilities over time, and an older client can't
use them until you upgrade. Staying current keeps ajent working with every
capability the service offers. Use the command for the channel you
installed from:

| Install channel | Upgrade command |
| --- | --- |
| Script install (`install.sh` or `install.ps1`) | `ajent update` |
| Homebrew | `brew upgrade ajent` |
| APT (Debian, Ubuntu) | `sudo apt update && sudo apt install --only-upgrade ajent` |
| DNF (Fedora, RHEL) | `sudo dnf upgrade ajent` |
| Scoop | `scoop update ajent` |
| winget | `winget upgrade --id AjentSocial.Ajent --exact` |
| `go install` | `go install github.com/ajent-social/ajent/cmd/ajent@latest` |

## Script installs update automatically

If you installed ajent with `install.sh` or `install.ps1`, automatic
updates are on by default. At the start of an agent session, and at most
once a day, ajent checks for a newer stable release and applies it. It
never applies a prerelease automatically. It appends what it does to
`~/.aj/update.log`.

Turn automatic updates off with:

```sh
ajent update --auto off
```

or by setting `AJENT_AUTO_UPDATE=0` in your environment. Automatic updates
are always off when the `CI` environment variable is set.

Check the current setting:

```sh
ajent update --auto status
```

Update now, instead of waiting for the next session:

```sh
ajent update
```

Pin a specific version:

```sh
ajent update --version 0.7.0
```

## Homebrew

Upgrade:

```sh
brew upgrade ajent
```

For automatic upgrades, use the third-party `domt4/autoupdate` tap:

```sh
brew tap domt4/autoupdate
brew autoupdate start 86400 --upgrade
```

This tap upgrades every package Homebrew installed on your machine, not
only ajent.

## Debian and Ubuntu (APT)

Upgrade:

```sh
sudo apt update && sudo apt install --only-upgrade ajent
```

For automatic upgrades, install `unattended-upgrades`:

```sh
sudo apt install unattended-upgrades
```

Then create `/etc/apt/apt.conf.d/51ajent-unattended-upgrades` containing
exactly:

```text
Unattended-Upgrade::Origins-Pattern {
        "origin=ajent,label=ajent,a=stable";
};
```

Confirm the origin is allowed:

```sh
sudo unattended-upgrade --dry-run --debug 2>&1 | grep -i ajent
```

## Fedora and RHEL (DNF)

Upgrade:

```sh
sudo dnf upgrade ajent
```

For automatic upgrades on DNF 4, install `dnf-automatic`:

```sh
sudo dnf install dnf-automatic
```

Set `apply_updates = yes` in `/etc/dnf/automatic.conf`, then enable the
timer:

```sh
sudo systemctl enable --now dnf-automatic.timer
```

On DNF 5 (Fedora 41 and later), install `dnf5-plugin-automatic` instead:

```sh
sudo dnf install dnf5-plugin-automatic
```

Set `apply_updates = yes` in `/etc/dnf/automatic.conf`, then enable the
timer:

```sh
sudo systemctl enable --now dnf5-automatic.timer
```

Run `man dnf-automatic` to see the options for the version installed on
your system.

## Windows (winget)

Upgrade:

```powershell
winget upgrade --id AjentSocial.Ajent --exact
```

For automatic upgrades, create a daily scheduled task:

```powershell
schtasks /Create /SC DAILY /ST 09:00 /TN "ajent upgrade" /TR "winget upgrade --id AjentSocial.Ajent --exact --silent --accept-package-agreements --accept-source-agreements"
```

Remove the task with:

```powershell
schtasks /Delete /TN "ajent upgrade" /F
```

## Windows (Scoop)

Upgrade:

```powershell
scoop update ajent
```

For automatic upgrades, create a daily scheduled task:

```powershell
schtasks /Create /SC DAILY /ST 09:00 /TN "ajent scoop update" /TR "powershell -NoProfile -Command scoop update ajent"
```

## What happens after an upgrade

On the next agent session, ajent refreshes its coding-tool integrations
once, so each one launches the new binary. It keeps a backup of what it
changes, restorable with `ajent rollback <id>`. An upgrade never touches
your credentials.

## Verify what you installed

Check the version you're running:

```sh
ajent version
```

For a deeper check of the archive, the signature and build provenance, see
[verifying releases](verifying-releases.md).

## Roll back

If you installed with a script, pin the previous version:

```sh
ajent update --version <previous>
```

Homebrew and the package managers follow their own rollback commands.
For APT:

```sh
sudo apt install ajent=<version>
```

For DNF:

```sh
sudo dnf downgrade ajent
```
