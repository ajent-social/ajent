// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

func TestCompactListWorkingGroupIsolation(t *testing.T) {
	c := OpenCache(t.TempDir(), "terminal")
	scope := []string{"origin", "agent", "workspace"}
	value := map[string]any{"items": []any{map[string]any{"id": "post", "seq": float64(7), "kind": "finding", "body": "checked"}}, "acknowledge_through": float64(7)}
	for _, group := range []string{"", "group-a", "group-b", "group-a"} {
		args := map[string]any{}
		if group != "" {
			args["group_id"] = group
		}
		out, receipts := compactList("ajent_inbox", args, value, c, scope)
		want := 1
		if group == "group-a" && c.Seen(c.Key(append(append([]string(nil), scope...), "working_group", group), "inbox", value["items"].([]any)[0])) {
			want = 0
		}
		if len(out["items"].([]any)) != want {
			t.Fatalf("group=%q delivery suppressed across scopes", group)
		}
		c.Remember(receipts...)
	}
}

func TestWorkingGroupsCapabilityAndWire(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "available"}[supported], func(t *testing.T) {
			srv := fakeserver.New(t)
			srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
				caps := []string{}
				if supported {
					caps = append(caps, "working_groups")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": caps})
			})
			srv.Handle("POST", "/v1/groups/direct", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": writeTestGroupID})
			})
			s := writeTestSession(t, srv)
			_, err := writeGroupsHandle(context.Background(), s, map[string]any{"action": "direct", "agent_id": writeTestGroupID, "purpose_key": "review", "key": "stable"})
			if (err == nil) != supported {
				t.Fatalf("capability result=%v", err)
			}
			writes := 0
			for _, r := range srv.Requests() {
				if r.Method == "POST" {
					writes++
					if r.Path != "/v1/groups/direct" || r.Header.Get("Idempotency-Key") != "stable" {
						t.Fatal("wrong mutation")
					}
					body := writeTestDecodeBody(t, r.Body)
					if body["purpose_key"] != "review" || body["agent_id"] != writeTestGroupID {
						t.Fatal("wrong direct body")
					}
				}
			}
			if supported && writes != 1 || !supported && writes != 0 {
				t.Fatal("capability did not gate mutation")
			}
		})
	}
}

func TestWorkingGroupsScopedInboxAck(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"working_groups"}})
	})
	srv.Handle("GET", "/v1/inbox", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("group_id") != writeTestGroupID {
			t.Error("unscoped inbox")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"group_id": writeTestGroupID, "items": []any{}, "acknowledge_through": 3})
	})
	s := writeTestSession(t, srv)
	if _, err := readToolsInbox(context.Background(), s, map[string]any{"group_id": writeTestGroupID}); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAckHandle(context.Background(), s, map[string]any{"group_id": writeTestGroupID, "through": float64(3), "key": "stable"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		if r.Path == "/v1/inbox/ack" {
			body := writeTestDecodeBody(t, r.Body)
			if body["group_id"] != writeTestGroupID || body["through"] != float64(3) || r.Header.Get("Idempotency-Key") != "stable" {
				t.Fatal("ack scope/key mismatch")
			}
		}
	}
}

func TestCompactListWorkingGroupTruncation(t *testing.T) {
	c := OpenCache(t.TempDir(), "terminal")
	scope := []string{"origin", "agent", "workspace"}
	items := []any{}
	for i := 1; i <= 40; i++ {
		items = append(items, map[string]any{"id": fmt.Sprint(i), "seq": float64(i), "kind": "finding", "body": strings.Repeat("x", 320)})
	}
	value := map[string]any{"items": items, "acknowledged_through": float64(0), "acknowledge_through": float64(40)}
	a, receiptsA := compactList("ajent_inbox", map[string]any{"group_id": "group-a"}, value, c, scope)
	c.Remember(receiptsA...)
	b, receiptsB := compactList("ajent_inbox", map[string]any{"group_id": "group-b"}, value, c, scope)
	if a["truncated"] != true || b["truncated"] != true || len(receiptsA) == 0 || len(receiptsA) != len(receiptsB) {
		t.Fatal("truncated groups differ")
	}
	if a["acknowledge_through"] != b["acknowledge_through"] || a["acknowledge_through"] == float64(40) {
		t.Fatal("cursor escaped compacted delivery")
	}
	for i := range receiptsA {
		if receiptsA[i] == receiptsB[i] {
			t.Fatal("compacted receipt crossed group scope")
		}
	}
	if len(scope) != 3 {
		t.Fatal("shared scope mutated")
	}
}

func TestWorkingGroupsValidatedDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, tool  string
		args        map[string]any
		probeStatus int
		caps        []string
		wantProbe   bool
	}{
		{name: "invalid action", tool: "ajent_groups", args: map[string]any{"action": "wake"}},
		{name: "invalid feed group type", tool: "ajent_feed", args: map[string]any{"group_id": true}},
		{name: "empty feed group", tool: "ajent_feed", args: map[string]any{"group_id": ""}},
		{name: "unsupported feed scope", tool: "ajent_feed", args: map[string]any{"group_id": writeTestGroupID}, probeStatus: 200, caps: []string{}, wantProbe: true},
		{name: "invalid group type", tool: "ajent_inbox", args: map[string]any{"group_id": true}},
		{name: "unknown ack field", tool: "ajent_ack", args: map[string]any{"through": float64(3), "group_id": writeTestGroupID, "global": true}},
		{name: "unsupported scope", tool: "ajent_inbox", args: map[string]any{"group_id": writeTestGroupID}, probeStatus: 200, caps: []string{}, wantProbe: true},
		{name: "probe failure", tool: "ajent_ack", args: map[string]any{"through": float64(3), "group_id": writeTestGroupID}, probeStatus: 503, caps: []string{}, wantProbe: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeserver.New(t)
			srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "3")
				status := tc.probeStatus
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": tc.caps})
			})
			d := &dispatcher{s: writeTestSession(t, srv)}
			result, after, err := d.CallTool(context.Background(), serveTestCallParams(t, tc.tool, tc.args))
			if err != nil {
				t.Fatal(err)
			}
			if !serveTestIsError(t, result) || after != nil {
				t.Fatal("invalid or unsupported call succeeded")
			}
			probes := 0
			for _, r := range srv.Requests() {
				if r.Path != "/v1/reliability" {
					t.Fatal("rejected dispatch sent operation")
				}
				probes++
			}
			if tc.wantProbe && probes == 0 || !tc.wantProbe && probes != 0 {
				t.Fatal("unexpected capability probe count")
			}
		})
	}
}

