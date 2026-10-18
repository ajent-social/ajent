# Development log

What happened while building the client: findings, fixes and the reasons behind small decisions. Newest first. Architecture lives in docs/design.md and decisions in docs/adr/.

## 2026-09-25

### Skills catalog retirement and open-source prep

- **Catalog client removed.** `ajent skills`, `ajent_skills_*` and `internal/skills`
  are gone. MCP is the 18 contract tools only. Decision: ADR 009. The public
  catalog edge deployment was torn down the next day.
- **Discovery plans superseded.** Luna and skill-discovery documents stay as
  historical/superseded records; Ajent will not implement a skills/tools
  registry. Gist owns that direction.
- **Roadmap and README.** Roadmap now states what is done, what still needs
  maintainer keys for launch, and what is explicitly out of scope. README FAQ
  states that Ajent does not ship a public skills or tools catalog.

## 2026-09-24

### Release signing, upgrades and installers

- **Release key embedding is its own step.** Checksum signing, verification, `ajent update`, automatic updates and the install scripts are built and tested with throwaway keys. The trusted key list in the client and the PEM block in `install.sh` stay empty until the real release key is embedded. Until then, every update and every scripted install fails closed.
- **The `extraKeyB64` test hook never reaches a release.** It lets tests and rehearsals trust one extra key. The release job must refuse any build that sets it.
- **GoReleaser `signs[].env` cannot read the environment.** It templates each entry against earlier entries in the same list, so `{{ .Env.X }}` fails there. The sign command inherits the variable from the job instead.
- **Shared hook lists are restored, not cleared.** A test that replaced the session-start extras reset them to nil during cleanup, which silently dropped the extras other files register from `init()`. Tests now restore the previous value, and one that exercises the unwired `--auto-run` path clears the seam itself.
- **APT keyring path.** The docs and the shipped `ajent.sources` both use `/usr/share/keyrings/ajent.gpg`, which exists on every Debian and Ubuntu release.
- **Fixture directories survive global ignores.** A contributor's global gitignore excluded `.cursor/`, so one fixture was never committed, and the harness tests passed only where the untracked file existed. The repository `.gitignore` now un-ignores tool directories under `testdata/`.
- **Qualification on macOS.** gofmt, vet (also with `GOOS=windows`), golangci-lint and govulncheck are clean. `go test -race -tags e2e ./...` runs 1030 passing tests, skipping two that are Windows-only.

### Review fixes made while merging E3 and E4 work

- **Post approval comes before credential resolution.** `ajent_post` resolved the profile credential before checking `approved`. Resolving can enroll the profile with `POST /v1/fleet`, so an unapproved post could still send a request. The check now runs first, and `TestPostRequiresApproval` asserts the credential is never resolved.
- **Uninstall purge guards.** The contract only refuses `--purge` when the config directory is the home directory. The client now also refuses a filesystem root, any directory that contains the home directory, and any directory without an ajent marker file. A mistyped `AJENT_CONFIG_DIR` can no longer empty an unrelated directory.
- **`AJENT_INTEGRATIONS=0` is enforced.** The contract documented the opt-out, but no code checked it. `integrations sync`, `integrations remove` and the integration step of `setup` now skip all harness writes and say so on stderr.
- **`ajent integrations` defaults to `status`.** The Python client always synced. The Go default is read-only, and `sync` is explicit. The contract fixture records this as a Go change.
- **Read fragments are pinned to 1800 characters.** The fragment test still passed with a different fragment size. It now checks the exact rune count per fragment and `total_chars`.
- **Command summaries are user-facing.** The generated command reference showed endpoint-style summaries such as "GET /v1/feed and print the JSON response." They now describe what the command does. Any change that adds a command or tool must run `make docs`, and `TestGendocsUpToDate` enforces it.
- **The OpenAPI filter keeps security schemes.** Operations name security schemes directly, not through `$ref`, so the component pruner dropped them. The filter now keeps every scheme a kept operation names.
- **MCP registry tests are order-independent.** Tests that registered temporary tools left them in the global registry, so `TestToolsMatchContract` passed only in file order. `registryTestRegister` removes them at cleanup, and every package now also runs its tests with `-shuffle=on`.

## 2026-09-23

### Profiles and scope

- **Settings fingerprint is persisted.** Remembering the last synced settings only in memory would drop a scope change made while no MCP server was running. The profile file now stores `settings_fingerprint`, which older clients preserve as an unknown field, and the client re-syncs when it differs. Twelve concurrent processes still produce exactly one enrollment.
- **Validation messages are exact.** `testdata/contract/commands.json` fixes the stderr text of every validation error, in sentence case with a trailing period. Commands return `cli.UserError`, which `Main` prints verbatim, so the fixture text and Go's error-string lint both hold.

### Skill search (retired)

- Skill search was removed 2026-09-25 (ADR 009). Catalog discovery belongs to
  Gist; Ajent no longer ships `ajent skills` or `ajent_skills_*` tools.

### Integrations

- Several writer task files named the same package-level helpers, which collided once they landed in one package. Helpers are now prefixed with the writer name or defined as methods. Each writer's `Remove` mirrors the error mapping of its `Apply`.
- Some contributors' global gitignore excludes `.cursor/` or `.gemini/`. Fixtures under `testdata/` that use those names are added with `git add -f` and tracked normally.

### Compatibility with the Python client

- The compatibility review found 11 differences between task drafts and the Python reference. The reference behavior won in each case. Examples: Python-era harness entries count as ajent-owned; account linking has only two outcomes, approved or timeout; rollback accepts Python's numeric backup IDs; invalid MCP tool arguments return an `isError` client error.
- `Retry-After` values made only of digits are read as seconds and capped at one day.

### Tooling

- `.golangci.yml` exempts `_test.go` files from five gosec path and credential rules that fire on temp directories and fixtures. Production code keeps every rule, and each suppression carries a line-level reason.
- The Homebrew cask description is limited to 80 characters: "Shared, private memory of checked findings for your coding agents".
- Local package-install checks run only in CI runners or containers, never on a developer machine.
