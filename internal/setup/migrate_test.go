// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/integrations"
)

// migrateProfileTemplateName and migrateVSCodeTemplateName are template
// files at the fixture root (testdata/migration/python-home/), not copied
// directly to the same relative path under the test's fake home: their
// destination depends on values only the test knows at copy time (the root
// credential's Identity(), and VS Code's per-OS user directory), per
// docs/tasks/T4.9.md step 6.
const (
	migrateProfileTemplateName = "profile-cursor.json.tmpl"
	migrateVSCodeTemplateName  = "vscode-mcp.json.tmpl"
)

// migratePythonHomeFixture returns the path to
// testdata/migration/python-home/<elem...>, relative to this package
// (docs/tasks/T4.9.md step 6).
func migratePythonHomeFixture(elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata", "migration", "python-home"}, elem...)...)
}

// migrateTestEnv builds a cli.Env for migrate tests: Home is a fresh
// t.TempDir() that migrateSetupFixture later seeds with the python-home
// fixture, ConfigDir is Home's default ".aj" subdirectory (the layout
// DetectPython, the fixture and EntryEnv's "default directory" rule all
// assume), and Cwd is a separate empty temp directory.
func migrateTestEnv(t *testing.T) (env *cli.Env, stdout, stderr *bytes.Buffer, home string) {
	t.Helper()

	home = t.TempDir()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	env = &cli.Env{
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		Getenv:     os.Getenv,
		Cwd:        t.TempDir(),
		Home:       home,
		ConfigDir:  filepath.Join(home, ".aj"),
		API:        &api.Client{},
		IsTerminal: func(uintptr) bool { return false },
		OpenTTY: func() (io.ReadWriteCloser, error) {
			return nil, errors.New("migrate test: no controlling terminal")
		},
		Now: time.Now,
	}
	return env, stdout, stderr, home
}

// migrateStubBinary pins integrations.BinaryOverride to an obviously fake
// path and fails integrations.LookPath for the duration of the test, so
// the claude and codex writers (which detect through LookPath) never probe
// the real PATH, and every writer that does apply launches a fixed binary
// (docs/tasks/T4.9.md step 7). Both package variables are restored on
// cleanup.
func migrateStubBinary(t *testing.T) {
	t.Helper()

	prevBinary := integrations.BinaryOverride
	integrations.BinaryOverride = "/opt/ajent/bin/ajent"
	t.Cleanup(func() { integrations.BinaryOverride = prevBinary })

	prevLookPath := integrations.LookPath
	integrations.LookPath = func(string) (string, error) {
		return "", fmt.Errorf("migrate test: %w", fs.ErrNotExist)
	}
	t.Cleanup(func() { integrations.LookPath = prevLookPath })
}

// migrateSubstitute replaces "{{HOME}}" with home and "{{SEP}}" with the
// OS path separator in data (docs/tasks/T4.9.md step 6).
func migrateSubstitute(data []byte, home string) []byte {
	// Placeholders occur inside JSON string literals, so paths must be escaped.
	homeJSON, _ := json.Marshal(home)
	separatorJSON, _ := json.Marshal(string(filepath.Separator))
	s := strings.ReplaceAll(string(data), "{{HOME}}", string(homeJSON[1:len(homeJSON)-1]))
	s = strings.ReplaceAll(s, "{{SEP}}", string(separatorJSON[1:len(separatorJSON)-1]))
	return []byte(s)
}

// migrateInstallPythonHome copies testdata/migration/python-home into
// home, substituting {{HOME}} and {{SEP}} in every file's contents
// (docs/tasks/T4.9.md step 6). The two root-level *.tmpl templates
// (migrateProfileTemplateName, migrateVSCodeTemplateName) are not written
// to their literal relative path; their substituted content is returned so
// the caller can place them at their identity- and OS-dependent
// destinations.
func migrateInstallPythonHome(t *testing.T, home string) (profileTemplate, vscodeTemplate []byte) {
	t.Helper()

	src := migratePythonHomeFixture()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(home, rel), 0o700)
		}

		data, err := os.ReadFile(path) //nolint:gosec // G304: path is produced by WalkDir over the fixture tree under testdata.
		if err != nil {
			return err
		}
		data = migrateSubstitute(data, home)

		switch rel {
		case migrateProfileTemplateName:
			profileTemplate = data
			return nil
		case migrateVSCodeTemplateName:
			vscodeTemplate = data
			return nil
		}

		target := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("migrateInstallPythonHome(%s): %v", src, err)
	}
	if profileTemplate == nil {
		t.Fatalf("fixture missing %s", migrateProfileTemplateName)
	}
	if vscodeTemplate == nil {
		t.Fatalf("fixture missing %s", migrateVSCodeTemplateName)
	}
	return profileTemplate, vscodeTemplate
}

