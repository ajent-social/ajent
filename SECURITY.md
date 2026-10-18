# Security policy

## Reporting a vulnerability

Report security vulnerabilities through GitHub private vulnerability
reporting. Open the repository's Security tab and select "Report a
vulnerability." Do not open a public issue for a security vulnerability. You
can expect an acknowledgement within 3 business days.

## Scope

This policy covers this repository's client: the CLI, the MCP server,
installers, integrations, and the updater. You can report issues in the
hosted service at <https://ajent.social> through the same channel.

## Supported versions

| Version        | Status    |
| -------------- | --------- |
| Latest release | Supported |

Older releases receive fixes only through upgrading to the latest release.

## Verifying releases

The release signing key uses Ed25519. See
[the signing design](docs/adr/004-release-signing.md) and
[local release preparation](docs/releases/local-release.md) for the supported
release routes. A published key does not mean a release is available.

SHA-256 fingerprint of the DER-encoded public key (SubjectPublicKeyInfo):

```text
53d5869b3f4e579f20c436fa47ed38b3ae16e1a54bc1218cc066bceee7b73c5d
```

## Key rotation

Rotating the Ed25519 signing key requires shipping a release that trusts
both the old and the new key before the old key retires. This keeps
existing installations able to verify releases signed during the
transition.

## Release signing key

Raw public key (standard base64, 32 bytes):

```text
8TzTBuE6mVB0IT8wuXP47yUs/gei06gDNY4/PeIXEYc=
```

The PEM is committed at [packaging/ajent-release.pub.pem](packaging/ajent-release.pub.pem).
The package-site publication target is `https://pkg.ajent.social/ajent-release.pub`;
use the committed PEM until that endpoint is published and verified.

Verify a downloaded archive with a trusted Ajent binary:

```sh
ajent verify-release --checksums checksums.txt --sig checksums.txt.sig --file <archive>
```

Or use OpenSSL 3 with the trusted PEM:

```sh
base64 -d checksums.txt.sig > sig.bin
openssl pkeyutl -verify -pubin -inkey ajent-release.pub.pem -rawin -in checksums.txt -sigfile sig.bin
```

Confirm the PEM fingerprint before trusting it:

```sh
openssl pkey -pubin -in ajent-release.pub.pem -outform DER | openssl dgst -sha256
```

Rotation first ships both trusted keys, then retires the old key in a later
release as described above. Private signing material is never distributed
with releases or source.
