// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/config"
	"github.com/ajent-social/ajent/internal/testutil/fakerelease"
	"github.com/ajent-social/ajent/internal/update"
)

// upgradeInstallRecord is the subset of install.json (written by the
// install scripts, T5.9) that internal/update.Detect reads to classify a
// script install.
type upgradeInstallRecord struct {
	Method      string `json:"method"`
	Version     string `json:"version"`
	Path        string `json:"path"`
	InstalledAt string `json:"installed_at"`
}

// upgradeCredentialFixture is the root credential this test installs
// before the run starts, so it can assert its bytes never change across
// every phase (docs/tasks/T6.8.md step 2.4).
type upgradeCredentialFixture struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	Handle string `json:"handle"`
}

// TestUpgrade is a black-box end-to-end test of the self-update path
// (docs/adr/005-upgrade-strategy.md, docs/tasks/T6.8.md). It builds three
// real ajent binaries (0.7.0, 0.7.1 and 0.7.2), installs 0.7.0 as an
// isolated script install with fake harness configuration, then drives
// "ajent hook session-start" against a local fakerelease server to prove:
//
//   - the detached automatic update replaces the binary with the newest
//     verified stable release (phase A);
//   - the post-upgrade sync runs exactly once per version and a
//     harness's stale entry becomes the stable binary path (phase B);
//   - the resynced Cursor entry still launches a working "ajent mcp"
//     (phase C);
//   - a tampered next release is refused and the binary stays put
//     (phase D);
//   - AJENT_AUTO_UPDATE=0 opts a script install back out (phase E).
//
// Every binary, config file and "install" in this test lives under
// t.TempDir(): HOME, USERPROFILE, AJENT_CONFIG_DIR and the install
// directory are all test-owned temporary directories, and the release
// server is an httptest server on loopback (internal/testutil/fakerelease,
// T6.2). Nothing here ever touches a real $HOME, a real harness config or
// the host's actual ajent installation.
func TestUpgrade(t *testing.T) {
	root := upgradeModuleRoot(t)

	// ---- step 2.1: a throwaway Ed25519 key pair every built binary
	// trusts via -X .../internal/update.extraKeyB64=<pubB64>, and only
	// this test's fakerelease releases are ever signed with.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("upgrade: generating release key: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	// ---- step 2.2: build the N-1, N and N+1 binaries into t.TempDir().
	buildDir := t.TempDir()
	bin070 := filepath.Join(buildDir, upgradeExeName("ajent-0.7.0"))
	bin071 := filepath.Join(buildDir, upgradeExeName("ajent-0.7.1"))
	bin072 := filepath.Join(buildDir, upgradeExeName("ajent-0.7.2"))
	upgradeBuild(t, root, bin070, "0.7.0", pubB64)
	upgradeBuild(t, root, bin071, "0.7.1", pubB64)
	upgradeBuild(t, root, bin072, "0.7.2", pubB64)

	bin071Bytes, err := os.ReadFile(bin071) //nolint:gosec // G304: bin071 is the path this test just built into its own t.TempDir().
	if err != nil {
		t.Fatalf("upgrade: reading built 0.7.1 binary: %v", err)
	}
	bin072Bytes, err := os.ReadFile(bin072) //nolint:gosec // G304: bin072 is the path this test just built into its own t.TempDir().
	if err != nil {
		t.Fatalf("upgrade: reading built 0.7.2 binary: %v", err)
	}

	// ---- step 2.3: the fake release server, and the first (genuine)
	// stable release, 0.7.1.
	fr := fakerelease.New(t)
	fr.AddRelease("0.7.1", false, fakerelease.BuildAssets(t, "0.7.1", runtime.GOOS, runtime.GOARCH, bin071Bytes, priv))

	// ---- step 2.4: the isolated "installation".
	home := t.TempDir()
	cfg := filepath.Join(home, ".aj")
	binDir := filepath.Join(home, ".local", "bin")
	bin := filepath.Join(binDir, upgradeExeName("ajent"))
	logPath := filepath.Join(cfg, "update.log")

	// t.Cleanup runs its registered funcs in LIFO order. home's own
	// removal was already queued by the t.TempDir() call above, so
	// registering this cleanup now makes it run first, before that
	// removal, giving a still-running detached child process (there is no
	// PID tracking for it; see upgradeWaitQuiet) a chance to finish
	// writing its log and exit before home is torn down.
	t.Cleanup(func() {
		upgradeWaitQuiet(logPath, time.Second, 5*time.Second)
		upgradeCleanupRetryRemove(bin)
		upgradeCleanupRetryRemove(bin + ".old")
	})

	if err := os.MkdirAll(binDir, 0o755); err != nil { //nolint:gosec // G301: binDir mirrors a real ~/.local/bin, which is world-traversable on every supported OS.
		t.Fatalf("upgrade: creating %s: %v", binDir, err)
	}
	upgradeCopyFile(t, bin070, bin, 0o755)

	if err := config.EnsurePrivateDir(cfg); err != nil {
		t.Fatalf("upgrade: creating %s: %v", cfg, err)
	}

	installRecord := upgradeInstallRecord{
		Method:      "script",
		Version:     "0.7.0",
		Path:        bin,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := config.WriteJSON(filepath.Join(cfg, "install.json"), installRecord); err != nil {
		t.Fatalf("upgrade: writing install.json: %v", err)
	}

	credFixture := upgradeCredentialFixture{
		Server: "http://127.0.0.1:1",
		Token:  "aj_0000000000000000_test", //nolint:gosec // G101: a fixture credential for an isolated t.TempDir() sandbox, never a real secret.
		Handle: "e2e",
	}
	credBytes, err := json.Marshal(credFixture)
	if err != nil {
		t.Fatalf("upgrade: marshaling the fixture credential: %v", err)
	}
	credPath := filepath.Join(cfg, "credential.json")
	if err := config.WritePrivate(credPath, credBytes); err != nil {
		t.Fatalf("upgrade: writing credential.json: %v", err)
	}

	cursorDir := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil { //nolint:gosec // G301: cursorDir mirrors a real ~/.cursor, which is world-traversable on every supported OS.
		t.Fatalf("upgrade: creating %s: %v", cursorDir, err)
	}
	cursorConfigPath := filepath.Join(cursorDir, "mcp.json")
	staleCursorJSON := `{"mcpServers":{"other":{"command":"x"},"ajent":{"command":"` +
		filepath.ToSlash(filepath.Join(home, "old", "ajent")) +
		`","args":["mcp","--profile","cursor","--project-profile"]}}}`
	if err := os.WriteFile(cursorConfigPath, []byte(staleCursorJSON), 0o644); err != nil { //nolint:gosec // G306: mcp.json is an ordinary harness config file, not a secret.
		t.Fatalf("upgrade: writing %s: %v", cursorConfigPath, err)
	}
	otherEntryBefore := upgradeReadCursorConfig(t, cursorConfigPath).MCPServers["other"]

	// integration-status.json existing is what makes needsPostUpgrade
	// (cmd/ajent/hook_postupgrade.go, T6.4) consider this project already
	// set up, so post-upgrade sync applies (docs/tasks/T6.4.md step 1).
	if err := config.WritePrivate(filepath.Join(cfg, "integration-status.json"), []byte("{}")); err != nil {
		t.Fatalf("upgrade: writing integration-status.json: %v", err)
	}

	// ---- step 2.5: the clean environment every exec in this test uses.
	baseEnv := upgradeEnv(home, cfg, binDir, fr.API(), fr.FallbackURL())

	// ==== Phase A: the detached auto-update (docs/tasks/T6.8.md step 3).
	if out, code := upgradeRun(t, home, baseEnv, bin, "hook", "session-start"); code != 0 {
		t.Fatalf("upgrade: phase A: %s hook session-start exited %d:\n%s", bin, code, out)
	}

	upgradePollVersion(t, home, baseEnv, bin, "0.7.1", 60*time.Second)
	upgradePollFileContains(t, logPath, "auto-run: updated 0.7.0 -> 0.7.1", 60*time.Second)

	credAfterA, err := os.ReadFile(credPath) //nolint:gosec // G304: credPath is this test's own fixture file under its t.TempDir() sandbox.
	if err != nil {
		t.Fatalf("upgrade: phase A: reading credential.json: %v", err)
	}
	if string(credAfterA) != string(credBytes) {
		t.Errorf("upgrade: phase A: credential.json changed:\nbefore: %s\nafter:  %s", credBytes, credAfterA)
	}

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(bin + ".old"); err == nil {
			t.Logf("upgrade: phase A: %s.old still exists; expecting cleanup on the next command run (docs/tasks/T6.8.md step 8)", bin)
		}
	}

	// ==== Phase B: post-upgrade sync (docs/tasks/T6.8.md step 4).
	if out, code := upgradeRun(t, home, baseEnv, bin, "hook", "session-start"); code != 0 {
		t.Fatalf("upgrade: phase B: %s hook session-start exited %d:\n%s", bin, code, out)
	}

	upgradePollUntil(t, 60*time.Second, 500*time.Millisecond,
		"state.json seen_version = 0.7.1 and the Cursor ajent entry command = "+bin,
		func() bool {
			st, err := update.LoadState(cfg)
			if err != nil || st.SeenVersion != "0.7.1" {
				return false
			}
			ajentEntry, ok := upgradeReadCursorConfig(t, cursorConfigPath).MCPServers["ajent"]
			if !ok {
				return false
			}
			command, _ := ajentEntry["command"].(string)
			return upgradeSamePath(command, bin)
		},
	)

	otherEntryAfter := upgradeReadCursorConfig(t, cursorConfigPath).MCPServers["other"]
	otherBefore, err := json.Marshal(otherEntryBefore)
	if err != nil {
		t.Fatalf("upgrade: marshaling the pre-sync \"other\" entry: %v", err)
	}
	otherAfter, err := json.Marshal(otherEntryAfter)
	if err != nil {
		t.Fatalf("upgrade: marshaling the post-sync \"other\" entry: %v", err)
	}
	if string(otherBefore) != string(otherAfter) {
		t.Errorf("upgrade: phase B: the \"other\" Cursor server entry changed:\nbefore: %s\nafter:  %s", otherBefore, otherAfter)
	}

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(bin + ".old"); err == nil {
			t.Errorf("upgrade: phase B: %s.old still exists after a second command run", bin)
		}
	}

	backupsAfterB := upgradeBackupIDs(t, cfg)

	// A third hook run: the sync already ran for this version, so it must
	// not run again (docs/tasks/T6.8.md step 4.4).
	if out, code := upgradeRun(t, home, baseEnv, bin, "hook", "session-start"); code != 0 {
		t.Fatalf("upgrade: phase B (third run): %s hook session-start exited %d:\n%s", bin, code, out)
	}
	// Give a wrongly-still-spawning post-upgrade sync a moment to run
	// before asserting it did not.
	time.Sleep(2 * time.Second)
	backupsAfterThird := upgradeBackupIDs(t, cfg)
	if len(backupsAfterThird) != len(backupsAfterB) {
		t.Errorf("upgrade: phase B: a third hook run created a new backup (had %v, now %v); the sync should run exactly once per version",
			backupsAfterB, backupsAfterThird)
	}

	// ==== Phase C: the resynced harness entry still launches a working
	// "ajent mcp" (docs/tasks/T6.8.md step 5).
	ajentEntry, ok := upgradeReadCursorConfig(t, cursorConfigPath).MCPServers["ajent"]
	if !ok {
		t.Fatal("upgrade: phase C: the Cursor config has no \"ajent\" server entry")
	}
	command, _ := ajentEntry["command"].(string)
	rawArgs, _ := ajentEntry["args"].([]any)
	args := make([]string, len(rawArgs))
	for i, a := range rawArgs {
		args[i], _ = a.(string)
	}

	initResult := upgradeMCPInitialize(t, home, baseEnv, command, args)
	serverInfo, _ := initResult["serverInfo"].(map[string]any)
	if name, _ := serverInfo["name"].(string); name != "ajent" {
		t.Errorf("upgrade: phase C: serverInfo.name = %q, want \"ajent\" (result: %#v)", name, initResult)
	}
	if version, _ := serverInfo["version"].(string); version != "0.7.1" {
		t.Errorf("upgrade: phase C: serverInfo.version = %q, want \"0.7.1\" (result: %#v)", version, initResult)
	}

	// ==== Phase D: a tampered N+1 release is refused (docs/tasks/T6.8.md
	// step 6).
	assets072 := fakerelease.BuildAssets(t, "0.7.2", runtime.GOOS, runtime.GOARCH, bin072Bytes, priv)
	_, badPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("upgrade: phase D: generating a different signing key: %v", err)
	}
	badSig := ed25519.Sign(badPriv, assets072["checksums.txt"])
	assets072["checksums.txt.sig"] = []byte(base64.StdEncoding.EncodeToString(badSig) + "\n")
	fr.AddRelease("0.7.2", false, assets072)

	upgradeResetLastAutoUpdate(t, cfg)

	if out, code := upgradeRun(t, home, baseEnv, bin, "hook", "session-start"); code != 0 {
		t.Fatalf("upgrade: phase D: %s hook session-start exited %d:\n%s", bin, code, out)
	}
	upgradePollFileContains(t, logPath, "auto-run: failed", 30*time.Second)

	if got := upgradeBinVersion(t, home, baseEnv, bin); got != "0.7.1" {
		t.Errorf("upgrade: phase D: %s version --json = %q after a tampered 0.7.2 release, want \"0.7.1\"", bin, got)
	}

	out, code := upgradeRun(t, home, baseEnv, bin, "update")
	if code != 1 {
		t.Errorf("upgrade: phase D: %s update exited %d, want 1; output:\n%s", bin, code, out)
	}
	if !upgradeContainsFold(out, "signature") {
		t.Errorf("upgrade: phase D: %s update output does not mention \"signature\":\n%s", bin, out)
	}

	// ==== Phase E: AJENT_AUTO_UPDATE=0 opts back out (docs/tasks/T6.8.md
	// step 7).
	assets073 := fakerelease.BuildAssets(t, "0.7.3", runtime.GOOS, runtime.GOARCH, bin071Bytes, priv)
	fr.AddRelease("0.7.3", false, assets073)
	upgradeResetLastAutoUpdate(t, cfg)

	logBeforeE := string(upgradeReadFile(logPath))
	optOutEnv := upgradeWithEnv(baseEnv, "AJENT_AUTO_UPDATE=0")
	if out, code := upgradeRun(t, home, optOutEnv, bin, "hook", "session-start"); code != 0 {
		t.Fatalf("upgrade: phase E: %s hook session-start exited %d:\n%s", bin, code, out)
	}

	time.Sleep(10 * time.Second)

	if got := upgradeBinVersion(t, home, baseEnv, bin); got != "0.7.1" {
		t.Errorf("upgrade: phase E: %s version --json = %q with AJENT_AUTO_UPDATE=0 set, want \"0.7.1\"", bin, got)
	}
	logAfterE := string(upgradeReadFile(logPath))
	newLog := logAfterE
	if len(logBeforeE) <= len(logAfterE) && logAfterE[:len(logBeforeE)] == logBeforeE {
		newLog = logAfterE[len(logBeforeE):]
	}
	if upgradeContainsFold(newLog, "auto-run") {
		t.Errorf("upgrade: phase E: update.log gained an \"auto-run\" line despite AJENT_AUTO_UPDATE=0:\n%s", newLog)
	}
}

