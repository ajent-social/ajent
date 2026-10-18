// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

const (
	writeTestGroupUUID    = "00000000-0000-4000-8000-000000000003"
	writeTestPostUUID     = "00000000-0000-4000-8000-000000000031"
	writeTestAgentUUID    = "00000000-0000-4000-8000-000000000071"
	writeTestQuestionUUID = "00000000-0000-4000-8000-000000000042"
	writeTestAnswerUUID   = "00000000-0000-4000-8000-000000000041"
)

// writeContractFixture reads and compacts a JSON fixture from
// testdata/contract, relative to the module root.
func writeContractFixture(t *testing.T, relPath string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contract", relPath))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", relPath, err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("compacting fixture %s: %v", relPath, err)
	}
	return buf.Bytes()
}

// writeAssertBody compares got, once compacted, against the compacted
// fixture at relPath. relPath == "" asserts that got is empty.
func writeAssertBody(t *testing.T, got []byte, relPath string) {
	t.Helper()
	if relPath == "" {
		if len(got) != 0 {
			t.Errorf("body = %s, want none", got)
		}
		return
	}

	want := writeContractFixture(t, relPath)
	var gotBuf bytes.Buffer
	if err := json.Compact(&gotBuf, got); err != nil {
		t.Fatalf("compacting recorded body %s: %v", got, err)
	}
	if gotBuf.String() != string(want) {
		t.Errorf("body =\n%s\nwant\n%s", gotBuf.String(), want)
	}
}

