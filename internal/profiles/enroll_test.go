// SPDX-License-Identifier: Apache-2.0

package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

func TestExistingNeverWrites(t *testing.T) {
	dir := t.TempDir()
	root := profilesTestRoot()

	if _, err := Existing(dir, root, "not a valid profile"); !errors.Is(err, credentials.ErrInvalidProfile) {
		t.Fatalf("Existing() err = %v, want %v", err, credentials.ErrInvalidProfile)
	}

	c, err := Existing(dir, root, "never-enrolled")
	if err != nil {
		t.Fatalf("Existing() err = %v", err)
	}
	if !reflect.DeepEqual(c, root) {
		t.Fatalf("Existing() for an unenrolled profile = %+v, want root %+v", c, root)
	}
	if _, statErr := os.Stat(credentials.ProfilePath(dir, root, "never-enrolled")); !os.IsNotExist(statErr) {
		t.Fatalf("Existing() created a profile file; stat err = %v", statErr)
	}

	pending := credentials.Credential{Server: root.Server, Token: "aj_pending0000000_pendingtoken00000000000000000000000000000", Pending: true}
	if err := credentials.SaveProfile(dir, root, "pending-profile", pending); err != nil {
		t.Fatalf("seeding pending profile: %v", err)
	}
	c, err = Existing(dir, root, "pending-profile")
	if err != nil {
		t.Fatalf("Existing() err = %v", err)
	}
	if !reflect.DeepEqual(c, root) {
		t.Fatalf("Existing() for a pending profile = %+v, want root %+v", c, root)
	}

	enrolled := credentials.Credential{Server: root.Server, Token: "aj_enrolled000000_enrolledtoken0000000000000000000000000000", AgentID: "agent-1"}
	if err := credentials.SaveProfile(dir, root, "enrolled-profile", enrolled); err != nil {
		t.Fatalf("seeding enrolled profile: %v", err)
	}
	c, err = Existing(dir, root, "enrolled-profile")
	if err != nil {
		t.Fatalf("Existing() err = %v", err)
	}
	if !reflect.DeepEqual(c, enrolled) {
		t.Fatalf("Existing() for an enrolled profile = %+v, want %+v", c, enrolled)
	}
	if _, statErr := os.Stat(credentials.ProfilePath(dir, root, "enrolled-profile")); statErr != nil {
		t.Fatalf("Existing() disturbed the profile file: %v", statErr)
	}
}

func countFleetRequests(srv *fakeserver.Server) int {
	n := 0
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPost && r.Path == "/v1/fleet" {
			n++
		}
	}
	return n
}

func TestResolveEnrollsOnce(t *testing.T) {
	srv := fakeserver.New(t)
	root := srv.Credential()
	dir := t.TempDir()
	projectRoot := t.TempDir()

	e := &Enroller{Dir: dir, API: &api.Client{Sleep: func(time.Duration) {}}, Now: time.Now}

	c1, err := e.Resolve(context.Background(), root, "enrolls-once", projectRoot)
	if err != nil {
		t.Fatalf("first Resolve() err = %v", err)
	}
	if c1.Pending {
		t.Fatalf("first Resolve() left the credential pending: %+v", c1)
	}
	if c1.Token == "" {
		t.Fatalf("first Resolve() returned an empty token")
	}
	if got := countFleetRequests(srv); got != 1 {
		t.Fatalf("after first Resolve(): %d POST /v1/fleet requests, want 1", got)
	}

	c2, err := e.Resolve(context.Background(), root, "enrolls-once", projectRoot)
	if err != nil {
		t.Fatalf("second Resolve() err = %v", err)
	}
	if c2.Token != c1.Token {
		t.Fatalf("second Resolve() token = %q, want %q", c2.Token, c1.Token)
	}
	if got := countFleetRequests(srv); got != 1 {
		t.Fatalf("after second Resolve() within 300s: %d POST /v1/fleet requests, want 1 (no re-sync)", got)
	}
}