// migratePlaceProfile writes template at
// <home>/.aj/profiles/<identity>/cursor-deadbeef.json, where identity is
// computed from the root credential's token with Credential.Identity()
// (docs/tasks/T4.9.md step 6).
func migratePlaceProfile(t *testing.T, home string, template []byte) {
	t.Helper()

	configDir := filepath.Join(home, ".aj")
	root, err := credentials.LoadRoot(configDir)
	if err != nil {
		t.Fatalf("LoadRoot for profile placement: %v", err)
	}
	dir := credentials.ProfileDir(configDir, root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cursor-deadbeef.json"), template, 0o600); err != nil {
		t.Fatalf("write profile fixture: %v", err)
	}
}

// migrateVSCodeUserDir returns VS Code's per-OS user settings directory
// under home, mirroring the unexported vscodeWriter.userDir
// (internal/integrations/vscode.go) so this test's fixture lands exactly
// where that writer looks. getenv supplies APPDATA (windows) or
// XDG_CONFIG_HOME (elsewhere), matching Params.Getenv.
func migrateVSCodeUserDir(home string, getenv func(string) string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	case "windows":
		appdata := getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appdata, "Code", "User")
	default:
		base := getenv("XDG_CONFIG_HOME")
		if base == "" || !strings.HasPrefix(base, "/") {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "Code", "User")
	}
}

// migratePlaceVSCode writes template at the per-OS VS Code user mcp.json
// path under home (docs/tasks/T4.9.md step 6).
func migratePlaceVSCode(t *testing.T, home string, template []byte, getenv func(string) string) {
	t.Helper()

	dir := migrateVSCodeUserDir(home, getenv)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir vscode user dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), template, 0o600); err != nil {
		t.Fatalf("write vscode fixture: %v", err)
	}
}

// migrateSetupFixture builds a migrate test env with the whole
// python-home fixture installed: client.py, credential.json, a completed
// cursor tool profile, Cursor's mcp.json, VS Code's user mcp.json (at the
// current OS's real per-OS path) and the Python-era shared skill. It also
// stubs the binary path and LookPath (migrateStubBinary) so Sync's real
// registered writers behave hermetically.
func migrateSetupFixture(t *testing.T) (env *cli.Env, stdout, stderr *bytes.Buffer, home string) {
	t.Helper()

	migrateStubBinary(t)
	env, stdout, stderr, home = migrateTestEnv(t)
	profileTemplate, vscodeTemplate := migrateInstallPythonHome(t, home)
	migratePlaceProfile(t, home, profileTemplate)
	migratePlaceVSCode(t, home, vscodeTemplate, env.Getenv)
	return env, stdout, stderr, home
}

// migrateHashTree returns a deterministic, sorted-by-walk-order listing of
// every regular file under root and its SHA-256 digest, so two calls can
// be compared to prove a directory tree was not touched.
func migrateHashTree(t *testing.T, root string) string {
	t.Helper()

	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is produced by WalkDir over the test's own t.TempDir() tree.
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s %x\n", filepath.ToSlash(rel), sum)
		return nil
	})
	if err != nil {
		t.Fatalf("migrateHashTree(%s): %v", root, err)
	}
	return b.String()
}

// migrateReadEntry decodes path as a { "<section>": { "ajent": {...} } }
// JSON document and returns the "ajent" entry, e.g. mcpServers.ajent or
// servers.ajent.
func migrateReadEntry(t *testing.T, path, section string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test-built path under t.TempDir().
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	raw, ok := doc[section]["ajent"]
	if !ok {
		t.Fatalf("%s: %s.ajent missing", path, section)
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("unmarshal %s.ajent: %v", section, err)
	}
	return entry
}