func TestWriteRequests(t *testing.T) {
	tests := []struct {
		name string
		args []string
		// index selects srv.Requests()[index]; -1 means the last request.
		index       int
		method      string
		path        string
		idempotency string // "" = no Idempotency-Key header expected
		fixture     string // "" = no request body expected
	}{
		{
			name:        "post",
			args:        []string{"post", "--kind", "finding", "--title", "Example finding", "--body", "Example body text.", "--key", "post-key-1"},
			index:       -1,
			method:      "POST",
			path:        "/v1/posts",
			idempotency: "post-key-1",
			fixture:     "requests/post_cli.json",
		},
		{
			name:    "ack",
			args:    []string{"ack", "7"},
			index:   -1,
			method:  "POST",
			path:    "/v1/inbox/ack",
			fixture: "requests/ack.json",
		},
		{
			name:   "retract",
			args:   []string{"retract", writeTestPostUUID},
			index:  -1,
			method: "DELETE",
			path:   "/v1/posts/" + writeTestPostUUID,
		},
		{
			name:   "group list",
			args:   []string{"group", "--json", `{"action":"list"}`},
			index:  -1,
			method: "GET",
			path:   "/v1/groups",
		},
		{
			name:   "group list cursor",
			args:   []string{"group", "--json", `{"action":"list","cursor":"example-cursor"}`},
			index:  -1,
			method: "GET",
			path:   "/v1/groups",
		},
		{
			name:   "group capability probe",
			args:   []string{"group", "--json", fmt.Sprintf(`{"action":"read","group_id":%q}`, writeTestGroupUUID)},
			index:  0,
			method: "GET",
			path:   "/v1/reliability",
		},
		{
			name:   "group read",
			args:   []string{"group", "--json", fmt.Sprintf(`{"action":"read","group_id":%q}`, writeTestGroupUUID)},
			index:  -1,
			method: "GET",
			path:   "/v1/groups/" + writeTestGroupUUID,
		},
		{
			name:        "group edit",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"edit","group_id":%q,"version":2,"key":"g-edit","description":"Example description.","rules":"Example rules."}`, writeTestGroupUUID)},
			index:       -1,
			method:      "PATCH",
			path:        "/v1/groups/" + writeTestGroupUUID,
			idempotency: "g-edit",
			fixture:     "requests/group_edit.json",
		},
		{
			name:        "group archive",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"archive","group_id":%q,"version":2,"key":"g-archive"}`, writeTestGroupUUID)},
			index:       -1,
			method:      "PATCH",
			path:        "/v1/groups/" + writeTestGroupUUID,
			idempotency: "g-archive",
			fixture:     "requests/group_archive.json",
		},
		{
			name:        "group reopen",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"reopen","group_id":%q,"version":2,"key":"g-reopen"}`, writeTestGroupUUID)},
			index:       -1,
			method:      "PATCH",
			path:        "/v1/groups/" + writeTestGroupUUID,
			idempotency: "g-reopen",
			fixture:     "requests/group_reopen.json",
		},
		{
			name:        "group leave",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"leave","group_id":%q,"version":2,"key":"g-leave"}`, writeTestGroupUUID)},
			index:       -1,
			method:      "DELETE",
			path:        "/v1/groups/" + writeTestGroupUUID + "/membership",
			idempotency: "g-leave",
			fixture:     "requests/group_leave.json",
		},
		{
			name:        "group transfer",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"transfer","group_id":%q,"version":2,"key":"g-transfer","agent_id":%q}`, writeTestGroupUUID, writeTestAgentUUID)},
			index:       -1,
			method:      "PUT",
			path:        "/v1/groups/" + writeTestGroupUUID + "/owner",
			idempotency: "g-transfer",
			fixture:     "requests/group_transfer.json",
		},
		{
			name:        "group pin",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"pin","group_id":%q,"version":2,"key":"g-pin","post_id":%q,"position":1}`, writeTestGroupUUID, writeTestPostUUID)},
			index:       -1,
			method:      "PUT",
			path:        "/v1/groups/" + writeTestGroupUUID + "/pins/" + writeTestPostUUID,
			idempotency: "g-pin",
			fixture:     "requests/group_pin.json",
		},
		{
			name:        "group unpin",
			args:        []string{"group", "--json", fmt.Sprintf(`{"action":"unpin","group_id":%q,"version":2,"key":"g-unpin","post_id":%q}`, writeTestGroupUUID, writeTestPostUUID)},
			index:       -1,
			method:      "DELETE",
			path:        "/v1/groups/" + writeTestGroupUUID + "/pins/" + writeTestPostUUID,
			idempotency: "g-unpin",
			fixture:     "requests/group_unpin.json",
		},
		{
			name:   "question capability probe",
			args:   []string{"question", "--json", fmt.Sprintf(`{"action":"accept","id":%q,"answer_id":%q,"version":1,"key":"q-accept"}`, writeTestQuestionUUID, writeTestAnswerUUID)},
			index:  0,
			method: "GET",
			path:   "/v1/reliability",
		},
		{
			name:        "question accept",
			args:        []string{"question", "--json", fmt.Sprintf(`{"action":"accept","id":%q,"answer_id":%q,"version":1,"key":"q-accept"}`, writeTestQuestionUUID, writeTestAnswerUUID)},
			index:       -1,
			method:      "PUT",
			path:        "/v1/questions/" + writeTestQuestionUUID + "/resolution",
			idempotency: "q-accept",
			fixture:     "requests/question_accept.json",
		},
		{
			name:        "question reopen",
			args:        []string{"question", "--json", fmt.Sprintf(`{"action":"reopen","id":%q,"version":1,"key":"q-reopen"}`, writeTestQuestionUUID)},
			index:       -1,
			method:      "PUT",
			path:        "/v1/questions/" + writeTestQuestionUUID + "/resolution",
			idempotency: "q-reopen",
			fixture:     "requests/question_reopen.json",
		},
		{
			name:        "question validate",
			args:        []string{"question", "--json", fmt.Sprintf(`{"action":"validate","id":%q,"key":"q-validate","environment":"linux/amd64 go1.26","result":"worked","evidence":"Example evidence.","checked_at":"2026-01-01T00:00:00Z"}`, writeTestAnswerUUID)},
			index:       -1,
			method:      "POST",
			path:        "/v1/answers/" + writeTestAnswerUUID + "/validations",
			idempotency: "q-validate",
			fixture:     "requests/question_validate.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			env, _, stderr, _ := newTestEnv(t, srv)

			code := run(t, env, tt.args...)
			if code != 0 {
				t.Fatalf("run(%v) exit = %d, stderr = %s", tt.args, code, stderr.String())
			}

			reqs := srv.Requests()
			idx := tt.index
			if idx < 0 {
				idx = len(reqs) - 1
			}
			if idx < 0 || idx >= len(reqs) {
				t.Fatalf("run(%v): got %d requests, want an index-%d request", tt.args, len(reqs), tt.index)
			}
			got := reqs[idx]

			if got.Method != tt.method || got.Path != tt.path {
				t.Errorf("request[%d] = %s %s, want %s %s", idx, got.Method, got.Path, tt.method, tt.path)
			}
			if key := got.Header.Get("Idempotency-Key"); key != tt.idempotency {
				t.Errorf("Idempotency-Key = %q, want %q", key, tt.idempotency)
			}
			writeAssertBody(t, got.Body, tt.fixture)
		})
	}
}

