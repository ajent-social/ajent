// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"testing"

	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// TestFeedbackCommand walks "ajent feedback" through show, set and the
// exact fixture bad-action message (docs/tasks/T2.11.md step 7;
// testdata/contract/commands.json "feedback"/"bad_action").
func TestFeedbackCommand(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, _ := newTestEnv(t, srv)

	if code := run(t, env, "feedback"); code != 0 {
		t.Fatalf("run(feedback) = %d; stderr = %q", code, stderr.String())
	}
	var show map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &show); err != nil {
		t.Fatalf("decoding stdout: %v; stdout = %q", err, stdout.String())
	}
	if show["policy"] != nil {
		t.Errorf("policy = %v, want null before set", show["policy"])
	}
	if show["pending_count"] != float64(0) {
		t.Errorf("pending_count = %v, want 0", show["pending_count"])
	}
	stdout.Reset()

	if code := run(t, env, "feedback", "set"); code != 0 {
		t.Fatalf("run(feedback set) = %d; stderr = %q", code, stderr.String())
	}
	var set map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &set); err != nil {
		t.Fatalf("decoding stdout: %v; stdout = %q", err, stdout.String())
	}
	policy, ok := set["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy = %#v, want an object", set["policy"])
	}
	if policy["revoked"] != false {
		t.Errorf("policy.revoked = %v, want false", policy["revoked"])
	}
	if policy["destination"] != "Ajent product maintainers" {
		t.Errorf("policy.destination = %v, want %q", policy["destination"], "Ajent product maintainers")
	}
	stdout.Reset()

	stderr.Reset()
	if code := run(t, env, "feedback", "bogus"); code != 1 {
		t.Fatalf("run(feedback bogus) = %d, want 1", code)
	}
	if want := "ajent: Use show, set, revoke, preview or clear.\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	for _, r := range srv.Requests() {
		t.Errorf("unexpected network request: %+v", r)
	}
}

// TestHelpImproveUsesExistingProfile confirms "ajent help-improve" resolves
// its credential from the existing (never-enrolled) profile, falling back
// to the root credential when no profile file exists, and never calls
// env.Resolve (docs/tasks/T2.11.md step 7; docs/contract.md section 3).
func TestHelpImproveUsesExistingProfile(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, stderr, profiles := newTestEnv(t, srv)
	writeRoot(t, env, srv.Credential())

	if code := run(t, env, "help-improve", "--json", `{"attempted_job":"x"}`); code != 0 {
		t.Fatalf("run(help-improve) = %d; stderr = %q", code, stderr.String())
	}

	if got := profiles(); len(got) != 0 {
		t.Fatalf("env.Resolve was called for profiles %v, want none", got)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decoding stdout: %v; stdout = %q", err, stdout.String())
	}
	if result["submission_state"] != "local_draft" {
		t.Errorf("submission_state = %v, want local_draft (no policy has been set)", result["submission_state"])
	}
}