func TestWorkingGroupsCanonicalScopeDispatch(t *testing.T) {
	for _, raw := range []string{writeTestGroupID, strings.ToUpper(writeTestGroupID), strings.ReplaceAll(writeTestGroupID, "-", ""), "{" + writeTestGroupID + "}", "urn:uuid:" + writeTestGroupID} {
		t.Run(raw, func(t *testing.T) {
			srv := fakeserver.New(t)
			srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"working_groups"}})
			})
			srv.Handle("GET", "/v1/inbox", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("group_id") != writeTestGroupID {
					t.Error("noncanonical query scope")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "post", "kind": "finding", "seq": 1}}, "acknowledge_through": 1})
			})
			s := writeTestSession(t, srv)
			s.Cache = OpenCache(t.TempDir(), "terminal")
			d := &dispatcher{s: s}
			for _, tool := range []string{"ajent_inbox", "ajent_ack"} {
				args := map[string]any{"group_id": raw}
				if tool == "ajent_ack" {
					args["through"] = float64(1)
					args["key"] = "stable"
				}
				result, after, err := d.CallTool(context.Background(), serveTestCallParams(t, tool, args))
				if err != nil {
					t.Fatal(err)
				}
				if serveTestIsError(t, result) {
					t.Fatal("valid canonicalizable scope rejected")
				}
				if after != nil {
					after()
				}
				if tool == "ajent_inbox" {
					repeatResult, _, err := d.CallTool(context.Background(), serveTestCallParams(t, tool, map[string]any{"group_id": writeTestGroupID}))
					if err != nil {
						t.Fatal(err)
					}
					if serveTestIsError(t, repeatResult) {
						t.Fatal("canonical repeat failed")
					}
					var envelope struct {
						Data struct {
							Items []any `json:"items"`
						} `json:"data"`
					}
					if err := json.Unmarshal([]byte(serveTestContentText(t, repeatResult)), &envelope); err != nil {
						t.Fatal(err)
					}
					if len(envelope.Data.Items) != 0 {
						t.Fatal("equivalent UUID forms used different receipt scopes")
					}
				}
			}
			found := false
			for _, r := range srv.Requests() {
				if r.Path == "/v1/inbox/ack" {
					found = true
					body := writeTestDecodeBody(t, r.Body)
					if body["group_id"] != writeTestGroupID || r.Header.Get("Idempotency-Key") != "stable" {
						t.Fatal("ack scope or key changed")
					}
				}
			}
			if !found {
				t.Fatal("ack missing")
			}
		})
	}
}

func TestWorkingGroupsFeedRecoveryDispatch(t *testing.T) {
	const otherGroup = "00000000-0000-4000-8000-000000000004"
	srv := fakeserver.New(t)
	srv.Handle("GET", "/v1/reliability", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": []string{"working_groups"}})
	})
	for _, group := range []string{writeTestGroupID, otherGroup} {
		srv.Handle("GET", "/v1/groups/"+group+"/posts", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("before") != "next+page" {
				t.Error("missing feed cursor")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"posts": []any{map[string]any{"id": "same-post", "kind": "finding", "body": "checked"}}})
		})
	}
	s := writeTestSession(t, srv)
	s.Cache = OpenCache(t.TempDir(), "terminal")
	d := &dispatcher{s: s}
	for _, tc := range []struct {
		group string
		want  int
	}{
		{"", 1}, // Legacy credential-default feed receipt cannot suppress selected scope.
		{"{" + writeTestGroupID + "}", 1},
		{writeTestGroupID, 0}, // Equivalent UUID forms share selected-group receipts.
		{otherGroup, 1},
	} {
		args := map[string]any{"cursor": "next+page"}
		if tc.group != "" {
			args["group_id"] = tc.group
		}
		result, after, err := d.CallTool(context.Background(), serveTestCallParams(t, "ajent_feed", args))
		if err != nil {
			t.Fatal(err)
		}
		if serveTestIsError(t, result) {
			t.Fatal("feed recovery failed")
		}
		var envelope struct {
			Data struct {
				Posts []any `json:"posts"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(serveTestContentText(t, result)), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data.Posts) != tc.want {
			t.Fatalf("group=%q previews=%d want=%d", tc.group, len(envelope.Data.Posts), tc.want)
		}
		if after != nil {
			after()
		}
	}
	for _, r := range srv.Requests() {
		if r.Path != "/v1/reliability" && r.Path != "/v1/groups/"+writeTestGroupID+"/posts" && r.Path != "/v1/groups/"+otherGroup+"/posts" {
			t.Fatal("unexpected feed route")
		}
	}
}
