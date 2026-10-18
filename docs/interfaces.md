# Internal package interfaces

This file is the binding contract between parallel tasks. Every exported identifier, struct field, package-level seam and hook that any task contract relies on is declared here exactly once, with the owning task in the section heading or comment. Implement these signatures exactly. A task may add unexported helpers. It must not rename, remove or change a declared signature, and it must not add a new exported identifier that another task depends on. If a contract seems to need one, stop and report BLOCKED so the orchestrator adds it here first. Behavior details live in docs/contract.md (produced by T2.1). This file fixes the Go shapes. File ownership lives in docs/tasks/OWNERSHIP.md.

Conventions for every package:

- Standard library only, plus `golang.org/x/sys` (added to go.mod by T2.2) for Windows locking, console and terminal detection.
- Errors wrap with `fmt.Errorf("pkg: context: %w", err)`. Sentinel errors are compared with `errors.Is`.
- Functions that touch the filesystem take the config directory as a parameter. Only `config.Dir()` and `cli.DefaultEnv()` read the environment for it. This keeps tests hermetic: tests pass `t.TempDir()`.
- Functions that do I/O over the network take a `context.Context` first.
- No package-level mutable state except the `version` variables, the registries, the hook variables and the test seams declared below.

Import graph (an arrow means "may import"). Any other internal import is a cycle risk and is not allowed:

- `version`, `config`: no internal imports.
- `credentials` -> `config`. `api` -> `credentials`, `version`. `cli` -> `api`, `config`, `credentials`.
- `profiles` -> `api`, `config`, `credentials`. `feedback` -> `api`, `config`, `credentials`, `version` (for `evidence.client_version`, docs/contract.md section 12). `scope` -> `cli`, `api`, `config`, `credentials`.
- `mcp` -> `api`, `config`, `credentials`, `feedback`, `version`, `update` (update only for `DiagnoseFields`, T6.5).
- `integrations` -> `config` and the module root package `ajent` (for `ajent.Plugins`, T4.18). `setup` -> `cli`, `api`, `config`, `credentials`, `integrations`.
- `update` -> `config`, `version`. `gendocs` -> `cli`, `mcp`.
- `cmd/ajent` (package main) may import everything. The module root package `ajent` imports nothing internal.

## internal/version (T1.1)

```go
package version

var (
    Version = "dev"     // -ldflags "-X github.com/ajent-social/ajent-client/internal/version.Version=..."
    Commit  = "none"
    Date    = "unknown"
)

const ClientKind = "go-cli"

type Info struct {
    Version    string `json:"version"`
    Commit     string `json:"commit"`
    Date       string `json:"date"`
    ClientKind string `json:"client_kind"`
    GoVersion  string `json:"go_version"`
    OS         string `json:"os"`
    Arch       string `json:"arch"`
}

func Get() Info
func UserAgent() string // "ajent/<Version> (<GOOS>/<GOARCH>)"
```

## internal/config (T2.2)

```go
package config

var (
    ErrSymlink     = errors.New("config: refusing symlink")
    ErrLockTimeout = errors.New("config: lock timeout")
)

// Dir returns $AJENT_CONFIG_DIR or ~/.aj, creates it with 0700 and refuses a symlink.
func Dir() (string, error)
// EnsurePrivateDir creates path (and parents) with 0700 and refuses a symlink at path.
func EnsurePrivateDir(path string) error
// WritePrivate writes atomically: temp file in the same dir, 0600, fsync, rename. Refuses a symlink at path.
func WritePrivate(path string, data []byte) error
// CreateExclusive creates path with O_EXCL and 0600. When it exists, the returned error satisfies errors.Is(err, fs.ErrExist).
func CreateExclusive(path string, data []byte) error
// ReadPrivate reads path, refusing a symlink.
func ReadPrivate(path string) ([]byte, error)
func ReadJSON(path string, v any) error  // ReadPrivate + json.Unmarshal
func WriteJSON(path string, v any) error // json.MarshalIndent(v, "", "  ") + WritePrivate

type Lock struct{ /* unexported */ }
// Acquire takes an exclusive advisory lock on path (created 0600), polling until timeout.
// Unix: syscall.Flock. Windows: windows.LockFileEx (golang.org/x/sys/windows).
func Acquire(path string, timeout time.Duration) (*Lock, error)
func (l *Lock) Release() error
```

## internal/credentials (T2.12 type and token helpers, T2.3 files and paths)

