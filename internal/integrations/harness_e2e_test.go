// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// harnessOtherSkillKey is the relative path (as harnessHashTree keys it) of
// the foreign skill file the fixture home ships, which must never be
// touched by any writer (docs/tasks/T4.7.md step 1).
const harnessOtherSkillKey = ".agents/skills/other-skill/SKILL.md"

// harnessAjentNetworkSkillKey is the relative path of the file the
// agentskills writer creates. It does not exist in the fixture, so
// Rollback must delete it (docs/tasks/T4.7.md step 5.6).
const harnessAjentNetworkSkillKey = ".agents/skills/ajent-network/SKILL.md"

// harnessCodexConfigKey is the relative path of the file the fake codex CLI
// writes on "mcp add". It does not exist in the fixture either, so Rollback
// must delete it too.
const harnessCodexConfigKey = ".codex/config.toml"

// harnessParams builds the Params every TestHarness* test drives Sync and
// Rollback with (docs/tasks/T4.7.md step 4): a stable binary path unrelated
// to the temp home, the default config dir under home, the host GOOS and a
// Getenv that only ever answers APPDATA.
func harnessParams(home, appdata string) Params {
	return Params{
		Home:      home,
		Binary:    filepath.FromSlash("/opt/ajent/bin/ajent"),
		ConfigDir: filepath.Join(home, ".aj"),
		GOOS:      runtime.GOOS,
		Getenv:    harnessGetenv(appdata),
	}
}

// harnessGetenv returns a Params.Getenv that answers APPDATA with appdata
// (used only on windows) and everything else with "".
func harnessGetenv(appdata string) func(string) string {
	return func(key string) string {
		if key == "APPDATA" {
			return appdata
		}
		return ""
	}
}

// harnessCopyHome copies testdata/integrations/e2e/home into a fresh
// t.TempDir() and relocates vscode-user/mcp.json to VS Code's real per-OS
// user directory, computed through vscodeWriter's own userDir so the
// fixture never drifts from the writer it exercises (docs/tasks/T4.7.md
// steps 1 and 2). It returns the new home and, on windows, the APPDATA
// value the moved file now lives under.
func harnessCopyHome(t *testing.T) (home, appdata string) {
	t.Helper()

	home = t.TempDir()
	src := filepath.Join("..", "..", "testdata", "integrations", "e2e", "home")
	harnessCopyTree(t, src, home)

	if runtime.GOOS == "windows" {
		appdata = filepath.Join(home, "AppData", "Roaming")
	}

	dest := (vscodeWriter{}).userDir(harnessParams(home, appdata))
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatalf("harness: mkdir vscode user dir %s: %v", dest, err)
	}

	vscodeSrc := filepath.Join(home, "vscode-user", "mcp.json")
	data, err := os.ReadFile(vscodeSrc)
	if err != nil {
		t.Fatalf("harness: read fixture %s: %v", vscodeSrc, err)
	}
	if err := os.WriteFile(filepath.Join(dest, "mcp.json"), data, 0o600); err != nil {
		t.Fatalf("harness: write vscode mcp.json: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(home, "vscode-user")); err != nil {
		t.Fatalf("harness: remove fixture vscode-user dir: %v", err)
	}

	return home, appdata
}

// harnessCopyTree copies every file under src into dst, preserving relative
// paths and creating directories as needed.
func harnessCopyTree(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G122: path is produced by WalkDir over the repository's own testdata fixture tree, not attacker input; no TOCTOU risk.
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("harness: copy tree %s -> %s: %v", src, dst, err)
	}
}

// harnessHashTree hashes every regular file under root and returns a map
// from its root-relative, slash-separated path to the hex sha256 of its
// bytes. A path absent from the map means the file does not exist.
func harnessHashTree(t *testing.T, root string) map[string]string {
	t.Helper()

	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G122: path is produced by WalkDir over this test's own t.TempDir() home, not attacker input; no TOCTOU risk.
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("harness: hash tree %s: %v", root, err)
	}
	return out
}

// harnessTreeEqualExcept fails the test for every key present in before or
// after, other than those skip reports true for, whose presence or hash
// differs between the two trees.
func harnessTreeEqualExcept(t *testing.T, before, after map[string]string, skip func(string) bool, label string) {
	t.Helper()

	keys := make(map[string]bool, len(before)+len(after))
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}

	for k := range keys {
		if skip(k) {
			continue
		}
		b, inBefore := before[k]
		a, inAfter := after[k]
		switch {
		case inBefore != inAfter:
			t.Errorf("%s: %s present-before=%v present-after=%v", label, k, inBefore, inAfter)
		case inBefore && b != a:
			t.Errorf("%s: %s bytes changed", label, k)
		}
	}
}

