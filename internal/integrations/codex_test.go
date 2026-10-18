// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCodexCLI is a fake `codex` CLI for tests: it stores at most one
// "ajent" entry in memory, answers `mcp get ajent --json` the way the real
// CLI does, and records every argv it receives so tests can assert on the
// exact `mcp add`/`mcp remove` invocations.
type fakeCodexCLI struct {
	present bool
	entry   map[string]any // command, args ([]any), optional env (map[string]any)

	getRaw []byte // when set, overrides the "mcp get" response body (still a success)
	getErr error  // when set, "mcp get" fails with this error

	addErr     error
	removeErr  error
	failOutput []byte // returned alongside a forced error; must never leak into a Result.Detail

	calls [][]string // every argv this CLI received, recorded in call order
}

func (f *fakeCodexCLI) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "codex" {
		return nil, fmt.Errorf("fakeCodexCLI: unexpected command %q", name)
	}
	recorded := append([]string{}, args...)
	f.calls = append(f.calls, recorded)

	switch {
	case len(args) >= 3 && args[0] == "mcp" && args[1] == "get" && args[2] == "ajent":
		if f.getErr != nil {
			return f.failOutput, f.getErr
		}
		if f.getRaw != nil {
			return f.getRaw, nil
		}
		if !f.present {
			return nil, errors.New("fakeCodexCLI: no such server: ajent")
		}
		return f.marshalGetResponse()
	case len(args) >= 2 && args[0] == "mcp" && args[1] == "add":
		if f.addErr != nil {
			return f.failOutput, f.addErr
		}
		f.entry = parseCodexAddArgv(args)
		f.present = true
		return nil, nil
	case len(args) >= 3 && args[0] == "mcp" && args[1] == "remove" && args[2] == "ajent":
		if f.removeErr != nil {
			return f.failOutput, f.removeErr
		}
		f.present = false
		f.entry = nil
		return nil, nil
	default:
		return nil, fmt.Errorf("fakeCodexCLI: unexpected args %v", args)
	}
}

func (f *fakeCodexCLI) marshalGetResponse() ([]byte, error) {
	transport := map[string]any{
		"type":    "stdio",
		"command": f.entry["command"],
		"args":    f.entry["args"],
	}
	if env, ok := f.entry["env"]; ok {
		transport["env"] = env
	}
	body, err := json.Marshal(map[string]any{"name": "ajent", "transport": transport})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (f *fakeCodexCLI) callsMatching(prefix ...string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) < len(prefix) {
			continue
		}
		match := true
		for i, p := range prefix {
			if c[i] != p {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeCodexCLI) addCalls() [][]string    { return f.callsMatching("mcp", "add") }
func (f *fakeCodexCLI) removeCalls() [][]string { return f.callsMatching("mcp", "remove", "ajent") }

// parseCodexAddArgv reverses codexAddArgv: ["mcp","add", ("--env","K=V")*,
// "ajent","--",command,args...] back into an entry map.
func parseCodexAddArgv(args []string) map[string]any {
	i := 2
	env := map[string]any{}
	for i+1 < len(args) && args[i] == "--env" {
		kv := strings.SplitN(args[i+1], "=", 2)
		env[kv[0]] = kv[1]
		i += 2
	}
	i++ // "ajent"
	i++ // "--"
	command := args[i]
	i++
	rest := args[i:]
	restAny := make([]any, len(rest))
	for j, r := range rest {
		restAny[j] = r
	}
	entry := map[string]any{"command": command, "args": restAny}
	if len(env) > 0 {
		entry["env"] = env
	}
	return entry
}

// withFakeCodexRun swaps the package-level Run seam for cli.run for the
// duration of the test.
func withFakeCodexRun(t *testing.T, cli *fakeCodexCLI) {
	t.Helper()
	prev := Run
	Run = cli.run
	t.Cleanup(func() { Run = prev })
}

func codexTestParams(t *testing.T) Params {
	t.Helper()
	home := filepath.FromSlash("/HOME")
	return Params{
		Home:      home,
		Binary:    "/opt/ajent/bin/ajent",
		ConfigDir: filepath.Join(home, ".aj"),
	}
}

func TestCodexTarget(t *testing.T) {
	got := codexWriter{}.Target()
	want := Target{Name: "codex", Profile: "codex"}
	if got != want {
		t.Fatalf("Target() = %+v, want %+v", got, want)
	}
}

func TestCodexPaths(t *testing.T) {
	p := codexTestParams(t)
	got := codexWriter{}.Paths(p)
	want := []string{filepath.Join(p.Home, ".codex", "config.toml")}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}
}

func TestCodexDetect(t *testing.T) {
	var w codexWriter

	t.Run("found", func(t *testing.T) {
		prev := LookPath
		LookPath = func(string) (string, error) { return "/usr/local/bin/codex", nil }
		t.Cleanup(func() { LookPath = prev })
		if !w.Detect(codexTestParams(t)) {
			t.Fatal("Detect() = false, want true")
		}
	})

	t.Run("absent", func(t *testing.T) {
		prev := LookPath
		LookPath = func(string) (string, error) { return "", errors.New("not found") }
		t.Cleanup(func() { LookPath = prev })
		if w.Detect(codexTestParams(t)) {
			t.Fatal("Detect() = true, want false")
		}
	})
}

func TestCodexAddArgv(t *testing.T) {
	cli := &fakeCodexCLI{}
	withFakeCodexRun(t, cli)

	p := codexTestParams(t)
	res, err := codexWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", res.Status)
	}

	calls := cli.addCalls()
	if len(calls) != 1 {
		t.Fatalf("add calls = %d, want 1: %v", len(calls), cli.calls)
	}
	got := strings.Join(append([]string{"codex"}, calls[0]...), " ")
	want := "codex mcp add ajent -- /opt/ajent/bin/ajent mcp --profile codex --project-profile"
	if got != want {
		t.Fatalf("add argv = %q, want %q", got, want)
	}
}

