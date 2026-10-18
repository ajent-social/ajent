// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

// TestNoConsentNoSend captures a failure, then reports without ever having
// granted policy: the result is local_draft, and neither /v1/improvements
// nor /v1/reliability is ever called, because permission is checked before
// capability (docs/tasks/T2.11.md step 7).
func TestNoConsentNoSend(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	srv.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_ = c.Do(context.Background(), cred, api.Request{Method: "GET", Route: "/v1/me", Attempts: 1}, nil)

	result, err := store.HelpImprove(context.Background(), c, cred, map[string]any{"attempted_job": "x"})
	if err != nil {
		t.Fatalf("HelpImprove() = %v", err)
	}
	if got := result.(map[string]any)["submission_state"]; got != "local_draft" {
		t.Fatalf("submission_state = %v, want local_draft", got)
	}

	for _, r := range srv.Requests() {
		if r.Path == "/v1/improvements" {
			t.Errorf("unexpected request to /v1/improvements")
		}
		if r.Path == "/v1/reliability" {
			t.Errorf("unexpected request to /v1/reliability")
		}
	}
}

// TestConsentSendsOnce reports the same draft twice after granting
// permission: only one POST reaches the server, carrying an
// Idempotency-Key equal to occurrence_id, and the payload has exactly the
// frozen key set (docs/tasks/T2.11.md step 7).
func TestConsentSendsOnce(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	if _, err := store.Local("set"); err != nil {
		t.Fatalf("Local(set) = %v", err)
	}

	var (
		bodies []json.RawMessage
		keys   []string
	)
	srv.Handle("POST", "/v1/improvements", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"receipt","submission_state":"received"}`))
	})

	args := map[string]any{"attempted_job": "Search for prior findings."}

	first, err := store.HelpImprove(context.Background(), c, cred, args)
	if err != nil {
		t.Fatalf("HelpImprove() #1 = %v", err)
	}
	if got := first.(map[string]any)["id"]; got != "receipt" {
		t.Fatalf("result #1 = %#v, want the server response", first)
	}

	// The record now carries a stored result, so a second call for the
	// same evidence_id is a pure replay: it must not touch the network
	// again, and returns the stored result unchanged.
	preview, err := store.Local("preview")
	if err != nil {
		t.Fatalf("Local(preview) = %v", err)
	}
	records := preview.(map[string]any)["records"].([]record)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	evidenceID, _ := records[0]["evidence_id"].(string)
	if evidenceID == "" {
		t.Fatalf("evidence_id is empty")
	}

	second, err := store.HelpImprove(context.Background(), c, cred, map[string]any{"evidence_id": evidenceID})
	if err != nil {
		t.Fatalf("HelpImprove() #2 = %v", err)
	}
	if got := second.(map[string]any)["id"]; got != "receipt" {
		t.Fatalf("result #2 = %#v, want the server response", second)
	}

	if len(bodies) != 1 {
		t.Fatalf("POST /v1/improvements calls = %d, want 1", len(bodies))
	}
	if keys[0] == "" {
		t.Fatalf("Idempotency-Key is empty")
	}

	var payload map[string]any
	if err := json.Unmarshal(bodies[0], &payload); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	if payload["occurrence_id"] != keys[0] {
		t.Errorf("occurrence_id = %v, Idempotency-Key = %v, want equal", payload["occurrence_id"], keys[0])
	}

	wantKeys := []string{"occurrence_id", "policy_version", "category", "evidence", "attempted_job", "expected", "observed", "workaround", "workaround_minutes"}
	if len(payload) != len(wantKeys) {
		t.Fatalf("payload = %v, want exactly the keys %v", payload, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload is missing key %q", k)
		}
	}
}

// TestRevokeBlocksSend grants, then revokes permission before reporting:
// the result stays local_draft and no POST ever reaches the server
// (docs/tasks/T2.11.md step 7).
func TestRevokeBlocksSend(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	if _, err := store.Local("set"); err != nil {
		t.Fatalf("Local(set) = %v", err)
	}
	if _, err := store.Local("revoke"); err != nil {
		t.Fatalf("Local(revoke) = %v", err)
	}

	var posts int
	srv.Handle("POST", "/v1/improvements", func(w http.ResponseWriter, _ *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	})

	result, err := store.HelpImprove(context.Background(), c, cred, map[string]any{"attempted_job": "x"})
	if err != nil {
		t.Fatalf("HelpImprove() = %v", err)
	}
	if got := result.(map[string]any)["submission_state"]; got != "local_draft" {
		t.Fatalf("submission_state = %v, want local_draft", got)
	}
	if posts != 0 {
		t.Fatalf("POST /v1/improvements calls = %d, want 0", posts)
	}
}

// TestQueuedOriginalOnChange leaves a report queued (its send failed), then
// retries the same evidence_id with a changed context field: the retry is
// rejected as queued_original without ever touching the network again
// (docs/tasks/T2.11.md step 7).
func TestQueuedOriginalOnChange(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	if _, err := store.Local("set"); err != nil {
		t.Fatalf("Local(set) = %v", err)
	}

	var posts int
	srv.Handle("POST", "/v1/improvements", func(w http.ResponseWriter, _ *http.Request) {
		posts++
		w.WriteHeader(http.StatusBadRequest)
	})

	args := map[string]any{"attempted_job": "Search for prior findings.", "expected": "results"}
	first, err := store.HelpImprove(context.Background(), c, cred, args)
	if err != nil {
		t.Fatalf("HelpImprove() #1 = %v", err)
	}
	firstMap := first.(map[string]any)
	if firstMap["submission_state"] != "queued" {
		t.Fatalf("submission_state #1 = %v, want queued", firstMap["submission_state"])
	}
	evidenceID, _ := firstMap["evidence_id"].(string)
	if evidenceID == "" {
		t.Fatalf("evidence_id is empty")
	}
	if posts != 1 {
		t.Fatalf("POST calls after #1 = %d, want 1", posts)
	}

	changed := map[string]any{"evidence_id": evidenceID, "observed": "changed context"}
	second, err := store.HelpImprove(context.Background(), c, cred, changed)
	if err != nil {
		t.Fatalf("HelpImprove() #2 = %v", err)
	}
	secondMap := second.(map[string]any)
	if secondMap["submission_state"] != "queued_original" {
		t.Fatalf("submission_state #2 = %v, want queued_original", secondMap["submission_state"])
	}
	if secondMap["evidence_id"] != evidenceID {
		t.Errorf("evidence_id #2 = %v, want %v", secondMap["evidence_id"], evidenceID)
	}
	if posts != 1 {
		t.Fatalf("POST calls after #2 = %d, want 1 (queued_original must not resend)", posts)
	}
}

// TestVerifyFlow drives the verify action: a first submission fails and
// stays queued, a retry with a changed outcome is rejected as
// queued_original without a network call, and a retry with the original
// outcome and evidence replays the exact same payload and succeeds
// (docs/tasks/T2.11.md step 7).
func TestVerifyFlow(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	if _, err := store.Local("set"); err != nil {
		t.Fatalf("Local(set) = %v", err)
	}

	var bodies []json.RawMessage
	srv.Handle("POST", "^/v1/improvements/[0-9a-f-]{36}/verify$", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"verified","replayed":true}`))
	})

	args := map[string]any{
		"action":                "verify",
		"report_id":             "00000000-0000-4000-8000-000000000061",
		"deployed_revision":     "abc123",
		"outcome":               "not_reproduced",
		"verification_evidence": "retried original workflow",
	}

	first, err := store.HelpImprove(context.Background(), c, cred, args)
	if err != nil {
		t.Fatalf("HelpImprove() #1 = %v", err)
	}
	firstMap := first.(map[string]any)
	if firstMap["submission_state"] != "queued" {
		t.Fatalf("submission_state #1 = %v, want queued", firstMap["submission_state"])
	}
	if len(bodies) != 1 {
		t.Fatalf("verify POST calls after #1 = %d, want 1", len(bodies))
	}

	changedOutcome := map[string]any{}
	for k, v := range args {
		changedOutcome[k] = v
	}
	changedOutcome["outcome"] = "reproduced"
	changed, err := store.HelpImprove(context.Background(), c, cred, changedOutcome)
	if err != nil {
		t.Fatalf("HelpImprove() #2 = %v", err)
	}
	changedMap := changed.(map[string]any)
	if changedMap["submission_state"] != "queued_original" {
		t.Fatalf("submission_state #2 = %v, want queued_original", changedMap["submission_state"])
	}
	if len(bodies) != 1 {
		t.Fatalf("verify POST calls after #2 = %d, want 1 (queued_original must not resend)", len(bodies))
	}

	second, err := store.HelpImprove(context.Background(), c, cred, args)
	if err != nil {
		t.Fatalf("HelpImprove() #3 = %v", err)
	}
	secondMap := second.(map[string]any)
	if secondMap["status"] != "verified" {
		t.Fatalf("result #3 = %#v, want the server response", second)
	}
	if len(bodies) != 2 {
		t.Fatalf("verify POST calls after #3 = %d, want 2", len(bodies))
	}
	if string(bodies[0]) != string(bodies[1]) {
		t.Errorf("verify payload changed between retries:\n#1: %s\n#2: %s", bodies[0], bodies[1])
	}
}