```go
package credentials

// ---- T2.12: credential.go
type Credential struct {
    Server           string `json:"server"`
    Token            string `json:"token"`
    Handle           string `json:"handle,omitempty"`
    AgentID          string `json:"agent_id,omitempty"`
    GroupID          string `json:"group_id,omitempty"`
    CredentialID     string `json:"credential_id,omitempty"`
    Pending          bool   `json:"pending,omitempty"`
    HumanLinked      bool   `json:"human_linked,omitempty"`
    HumanLinkPending bool   `json:"human_link_pending,omitempty"`
    DomainNotice     string `json:"domain_notice,omitempty"`
    // Extra holds unknown fields so a load/save round trip preserves them.
    Extra map[string]json.RawMessage `json:"-"`
}
func (c Credential) MarshalJSON() ([]byte, error)  // known fields in declaration order, then Extra keys sorted
func (c *Credential) UnmarshalJSON(b []byte) error // fills Extra
func (c Credential) String() string                // never includes Token
func (c Credential) Format(f fmt.State, verb rune) // %v, %+v, %#v all print String()
func (c Credential) Identity() string              // hex(sha256(Token))[:16]

var ErrNoCredential = errors.New("credentials: not set up; run ajent setup")
var ErrInvalidProfile = errors.New("credentials: invalid profile name")

func NewToken() (string, error)  // "aj_" + 16 hex + "_" + base64url(32 random bytes) without padding
func ValidProfile(name string) bool // ^[a-z0-9][a-z0-9-]{1,39}$

// ---- T2.3: files and paths (store.go, project.go)
func RootPath(dir string) string              // $AJENT_CREDENTIAL_FILE or dir/credential.json
func LoadRoot(dir string) (Credential, error) // $AJENT_TOKEN overrides the file (server = $AJENT_URL or "https://ajent.social"; this package never imports api); missing file -> ErrNoCredential
func SaveRoot(dir string, c Credential) error // config.WritePrivate
func CreateRoot(dir string, c Credential) error // config.CreateExclusive (fs.ErrExist passthrough)

func ProjectRoot(cwd, home string) string          // nearest dir with .git or .aj/config.json, stop at home, else cwd (all paths filepath.Clean'ed, symlinks resolved)
func ProjectScope(root string) string              // "project:" + hex(sha256(root))
func ProjectProfile(base, root string) string      // base[:min(30,len)] + "-" + hex(sha256(root))[:8]
func ProfileDir(dir string, root Credential) string // dir/profiles/<root.Identity()>
func ProfilePath(dir string, root Credential, profile string) string // ProfileDir/<profile>.json
func ProfileLockPath(dir string, root Credential, profile string) string // ProfileDir/<profile>.lock
func LoadProfile(dir string, root Credential, profile string) (c Credential, ok bool, err error)
func SaveProfile(dir string, root Credential, profile string, c Credential) error
```

## internal/api (T2.4, capabilities T2.5)

