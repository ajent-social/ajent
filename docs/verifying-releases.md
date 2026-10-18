# Verifying releases

The local release route signs `checksums.txt` with Ed25519. That file covers the
six platform archives and `verification.json`. It does not establish package
channels, GitHub build provenance or operating-system signing.

## Checksums

Download the archive, `checksums.txt` and `checksums.txt.sig` from the same
[GitHub release](https://github.com/ajent-social/ajent/releases). Compare the
archive's SHA-256 hash with its exact filename entry:

```sh
sha256sum --ignore-missing -c checksums.txt
```

On macOS:

```sh
shasum -a 256 --ignore-missing -c checksums.txt
```

A checksum alone is not authentication. Verify the checksum file's signature
with a trusted key before running the archive's binary.

## Signature

Use the committed [packaging PEM](../packaging/ajent-release.pub.pem) from the
reviewed release tag and confirm its DER public-key fingerprint against
[SECURITY.md](../SECURITY.md). Save that PEM as `ajent-release.pub.pem` beside
the downloaded checksum files before running the verification commands. For
v0.7.0, the pinned source download and fingerprint check are:

```sh
curl -fL https://github.com/ajent-social/ajent/raw/refs/tags/v0.7.0/packaging/ajent-release.pub.pem -o ajent-release.pub.pem
openssl pkey -pubin -in ajent-release.pub.pem -outform DER | openssl dgst -sha256
```

Compare the printed fingerprint with SECURITY.md before trusting this key.
Package-site publication of the key is not established by the local release
route.

With OpenSSL 3, decode the base64 signature before verifying its raw bytes:

```sh
openssl base64 -d -A -in checksums.txt.sig -out sig.bin
openssl pkeyutl -verify -pubin -inkey ajent-release.pub.pem -rawin -in checksums.txt -sigfile sig.bin
```

A successful signature check authenticates the checksum file; compare the
archive hash as above. OpenSSL must support Ed25519 and `pkeyutl -rawin`.

An already trusted Ajent binary can verify both the signature and one archive:

```sh
ajent verify-release --checksums checksums.txt --sig checksums.txt.sig --file ajent_<version>_<os>_<arch>.tar.gz
```

Use the `.zip` filename for Windows. Success prints `ok` and exits 0. Do not
bootstrap trust by running an unverified downloaded binary as its own verifier.

## Build provenance

Locally prepared archives do not carry GitHub artifact attestations. The signed
`verification.json` records the source, toolchain and checks observed on the
release workstation. It is local evidence, not independent build provenance;
`gh attestation verify` is not supported for this route's artifacts.

## macOS notarization

These macOS binaries are unsigned by Developer ID and unnotarized. The Ed25519
archive signature does not make Gatekeeper accept them. Windows binaries are
not Authenticode-signed either. See
[troubleshooting](troubleshooting.md#macos-cannot-be-opened).

## Package repositories

APT and DNF/YUM distribution require a separate package-signing key and published
metadata. The local archive route supplies neither. See the planned channel
status in [installation](install.md#apt).