// harnessSkipSyncVolatile reports the two paths a second, no-op Sync run is
// still allowed to change: the status file it always rewrites, and any
// path under the fresh backup directory the second Snapshot creates
// (docs/tasks/T4.7.md step 5.5).
func harnessSkipSyncVolatile(path string) bool {
	return path == ".aj/integration-status.json" || strings.HasPrefix(path, ".aj/backups/")
}

// harnessSkipConfigDir reports any path under the ajent config directory,
// excluded when comparing a rollback against the pre-sync fixture
// (docs/tasks/T4.7.md step 5.6): the config dir itself never existed
// before Sync created it.
func harnessSkipConfigDir(path string) bool {
	return strings.HasPrefix(path, ".aj/")
}

// harnessNoLookPath fails for every name, simulating a machine with no
// coding tool installed (docs/tasks/T4.7.md step 7).
func harnessNoLookPath(string) (string, error) {
	return "", errors.New("harness: fake LookPath: not found")
}

// harnessFailRun is a Run seam that fails the test immediately if called,
// for tests where no writer should ever invoke a vendor CLI.
func harnessFailRun(t *testing.T) Runner {
	return func(context.Context, string, ...string) ([]byte, error) {
		t.Helper()
		t.Fatalf("harness: unexpected Run call")
		return nil, nil
	}
}

// harnessFakeCLIs emulates the claude and codex CLIs well enough to drive
// claudeWriter and codexWriter through a real Sync/Rollback cycle
// (docs/tasks/T4.7.md step 3): "mcp add-json"/"mcp remove" and the
// marketplace calls edit <home>/.claude.json and an in-memory marketplace
// set; "mcp add"/"mcp remove"/"mcp get ajent --json" keep an in-memory
// codex entry and mirror it into <home>/.codex/config.toml so that file has
// something for a backup to restore.
type harnessFakeCLIs struct {
	t    *testing.T
	home string

	marketplace []string

	codexPresent bool
	codexEntry   map[string]any
}

// harnessInstallFakeCLIs installs f.run and a LookPath that only finds
// claude and codex as the package's Run and LookPath seams, restoring the
// previous ones on test cleanup.
func harnessInstallFakeCLIs(t *testing.T, home string) *harnessFakeCLIs {
	t.Helper()

	f := &harnessFakeCLIs{t: t, home: home}

	prevRun, prevLookPath := Run, LookPath
	Run = f.run
	LookPath = func(name string) (string, error) {
		switch name {
		case "claude", "codex":
			return "/usr/local/bin/" + name, nil
		default:
			return "", errors.New("harness: fake LookPath: not found")
		}
	}
	t.Cleanup(func() {
		Run = prevRun
		LookPath = prevLookPath
	})

	return f
}

func (f *harnessFakeCLIs) run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "claude":
		return f.runClaude(args)
	case "codex":
		return f.runCodex(args)
	default:
		f.t.Fatalf("harness fake CLI: unexpected command %q %v", name, args)
		return nil, nil
	}
}

func (f *harnessFakeCLIs) runClaude(args []string) ([]byte, error) {
	switch {
	case len(args) >= 2 && args[0] == "mcp" && args[1] == "add-json":
		var entry map[string]any
		if err := json.Unmarshal([]byte(args[len(args)-1]), &entry); err != nil {
			f.t.Fatalf("harness fake claude: add-json payload not JSON: %v", err)
		}
		f.setClaudeEntry(entry)
		return nil, nil
	case len(args) >= 2 && args[0] == "mcp" && args[1] == "remove":
		f.deleteClaudeEntry()
		return nil, nil
	case len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "list":
		return []byte(strings.Join(f.marketplace, "\n")), nil
	case len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add":
		f.marketplace = append(f.marketplace, args[len(args)-1])
		return nil, nil
	case len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "remove":
		kept := f.marketplace[:0]
		for _, m := range f.marketplace {
			if m != args[len(args)-1] {
				kept = append(kept, m)
			}
		}
		f.marketplace = kept
		return nil, nil
	default:
		f.t.Fatalf("harness fake claude: unexpected args %v", args)
		return nil, nil
	}
}

func (f *harnessFakeCLIs) claudeJSONPath() string {
	return filepath.Join(f.home, ".claude.json")
}

