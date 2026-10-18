// SPDX-License-Identifier: Apache-2.0

package api

import (
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name string
		h    string
		want *int
	}{
		{"empty gives nil", "", nil},
		{"digits give seconds", "5", intPtr(5)},
		{"zero digits", "0", intPtr(0)},
		{"http date 10s ahead gives ceil+1", now.Add(10 * time.Second).UTC().Format(http1123), intPtr(11)},
		{"http date in the past gives 1", now.Add(-1 * time.Hour).UTC().Format(http1123), intPtr(1)},
		{"garbage gives 60", "not-a-value", intPtr(60)},
		{"very large digits capped at 86400", "999999999", intPtr(86400)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.h, now)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tt.h, dref(got), dref(tt.want))
			}
			if got != nil && *got != *tt.want {
				t.Errorf("parseRetryAfter(%q) = %d, want %d", tt.h, *got, *tt.want)
			}
		})
	}
}

// http1123 is the reference layout for an HTTP-date header value.
const http1123 = "Mon, 02 Jan 2006 15:04:05 GMT"

func dref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestRetryableAllowlist(t *testing.T) {
	const uuid = "00000000-0000-4000-8000-000000000001"

	tests := []struct {
		name   string
		method string
		route  string
		key    string
		want   bool
	}{
		{"PATCH group with key", "PATCH", "/v1/groups/" + uuid, "k", true},
		{"PATCH group without key", "PATCH", "/v1/groups/" + uuid, "", false},
		{"PUT group owner with key", "PUT", "/v1/groups/" + uuid + "/owner", "k", true},
		{"PUT group owner without key", "PUT", "/v1/groups/" + uuid + "/owner", "", false},
		{"PUT group pin with key", "PUT", "/v1/groups/" + uuid + "/pins/" + uuid, "k", true},
		{"PUT group pin without key", "PUT", "/v1/groups/" + uuid + "/pins/" + uuid, "", false},
		{"PUT question resolution with key", "PUT", "/v1/questions/" + uuid + "/resolution", "k", true},
		{"PUT question resolution without key", "PUT", "/v1/questions/" + uuid + "/resolution", "", false},
		{"DELETE group membership with key", "DELETE", "/v1/groups/" + uuid + "/membership", "k", true},
		{"DELETE group membership without key", "DELETE", "/v1/groups/" + uuid + "/membership", "", false},
		{"DELETE group pin with key", "DELETE", "/v1/groups/" + uuid + "/pins/" + uuid, "k", true},
		{"DELETE group pin without key", "DELETE", "/v1/groups/" + uuid + "/pins/" + uuid, "", false},
		{"POST goals with key", "POST", "/v1/goals", "k", true},
		{"POST goals without key", "POST", "/v1/goals", "", false},
		{"POST goal events with key", "POST", "/v1/goals/" + uuid + "/events", "k", true},
		{"POST goal events without key", "POST", "/v1/goals/" + uuid + "/events", "", false},
		{"POST answer validations with key", "POST", "/v1/answers/" + uuid + "/validations", "k", true},
		{"POST answer validations without key", "POST", "/v1/answers/" + uuid + "/validations", "", false},
		{"POST posts with key", "POST", "/v1/posts", "k", true},
		{"POST posts without key", "POST", "/v1/posts", "", false},
		{"POST improvements with key", "POST", "/v1/improvements", "k", true},
		{"POST improvements without key", "POST", "/v1/improvements", "", false},
		{"POST verify suffix with key", "POST", "/v1/improvements/" + uuid + "/verify", "k", true},
		{"POST verify suffix without key", "POST", "/v1/improvements/" + uuid + "/verify", "", false},
		{"GET is always safe, no key", "GET", "/v1/me", "", true},
		{"GET arbitrary route, no key", "GET", "/v1/anything/goes", "", true},
		{"HEAD is always safe, no key", "HEAD", "/v1/me", "", true},
		{"DELETE post prefix, no key", "DELETE", "/v1/posts/" + uuid, "", true},
		{"DELETE post prefix without trailing segment", "DELETE", "/v1/posts/", "", true},
		{"POST inbox ack, no key", "POST", "/v1/inbox/ack", "", true},

		// Negative cases outside the allowlist entirely.
		{"POST arbitrary route is unsafe", "POST", "/v1/fleet", "k", false},
		{"POST signup is unsafe", "POST", "/v1/signup", "", false},
		{"DELETE group (not membership or pins) is unsafe", "DELETE", "/v1/groups/" + uuid, "k", false},
		{"PUT arbitrary route is unsafe", "PUT", "/v1/something", "k", false},
		{"PATCH without matching uuid shape is unsafe", "PATCH", "/v1/groups/not-a-uuid", "k", false},

		// Query string is stripped before matching.
		{"query string stripped, allowlisted", "GET", "/v1/me?x=1", "", true},
		{"query string stripped, requires_key still enforced", "POST", "/v1/posts?x=1", "", false},
		{"query string stripped, requires_key satisfied", "POST", "/v1/posts?x=1", "k", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Retryable(tt.method, tt.route, tt.key); got != tt.want {
				t.Errorf("Retryable(%q, %q, %q) = %v, want %v", tt.method, tt.route, tt.key, got, tt.want)
			}
		})
	}
}

func TestBackoffBounded(t *testing.T) {
	for attempt := 0; attempt < 6; attempt++ {
		for i := 0; i < 20; i++ {
			d := backoff(attempt)
			if d < 0 || d > 2*time.Second {
				t.Fatalf("backoff(%d) = %v, out of [0, 2s]", attempt, d)
			}
		}
	}
}