func TestDetectPython(t *testing.T) {
	dir := t.TempDir()
	if DetectPython(dir) {
		t.Fatal("DetectPython() = true before client.py exists")
	}

	if err := os.WriteFile(filepath.Join(dir, "client.py"), []byte("# fake"), 0o600); err != nil {
		t.Fatalf("write client.py: %v", err)
	}
	if !DetectPython(dir) {
		t.Fatal("DetectPython() = false after client.py exists")
	}

	dirWithDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirWithDir, "client.py"), 0o700); err != nil {
		t.Fatalf("mkdir client.py: %v", err)
	}
	if DetectPython(dirWithDir) {
		t.Fatal("DetectPython() = true for a directory named client.py")
	}
}

func TestPlanMigrationDryRunChangesNothing(t *testing.T) {
	env, _, _, home := migrateSetupFixture(t)

	before := migrateHashTree(t, home)

	plan, err := PlanMigration(context.Background(), env)
	if err != nil {
		t.Fatalf("PlanMigration: %v", err)
	}

	after := migrateHashTree(t, home)
	if before != after {
		t.Fatalf("PlanMigration changed the home tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	joined := strings.Join(plan.Changes, "\n")
	for _, want := range []string{"cursor", "vscode", "agentskills", "1 tool profiles"} {
		if !strings.Contains(joined, want) {
			t.Errorf("plan missing %q; got:\n%s", want, joined)
		}
	}
}

func TestMigrateRewritesAndPreservesIdentity(t *testing.T) {
	env, stdout, _, home := migrateSetupFixture(t)
	configDir := filepath.Join(home, ".aj")

	credBefore, err := os.ReadFile(filepath.Join(configDir, "credential.json"))
	if err != nil {
		t.Fatalf("read credential.json before Migrate: %v", err)
	}
	root, err := credentials.LoadRoot(configDir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	profilePath := filepath.Join(credentials.ProfileDir(configDir, root), "cursor-deadbeef.json")
	profileBefore, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile before Migrate: %v", err)
	}
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")

	backup, err := Migrate(context.Background(), env)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ajentEntry := migrateReadEntry(t, cursorPath, "mcpServers")
	if got, want := ajentEntry["command"], "/opt/ajent/bin/ajent"; got != want {
		t.Errorf("cursor ajent.command = %v, want %q", got, want)
	}
	if _, hasEnv := ajentEntry["env"]; hasEnv {
		t.Errorf("cursor ajent entry has an env key, want none (default config dir): %#v", ajentEntry)
	}

	data, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read cursor mcp.json: %v", err)
	}
	var doc struct {
		McpServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal cursor mcp.json: %v", err)
	}
	if got, want := doc.McpServers["github"].Command, "npx"; got != want {
		t.Errorf("github.command = %q, want %q (unchanged)", got, want)
	}

	credAfter, err := os.ReadFile(filepath.Join(configDir, "credential.json"))
	if err != nil {
		t.Fatalf("read credential.json after Migrate: %v", err)
	}
	if !bytes.Equal(credBefore, credAfter) {
		t.Errorf("credential.json changed:\nbefore:\n%s\nafter:\n%s", credBefore, credAfter)
	}
	profileAfter, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile after Migrate: %v", err)
	}
	if !bytes.Equal(profileBefore, profileAfter) {
		t.Errorf("profile file changed:\nbefore:\n%s\nafter:\n%s", profileBefore, profileAfter)
	}

	if _, err := os.Stat(filepath.Join(configDir, "client.py")); err != nil {
		t.Errorf("client.py missing after Migrate: %v", err)
	}

	skillData, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "ajent-network", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	if bytes.Contains(skillData, []byte("client.py")) {
		t.Errorf("skill file still mentions client.py after Migrate:\n%s", skillData)
	}

	migData, err := os.ReadFile(filepath.Join(configDir, "migration.json"))
	if err != nil {
		t.Fatalf("read migration.json: %v", err)
	}
	var mig map[string]any
	if err := json.Unmarshal(migData, &mig); err != nil {
		t.Fatalf("unmarshal migration.json: %v", err)
	}
	if mig["from"] != "python" {
		t.Errorf("migration.json from = %v, want \"python\"", mig["from"])
	}
	if mig["backup"] != backup.ID {
		t.Errorf("migration.json backup = %v, want %q", mig["backup"], backup.ID)
	}

	if !strings.Contains(stdout.String(), "Migration complete. Roll back with: ajent rollback "+backup.ID) {
		t.Errorf("stdout missing the completion line: %s", stdout.String())
	}
}