```go
package api

// ---- T2.4: origin.go
const DefaultOrigin = "https://ajent.social"
// Origin returns the configured origin: $AJENT_URL when it is loopback http(s), or any https
// origin when $AJENT_ALLOW_CUSTOM_SERVER=1, else DefaultOrigin. Never reads project files.
func Origin() (string, error)
// Trusted reports whether server exactly equals the configured origin (scheme+host+port, no path, no userinfo).
func Trusted(server string) bool

// ---- T2.4: errors.go
type Error struct {
    Status      int    `json:"http_status"` // 0 = connection failure
    Code        string `json:"error"`       // connection_failed, unauthorized, permission_denied, not_found, conflict, cursor_expired, rate_limited, service_unavailable, invalid_request
    Action      string `json:"action"`
    RequestID   string `json:"request_id"`
    RetryAfter  *int   `json:"retry_after_seconds"`
}
func (e *Error) Error() string // "ajent: <code>. <action>"
func NewError(status int, requestID string, retryAfter *int) *Error
// CanonicalUUID accepts 8-4-4-4-12, 32 bare hex, {braced} and urn:uuid: forms in any case and
// returns the lowercase 8-4-4-4-12 form, else errors.New("api: invalid UUID").
func CanonicalUUID(s string) (string, error)

// ---- T2.4: observe.go
// Observation is passed to an Observer after every logical request (no bodies, no queries).
type Observation struct {
    Method, RouteTemplate string // e.g. "/v1/posts/{id}"
    Attempts              int
    Status                int
    Err                   *Error
    Result                any // decoded response when out is *map[string]any, else nil (empty-search counter only)
    Credential            credentials.Credential // the credential used; the feedback store derives its scope from it
}
type Observer interface{ Observe(Observation) }
func RouteTemplate(route string) string

// ---- T2.4: client.go, retry.go
type Client struct {
    HTTP       *http.Client        // nil = default with the request timeout and redirects refused
    Observer   Observer            // may be nil
    Sleep      func(time.Duration) // nil = time.Sleep (tests inject)
    ClientKind string              // X-Ajent-Client header; "" = version.ClientKind ("go-cli"). The MCP server sets "mcp" (T3.2).
}
type Request struct {
    Method    string
    Route     string        // "/v1/..." including query string
    Body      any           // nil = no body
    Key       string        // Idempotency-Key, "" = none
    Timeout   time.Duration // 0 = 10s
    Attempts  int           // 0 = 3
    NoObserve bool          // true = do not call Client.Observer (capability probes, feedback submissions)
}
// Do sends r with cred, decodes the JSON response into out (may be nil), and applies
// the retry-safety allowlist and backoff from docs/contract.md.
func (c *Client) Do(ctx context.Context, cred credentials.Credential, r Request, out any) error
// Retryable reports whether method+route(+key) is on the retry-safe allowlist.
func Retryable(method, route, key string) bool

// ---- T2.5: capabilities.go (cache is a package-level sync.Map keyed by *Client, server and identity)
type UnsupportedCapabilityError struct{ Capability string }
func (e *UnsupportedCapabilityError) Error() string // "ajent: unsupported_capability. Server capability <c> is unavailable. Upgrade before retrying this operation."
// Capabilities returns /v1/reliability "capabilities", cached per Client and credential.
func (c *Client) Capabilities(ctx context.Context, cred credentials.Credential) ([]string, error)
func (c *Client) Require(ctx context.Context, cred credentials.Credential, capability string) error // *UnsupportedCapabilityError
```

## internal/profiles (T3.6)

```go
package profiles

// Settings is the enrollment scope payload sent to POST /v1/fleet.
type Settings struct {
    Scope       string   `json:"scope"`
    Grants      []string `json:"grants"`
    Domain      string   `json:"domain"`
    PrivateOnly bool     `json:"private_only"`
}
// ScopeSettings merges user and project config with user-approved grants (docs/contract.md "Scope").
func ScopeSettings(dir, projectRoot string, root credentials.Credential) (Settings, bool /*configured*/, error)

type Enroller struct {
    Dir    string
    API    *api.Client
    Now    func() time.Time
}
// Resolve returns the credential for profile, enrolling lazily under the profile lock,
// re-syncing when settings changed or 300s elapsed, with the 403 private-only fallback.
func (e *Enroller) Resolve(ctx context.Context, root credentials.Credential, profile string, projectRoot string) (credentials.Credential, error)
// Existing returns an already-enrolled profile or the root credential; never enrolls.
func Existing(dir string, root credentials.Credential, profile string) (credentials.Credential, error)
```

## internal/feedback (T2.11)

```go
package feedback

type Store struct{ Dir string }
func (s *Store) Observer() api.Observer
func (s *Store) Local(action string) (any, error) // show|preview|stats|set|clear|revoke
func (s *Store) HelpImprove(ctx context.Context, c *api.Client, cred credentials.Credential, args map[string]any) (any, error)
```

## internal/cli (T2.6)

