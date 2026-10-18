# E1 -- Repository foundation and open-source hygiene

Acceptance: A contributor can clone, build, test and open a DCO-signed PR. CI runs format, vet, lint, race tests and vulnerability scanning on macOS, Linux and Windows. Secret and private-denylist scans block leaks on `main`.
fidelity: executable

- [x] T1.1 Initialize the Go module and layout  Owner: TBD  Est: 45m  verifies: [infrastructure]  deps: []  acc: [go build ./... succeeds and ./ajent version prints version, commit and build date]
  - Create `go.mod` (`module github.com/ajent-social/ajent-client`, `go 1.26`), `cmd/ajent/main.go`, `internal/version` (ldflags vars `Version`, `Commit`, `Date`, and `ClientKind = "go-cli"`), and empty package dirs from docs/design.md.
  - `.gitignore`: `dist/`, `.claude/scratch/`, `ajent.social`, `*.test`, `coverage.out`. `.editorconfig`. `Makefile` targets `fmt`, `lint`, `test`, `build`, `docs`.
  - S1.1.1 Test: `internal/version` unit test covers default values and the `ajent version --json` shape.
- [x] T1.2 Add the Apache-2.0 license, NOTICE and header check  Owner: TBD  Est: 30m  verifies: [infrastructure]  deps: []  acc: [scripts/check-license-headers.sh exits 0 on the tree and 1 when a .go file lacks the SPDX header]
  - `LICENSE` holds the verbatim Apache-2.0 text. `NOTICE` reads "ajent, Copyright 2026 The Ajent Authors". Every Go and shell file starts with `// SPDX-License-Identifier: Apache-2.0` (or `#` for shell). Decision: docs/adr/001-open-source-client-apache-2.md.
  - S1.2.1 Test: a CI step runs the header check. A seeded fixture without the header proves the check fails.
- [x] T1.3 Write the community health files  Owner: TBD  Est: 60m  delivers: [CONTRIBUTING.md, CODE_OF_CONDUCT.md, SECURITY.md, issue and PR templates, CODEOWNERS]  deps: []
  - CONTRIBUTING: prerequisites (Go only), build and test commands, DCO sign-off (`git commit -s`), Conventional Commits, small PRs, how to add a harness writer (link to ADR 007).
  - CODE_OF_CONDUCT: Contributor Covenant 2.1, with reports going through GitHub's reporting flow for the org.
  - SECURITY: report through GitHub private vulnerability reporting. Scope is this client. Include the supported-versions table and the release-key fingerprint placeholder, which T5.3 fills in.
  - `.github/ISSUE_TEMPLATE/`: bug (include `ajent doctor` output), feature, new-harness request, and config.yml linking Discussions. `PULL_REQUEST_TEMPLATE.md` with a test checklist.
  - Run the google-style and humanizer skills over all prose.
- [x] T1.4 Add the CI workflow  Owner: TBD  Est: 60m  verifies: [infrastructure]  deps: [T1.1]  acc: [ci.yml runs gofmt -l (empty), go vet, golangci-lint, go test -race and govulncheck on ubuntu, macos and windows and all jobs pass on the skeleton]
  - `.github/workflows/ci.yml` on push and PR. Pin actions by SHA. Set `permissions: contents: read`. Add `.golangci.yml` with the default linters plus `gosec`, `errorlint`, `misspell` and `revive`. Add a cross-compile job (`GOOS`/`GOARCH` matrix, `CGO_ENABLED=0`).
  - S1.4.1 Test: open a throwaway PR with a deliberately misformatted file and observe the format job fail. Record the result in the PR body, then close the PR.
- [x] T1.5 Add public-safety gates  Owner: TBD  Est: 60m  verifies: [infrastructure]  deps: [T1.1]  acc: [on a test branch, a file containing a seeded denylist pattern makes the leak-scan job fail, and a DCO-unsigned commit makes the DCO job fail]
  - gitleaks action on push and PR. `scripts/leak-scan.sh` reads newline-separated regexes from the `LEAK_DENYLIST` secret and scans the tree plus the PR diff. It fails closed on `main` pushes when the secret is missing and skips with a notice on fork PRs. It never echoes patterns or matches, only file:line. Add a DCO check workflow.
  - The denylist content is maintained privately by maintainers and is never committed (ADR 001).
- [x] T1.6 Write agent-contributor guidance  Owner: TBD  Est: 30m  delivers: [AGENTS.md and CLAUDE.md pointer]  deps: []
  - AGENTS.md covers the stdlib-first rule, `flag` only, no network in unit tests, stdout purity for MCP, the backup-before-write rule, small commits, and running `make fmt lint test` before PRs. CLAUDE.md is one line that imports AGENTS.md.
- [x] T1.7 Configure repository settings  Owner: TBD  Est: 30m  verifies: [infrastructure]  deps: []  kind: any  acc: [gh api repos/ajent-social/ajent-client shows the description, topics and homepage set, and branch protection requires CI, DCO and leak-scan]
  - Use `gh api` to set: private vulnerability reporting on, Discussions on, squash or rebase merges only, and a `main` protection rule with linear history and required checks from T1.4 and T1.5. Leave the repository private until E8. Description and topics come from the plan Appendix A.
- [x] T1.8 Set the private leak denylist secret  Owner: David  Est: 20m  verifies: [infrastructure]  deps: [T1.5]  kind: human  acc: [gh secret list -R ajent-social/ajent-client shows LEAK_DENYLIST, and the leak-scan job on main passes]
  - Until this row is done, pushes to `main` fail by design, because the leak scan fails closed.
