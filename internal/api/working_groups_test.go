// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

func TestWorkingGroupRequestRoutes(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000003"
	for _, tc := range []struct {
		action, method, route string
		body                  map[string]any
	}{
		{"create", "POST", "/v1/groups", map[string]any{"name": "review", "description": "bounded", "visibility": "private", "purpose_key": "review"}},
		{"direct", "POST", "/v1/groups/direct", map[string]any{"agent_id": id, "purpose_key": "review"}},
		{"join", "POST", "/v1/groups/" + id + "/join", map[string]any{}},
		{"invite", "POST", "/v1/groups/" + id + "/members/" + id, map[string]any{"action": "add"}},
		{"subscribe", "PUT", "/v1/groups/" + id + "/subscription", map[string]any{"subscribed": true}},
		{"unsubscribe", "PUT", "/v1/groups/" + id + "/subscription", map[string]any{"subscribed": false}},
		{"subscriptions", "GET", "/v1/groups/subscriptions?cursor=next%2Bpage", nil},
	} {
		t.Run(tc.action, func(t *testing.T) {
			args := map[string]any{"group_id": id, "agent_id": id, "key": "stable", "purpose_key": "review", "name": "review", "description": "bounded", "visibility": "private", "cursor": "next+page"}
			req, handled, err := WorkingGroupRequest(tc.action, args)
			if err != nil || !handled {
				t.Fatalf("build: handled=%v err=%v", handled, err)
			}
			if req.Method != tc.method || req.Route != tc.route || req.Key != "stable" {
				t.Fatalf("unexpected request shape: %s %s", req.Method, req.Route)
			}
			if tc.body != nil && !reflect.DeepEqual(req.Body, tc.body) {
				t.Fatalf("body=%v want=%v", req.Body, tc.body)
			}
		})
	}
}

func TestWorkingGroupRequestValidation(t *testing.T) {
	for _, action := range []string{"create", "direct", "join", "invite", "subscribe", "unsubscribe"} {
		t.Run(action, func(t *testing.T) {
			if _, handled, err := WorkingGroupRequest(action, map[string]any{}); !handled || err == nil {
				t.Fatal("invalid mutation accepted")
			}
		})
	}
	if _, handled, err := WorkingGroupRequest("edit", nil); handled || err != nil {
		t.Fatal("legacy action intercepted")
	}
}

func TestWorkingGroupRetrySafety(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000003"
	for _, route := range []string{"/v1/groups", "/v1/groups/direct", "/v1/groups/" + id + "/join", "/v1/groups/" + id + "/members/" + id, "/v1/groups/" + id + "/subscription"} {
		method := "POST"
		if route == "/v1/groups/"+id+"/subscription" {
			method = "PUT"
		}
		t.Run(route, func(t *testing.T) {
			if Retryable(method, route, "") || !Retryable(method, route, "stable") {
				t.Fatal("key requirement violated")
			}
			srv := fakeserver.New(t)
			calls := 0
			srv.Handle(method, route, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Idempotency-Key") != "stable" {
					t.Error("retry key changed")
				}
				w.Header().Set("Retry-After", "0")
				if calls == 1 {
					w.WriteHeader(503)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			})
			var out map[string]any
			if err := (&Client{}).Do(context.Background(), srv.Credential(), Request{Method: method, Route: route, Key: "stable", Body: map[string]any{}}, &out); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
	for _, route := range []string{"/v1/groups/direct/extra", "/v1/groups/invalid/join", "/v1/groups/subscriptions", "/v1/groups/" + id + "/members/invalid"} {
		if Retryable("POST", route, "stable") {
			t.Fatalf("unsafe route allowed: %s", route)
		}
	}
}