func (f *harnessFakeCLIs) readClaudeDoc() map[string]any {
	data, err := os.ReadFile(f.claudeJSONPath())
	if err != nil {
		return map[string]any{}
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		f.t.Fatalf("harness fake claude: .claude.json is not JSON: %v", err)
	}
	return doc
}

func (f *harnessFakeCLIs) writeClaudeDoc(doc map[string]any) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		f.t.Fatalf("harness fake claude: marshal .claude.json: %v", err)
	}
	if err := os.WriteFile(f.claudeJSONPath(), append(data, '\n'), 0o600); err != nil {
		f.t.Fatalf("harness fake claude: write .claude.json: %v", err)
	}
}

func (f *harnessFakeCLIs) setClaudeEntry(entry map[string]any) {
	doc := f.readClaudeDoc()
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		servers = map[string]any{}
	}
	servers["ajent"] = entry
	doc["mcpServers"] = servers
	f.writeClaudeDoc(doc)
}

func (f *harnessFakeCLIs) deleteClaudeEntry() {
	doc := f.readClaudeDoc()
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		return
	}
	delete(servers, "ajent")
	doc["mcpServers"] = servers
	f.writeClaudeDoc(doc)
}

func (f *harnessFakeCLIs) runCodex(args []string) ([]byte, error) {
	switch {
	case len(args) >= 3 && args[0] == "mcp" && args[1] == "get" && args[2] == "ajent":
		if !f.codexPresent {
			return nil, errors.New("harness fake codex: no such server: ajent")
		}
		return f.marshalCodexGet()
	case len(args) >= 2 && args[0] == "mcp" && args[1] == "add":
		f.codexEntry = harnessDecodeCodexAdd(args[2:])
		f.codexPresent = true
		f.writeCodexConfigToml()
		return nil, nil
	case len(args) >= 3 && args[0] == "mcp" && args[1] == "remove" && args[2] == "ajent":
		f.codexPresent = false
		f.codexEntry = nil
		f.removeCodexConfigToml()
		return nil, nil
	default:
		f.t.Fatalf("harness fake codex: unexpected args %v", args)
		return nil, nil
	}
}

func (f *harnessFakeCLIs) marshalCodexGet() ([]byte, error) {
	transport := map[string]any{
		"type":    "stdio",
		"command": f.codexEntry["command"],
		"args":    f.codexEntry["args"],
	}
	if env, ok := f.codexEntry["env"]; ok {
		transport["env"] = env
	}
	return json.Marshal(map[string]any{"name": "ajent", "transport": transport})
}

// harnessDecodeCodexAdd reverses codexAddArgv's shape, starting right after
// "mcp add": zero or more "--env KEY=VALUE" pairs, then "ajent", "--", the
// command and its arguments.
func harnessDecodeCodexAdd(args []string) map[string]any {
	i := 0
	env := map[string]any{}
	for i+1 < len(args) && args[i] == "--env" {
		parts := strings.SplitN(args[i+1], "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
		i += 2
	}
	i++ // "ajent"
	i++ // "--"

	entry := map[string]any{"command": args[i]}
	rest := args[i+1:]
	restAny := make([]any, len(rest))
	for j, r := range rest {
		restAny[j] = r
	}
	entry["args"] = restAny
	if len(env) > 0 {
		entry["env"] = env
	}
	return entry
}

func (f *harnessFakeCLIs) writeCodexConfigToml() {
	path := filepath.Join(f.home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatalf("harness fake codex: mkdir %s: %v", filepath.Dir(path), err)
	}
	content := "[mcp_servers.ajent]\ncommand = " + harnessTOMLQuote(f.codexEntry["command"]) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatalf("harness fake codex: write config.toml: %v", err)
	}
}

func (f *harnessFakeCLIs) removeCodexConfigToml() {
	path := filepath.Join(f.home, ".codex", "config.toml")
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		f.t.Fatalf("harness fake codex: remove config.toml: %v", err)
	}
}

// harnessTOMLQuote renders v (a string command value) as a double-quoted
// TOML string, matching the shape the real Codex CLI writes for a string
// value.
func harnessTOMLQuote(v any) string {
	s, _ := v.(string)
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

// harnessKV names one nested field a synced config file must carry, and the
// value it must decode to.
type harnessKV struct {
	path []string
	want any
}

// harnessAssertConfigSynced reads the JSON document at configPath and
// checks that every kv still decodes to its expected value (the foreign
// settings the fixture shipped with), and that section.ajent.command equals
// wantBinary (docs/tasks/T4.7.md step 5.3).
func harnessAssertConfigSynced(t *testing.T, configPath, section, wantBinary string, kvs ...harnessKV) {
	t.Helper()

	doc := harnessReadJSON(t, configPath)
	for _, kv := range kvs {
		harnessAssertDig(t, doc, kv.want, kv.path...)
	}
	harnessAssertDig(t, doc, wantBinary, section, "ajent", "command")
}

func harnessReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("harness: read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("harness: decode %s: %v", path, err)
	}
	return doc
}

