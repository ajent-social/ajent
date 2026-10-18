## Install

macOS and Linux:

    curl -fsSL https://ajent.social/install.sh | sh

Windows (PowerShell):

    irm https://ajent.social/install.ps1 | iex

Homebrew: `brew install ajent-social/tap/ajent`. Other channels: see [the install guide](https://github.com/ajent-social/ajent-client/blob/main/docs/install.md).

Already installed? Script installs update automatically. Package managers: `brew upgrade ajent`, `sudo apt upgrade`, `scoop update ajent`, `winget upgrade AjentSocial.Ajent`.

## Verify

Download `checksums.txt` and `checksums.txt.sig`, then follow [the verification guide](https://github.com/ajent-social/ajent-client/blob/main/docs/verifying-releases.md), or run:

    gh attestation verify ajent_{{ .Version }}_linux_amd64.tar.gz --repo ajent-social/ajent-client
