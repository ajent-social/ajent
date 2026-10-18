# Contributing to ajent

Thank you for your interest in contributing to ajent. The three most useful
contributions are harness writers that add support for a new coding tool, bug
reports that include the output of `ajent doctor`, and fixes to the docs.

## Prerequisites

You need Go 1.26 or later and Git. Nothing else is required. `golangci-lint`
is optional for local development, and CI runs it on every pull request.

## Build and test

- `make build` builds the `ajent` binary.
- `make test` runs the test suite with race detection.
- `make lint` runs gofmt, `go vet`, and `golangci-lint`.
- `make fmt` formats the code with `gofmt`.

## Sign your commits (DCO)

This project requires the Developer Certificate of Origin (DCO) on every
commit. The DCO is a statement that you wrote the contribution or otherwise
have the right to submit it under the project's license. Signing off adds a
`Signed-off-by` trailer to your commit message and takes one flag. Read the
full text at <https://developercertificate.org>.

Sign off each commit with the `-s` flag:

```sh
git commit -s -m "fix: correct the retry backoff calculation"
```

If you forget to sign off your most recent commit, fix it with:

```sh
git commit --amend -s
```

If you need to sign off several commits on your branch, rebase onto `main`
with the `--signoff` flag:

```sh
git rebase --signoff main
```

## Commit messages

This project uses Conventional Commits: `feat:`, `fix:`, `docs:`, `chore:`,
`refactor:`, `test:`, and `ci:`. Release notes are generated from these
commit messages, so an accurate type and a clear summary line matter.

## Pull requests

Keep pull requests small and focused on one change. Include tests for the
behavior you add or fix. Keep one logical change per commit. CI must be
green before a pull request merges.

## Adding support for a coding tool

Read docs/adr/007-harness-integration-model.md before you start. A new
harness writer lives at `internal/integrations/<tool>.go` and ships with
golden fixtures under `testdata/integrations/<tool>/`. Issues for this work
carry the `good first issue` label.

## Security issues

Never file a public issue for a security vulnerability. See SECURITY.md for
how to report one.

## License

By contributing, you agree that your contributions are licensed under
Apache-2.0.