func TestMigrateRollbackRestoresPython(t *testing.T) {
	env, _, _, home := migrateSetupFixture(t)
	configDir := filepath.Join(home, ".aj")

	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	cursorBefore, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read cursor fixture: %v", err)
	}
	vscodePath := filepath.Join(migrateVSCodeUserDir(home, env.Getenv), "mcp.json")
	vscodeBefore, err := os.ReadFile(vscodePath)
	if err != nil {
		t.Fatalf("read vscode fixture: %v", err)
	}
	skillPath := filepath.Join(home, ".agents", "skills", "ajent-network", "SKILL.md")
	skillBefore, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill fixture: %v", err)
	}

	backup, err := Migrate(context.Background(), env)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := integrations.Rollback(configDir, backup.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	cursorAfter, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read cursor after rollback: %v", err)
	}
	if !bytes.Equal(cursorBefore, cursorAfter) {
		t.Errorf("cursor mcp.json not restored:\nbefore:\n%s\nafter:\n%s", cursorBefore, cursorAfter)
	}

	vscodeAfter, err := os.ReadFile(vscodePath)
	if err != nil {
		t.Fatalf("read vscode after rollback: %v", err)
	}
	if !bytes.Equal(vscodeBefore, vscodeAfter) {
		t.Errorf("vscode mcp.json not restored:\nbefore:\n%s\nafter:\n%s", vscodeBefore, vscodeAfter)
	}

	skillAfter, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill after rollback: %v", err)
	}
	if !bytes.Equal(skillBefore, skillAfter) {
		t.Errorf("skill file not restored:\nbefore:\n%s\nafter:\n%s", skillBefore, skillAfter)
	}
}

func TestMigrateForeignServerCredential(t *testing.T) {
	migrateStubBinary(t)
	env, _, _, home := migrateTestEnv(t)
	configDir := filepath.Join(home, ".aj")

	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "client.py"), []byte("# earlier client placeholder\n"), 0o600); err != nil {
		t.Fatalf("write client.py: %v", err)
	}
	foreign := credentials.Credential{
		Server: "https://example.invalid",
		Token:  "aj_deadbeefdeadbeef_foreignservertesttoken",
	}
	if err := credentials.CreateRoot(configDir, foreign); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	before := migrateHashTree(t, home)

	if _, err := Migrate(context.Background(), env); err == nil {
		t.Fatal("Migrate() error = nil, want the other-server error")
	} else if !errors.Is(err, errMigrateForeignServer) {
		t.Fatalf("Migrate() error = %v, want errMigrateForeignServer", err)
	}

	after := migrateHashTree(t, home)
	if before != after {
		t.Fatalf("Migrate changed files despite the other-server error:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestPreSetupOffersMigrationNonInteractive(t *testing.T) {
	env, stdout, _, home := migrateSetupFixture(t)
	env.IsTerminal = func(uintptr) bool { return false }

	before := migrateHashTree(t, home)

	if err := migrateOfferDuringSetup(context.Background(), env, Options{}); err != nil {
		t.Fatalf("migrateOfferDuringSetup: %v", err)
	}

	after := migrateHashTree(t, home)
	if before != after {
		t.Fatalf("non-interactive offer changed files:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	if !strings.Contains(stdout.String(), migrateHintLater) {
		t.Errorf("stdout missing the hint %q: %s", migrateHintLater, stdout.String())
	}

	if _, err := os.Stat(filepath.Join(home, ".aj", "migration.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("migration.json exists after a non-interactive offer (stat err = %v)", err)
	}
}

func TestMigrateSubstituteJSONPaths(t *testing.T) {
	for _, home := range []string{`C:\Users\fixture`, `home"quoted`, "home\nnewline"} {
		t.Run(home, func(t *testing.T) {
			data := migrateSubstitute([]byte(`{"path":"{{HOME}}{{SEP}}.aj"}`), home)
			var got struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("invalid fixture JSON: %v", err)
			}
			if want := home + string(filepath.Separator) + ".aj"; got.Path != want {
				t.Fatalf("path=%q, want %q", got.Path, want)
			}
		})
	}
}