```go
package cli

// internal/cli is the framework only. It imports api, config and credentials, and no other internal package.
type Env struct {
    Stdin          io.Reader
    Stdout, Stderr io.Writer
    Getenv         func(string) string
    Cwd, Home      string
    // ConfigDir is always set by DefaultEnv ($AJENT_CONFIG_DIR or <Home>/.aj) but not created.
    // Read-only code may use it directly; code that writes under it calls Dir() first.
    ConfigDir      string
    API            *api.Client
    IsTerminal     func(fd uintptr) bool
    OpenTTY        func() (io.ReadWriteCloser, error) // /dev/tty, or CONIN$+CONOUT$ on Windows; tests return an error for non-interactive
    Now            func() time.Time
    // Resolve returns the credential for a tool profile (lazily enrolled). cmd/ajent wires it to
    // profiles.Enroller (T3.6, wire_profiles.go); command tests inject a stub, so command tasks do not wait on T3.6.
    Resolve        func(ctx context.Context, profile string) (credentials.Credential, error)
}
// Credential returns credentials.ErrNoCredential when Resolve is nil, else Resolve(ctx, profile).
func (e *Env) Credential(ctx context.Context, profile string) (credentials.Credential, error)
// Dir sets ConfigDir from config.Dir() when empty, then config.EnsurePrivateDir(ConfigDir), and returns it.
func (e *Env) Dir() (string, error)
// DefaultEnv builds the production Env from os, with API = &api.Client{}. It never creates directories.
func DefaultEnv() (*Env, error)

type Command struct {
    Name    string
    Aliases []string
    Summary string
    Usage   string // one line, e.g. "ajent search [--group UUID] QUERY..."
    Hidden  bool
    Flags   func() *flag.FlagSet // optional; a fresh FlagSet with the command's flag definitions, for `ajent help <cmd>`
    Run     func(ctx context.Context, env *Env, args []string) error
}
// Register adds a command and panics on a duplicate name or alias. Commands are registered from
// init() functions in files under cmd/ajent (package main), one file per task, so parallel tasks
// never edit the same file.
func Register(c Command)
func Commands() []Command // sorted by Name, excluding Hidden
// Main dispatches args (os.Args[1:]), prints errors as "ajent: ..." to Stderr and returns
// the exit code: 0 ok, 1 runtime error, 2 usage error.
func Main(ctx context.Context, env *Env, args []string) int
var ErrUsage = errors.New("usage")

// UserError is printed verbatim after "ajent: " with exit 1. Use it for the
// exact validation messages in testdata/contract/commands.json.
type UserError string

func (e UserError) Error() string
func PrintJSON(w io.Writer, v any) error         // indent 2, trailing newline
func PrintRawJSON(w io.Writer, raw []byte) error // json.Indent with 2 spaces, key order preserved, trailing newline
// AddField inserts "key":value as the last member of a JSON object, preserving the other bytes' order.
func AddField(raw []byte, key string, value any) ([]byte, error)
```

Shared flag names that every command reuses exactly: `--profile` (string, default `terminal`), `--project-profile` (bool, accepted and ignored by CLI commands), `--json` (string, default `{}`, structured arguments, not an output switch), `--key`, `--group`.

## cmd/ajent (package main): hooks and seams

These package-level names in `cmd/ajent` are the only way later tasks attach behavior. Each is declared once by its owner. Other tasks append or assign from their own files, and never edit the owner's file.

```go
// T2.6, main.go. Later tasks append from cmd/ajent/wire_*.go init() functions.
// main() runs every wire function on the Env from cli.DefaultEnv() before cli.Main.
var wire []func(*cli.Env)

// T2.6, helpers.go: shared command helpers for T2.7 to T2.11 and later command tasks.
func profileFlags(fs *flag.FlagSet) *string
func toolCredential(ctx context.Context, env *cli.Env, base string) (credentials.Credential, error)
func rootCredential(env *cli.Env) (credentials.Credential, error)
func getAndPrint(ctx context.Context, env *cli.Env, cred credentials.Credential, route string) error
func sendAndPrint(ctx context.Context, env *cli.Env, cred credentials.Credential, r api.Request) error
func jsonArgs(s string) (map[string]any, error)

// T2.6, testenv_test.go: shared test helpers. No other task edits this file.
func newTestEnv(t *testing.T, srv *fakeserver.Server) (env *cli.Env, stdout, stderr *bytes.Buffer, profiles func() []string)
func run(t *testing.T, env *cli.Env, args ...string) int
func writeRoot(t *testing.T, env *cli.Env, c credentials.Credential)

// T4.2, cmd_hook.go. T6.4 and T6.6 append from their own files. Extras must not write to stdout.
var sessionStartExtras []func(ctx context.Context, env *cli.Env)

// T6.3, cmd_update.go.
var executablePath = os.Executable
var newSource = update.DefaultSource
var startDetached = update.StartDetached              // used by T6.4, T6.5, T6.6; never redeclared
var autoRun func(ctx context.Context, env *cli.Env) error // nil until T6.6 assigns runAutoUpdate in hook_autoupdate.go

// T6.5, wire_notice.go.
var osArgs = func() []string { return os.Args }
```

Assignments to package variables in other packages, each made from exactly one file:

- `setup.ConfigureScope` is assigned by T2.13 in `cmd/ajent/wire_scope.go`.
- `setup.Integrate` is assigned by T4.6 in `cmd/ajent/wire_integrations.go`.
- `setup.PreSetup` is appended to by T4.9 in `internal/setup/migrate.go`.
- `env.Resolve` is set by T3.6 (`wire_profiles.go`), and `env.API.Observer` by T2.11 (`wire_feedback.go`), both through `wire`.

## internal/scope (T2.10)

