// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// claudeTestParams builds Params the way the contract's step 8 does: a
// fresh Home, the stable binary path fixed by testdata/integrations/claude,
// and the default config dir under that Home unless the caller overrides
// it.
func claudeTestParams(home string) Params {
	return Params{
		Home:      home,
		Binary:    "/opt/ajent/bin/ajent",
		ConfigDir: filepath.Join(home, ".aj"),
		GOOS:      runtime.GOOS,
		Getenv:    func(string) string { return "" },
	}
}

// claudeFixtureHome copies the top-level files of
// testdata/integrations/claude/<name> into a fresh t.TempDir(), which then
// serves as Params.Home. The ".gitkeep" placeholder used to keep the
// intentionally empty "claude-json-absent" fixture tracked in git is
// skipped.
func claudeFixtureHome(t *testing.T, name string) string {
	t.Helper()
	home := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "integrations", "claude", name)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read fixture dir %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == ".gitkeep" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read fixture file %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(home, e.Name()), data, 0o600); err != nil {
			t.Fatalf("seed fixture file %s: %v", e.Name(), err)
		}
	}
	return home
}

// fakeClaudeCLI emulates the claude CLI well enough to exercise
// claudeWriter: "mcp add-json" and "mcp remove" edit <home>/.claude.json
// the same way the real CLI would, and "plugin marketplace" list/add/remove
// keep an in-memory marketplace list. Every argv is recorded.
type fakeClaudeCLI struct {
	t    *testing.T
	home string

	calls       [][]string
	marketplace []string

	failAddJSON bool
	failList    bool
}

// newFakeClaudeCLI installs the fake as the package's Run and LookPath
// seams and restores the previous ones on test cleanup.
func newFakeClaudeCLI(t *testing.T, home string) *fakeClaudeCLI {
	t.Helper()
	f := &fakeClaudeCLI{t: t, home: home}

	prevRun, prevLookPath := Run, LookPath
	Run = f.run
	LookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	t.Cleanup(func() {
		Run = prevRun
		LookPath = prevLookPath
	})

	return f
}

func (f *fakeClaudeCLI) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.t.Helper()
	argv := append([]string{name}, args...)
	f.calls = append(f.calls, argv)

	switch {
	case name == "claude" && len(args) >= 2 && args[0] == "mcp" && args[1] == "add-json":
		if f.failAddJSON {
			return []byte("SECRET-OUTPUT"), errors.New("fake claude: add-json failed")
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(args[len(args)-1]), &entry); err != nil {
			f.t.Fatalf("fake claude: add-json payload not JSON: %v", err)
		}
		f.setEntry(entry)
		return nil, nil
	case name == "claude" && len(args) >= 2 && args[0] == "mcp" && args[1] == "remove":
		f.deleteEntry()
		return nil, nil
	case name == "claude" && len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "list":
		if f.failList {
			return []byte("SECRET-OUTPUT"), errors.New("fake claude: marketplace list failed")
		}
		return []byte(strings.Join(f.marketplace, "\n")), nil
	case name == "claude" && len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add":
		f.marketplace = append(f.marketplace, args[len(args)-1])
		return nil, nil
	case name == "claude" && len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "remove":
		kept := f.marketplace[:0]
		for _, m := range f.marketplace {
			if m != args[len(args)-1] {
				kept = append(kept, m)
			}
		}
		f.marketplace = kept
		return nil, nil
	default:
		f.t.Fatalf("fake claude: unexpected call %v", argv)
		return nil, nil
	}
}

// findCall returns the first recorded call whose arguments, after the
// leading "claude", start with want, or nil.
func (f *fakeClaudeCLI) findCall(want ...string) []string {
	for _, call := range f.calls {
		if call[0] != "claude" || len(call) < 1+len(want) {
			continue
		}
		match := true
		for i, w := range want {
			if call[1+i] != w {
				match = false
				break
			}
		}
		if match {
			return call
		}
	}
	return nil
}

func (f *fakeClaudeCLI) claudeJSONPath() string {
	return filepath.Join(f.home, ".claude.json")
}

func (f *fakeClaudeCLI) readDoc() map[string]any {
	data, err := os.ReadFile(f.claudeJSONPath())
	if err != nil {
		return map[string]any{}
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		f.t.Fatalf("fake claude: existing .claude.json is not JSON: %v", err)
	}
	return doc
}

