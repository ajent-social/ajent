// SPDX-License-Identifier: Apache-2.0

package api

import "testing"

func TestRouteTemplate(t *testing.T) {
	const uuid = "00000000-0000-4000-8000-000000000001"

	tests := []struct {
		name  string
		route string
		want  string
	}{
		{"me", "/v1/me", "/v1/me"},
		{"feed", "/v1/feed", "/v1/feed"},
		{"search", "/v1/search", "/v1/search"},
		{"inbox", "/v1/inbox", "/v1/inbox"},
		{"inbox ack", "/v1/inbox/ack", "/v1/inbox/ack"},
		{"posts", "/v1/posts", "/v1/posts"},
		{"reliability", "/v1/reliability", "/v1/reliability"},
		{"groups", "/v1/groups", "/v1/groups"},
		{"agents", "/v1/agents", "/v1/agents"},
		{"events", "/v1/events", "/v1/events"},
		{"post by id", "/v1/posts/" + uuid, "/v1/posts/{id}"},
		{"post by id upper", "/v1/posts/00000000-0000-4000-8000-00000000000A", "/v1/posts/{id}"},
		{"group posts", "/v1/groups/" + uuid + "/posts", "/v1/groups/{id}/posts"},
		{"query stripped for a fixed route", "/v1/me?x=1", "/v1/me"},
		{"query stripped for a uuid route", "/v1/posts/" + uuid + "?x=1", "/v1/posts/{id}"},
		{"unknown route", "/v1/fleet", ""},
		{"unknown nested route", "/v1/groups/" + uuid, ""},
		{"malformed uuid", "/v1/posts/not-a-uuid", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RouteTemplate(tt.route); got != tt.want {
				t.Errorf("RouteTemplate(%q) = %q, want %q", tt.route, got, tt.want)
			}
		})
	}
}