// TestHelpImproveUnsupportedCapability confirms a server that does not
// advertise "improvements" leaves the draft queued locally without ever
// posting it (docs/contract.md section 12).
func TestHelpImproveUnsupportedCapability(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	if _, err := store.Local("set"); err != nil {
		t.Fatalf("Local(set) = %v", err)
	}
	srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"capabilities":[]}`))
	})

	var posts int
	srv.Handle("POST", "/v1/improvements", func(w http.ResponseWriter, _ *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	})

	result, err := store.HelpImprove(context.Background(), c, cred, map[string]any{"attempted_job": "x"})
	if err != nil {
		t.Fatalf("HelpImprove() = %v", err)
	}
	if got := result.(map[string]any)["submission_state"]; got != "unsupported" {
		t.Fatalf("submission_state = %v, want unsupported", got)
	}
	if posts != 0 {
		t.Fatalf("POST calls = %d, want 0", posts)
	}
}

// TestHelpImproveEmptyResult confirms an empty --json with no pending
// evidence and no attempted_job produces the "empty" result without
// creating a record (docs/contract.md section 12).
func TestHelpImproveEmptyResult(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	store := &Store{Dir: t.TempDir()}
	c := feedbackTestClient(store)

	result, err := store.HelpImprove(context.Background(), c, cred, map[string]any{})
	if err != nil {
		t.Fatalf("HelpImprove() = %v", err)
	}
	if got := result.(map[string]any)["submission_state"]; got != "empty" {
		t.Fatalf("submission_state = %v, want empty", got)
	}

	preview, err := store.Local("preview")
	if err != nil {
		t.Fatalf("Local(preview) = %v", err)
	}
	if got := len(preview.(map[string]any)["records"].([]record)); got != 0 {
		t.Fatalf("records = %d, want 0", got)
	}
}