func TestCodexAddArgvWithEnv(t *testing.T) {
	cli := &fakeCodexCLI{}
	withFakeCodexRun(t, cli)

	home := filepath.FromSlash("/HOME")
	p := Params{
		Home:           home,
		Binary:         "/opt/ajent/bin/ajent",
		ConfigDir:      "/x/c",
		CredentialFile: "/x/c/cred.json",
	}

	res, err := codexWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", res.Status)
	}

	calls := cli.addCalls()
	if len(calls) != 1 {
		t.Fatalf("add calls = %d, want 1: %v", len(calls), cli.calls)
	}
	got := strings.Join(calls[0], " ")
	if !strings.Contains(got, "--env AJENT_CONFIG_DIR=/x/c --env AJENT_CREDENTIAL_FILE=/x/c/cred.json ajent") {
		t.Fatalf("add argv = %q, want env flags in sorted order before ajent", got)
	}
}

func TestCodexUnchangedSecondRun(t *testing.T) {
	cli := &fakeCodexCLI{}
	withFakeCodexRun(t, cli)
	p := codexTestParams(t)
	w := codexWriter{}

	first, err := w.Apply(context.Background(), p)
	if err != nil || first.Status != "configured" {
		t.Fatalf("first Apply() = %+v, err = %v, want configured", first, err)
	}

	second, err := w.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if second.Status != "unchanged" {
		t.Fatalf("second Apply() status = %q, want unchanged", second.Status)
	}
	if len(cli.addCalls()) != 1 {
		t.Fatalf("add calls after second run = %d, want 1 (no re-add)", len(cli.addCalls()))
	}
	if len(cli.removeCalls()) != 0 {
		t.Fatalf("remove calls after second run = %d, want 0", len(cli.removeCalls()))
	}
}

func TestCodexReplacesPythonEntry(t *testing.T) {
	home := filepath.FromSlash("/HOME")
	cli := &fakeCodexCLI{
		present: true,
		entry: map[string]any{
			"command": "python3",
			"args":    []any{filepath.Join(home, ".aj", "client.py"), "mcp", "--profile", "codex", "--project-profile"},
		},
	}
	withFakeCodexRun(t, cli)

	p := codexTestParams(t)
	res, err := codexWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.Status != "configured" {
		t.Fatalf("Apply() status = %q, want configured", res.Status)
	}

	removeCalls := cli.removeCalls()
	addCalls := cli.addCalls()
	if len(removeCalls) != 1 {
		t.Fatalf("remove calls = %d, want 1: %v", len(removeCalls), cli.calls)
	}
	if len(addCalls) != 1 {
		t.Fatalf("add calls = %d, want 1: %v", len(addCalls), cli.calls)
	}

	removeIdx, addIdx := -1, -1
	for i, c := range cli.calls {
		if len(c) >= 3 && c[0] == "mcp" && c[1] == "remove" && c[2] == "ajent" {
			removeIdx = i
		}
		if len(c) >= 2 && c[0] == "mcp" && c[1] == "add" {
			addIdx = i
		}
	}
	if removeIdx < 0 || addIdx < 0 || removeIdx > addIdx {
		t.Fatalf("expected remove before add, calls = %v", cli.calls)
	}

	if !cli.present {
		t.Fatal("expected an entry to be present after replacing the Python entry")
	}
	if !Owned(cli.entry) {
		t.Fatalf("final entry = %v, want an ajent-owned entry", cli.entry)
	}
}