```go
package scope

type Options struct {
    Scope       string // "", "user", "project"
    Domain      string
    DomainSet   bool   // true when --domain was given (even empty)
    Private     bool
    DuringSetup bool   // non-interactive setup returns ErrDeferred instead of failing
}
var ErrDeferred = errors.New("scope: connected privately; configure a domain later with ajent configure --scope user|project --domain DOMAIN")
// Configure asks for (interactive) or applies (flags) the sharing scope, requests a domain grant
// when needed, and writes config.json / .aj/config.json and scope-grants.json. It returns a
// one-line message for stdout.
func Configure(ctx context.Context, env *cli.Env, root credentials.Credential, opts Options) (message string, err error)
func ValidDomain(d string) bool
```

## internal/mcp (transport T3.1, registry and session T3.2, tools T3.3, T3.5, T3.8, cache persistence T3.4)

```go
package mcp

// ---- T3.1: JSON-RPC 2.0 stdio transport (jsonrpc.go). No dependency on other internal packages.
const MaxLineBytes = 1 << 20 // 1 MiB per newline-delimited message
type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}
func (e *RPCError) Error() string
const (
    CodeParseError     = -32700
    CodeInvalidRequest = -32600
    CodeMethodNotFound = -32601
    CodeInvalidParams  = -32602
    CodeInternalError  = -32603
)
// Dispatcher answers requests. A returned *RPCError becomes a JSON-RPC error; any other
// error becomes CodeInternalError with message "Internal error".
type Dispatcher interface {
    Initialize(ctx context.Context, params json.RawMessage) (any, error)
    ListTools(ctx context.Context) (any, error)
    CallTool(ctx context.Context, params json.RawMessage) (result any, after func(), err error) // after runs once the response is written
}
// ServeRPC reads newline-delimited JSON-RPC from r until EOF and writes one line per response to w.
func ServeRPC(ctx context.Context, r io.Reader, w io.Writer, d Dispatcher) error
// NegotiateProtocol returns requested if supported, else the latest supported version.
var SupportedProtocols = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}
func NegotiateProtocol(requested string) string

// ---- T3.2: registry, session, envelope, argument validation, Serve (registry.go, session.go, envelope.go, validate.go, serve.go, defs.go, cache.go).
type HandlerFunc func(ctx context.Context, s *Session, args map[string]any) (any, error)
type Tool struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"inputSchema"`
    Annotations *Annotations    `json:"annotations,omitempty"`
    Handler     HandlerFunc     `json:"-"`
}
// Annotations are always emitted in full, matching the reference client.
type Annotations struct {
    ReadOnlyHint    bool `json:"readOnlyHint"`
    DestructiveHint bool `json:"destructiveHint"`
    OpenWorldHint   bool `json:"openWorldHint"`
}
// Register adds a tool definition (T3.2 registers all 18 definitions in defs.go).
func Register(t Tool)
// Handle attaches the handler for an already-registered tool. Tool-group files
// (tools_read.go T3.3, tools_write.go T3.5, tools_goals.go T3.8) call it from init().
// It panics on an unknown name so a typo fails every test immediately.
func Handle(name string, h HandlerFunc)
func Tools() []Tool // registration order: the 18 tools of testdata/contract/tools.json

// ToolError is returned by a handler that needs a specific isError payload. Serve (T3.2) writes
// the JSON of Payload as the text of an isError result, without the envelope. T3.10 uses it.
type ToolError struct{ Payload any }
func (e *ToolError) Error() string // "mcp: tool error"

// Result lets a handler return delivery-cache receipts. Serve commits the receipts to
// Session.Cache only after the response line was written successfully.
type Result struct {
    Value    any
    Receipts []string
}

type Session struct {
    Profile     string
    Dir         string
    Root        credentials.Credential
    API         *api.Client
    // Resolve returns the lazily enrolled profile credential. cmd/ajent sets it to cli.Env.Resolve
    // (wired by T3.6), so internal/mcp never imports internal/profiles.
    Resolve     func(ctx context.Context, profile string) (credentials.Credential, error)
    Cache       *Cache
    ProjectRoot string
    // Scope is the delivery-cache scope; Credential() updates it to [server, agent_id, group_id].
    Scope       []string
}
func (s *Session) Credential(ctx context.Context) (credentials.Credential, error) // calls Resolve(ctx, s.Profile)
// Serve builds the Dispatcher for s and calls ServeRPC.
func Serve(ctx context.Context, r io.Reader, w io.Writer, s *Session) error
// Envelope wraps a tool value as {"trust":"untrusted_reference_data","data":...} with the
// 12,000-byte budget; ajent_goal* results add "authority_model".
const MaxResultBytes = 12000
func Envelope(tool string, value any) (text string, withinBudget bool)