func TestPostRequiresKey(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "post", "--title", "x")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: Supply --key with a unique operation name; reuse it when retrying.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestPostRetryReusesKey(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	var calls int
	srv.Handle(http.MethodPost, "/v1/posts", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"p1"}`))
	})

	code := run(t, env, "post", "--title", "x", "--key", "k1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0, stderr = %s", code, stderr.String())
	}

	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for i, r := range reqs {
		if key := r.Header.Get("Idempotency-Key"); key != "k1" {
			t.Errorf("request[%d] Idempotency-Key = %q, want %q", i, key, "k1")
		}
	}
	if !bytes.Equal(reqs[0].Body, reqs[1].Body) {
		t.Errorf("request bodies differ: %s vs %s", reqs[0].Body, reqs[1].Body)
	}
}

func TestAckValidation(t *testing.T) {
	// "-1" is parsed by the flag package as an unrecognized flag, not a
	// positional argument, so it exits 1 with the flag package's own
	// message rather than errWriteAckCursor; TestAckBadCursor below checks
	// the exact fixture message for the cases that do reach our validation.
	for _, args := range [][]string{
		{"ack", "abc"},
		{"ack", "-1"},
		{"ack", "1.5"},
		{"ack"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			srv := fakeserver.New(t)
			env, _, _, _ := newTestEnv(t, srv)

			code := run(t, env, args...)
			if code != 1 {
				t.Fatalf("run(%v) exit = %d, want 1", args, code)
			}
			if n := len(srv.Requests()); n != 0 {
				t.Errorf("run(%v) requests = %d, want 0", args, n)
			}
		})
	}
}

func TestAckBadCursor(t *testing.T) {
	want := "ajent: Supply the delivered inbox cursor.\n"
	for _, args := range [][]string{
		{"ack", "abc"},
		{"ack", "1.5"},
		{"ack"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			srv := fakeserver.New(t)
			env, _, stderr, _ := newTestEnv(t, srv)

			code := run(t, env, args...)
			if code != 1 {
				t.Fatalf("run(%v) exit = %d, want 1", args, code)
			}
			if stderr.String() != want {
				t.Errorf("run(%v) stderr = %q, want %q", args, stderr.String(), want)
			}
		})
	}
}

func TestRetractValidation(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "retract", "not-a-uuid")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: Supply the post UUID to retract.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestGroupCapabilityAbsent(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	srv.Handle(http.MethodGet, "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":[]}`))
	})

	code := run(t, env, "group", "--json", fmt.Sprintf(`{"action":"read","group_id":%q}`, writeTestGroupUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: unsupported_capability. Server capability group_lifecycle is unavailable. Upgrade before retrying this operation.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	for _, r := range srv.Requests() {
		if strings.HasPrefix(r.Path, "/v1/groups/") {
			t.Errorf("unexpected request to %s", r.Path)
		}
	}
}

func TestGroupUnknownAction(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "group", "--json", fmt.Sprintf(`{"action":"bogus","group_id":%q,"version":2,"key":"g1"}`, writeTestGroupUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: Unknown group action.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestQuestionCapabilityAbsent(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)
	srv.Handle(http.MethodGet, "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":[]}`))
	})

	code := run(t, env, "question", "--json", fmt.Sprintf(`{"action":"validate","id":%q,"key":"k"}`, writeTestAnswerUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: unsupported_capability. Server capability question_checks is unavailable. Upgrade before retrying this operation.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	for _, r := range srv.Requests() {
		if strings.HasPrefix(r.Path, "/v1/answers/") || strings.HasPrefix(r.Path, "/v1/questions/") {
			t.Errorf("unexpected request to %s", r.Path)
		}
	}
}

func TestQuestionNoKey(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "question", "--json", fmt.Sprintf(`{"action":"validate","id":%q}`, writeTestAnswerUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: A stable operation key is required.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestQuestionVersionRequired(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "question", "--json", fmt.Sprintf(`{"action":"accept","id":%q,"answer_id":%q,"key":"q1"}`, writeTestQuestionUUID, writeTestAnswerUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: Read the current question version.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestQuestionUnknownAction(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "question", "--json", fmt.Sprintf(`{"action":"bogus","id":%q,"key":"q1"}`, writeTestAnswerUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: Unknown question action.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestGroupVersionRequired(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, stderr, _ := newTestEnv(t, srv)

	code := run(t, env, "group", "--json", fmt.Sprintf(`{"action":"edit","group_id":%q,"key":"g1"}`, writeTestGroupUUID))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "ajent: Read the group first; supply its version and a stable operation key.\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPatch {
			t.Errorf("unexpected PATCH request to %s", r.Path)
		}
	}
}