func TestResolveResyncsOnSettingsChange(t *testing.T) {
	srv := fakeserver.New(t)
	root := srv.Credential()
	dir := t.TempDir()
	projectRoot := t.TempDir()

	e := &Enroller{Dir: dir, API: &api.Client{Sleep: func(time.Duration) {}}, Now: time.Now}

	if _, err := e.Resolve(context.Background(), root, "resyncs-on-change", projectRoot); err != nil {
		t.Fatalf("first Resolve() err = %v", err)
	}
	if got := countFleetRequests(srv); got != 1 {
		t.Fatalf("after first Resolve(): %d POST /v1/fleet requests, want 1", got)
	}

	profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{"domain": "", "private_only": true})

	if _, err := e.Resolve(context.Background(), root, "resyncs-on-change", projectRoot); err != nil {
		t.Fatalf("second Resolve() err = %v", err)
	}
	if got := countFleetRequests(srv); got != 2 {
		t.Fatalf("after second Resolve() with changed settings: %d POST /v1/fleet requests, want 2", got)
	}
}

func TestResolveResyncsAfter300s(t *testing.T) {
	srv := fakeserver.New(t)
	root := srv.Credential()
	dir := t.TempDir()
	projectRoot := t.TempDir()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := &Enroller{Dir: dir, API: &api.Client{Sleep: func(time.Duration) {}}, Now: func() time.Time { return now }}

	if _, err := e.Resolve(context.Background(), root, "resyncs-after-300s", projectRoot); err != nil {
		t.Fatalf("first Resolve() err = %v", err)
	}
	if got := countFleetRequests(srv); got != 1 {
		t.Fatalf("after first Resolve(): %d POST /v1/fleet requests, want 1", got)
	}

	now = now.Add(301 * time.Second)
	if _, err := e.Resolve(context.Background(), root, "resyncs-after-300s", projectRoot); err != nil {
		t.Fatalf("second Resolve() err = %v", err)
	}
	if got := countFleetRequests(srv); got != 2 {
		t.Fatalf("after second Resolve() 301s later: %d POST /v1/fleet requests, want 2", got)
	}
}

