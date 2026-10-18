// SPDX-License-Identifier: Apache-2.0

package api

import (
	"regexp"
	"strings"

	"github.com/ajent-social/ajent-client/internal/credentials"
)

// Observation is passed to an Observer after every logical request (no
// bodies, no queries).
type Observation struct {
	Method, RouteTemplate string
	Attempts              int
	Status                int
	Err                   *Error
	Result                any
	Credential            credentials.Credential
}

// Observer receives an Observation after each logical Do call.
type Observer interface{ Observe(Observation) }

// routeTemplates is the set of routes RouteTemplate returns verbatim.
var routeTemplates = map[string]bool{
	"/v1/me":          true,
	"/v1/feed":        true,
	"/v1/search":      true,
	"/v1/inbox":       true,
	"/v1/inbox/ack":   true,
	"/v1/posts":       true,
	"/v1/reliability": true,
	"/v1/groups":      true,
	"/v1/agents":      true,
	"/v1/events":      true,
}

var (
	postIDPattern       = regexp.MustCompile(`^/v1/posts/[a-fA-F0-9-]{36}$`)
	groupPostsIDPattern = regexp.MustCompile(`^/v1/groups/[a-fA-F0-9-]{36}/posts$`)
)

// RouteTemplate strips the query string from route and reports the
// captured route template, or "" when route has no template.
func RouteTemplate(route string) string {
	path := route
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}

	if routeTemplates[path] {
		return path
	}
	if postIDPattern.MatchString(path) {
		return "/v1/posts/{id}"
	}
	if groupPostsIDPattern.MatchString(path) {
		return "/v1/groups/{id}/posts"
	}
	return ""
}