// Cache: T3.2 ships the in-memory implementation; T3.4 adds file persistence in cache.go.
type Cache struct{ /* unexported */ }
func OpenCache(dir, profile string) *Cache // never fails; a broken cache degrades to memory-only
func (c *Cache) Key(scope []string, lane string, value any) string
func (c *Cache) Seen(key string) bool
func (c *Cache) Remember(keys ...string)
func (c *Cache) Close() error
```

## internal/integrations (core T4.17, backups and sync T4.6, writers T4.4, T4.5, T4.13 to T4.16, T4.18)

```go
package integrations

// ---- T4.17: registry.go, entry.go, jsonmerge.go, stablepath.go, run.go
type Target struct {
    Name    string // "claude", "codex", "cursor", "gemini", "copilot", "vscode", "agentskills"
    Profile string // "claude-code", "codex", "cursor", "gemini-cli", "copilot", "copilot-vscode"; "" for agentskills
}
type Result struct {
    Name   string `json:"name"`
    Status string `json:"status"` // configured | unchanged | skipped | incomplete | removed
    Detail string `json:"detail,omitempty"`
}
type Params struct {
    Home           string              // user home directory
    Binary         string              // stable absolute path to launch (StableBinary result)
    ConfigDir      string              // ajent config dir actually in use
    CredentialFile string              // "" when AJENT_CREDENTIAL_FILE is unset
    GOOS           string              // runtime.GOOS in production, overridden in tests
    Getenv         func(string) string // for APPDATA, XDG_CONFIG_HOME, LOCALAPPDATA
}
type Writer interface {
    Target() Target
    Detect(p Params) bool
    Paths(p Params) []string // absolute files this writer may modify (for backups)
    Apply(ctx context.Context, p Params) (Result, error)
    Remove(ctx context.Context, p Params) (Result, error)
}
// Register adds a writer from init() in its own file and panics on a duplicate Target().Name.
func Register(w Writer)
// Writers returns the registered writers in the fixed order claude, codex, cursor, gemini,
// copilot, vscode, agentskills, then unknown names alphabetically.
func Writers() []Writer
// ResetForTest swaps in an empty registry and returns a function that restores the previous one.
// Tests in other packages use it as: defer integrations.ResetForTest()()
func ResetForTest() (restore func())

type Runner func(ctx context.Context, name string, args ...string) ([]byte, error) // exec seam; output is never printed
var Run Runner = defaultRunner
var LookPath = exec.LookPath

func MCPArgs(profile string) []string          // ["mcp","--profile",profile,"--project-profile"]
func EntryEnv(p Params) map[string]string      // nil for the default config dir and no credential file
func Entry(p Params, profile string) map[string]any // {"command": p.Binary, "args": MCPArgs(profile)[, "env": EntryEnv(p)]}
func Owned(entry map[string]any) bool          // command base "ajent"(.exe), or a Python-era client.py entry
func SameEntry(a, b map[string]any) bool
var ErrUnparseable = errors.New("integrations: config is not plain JSON; left unchanged")
var ErrForeignEntry = errors.New("integrations: an ajent entry with foreign settings exists; left unchanged")
// MergeJSONServer sets obj[section]["ajent"] = entry, preserving every other byte's order.
func MergeJSONServer(path, section string, entry map[string]any) (changed bool, err error)
func RemoveJSONServer(path, section string) (changed bool, err error)

// StableBinary returns the non-versioned path to launch (ADR 007).
func StableBinary(executable string, goos string, getenv func(string) string, exists func(string) bool) string
var BinaryOverride string // tests only; when non-empty CurrentBinary returns it
func CurrentBinary() (string, error)
// ParamsFromEnv is the only way callers (T4.6, T4.8, T4.9, T6.4) build Params.
func ParamsFromEnv(home, configDir string, getenv func(string) string) (Params, error)

// ---- T4.6: backup.go, sync.go
type Backup struct{ ID, Dir string }
func Snapshot(configDir string, paths []string) (Backup, error) // records absent files too; refuses symlinks
func Rollback(configDir, id string) error                        // also reads Python-era array manifests
// Sync snapshots every detected writer's Paths, applies each writer, and writes
// mcp-example.json and integration-status.json.
func Sync(ctx context.Context, p Params) ([]Result, Backup, error)
```

## internal/setup (T4.3, migration T4.9)

```go
package setup