func TestResolve403FallsBackToPrivate(t *testing.T) {
	srv := fakeserver.New(t)
	root := srv.Credential()
	dir := t.TempDir()
	projectRoot := t.TempDir()
	scope := credentials.ProjectScope(projectRoot)

	profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{"domain": "example.com", "private_only": false})
	profilesWriteJSON(t, filepath.Join(dir, "scope-grants.json"), map[string]any{
		root.Identity(): map[string]any{
			"user": []map[string]string{{"domain": "example.com", "id": "00000000-0000-4000-8000-000000000010"}},
		},
	})

	var mu sync.Mutex
	var seenBodies [][]byte
	srv.Handle(http.MethodPost, "/v1/fleet", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)

		mu.Lock()
		seenBodies = append(seenBodies, body)
		call := len(seenBodies)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"agent_id":"agent-fallback"}`))
	})

	e := &Enroller{Dir: dir, API: &api.Client{Sleep: func(time.Duration) {}}, Now: time.Now}
	c, err := e.Resolve(context.Background(), root, "falls-back-to-private", projectRoot)
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}
	if c.DomainNotice != profilesDomainNotice {
		t.Fatalf("DomainNotice = %q, want %q", c.DomainNotice, profilesDomainNotice)
	}
	if c.AgentID != "agent-fallback" {
		t.Fatalf("AgentID = %q, want %q", c.AgentID, "agent-fallback")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenBodies) != 2 {
		t.Fatalf("saw %d POST /v1/fleet requests, want 2", len(seenBodies))
	}
	var second map[string]any
	if err := json.Unmarshal(seenBodies[1], &second); err != nil {
		t.Fatalf("decoding second request body: %v", err)
	}
	if second["private_only"] != true {
		t.Fatalf("second request private_only = %v, want true", second["private_only"])
	}
	grants, ok := second["grants"].([]any)
	if !ok || len(grants) != 0 {
		t.Fatalf("second request grants = %v, want an empty list", second["grants"])
	}
	if second["scope"] != scope {
		t.Fatalf("second request scope = %v, want %q", second["scope"], scope)
	}
	if second["domain"] != "" {
		t.Fatalf("second request domain = %v, want empty", second["domain"])
	}
}

// TestResolveConcurrentProcesses launches 12 separate OS processes (the
// standard os/exec subprocess-helper pattern; see TestHelperProcess in
// helper_test.go) that each resolve the same brand-new profile against one
// shared fake server and config directory. The profile lock must let
// exactly one of them create and enroll it: docs/tasks/T3.6.md's
// genuine-red check removes the config.Acquire call in Resolve and expects
// this test to fail.
func TestResolveConcurrentProcesses(t *testing.T) {
	srv := fakeserver.New(t)
	root := srv.Credential()
	dir := t.TempDir()
	projectRoot := t.TempDir()

	const workers = 12
	tokens := make([]string, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = runProfilesHelperProcess(dir, root, "concurrent-enroll", projectRoot)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("subprocess %d: %v", i, err)
		}
	}
	for i, tok := range tokens {
		if tok == "" {
			t.Fatalf("subprocess %d printed no token", i)
		}
		if tok != tokens[0] {
			t.Fatalf("subprocess %d token %q differs from subprocess 0 token %q", i, tok, tokens[0])
		}
	}

	if got := countFleetRequests(srv); got != 1 {
		t.Fatalf("got %d POST /v1/fleet requests across %d subprocesses, want 1", got, workers)
	}
}

// runProfilesHelperProcess runs the test binary as a subprocess restricted
// to TestHelperProcess (helper_test.go), which resolves profile against
// dir and root, and returns the token it printed.
func runProfilesHelperProcess(dir string, root credentials.Credential, profile, projectRoot string) (string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$") //nolint:gosec // G204: os.Args[0] is this test binary, not attacker-controlled input.
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER=1",
		"AJENT_PROFILES_HELPER_DIR="+dir,
		"AJENT_PROFILES_HELPER_SERVER="+root.Server,
		"AJENT_PROFILES_HELPER_TOKEN="+root.Token,
		"AJENT_PROFILES_HELPER_PROFILE="+profile,
		"AJENT_PROFILES_HELPER_PROJECT_ROOT="+projectRoot,
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			return "", errors.New("helper process failed: " + string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func TestResolveResyncsSettingsChangedBetweenProcesses(t *testing.T) {
	srv := fakeserver.New(t)
	root := srv.Credential()
	dir := t.TempDir()
	projectRoot := t.TempDir()
	newEnroller := func() *Enroller {
		return &Enroller{Dir: dir, API: &api.Client{Sleep: func(time.Duration) {}}, Now: time.Now}
	}

	if _, err := newEnroller().Resolve(context.Background(), root, "between-processes", projectRoot); err != nil {
		t.Fatalf("first process Resolve() err = %v", err)
	}
	if _, err := newEnroller().Resolve(context.Background(), root, "between-processes", projectRoot); err != nil {
		t.Fatalf("second process Resolve() err = %v", err)
	}
	if got := countFleetRequests(srv); got != 1 {
		t.Fatalf("unchanged settings across processes: %d POST /v1/fleet requests, want 1", got)
	}

	profilesWriteJSON(t, filepath.Join(dir, "config.json"), map[string]any{"domain": "", "private_only": true})

	if _, err := newEnroller().Resolve(context.Background(), root, "between-processes", projectRoot); err != nil {
		t.Fatalf("third process Resolve() err = %v", err)
	}
	if got := countFleetRequests(srv); got != 2 {
		t.Fatalf("settings changed while no process ran: %d POST /v1/fleet requests, want 2", got)
	}
}
