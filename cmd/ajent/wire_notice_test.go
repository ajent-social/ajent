// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/update"
)

// noticeTestNow is the fixed clock every wire_notice_test.go case measures
// relative to.
var noticeTestNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// noticeTestEnv builds a minimal *cli.Env for wireNotice, independent of
// the shared newTestEnv helper (cmd/ajent/testenv_test.go): wireNotice
// never resolves a credential or calls the API, so it needs no
// fakeserver, but it does read env.Getenv and env.IsTerminal, which
// newTestEnv fixes to host values and false respectively. Every field
// here starts from a safe, host-independent default; tests override what
// they need.
func noticeTestEnv(t *testing.T) (env *cli.Env, stderr *bytes.Buffer) {
	t.Helper()

	stderr = &bytes.Buffer{}
	env = &cli.Env{
		Stdout:     &bytes.Buffer{},
		Stderr:     stderr,
		Getenv:     func(string) string { return "" },
		Cwd:        t.TempDir(),
		Home:       t.TempDir(),
		ConfigDir:  t.TempDir(),
		IsTerminal: func(uintptr) bool { return false },
		Now:        func() time.Time { return noticeTestNow },
	}
	return env, stderr
}

// noticeTestSetArgs overrides the osArgs seam to args, restoring the
// original function on cleanup.
func noticeTestSetArgs(t *testing.T, args ...string) {
	t.Helper()
	original := osArgs
	osArgs = func() []string { return args }
	t.Cleanup(func() { osArgs = original })
}

// noticeTestSpy overrides the startDetached seam, recording every call and
// restoring the original function on cleanup.
func noticeTestSpy(t *testing.T) *[]string {
	t.Helper()
	original := startDetached
	var calls []string
	startDetached = func(executable string, _ []string, _ string) error {
		calls = append(calls, executable)
		return nil
	}
	t.Cleanup(func() { startDetached = original })
	return &calls
}

// TestNoticePrintedOnTTY proves wireNotice prints the update notice on
// env.Stderr, and records LastNotice, when stderr is a terminal and a
// newer release is cached (docs/tasks/T6.5.md step 3).
func TestNoticePrintedOnTTY(t *testing.T) {
	noticeTestSetArgs(t, "ajent", "search", "hello")
	noticeTestSpy(t)

	env, stderr := noticeTestEnv(t)
	env.IsTerminal = func(uintptr) bool { return true }
	cmdUpdateTestSetVersion(t, "0.7.0")

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	// LastCheck is already fresh, so this test exercises only the notice
	// path, not the background refresh (TestRefreshSpawnedOncePerDay
	// covers that).
	if err := update.SaveState(dir, update.State{Latest: "0.8.0", LastCheck: noticeTestNow}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	wireNotice(env)

	want := "A new ajent release is available: 0.7.0 -> 0.8.0. Run: ajent update\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}

	st, err := update.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !st.LastNotice.Equal(noticeTestNow) {
		t.Errorf("LastNotice = %v, want %v", st.LastNotice, noticeTestNow)
	}
}

// TestNoticeSkippedNonTTY proves wireNotice prints nothing when stderr is
// not a terminal, even though a newer release is cached
// (docs/tasks/T6.5.md step 3). This is also the genuine-red case for the
// IsTerminal check (docs/tasks/T6.5.md, "Acceptance criteria").
func TestNoticeSkippedNonTTY(t *testing.T) {
	noticeTestSetArgs(t, "ajent", "search", "hello")
	noticeTestSpy(t)

	env, stderr := noticeTestEnv(t) // IsTerminal defaults to false
	cmdUpdateTestSetVersion(t, "0.7.0")

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := update.SaveState(dir, update.State{Latest: "0.8.0", LastCheck: noticeTestNow}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	wireNotice(env)

	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

// TestNoticeSkippedForMCP proves wireNotice does nothing at all -- no
// notice and no background refresh -- when the running command is mcp
// (docs/tasks/T6.5.md step 3; the objective: "mcp ... never print it").
func TestNoticeSkippedForMCP(t *testing.T) {
	for _, command := range []string{"mcp", "watch"} {
		t.Run(command, func(t *testing.T) {
			testNoticeSkippedCommand(t, command)
		})
	}
}

func testNoticeSkippedCommand(t *testing.T, command string) {
	t.Helper()
	noticeTestSetArgs(t, "ajent", command)
	calls := noticeTestSpy(t)

	env, stderr := noticeTestEnv(t)
	env.IsTerminal = func(uintptr) bool { return true }
	cmdUpdateTestSetVersion(t, "0.7.0")

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	// Never checked and a newer release cached: both the notice and the
	// refresh would fire if the mcp guard did not short-circuit first.
	if err := update.SaveState(dir, update.State{Latest: "0.8.0"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	wireNotice(env)

	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	if len(*calls) != 0 {
		t.Errorf("startDetached called %d time(s), want 0", len(*calls))
	}
}

// TestRefreshSpawnedOncePerDay proves wireNotice starts the background
// "ajent update --check" at most once per day: two calls under the same
// fake Now produce exactly one spawn, because the first call already
// persists LastCheck before the second one loads state
// (docs/tasks/T6.5.md step 3).
func TestRefreshSpawnedOncePerDay(t *testing.T) {
	noticeTestSetArgs(t, "ajent", "search", "hello")
	calls := noticeTestSpy(t)

	env, _ := noticeTestEnv(t)
	cmdUpdateTestSetVersion(t, "0.7.0")

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	// No state.json yet: LastCheck is the zero value, so the first call
	// must refresh.

	wireNotice(env)
	wireNotice(env)

	if len(*calls) != 1 {
		t.Errorf("startDetached called %d time(s), want 1", len(*calls))
	}

	st, err := update.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !st.LastCheck.Equal(noticeTestNow) {
		t.Errorf("LastCheck = %v, want %v", st.LastCheck, noticeTestNow)
	}
}

// TestNoticeSkippedInCI proves wireNotice does nothing when CI is set,
// matching update.CheckAllowed (docs/tasks/T6.5.md step 3; ADR 005,
// "Automatic updates are always off in CI").
func TestNoticeSkippedInCI(t *testing.T) {
	noticeTestSetArgs(t, "ajent", "search", "hello")
	calls := noticeTestSpy(t)

	env, stderr := noticeTestEnv(t)
	env.IsTerminal = func(uintptr) bool { return true }
	env.Getenv = func(key string) string {
		if key == "CI" {
			return "true"
		}
		return ""
	}
	cmdUpdateTestSetVersion(t, "0.7.0")

	dir, err := env.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := update.SaveState(dir, update.State{Latest: "0.8.0"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	wireNotice(env)

	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	if len(*calls) != 0 {
		t.Errorf("startDetached called %d time(s), want 0", len(*calls))
	}
}