func (f *fakeClaudeCLI) writeDoc(doc map[string]any) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		f.t.Fatalf("fake claude: marshal .claude.json: %v", err)
	}
	if err := os.WriteFile(f.claudeJSONPath(), append(data, '\n'), 0o600); err != nil {
		f.t.Fatalf("fake claude: write .claude.json: %v", err)
	}
}

func (f *fakeClaudeCLI) setEntry(entry map[string]any) {
	doc := f.readDoc()
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		servers = map[string]any{}
	}
	servers["ajent"] = entry
	doc["mcpServers"] = servers
	f.writeDoc(doc)
}

func (f *fakeClaudeCLI) deleteEntry() {
	doc := f.readDoc()
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		return
	}
	delete(servers, "ajent")
	doc["mcpServers"] = servers
	f.writeDoc(doc)
}

func TestClaudeDetect(t *testing.T) {
	prev := LookPath
	t.Cleanup(func() { LookPath = prev })
	LookPath = func(string) (string, error) { return "", errors.New("claude: not found") }

	if got := (claudeWriter{}).Detect(Params{}); got {
		t.Fatal("Detect() = true, want false when LookPath errors")
	}
}

func TestClaudeAddWhenAbsent(t *testing.T) {
	home := claudeFixtureHome(t, "claude-json-absent")
	fake := newFakeClaudeCLI(t, home)
	p := claudeTestParams(home)

	got, err := (claudeWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != "configured" {
		t.Fatalf("Status = %q, want %q", got.Status, "configured")
	}

	addJSON := fake.findCall("mcp", "add-json")
	if addJSON == nil {
		t.Fatalf("no mcp add-json call recorded; calls = %v", fake.calls)
	}
	wantArgv := []string{
		"claude", "mcp", "add-json", "--scope", "user", "ajent",
		`{"args":["mcp","--profile","claude-code","--project-profile"],"command":"/opt/ajent/bin/ajent","type":"stdio"}`,
	}
	if !reflect.DeepEqual(addJSON, wantArgv) {
		t.Fatalf("add-json argv = %v, want %v", addJSON, wantArgv)
	}

	addMarket := fake.findCall("plugin", "marketplace", "add")
	if addMarket == nil {
		t.Fatalf("no plugin marketplace add call recorded; calls = %v", fake.calls)
	}
	if got, want := addMarket[len(addMarket)-1], "ajent-social/ajent"; got != want {
		t.Fatalf("marketplace add argument = %q, want %q", got, want)
	}
}

func TestClaudeUnchangedSecondRun(t *testing.T) {
	home := claudeFixtureHome(t, "claude-json-current")
	fake := newFakeClaudeCLI(t, home)
	p := claudeTestParams(home)

	got, err := (claudeWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != "unchanged" {
		t.Fatalf("Status = %q, want %q", got.Status, "unchanged")
	}
	if call := fake.findCall("mcp", "add-json"); call != nil {
		t.Fatalf("unexpected mcp add-json call: %v", call)
	}
	if call := fake.findCall("mcp", "remove"); call != nil {
		t.Fatalf("unexpected mcp remove call: %v", call)
	}
}

func TestClaudeReplacesPythonEntry(t *testing.T) {
	home := claudeFixtureHome(t, "claude-json-owned-old")
	fake := newFakeClaudeCLI(t, home)
	p := claudeTestParams(home)

	got, err := (claudeWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != "configured" {
		t.Fatalf("Status = %q, want %q", got.Status, "configured")
	}

	removeIdx, addIdx := -1, -1
	for i, call := range fake.calls {
		if len(call) >= 3 && call[1] == "mcp" && call[2] == "remove" {
			removeIdx = i
		}
		if len(call) >= 3 && call[1] == "mcp" && call[2] == "add-json" {
			addIdx = i
		}
	}
	if removeIdx == -1 {
		t.Fatalf("no mcp remove call recorded; calls = %v", fake.calls)
	}
	if addIdx == -1 {
		t.Fatalf("no mcp add-json call recorded; calls = %v", fake.calls)
	}
	if removeIdx > addIdx {
		t.Fatalf("mcp remove (call %d) did not precede mcp add-json (call %d): %v", removeIdx, addIdx, fake.calls)
	}
}

func TestClaudeForeignPreserved(t *testing.T) {
	home := claudeFixtureHome(t, "claude-json-foreign")
	fake := newFakeClaudeCLI(t, home)
	p := claudeTestParams(home)

	got, err := (claudeWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != "incomplete" {
		t.Fatalf("Status = %q, want %q", got.Status, "incomplete")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected no calls for a foreign entry, got %v", fake.calls)
	}
}

func TestClaudeCLIFailure(t *testing.T) {
	home := claudeFixtureHome(t, "claude-json-absent")
	fake := newFakeClaudeCLI(t, home)
	fake.failAddJSON = true
	p := claudeTestParams(home)

	got, err := (claudeWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != "incomplete" {
		t.Fatalf("Status = %q, want %q", got.Status, "incomplete")
	}
	if !strings.Contains(got.Detail, "mcp-example.json") {
		t.Fatalf("Detail = %q, want it to contain %q", got.Detail, "mcp-example.json")
	}
	if strings.Contains(got.Detail, "SECRET-OUTPUT") {
		t.Fatalf("Detail leaked fake CLI output: %q", got.Detail)
	}
}

func TestClaudeEnvForCustomConfigDir(t *testing.T) {
	home := claudeFixtureHome(t, "claude-json-absent")
	fake := newFakeClaudeCLI(t, home)
	p := claudeTestParams(home)
	p.ConfigDir = "/x/custom"

	got, err := (claudeWriter{}).Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.Status != "configured" {
		t.Fatalf("Status = %q, want %q", got.Status, "configured")
	}

	addJSON := fake.findCall("mcp", "add-json")
	if addJSON == nil {
		t.Fatalf("no mcp add-json call recorded; calls = %v", fake.calls)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(addJSON[len(addJSON)-1]), &entry); err != nil {
		t.Fatalf("add-json payload not JSON: %v", err)
	}
	env, ok := entry["env"].(map[string]any)
	if !ok {
		t.Fatalf("entry has no env object: %#v", entry)
	}
	if got, want := env["AJENT_CONFIG_DIR"], "/x/custom"; got != want {
		t.Fatalf("env AJENT_CONFIG_DIR = %v, want %v", got, want)
	}
}

func TestClaudeRemoveOwnedOnly(t *testing.T) {
	t.Run("owned entry is removed", func(t *testing.T) {
		home := claudeFixtureHome(t, "claude-json-current")
		fake := newFakeClaudeCLI(t, home)
		p := claudeTestParams(home)

		got, err := (claudeWriter{}).Remove(context.Background(), p)
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if got.Status != "removed" {
			t.Fatalf("Status = %q, want %q", got.Status, "removed")
		}
		if call := fake.findCall("mcp", "remove"); call == nil {
			t.Fatalf("expected an mcp remove call; calls = %v", fake.calls)
		}
	})

	t.Run("foreign entry is preserved", func(t *testing.T) {
		home := claudeFixtureHome(t, "claude-json-foreign")
		fake := newFakeClaudeCLI(t, home)
		p := claudeTestParams(home)

		got, err := (claudeWriter{}).Remove(context.Background(), p)
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if got.Status != "unchanged" {
			t.Fatalf("Status = %q, want %q", got.Status, "unchanged")
		}
		if got.Detail != "foreign ajent entry preserved" {
			t.Fatalf("Detail = %q, want %q", got.Detail, "foreign ajent entry preserved")
		}
		if call := fake.findCall("mcp", "remove"); call != nil {
			t.Fatalf("unexpected mcp remove call: %v", call)
		}
	})

	t.Run("no entry is a no-op", func(t *testing.T) {
		home := claudeFixtureHome(t, "claude-json-absent")
		fake := newFakeClaudeCLI(t, home)
		p := claudeTestParams(home)

		got, err := (claudeWriter{}).Remove(context.Background(), p)
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if got.Status != "unchanged" {
			t.Fatalf("Status = %q, want %q", got.Status, "unchanged")
		}
		if call := fake.findCall("mcp", "remove"); call != nil {
			t.Fatalf("unexpected mcp remove call: %v", call)
		}
	})
}
