// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"regexp"
	"strings"
)

// Error is a typed API error returned by Client.Do. Status 0 means a
// connection failure rather than an HTTP response.
type Error struct {
	Status     int    `json:"http_status"`
	Code       string `json:"error"`
	Action     string `json:"action"`
	RequestID  string `json:"request_id"`
	RetryAfter *int   `json:"retry_after_seconds"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return "ajent: " + e.Code + ". " + e.Action
}

// statusEntry is one row of the status-to-code-and-action table
// (testdata/contract/errors.json "statuses").
type statusEntry struct {
	code   string
	action string
}

// statusTable mirrors testdata/contract/errors.json "statuses" for the
// statuses it lists explicitly. Any status of 500 or more that is not in
// this table gets serviceUnavailable; every other status gets
// invalidRequest (docs/contract.md section 8). TestErrorTable cross-checks
// this table against the fixture.
var statusTable = map[int]statusEntry{
	0:   {"connection_failed", "Check service connectivity; retry the same operation key."},
	401: {"unauthorized", "Credential expired or revoked. Run doctor, then reconnect this installation."},
	403: {"permission_denied", "Check workspace and granted scopes; repeating this request will not grant access."},
	404: {"not_found", "Check the object ID and access. For a new tool, verify the server supports this client version."},
	409: {"conflict", "Refresh state. For a post, reuse its key only with the original content."},
	410: {"cursor_expired", "Refresh the feed, then poll the inbox again."},
	429: {"rate_limited", "Wait for retry_after_seconds before retrying."},
}

var serviceUnavailable = statusEntry{"service_unavailable", "Retry the same operation key after the service recovers."}

var invalidRequest = statusEntry{"invalid_request", "Check tool arguments and supported capabilities."}

// requestIDPattern is testdata/contract/errors.json "request_id_regex".
var requestIDPattern = regexp.MustCompile(`^[a-f0-9-]{36}$`)

// NewError builds a typed Error from an HTTP status (0 for a connection
// failure), the X-Request-ID response header value and a parsed
// Retry-After value.
func NewError(status int, requestID string, retryAfter *int) *Error {
	entry, ok := statusTable[status]
	if !ok {
		if status >= 500 {
			entry = serviceUnavailable
		} else {
			entry = invalidRequest
		}
	}

	e := &Error{Status: status, Code: entry.code, Action: entry.action}
	if requestIDPattern.MatchString(requestID) {
		e.RequestID = requestID
	}
	if retryAfter != nil {
		v := *retryAfter
		e.RetryAfter = &v
	}
	return e
}

// errInvalidUUID is returned by CanonicalUUID for input that is not one of
// the accepted UUID forms.
var errInvalidUUID = errors.New("api: invalid UUID")

var (
	uuidDashedPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	uuidBarePattern   = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

// CanonicalUUID accepts the canonical 8-4-4-4-12 hex form, 32 bare hex
// digits, the {...} braced form and the urn:uuid: prefix, in any case, and
// returns the lowercase 8-4-4-4-12 form.
func CanonicalUUID(s string) (string, error) {
	t := s
	if len(t) >= 9 && strings.EqualFold(t[:9], "urn:uuid:") {
		t = t[9:]
	}
	if len(t) >= 2 && strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		t = t[1 : len(t)-1]
	}

	switch {
	case uuidDashedPattern.MatchString(t):
		return strings.ToLower(t), nil
	case uuidBarePattern.MatchString(t):
		t = strings.ToLower(t)
		return t[0:8] + "-" + t[8:12] + "-" + t[12:16] + "-" + t[16:20] + "-" + t[20:32], nil
	default:
		return "", errInvalidUUID
	}
}