func TestCodexForeignPreserved(t *testing.T) {
	cli := &fakeCodexCLI{
		present: true,
		entry: map[string]any{
			"command": "npx",
			"args":    []any{"-y", "@modelcontextprotocol/server-foreign"},
		},
	}
	withFakeCodexRun(t, cli)

	p := codexTestParams(t)
	res, err := codexWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", res.Status)
	}
	if res.Detail != "an existing ajent entry with other settings was preserved" {
		t.Fatalf("Apply() detail = %q", res.Detail)
	}
	if len(cli.addCalls()) != 0 || len(cli.removeCalls()) != 0 {
		t.Fatalf("expected no mutating calls, got %v", cli.calls)
	}
	if cli.entry["command"] != "npx" {
		t.Fatalf("foreign entry was mutated: %v", cli.entry)
	}
}

func TestCodexUnparseableGetIsForeign(t *testing.T) {
	cli := &fakeCodexCLI{getRaw: []byte("not json")}
	withFakeCodexRun(t, cli)

	p := codexTestParams(t)
	res, err := codexWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", res.Status)
	}
	if len(cli.addCalls()) != 0 || len(cli.removeCalls()) != 0 {
		t.Fatalf("expected no mutating calls, got %v", cli.calls)
	}
}

func TestCodexCLIFailureNoOutputLeak(t *testing.T) {
	cli := &fakeCodexCLI{
		addErr:     errors.New("codex mcp add failed"),
		failOutput: []byte("super-secret-token-should-not-leak"),
	}
	withFakeCodexRun(t, cli)

	p := codexTestParams(t)
	res, err := codexWriter{}.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.Status != "incomplete" {
		t.Fatalf("Apply() status = %q, want incomplete", res.Status)
	}
	want := "setup incomplete; existing settings preserved, see " + filepath.Join(p.ConfigDir, "mcp-example.json")
	if res.Detail != want {
		t.Fatalf("Apply() detail = %q, want %q", res.Detail, want)
	}
	if strings.Contains(res.Detail, "super-secret-token-should-not-leak") {
		t.Fatalf("Apply() detail leaked Run output: %q", res.Detail)
	}
}

func TestCodexRemove(t *testing.T) {
	t.Run("owned", func(t *testing.T) {
		cli := &fakeCodexCLI{
			present: true,
			entry: map[string]any{
				"command": "ajent",
				"args":    []any{"mcp", "--profile", "codex", "--project-profile"},
			},
		}
		withFakeCodexRun(t, cli)

		res, err := codexWriter{}.Remove(context.Background(), codexTestParams(t))
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if res.Status != "removed" {
			t.Fatalf("Remove() status = %q, want removed", res.Status)
		}
		if cli.present {
			t.Fatal("entry still present after Remove()")
		}
	})

	t.Run("foreign", func(t *testing.T) {
		cli := &fakeCodexCLI{
			present: true,
			entry: map[string]any{
				"command": "npx",
				"args":    []any{"-y", "@modelcontextprotocol/server-foreign"},
			},
		}
		withFakeCodexRun(t, cli)

		res, err := codexWriter{}.Remove(context.Background(), codexTestParams(t))
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if res.Status != "unchanged" || res.Detail != "foreign ajent entry preserved" {
			t.Fatalf("Remove() = %+v, want unchanged / foreign ajent entry preserved", res)
		}
		if !cli.present {
			t.Fatal("foreign entry was removed")
		}
	})

	t.Run("absent", func(t *testing.T) {
		cli := &fakeCodexCLI{}
		withFakeCodexRun(t, cli)

		res, err := codexWriter{}.Remove(context.Background(), codexTestParams(t))
		if err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if res.Status != "unchanged" || res.Detail != "" {
			t.Fatalf("Remove() = %+v, want unchanged with no detail", res)
		}
	})
}