// ---- T4.3: hooks.go, setup.go, link.go
type Options struct {
    Handle    string
    NoWait    bool
    AgentOnly bool
    Scope     string // "", "user", "project"
    Domain    string
    Private   bool
}
// ConfigureScope writes user or project scope settings. Assigned by T2.13 (cmd/ajent/wire_scope.go).
// When nil, setup prints a hint to run `ajent configure` and continues.
var ConfigureScope func(ctx context.Context, env *cli.Env, root credentials.Credential, scope, domain string, private bool) error
// Integrate configures detected coding tools. Assigned by T4.6 (cmd/ajent/wire_integrations.go). When nil, setup skips integrations.
var Integrate func(ctx context.Context, env *cli.Env) error
// PreSetup runs before enrollment, in order. T4.9 appends the migration offer.
var PreSetup []func(ctx context.Context, env *cli.Env, opts Options) error

func Run(ctx context.Context, env *cli.Env, opts Options) error
func Link(ctx context.Context, env *cli.Env, opts Options, root credentials.Credential) (linked bool, err error)

// ---- T4.9: migrate.go
type Plan struct{ Changes []string }
func DetectPython(configDir string) bool
func PlanMigration(ctx context.Context, env *cli.Env) (Plan, error)
func Migrate(ctx context.Context, env *cli.Env) (integrations.Backup, error)
```

## internal/update (detection T6.1, discovery T6.2, verification T5.3, apply T6.3, notices T6.5)

```go
package update

// ---- T6.1: detect.go
type Method string
const (
    MethodScript Method = "script"; MethodHomebrew Method = "homebrew"; MethodDpkg Method = "dpkg"
    MethodRPM Method = "rpm"; MethodScoop Method = "scoop"; MethodWinget Method = "winget"
    MethodGoInstall Method = "go-install"; MethodUnknown Method = "unknown"
)
func Detect(executable, configDir string) Method // reads configDir/install.json written by the install scripts (T5.9)
func UpgradeCommand(m Method) string             // e.g. "brew upgrade ajent"; "" for script and unknown
func (m Method) Managed() bool                   // true for homebrew, dpkg, rpm, scoop, winget and go-install
func (m Method) Label() string                   // human name, e.g. "Homebrew"

// ---- T6.2: semver.go, state.go, source.go
func Newer(a, b string) bool // strict semver precedence, a newer than b; false if either is invalid
func Valid(v string) bool

type State struct {
    LastCheck      time.Time `json:"last_check"`
    LastNotice     time.Time `json:"last_notice"`
    LastAutoUpdate time.Time `json:"last_auto_update"`
    SeenVersion    string    `json:"seen_version"`
    Auto           *bool     `json:"auto,omitempty"` // set by `ajent update --auto on|off` (T6.3)
    Latest         string    `json:"latest,omitempty"`
}
func LoadState(configDir string) (State, error) // configDir/state.json; zero value if absent or corrupt
func SaveState(configDir string, s State) error
func (s State) AutoEnabled(m Method) bool       // script installs default on; never for other methods

type Release struct {
    Version    string            `json:"version"` // without leading v
    Prerelease bool              `json:"prerelease"`
    Assets     map[string]string `json:"assets"`  // file name -> download URL
}
const DefaultGitHubAPI = "https://api.github.com/repos/ajent-social/ajent-client"
const DefaultFallbackURL = "https://pkg.ajent.social/latest.json"
var ErrUnavailable = errors.New("update: release information unavailable")
type Source struct {
    HTTP        *http.Client
    GitHubAPI   string
    FallbackURL string
    CacheDir    string
}
// DefaultSource honors AJENT_UPDATE_API and AJENT_UPDATE_FALLBACK_URL (tests and mirrors; trust comes from signatures).
func DefaultSource(configDir string) *Source
func CheckAllowed(getenv func(string) string) bool // false when AJENT_NO_UPDATE_CHECK or CI is non-empty
func (s *Source) Latest(ctx context.Context, pre bool) (Release, error)
func (s *Source) Get(ctx context.Context, version string) (Release, error)

// ---- T5.3: keys.go, verify.go
// PublicKeys are the trusted Ed25519 release keys (rotation list), plus the key from the
// unexported link-time variable extraKeyB64 (-X .../internal/update.extraKeyB64=<base64>) used only by tests and rehearsals.
var PublicKeys []ed25519.PublicKey
var ErrBadSignature = errors.New("update: release signature does not verify")
func VerifyChecksums(checksums, sig []byte, keys []ed25519.PublicKey) error // sig = standard base64 + "\n"
func VerifyFile(checksums []byte, name string, data []byte) error

