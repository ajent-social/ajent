// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

import "github.com/ajent-social/ajent/internal/testutil/fakeserver"

func TestWorkingGroupsCommands(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		args               []string
	}{
		{"create", "POST", "/v1/groups", []string{"group", "--json", `{"action":"create","name":"review","visibility":"private","key":"stable"}`}},
		{"direct", "POST", "/v1/groups/direct", []string{"group", "--json", `{"action":"direct","agent_id":"` + writeTestAgentUUID + `","purpose_key":"review","key":"stable"}`}},
		{"join", "POST", "/v1/groups/" + writeTestGroupUUID + "/join", []string{"group", "--json", `{"action":"join","group_id":"` + writeTestGroupUUID + `","key":"stable"}`}},
		{"invite", "POST", "/v1/groups/" + writeTestGroupUUID + "/members/" + writeTestAgentUUID, []string{"group", "--json", `{"action":"invite","group_id":"` + writeTestGroupUUID + `","agent_id":"` + writeTestAgentUUID + `","key":"stable"}`}},
		{"subscribe", "PUT", "/v1/groups/" + writeTestGroupUUID + "/subscription", []string{"group", "--json", `{"action":"subscribe","group_id":"` + writeTestGroupUUID + `","key":"stable"}`}},
		{"unsubscribe", "PUT", "/v1/groups/" + writeTestGroupUUID + "/subscription", []string{"group", "--json", `{"action":"unsubscribe","group_id":"` + writeTestGroupUUID + `","key":"stable"}`}},
		{"subscriptions", "GET", "/v1/groups/subscriptions", []string{"group", "--json", `{"action":"subscriptions","cursor":"next+page"}`}},
		{"inbox", "GET", "/v1/inbox", []string{"inbox", "--group", writeTestGroupUUID}},
		{"ack", "POST", "/v1/inbox/ack", []string{"ack", "--group", writeTestGroupUUID, "--key", "stable", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			env, _, stderr, _ := newTestEnv(t, srv)
			srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"working_groups"}})
			})
			srv.Handle(tc.method, tc.path, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			})
			if code := run(t, env, tc.args...); code != 0 {
				t.Fatalf("exit=%d: %s", code, stderr.String())
			}
			found := false
			for _, r := range srv.Requests() {
				if r.Method == tc.method && r.Path == tc.path {
					found = true
					if tc.method != "GET" && r.Header.Get("Idempotency-Key") != "stable" {
						t.Error("missing mutation key")
					}
					if tc.name == "inbox" && r.RawQuery != "group_id="+writeTestGroupUUID {
						t.Error("missing group query")
					}
					if tc.name == "subscriptions" && r.RawQuery != "cursor=next%2Bpage" {
						t.Error("cursor not escaped")
					}
					if tc.name == "ack" {
						var body map[string]any
						if err := json.Unmarshal(r.Body, &body); err != nil {
							t.Fatal(err)
						}
						if body["group_id"] != writeTestGroupUUID || body["through"] != float64(3) {
							t.Error("wrong ack scope")
						}
					}
				}
			}
			if !found {
				t.Fatal("expected request missing")
			}
		})
	}
}

func TestWorkingGroupsCommandsFailClosed(t *testing.T) {
	for _, args := range [][]string{
		{"group", "--json", `{"action":"join","group_id":"` + writeTestGroupUUID + `","key":"stable"}`},
		{"inbox", "--group", writeTestGroupUUID},
		{"ack", "--group", writeTestGroupUUID, "3"},
	} {
		t.Run(args[0], func(t *testing.T) {
			srv := fakeserver.New(t)
			env, _, _, _ := newTestEnv(t, srv)
			srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"capabilities":[]}`)) })
			if run(t, env, args...) == 0 {
				t.Fatal("unsupported scoped operation succeeded")
			}
			for _, r := range srv.Requests() {
				if r.Path == "/v1/inbox" || r.Path == "/v1/inbox/ack" || r.Path == "/v1/groups/"+writeTestGroupUUID+"/join" {
					t.Fatal("unsupported operation sent")
				}
			}
		})
	}
}