func harnessWriteJSON(t *testing.T, path string, doc map[string]any) {
	t.Helper()

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("harness: marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("harness: write %s: %v", path, err)
	}
}

// harnessDig walks doc through path, returning the value found and whether
// every step along the way was itself an object.
func harnessDig(doc map[string]any, path ...string) (any, bool) {
	var cur any = doc
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func harnessAssertDig(t *testing.T, doc map[string]any, want any, path ...string) {
	t.Helper()

	got, ok := harnessDig(doc, path...)
	if !ok {
		t.Fatalf("missing %s in %#v", strings.Join(path, "."), doc)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", strings.Join(path, "."), got, want)
	}
}

// harnessJSONValue decodes a JSON literal into the same any shape
// encoding/json would leave inside a decoded document, so it can be
// compared against one with reflect.DeepEqual.
func harnessJSONValue(t *testing.T, raw string) any {
	t.Helper()

	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("harness: decode literal %q: %v", raw, err)
	}
	return v
}

// harnessWriterNames is Writers() order for the seven writers this
// repository ships (docs/interfaces.md internal/integrations).
var harnessWriterNames = []string{"claude", "codex", "cursor", "gemini", "copilot", "vscode", "agentskills"}

// harnessAssertAllStatus checks that results has exactly one entry per
// harnessWriterNames, in that order, all reporting want.
func harnessAssertAllStatus(t *testing.T, results []Result, want string) {
	t.Helper()

	if len(results) != len(harnessWriterNames) {
		t.Fatalf("results = %d entries, want %d (every real writer): %+v", len(results), len(harnessWriterNames), results)
	}
	for i, r := range results {
		if r.Name != harnessWriterNames[i] {
			t.Fatalf("results[%d].Name = %q, want %q (order %v): %+v", i, r.Name, harnessWriterNames[i], harnessWriterNames, results)
		}
		if r.Status != want {
			t.Errorf("results[%d] (%s).Status = %q, want %q; detail=%q", i, r.Name, r.Status, want, r.Detail)
		}
	}
}

