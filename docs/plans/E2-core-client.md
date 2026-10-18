# E2 -- Core client: contract, config, credentials, API and CLI

Acceptance: Every Python-client terminal command has a Go equivalent that passes the frozen contract fixtures against an httptest server. Python-era credential files load without creating new identities.
fidelity: executable

Parallel layout:

- T2.1 (the contract) is executed by the frontier orchestrator. It is the only row that needs the reference Python source.
- T2.2 and T2.12 need only the module skeleton.
- T2.3 and T2.4 build on them.
- T2.6 owns the command framework plus `cmd/ajent/helpers.go` and `testenv_test.go`. After it lands, T2.7 to T2.11 run fully in parallel, each in its own `cmd/ajent/cmd_<group>.go` file.
- Command tasks inject `cli.Env.Resolve` in tests, so none of them waits for lazy enrollment (T3.6).

Signatures: docs/interfaces.md. Contracts: docs/tasks/T2.*.md.

- [x] T2.1 Freeze the compatibility contract  Owner: orchestrator  Est: 90m  verifies: [UC-006, UC-008, UC-009, UC-010, UC-011, UC-012, UC-013, UC-014, UC-015, UC-016, UC-020]  deps: []  lane: agent  done: 2026 09 23  acc: [testdata/contract/commands.json lists at least 34 commands (29 Python names plus Go-only commands), testdata/contract/tools.json lists all 18 MCP tools, every request references an existing routes.json entry and fixture file, and golden.json matches the reference hashes]
  - The source is the reference Python client 0.6.0 at `$AJENT_REFERENCE_DIR`, available only on the orchestrator machine and never committed. The outputs are `docs/contract.md` (13 sections), `testdata/contract/{commands,tools,errors,routes,golden}.json`, `responses/`, `requests/` and `files/`.
  - Record the intentional breaks: `install` becomes `setup`, legacy origin migration is dropped (`migrate` is reused for Python-to-Go migration), and CLI output is UTF-8 instead of ASCII escapes. Document only public `/v1` routes the client calls (ADR 001).
- [x] T2.12 Define the Credential type and token helpers  Owner: TBD  Est: 30m  verifies: [UC-006, UC-020]  deps: [T1.1]  acc: [go test ./internal/credentials passes, proving unknown JSON fields round-trip, the token never appears in any fmt verb output, and NewToken matches ^aj_[0-9a-f]{16}_[A-Za-z0-9_-]{43}$]
  - File: `internal/credentials/credential.go`. This unblocks T2.4 and T2.6 before the file store lands.
- [x] T2.2 Build the config package: directories, private writes and locks  Owner: TBD  Est: 90m  verifies: [UC-006, UC-020]  deps: [T1.1]  acc: [go test ./internal/config passes on ubuntu, macos and windows, covering atomic replace, exclusive create, 0600/0700 modes on unix, symlink refusal and a two-process lock exclusion test]
  - Implement `Dir`, `EnsurePrivateDir`, `WritePrivate`, `CreateExclusive`, `ReadPrivate`, `ReadJSON`, `WriteJSON` and `Acquire`/`Release`, using `syscall.Flock` on unix and `LockFileEx` via `golang.org/x/sys/windows`.
- [x] T2.3 Build the credentials and profiles file store  Owner: TBD  Est: 90m  verifies: [UC-006, UC-020]  deps: [T2.1, T2.2, T2.12]  acc: [Python-written fixture credential.json and profile files load unchanged with unknown fields preserved, and ProjectScope, ProjectProfile and Identity equal every value in testdata/contract/golden.json]
  - Files: `internal/credentials/{store,project}.go`. Includes the `AJENT_TOKEN` and `AJENT_CREDENTIAL_FILE` overrides and project-root discovery that stops at home.
- [x] T2.4 Build the API client with origin pinning, retries and typed errors  Owner: TBD  Est: 90m  verifies: [UC-008, UC-009, UC-014]  deps: [T1.1, T2.1, T2.12]  acc: [httptest cases prove redirect refusal, non-loopback http refusal, credential sent only to its issuing origin, retry with the same Idempotency-Key on 503 for allowlisted writes only, Retry-After honored up to 2s, and no retry on 401 or 403]
  - Files: `internal/api/{origin,errors,retry,client,observe}.go` and `internal/testutil/fakeserver`. Adds `api.CanonicalUUID`, `Request.NoObserve` and `Observation.Credential`.