// upgradeCopyFile copies src to dst with the given file mode, failing the
// test on any error.
func upgradeCopyFile(t *testing.T, src, dst string, perm os.FileMode) {
	t.Helper()

	data, err := os.ReadFile(src) //nolint:gosec // G304: src is always a path this test built or fixed up itself, under its own t.TempDir() sandboxes.
	if err != nil {
		t.Fatalf("upgrade: reading %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, perm); err != nil { //nolint:gosec // G306: dst mirrors a real installed ajent binary, which is world-executable.
		t.Fatalf("upgrade: writing %s: %v", dst, err)
	}
}

// upgradeResetLastAutoUpdate zeroes state.json's last_auto_update field
// under cfg, so the next hook run's 24-hour rate limit
// (cmd/ajent/hook_autoupdate.go, autoUpdateDue) is satisfied
// (docs/tasks/T6.8.md steps 6.2 and 7).
func upgradeResetLastAutoUpdate(t *testing.T, cfg string) {
	t.Helper()

	st, err := update.LoadState(cfg)
	if err != nil {
		t.Fatalf("upgrade: loading state.json: %v", err)
	}
	st.LastAutoUpdate = time.Time{}
	if err := update.SaveState(cfg, st); err != nil {
		t.Fatalf("upgrade: saving state.json: %v", err)
	}
}

// upgradeContainsFold reports whether s contains substr, ignoring case.
func upgradeContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