// TestHarnessSyncTwiceThenRollback runs every real registered writer
// against a realistic fake home, proving that: a first Sync configures all
// seven writers while leaving foreign configuration intact; a second Sync
// changes nothing; and Rollback to the first snapshot restores every file
// to its original bytes, including deleting files that did not exist
// before (docs/tasks/T4.7.md step 5).
func TestHarnessSyncTwiceThenRollback(t *testing.T) {
	home, appdata := harnessCopyHome(t)
	harnessInstallFakeCLIs(t, home)
	p := harnessParams(home, appdata)
	ctx := context.Background()

	before := harnessHashTree(t, home)

	res1, b1, err := Sync(ctx, p)
	if err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	harnessAssertAllStatus(t, res1, "configured")

	harnessAssertConfigSynced(t, filepath.Join(home, ".cursor", "mcp.json"), "mcpServers", p.Binary,
		harnessKV{[]string{"mcpServers", "github"}, harnessJSONValue(t, `{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}`)},
	)
	harnessAssertConfigSynced(t, filepath.Join(home, ".gemini", "settings.json"), "mcpServers", p.Binary,
		harnessKV{[]string{"theme"}, "Default"},
		harnessKV{[]string{"selectedAuthType"}, "oauth-personal"},
		harnessKV{[]string{"mcpServers", "fs"}, harnessJSONValue(t, `{"command":"node","args":["fs.js"]}`)},
	)
	harnessAssertConfigSynced(t, filepath.Join(home, ".copilot", "mcp-config.json"), "mcpServers", p.Binary,
		harnessKV{[]string{"mcpServers", "playwright"}, harnessJSONValue(t, `{"type":"local","command":"npx","args":["@playwright/mcp@latest"],"tools":["*"]}`)},
	)
	harnessAssertConfigSynced(t, filepath.Join((vscodeWriter{}).userDir(p), "mcp.json"), "servers", p.Binary,
		harnessKV{[]string{"inputs"}, harnessJSONValue(t, `[]`)},
		harnessKV{[]string{"servers", "github"}, harnessJSONValue(t, `{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)},
	)
	harnessAssertConfigSynced(t, filepath.Join(home, ".claude.json"), "mcpServers", p.Binary,
		harnessKV{[]string{"numStartups"}, harnessJSONValue(t, "3")},
		harnessKV{[]string{"mcpServers", "context7"}, harnessJSONValue(t, `{"type":"stdio","command":"npx","args":["-y","@upstash/context7-mcp"]}`)},
	)

	after1 := harnessHashTree(t, home)

	res2, _, err := Sync(ctx, p)
	if err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}
	harnessAssertAllStatus(t, res2, "unchanged")

	after2 := harnessHashTree(t, home)
	harnessTreeEqualExcept(t, after1, after2, harnessSkipSyncVolatile, "after2 vs after1")

	if err := Rollback(p.ConfigDir, b1.ID); err != nil {
		t.Fatalf("Rollback(%q) error = %v", b1.ID, err)
	}

	afterRollback := harnessHashTree(t, home)
	harnessTreeEqualExcept(t, before, afterRollback, harnessSkipConfigDir, "afterRollback vs before")

	if _, ok := afterRollback[harnessAjentNetworkSkillKey]; ok {
		t.Errorf("%s still present after rollback, want deleted (it did not exist before Sync)", harnessAjentNetworkSkillKey)
	}
	if _, ok := afterRollback[harnessCodexConfigKey]; ok {
		t.Errorf("%s still present after rollback, want deleted (it did not exist before Sync)", harnessCodexConfigKey)
	}

	wantOtherSkill, ok := before[harnessOtherSkillKey]
	if !ok {
		t.Fatalf("fixture is missing %s", harnessOtherSkillKey)
	}
	for stage, tree := range map[string]map[string]string{
		"before": before, "after1": after1, "after2": after2, "afterRollback": afterRollback,
	} {
		if got := tree[harnessOtherSkillKey]; got != wantOtherSkill {
			t.Errorf("%s: %s hash = %q, want %q (must stay untouched)", stage, harnessOtherSkillKey, got, wantOtherSkill)
		}
	}
}

// TestHarnessForeignAjentEntryBlocksOnlyThatTool pre-seeds Cursor's config
// with a foreign ajent entry and checks that Sync reports only cursor as
// incomplete, leaves the cursor file byte for byte unchanged, and still
// configures every other writer (docs/tasks/T4.7.md step 6).
func TestHarnessForeignAjentEntryBlocksOnlyThatTool(t *testing.T) {
	home, appdata := harnessCopyHome(t)
	harnessInstallFakeCLIs(t, home)
	p := harnessParams(home, appdata)

	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	doc := harnessReadJSON(t, cursorPath)
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("fixture cursor mcp.json has no mcpServers object: %#v", doc)
	}
	servers["ajent"] = map[string]any{"command": "npx", "args": []any{"foreign"}}
	doc["mcpServers"] = servers
	harnessWriteJSON(t, cursorPath, doc)

	before, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read seeded cursor mcp.json: %v", err)
	}

	res, _, err := Sync(context.Background(), p)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	for _, r := range res {
		if r.Name == "cursor" {
			if r.Status != "incomplete" {
				t.Errorf("cursor status = %q, want incomplete", r.Status)
			}
			continue
		}
		if r.Status != "configured" {
			t.Errorf("%s status = %q, want configured", r.Name, r.Status)
		}
	}

	after, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read cursor mcp.json after Sync: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("cursor mcp.json bytes changed despite a foreign ajent entry:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestHarnessNoToolsInstalled runs Sync against an empty home with every
// vendor CLI missing: only agentskills is not tied to a CLI or a config
// directory, so it is the only writer that reports anything, and Sync
// itself returns no error (docs/tasks/T4.7.md step 7).
func TestHarnessNoToolsInstalled(t *testing.T) {
	home := t.TempDir()

	prevLookPath := LookPath
	LookPath = harnessNoLookPath
	t.Cleanup(func() { LookPath = prevLookPath })

	prevRun := Run
	Run = harnessFailRun(t)
	t.Cleanup(func() { Run = prevRun })

	p := harnessParams(home, "")

	res, _, err := Sync(context.Background(), p)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(res) != 1 || res[0].Name != "agentskills" || res[0].Status != "configured" {
		t.Fatalf("results = %+v, want exactly one agentskills/configured result", res)
	}
}