- [x] T2.5 Add capability negotiation  Owner: TBD  Est: 45m  verifies: [UC-012, UC-013, UC-014]  deps: [T2.4]  acc: [Require returns UnsupportedCapabilityError without calling the gated route when /v1/reliability omits the capability, capabilities are cached per client, and errors are not cached]
  - File: `internal/api/capabilities.go`.
- [x] T2.6 Build the CLI dispatcher and shared command helpers  Owner: TBD  Est: 90m  verifies: [UC-008, UC-014]  deps: [T1.1, T2.2, T2.3, T2.4, T2.12]  acc: [ajent help lists registered commands, an unknown command exits 2 with usage on stderr, a runtime error exits 1 with a single "ajent: ..." line on stderr, and cmd/ajent/helpers.go hashes the CLI profile with ProjectProfile]
  - Files: `internal/cli`, `cmd/ajent/{main,cmd_version,cmd_help,helpers,wire hook}.go`, and `cmd/ajent/testenv_test.go`. `--json` is a structured-arguments flag, not an output switch; data commands always print JSON. Replaces T1.1's `main.go`.
- [x] T2.7 Port the read commands  Owner: TBD  Est: 60m  verifies: [UC-008, UC-010, UC-014]  deps: [T2.6]  acc: [me, feed, groups, agents, search (with --group), events, inbox and doctor issue the contract request and print the fixture body with key order preserved, and doctor uses the root credential and appends local_client_version]
  - File: `cmd/ajent/cmd_read.go`.
- [x] T2.8 Port the write commands  Owner: TBD  Est: 90m  verifies: [UC-009, UC-010, UC-012]  deps: [T2.5, T2.6]  acc: [post without --key exits 1, a post retry reuses the key, ack rejects a non-integer cursor, retract rejects a non-UUID, group and question actions send the fixture bodies, and gated actions fail with unsupported_capability without calling the route]
  - File: `cmd/ajent/cmd_write.go`.
- [x] T2.9 Port the goals commands  Owner: TBD  Est: 60m  verifies: [UC-013]  deps: [T2.5, T2.6]  acc: [goals, goal, goal-update and goal-track call the contract routes when the goals capability is present and fail with unsupported_capability, having called only /v1/reliability, when it is absent]
  - File: `cmd/ajent/cmd_goals.go`.
- [x] T2.10 Port the scope and domain commands  Owner: TBD  Est: 90m  verifies: [UC-016]  deps: [T2.6]  acc: [configure writes user or project scope settings, scope-grants.json gains a grant only from a successful POST /v1/domain-grants and never from project files, and revoke-grant issues DELETE for a valid UUID only]
  - Files: `internal/scope` (reused by `ajent setup` in T4.3) and `cmd/ajent/cmd_scope.go`. Prompts go through `Env.OpenTTY` only. Non-interactive runs require flags.
- [x] T2.11 Build the consent-gated feedback subsystem  Owner: TBD  Est: 90m  verifies: [UC-015]  deps: [T2.5, T2.6]  acc: [with consent unset, help-improve report stores locally and sends zero requests to /v1/improvements, after feedback set it sends exactly one request with the frozen payload keys, and no captured record or payload contains query text]
  - Files: `internal/feedback` and `cmd/ajent/{cmd_feedback,wire_feedback}.go`.
- [x] T2.13 Wire scope configuration into ajent setup  Owner: TBD  Est: 30m  verifies: [UC-006, UC-016]  deps: [T2.10, T4.3]  acc: [setup.ConfigureScope is assigned at init, a non-interactive setup without a domain prints the deferred hint and returns nil, and a private project choice writes .aj/config.json with private_only true]
  - Files: `cmd/ajent/wire_scope.go` and its test. Contract: docs/tasks/T2.13.md.