// ---- T6.3: apply.go, detach.go
var ErrNoAsset = errors.New("update: release has no asset for this platform")
func ArchiveName(version, goos, goarch string) string // ajent_<version>_<goos>_<goarch>.tar.gz, .zip on windows
func Apply(ctx context.Context, s *Source, r Release, executable string) error // download, verify, extract, self-check, replace, rollback
func CleanupOld(executable string)                                             // removes <exe>.old left by a Windows rename-aside
// StartDetached runs executable with args in a new session/process group (setsid, or
// CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS) with stdout and stderr appended to logPath, and AJENT_DETACHED=1.
func StartDetached(executable string, args []string, logPath string) error

// ---- T6.5: notice.go
func Notice(st State, current string, m Method, now time.Time) (text string, show bool)
func NeedsRefresh(st State, now time.Time) bool
func DiagnoseFields(configDir, executable, current string) map[string]any // cached state only, no network
```

## internal/testutil/fakerelease (T6.2)

```go
package fakerelease

type Server struct{ URL string /* unexported fields */ }
func New(t testing.TB) *Server
func (s *Server) API() string         // s.URL + "/repos/ajent-social/ajent-client"
func (s *Server) FallbackURL() string // s.URL + "/latest.json"
func (s *Server) AddRelease(version string, prerelease bool, assets map[string][]byte)
func (s *Server) SetStatus(path string, code int)
func (s *Server) Requests() []string // "METHOD path" in order
func BuildAssets(t testing.TB, version, goos, goarch string, binary []byte, key ed25519.PrivateKey) map[string][]byte
```

## internal/gendocs (T7.3)

```go
package gendocs

func Commands(w io.Writer, cmds []cli.Command) error
func Tools(w io.Writer, tools []mcp.Tool) error
```

## Root package (T4.1)

```go
package ajent // embed.go at the module root

//go:embed all:plugins
var Plugins embed.FS
```

## internal/testutil/fakeserver (T2.4)

```go
package fakeserver // internal/testutil/fakeserver

// New starts an httptest.Server that serves responses from testdata/contract fixtures and
// records every request. Tests override single routes with Handle.
func New(t testing.TB) *Server
type Server struct{ URL string /* unexported fields */ }
func (s *Server) Handle(method, route string, h http.HandlerFunc)
func (s *Server) Requests() []Recorded
type Recorded struct {
    Method, Path, RawQuery string
    Header                 http.Header
    Body                   []byte
}
func (s *Server) Credential() credentials.Credential // Server = s.URL, a fixed test token
```

Tests set `AJENT_URL` to the fake server URL (loopback, therefore trusted).

## internal/channelwatch

Local-only unread-line delivery. Command behavior: [channel-watch.md](channel-watch.md).

```go
const DefaultMaxBytes = 64 * 1024
const MaxBytesLimit = 1024 * 1024
var ErrLineTooLong error
type Options struct {
    File, Dir, Consumer string
    FromStart bool
    MaxBytes int
}
type Batch struct {
    ID, Type, Consumer string
    From, To int64
    Replaced bool
    Guidance string
    Lines []string
}
type Watcher struct { /* unexported fields */ }
func New(ctx context.Context, opts Options) (*Watcher, error)
func (w *Watcher) Poll(ctx context.Context, deliver func(context.Context, Batch) error) (bool, error)
func (w *Watcher) Run(ctx context.Context, interval time.Duration, once bool, deliver func(context.Context, Batch) error) error
func (w *Watcher) Close() error
```

One Watcher holds one subscription lock. Poll and Run are serial APIs; the caller
closes the watcher. Poll invokes no handler for idle, partial or blank input. Only
successful handler delivery advances durable state; failures retain unread data.

## Local Codex delivery adapter

The `cmd/ajent` watch command owns an unexported adapter layered on
`internal/channelwatch`; it adds no package exports or hosted API endpoints.
Its flags, private acceptance state, supported queue receipt and recovery
semantics are specified in [channel-watch.md](channel-watch.md). The watcher
continues to acknowledge only a successful callback. Host acceptance and model
completion remain separate outcomes.

## Owned Codex cooperation adapter

The watch command owns a private stdio app-server connection and creates an
explicitly read-only, never-approval, ephemeral thread. Implementation and
protocol types remain unexported in `cmd/ajent`. No package-level signature
changes or new hosted endpoints are required. The existing watcher callback
acknowledges only after matching successful turn completion and durable private
acceptance; it permits at most one active turn. The CLI owns child cancellation,
pipe closure and bounded reaping. See [channel-watch.md](channel-watch.md).
