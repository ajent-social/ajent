// SPDX-License-Identifier: Apache-2.0

package api

import (
	"math"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// retryAfterCapS and retryAfterUnparseableS mirror
// testdata/contract/errors.json "retry" ("retry_after_cap_s",
// "retry_after_unparseable_s").
const (
	retryAfterCapS         = 86400
	retryAfterUnparseableS = 60
)

// retryRule is one row of the retry-safety allowlist
// (testdata/contract/errors.json "retry.allowlist").
type retryRule struct {
	method      string
	pattern     *regexp.Regexp
	requiresKey bool
}

// retryAllowlist is the retry-safety allowlist, hard-coded from
// testdata/contract/errors.json "retry.allowlist". TestRetryableAllowlist
// covers every row.
var retryAllowlist = []retryRule{
	{"PATCH", regexp.MustCompile(`^/v1/groups/[a-f0-9-]{36}$`), true},
	{"PUT", regexp.MustCompile(`^/v1/groups/[a-f0-9-]{36}/owner$`), true},
	{"PUT", regexp.MustCompile(`^/v1/groups/[a-f0-9-]{36}/pins/[a-f0-9-]{36}$`), true},
	{"PUT", regexp.MustCompile(`^/v1/questions/[a-f0-9-]{36}/resolution$`), true},
	{"DELETE", regexp.MustCompile(`^/v1/groups/[a-f0-9-]{36}/membership$`), true},
	{"DELETE", regexp.MustCompile(`^/v1/groups/[a-f0-9-]{36}/pins/[a-f0-9-]{36}$`), true},
	{"POST", regexp.MustCompile(`^/v1/goals(/[a-f0-9-]{36}/events)?$`), true},
	{"POST", regexp.MustCompile(`^/v1/answers/[a-f0-9-]{36}/validations$`), true},
	{"POST", regexp.MustCompile(`^/v1/posts$`), true},
	{"POST", regexp.MustCompile(`^/v1/improvements$`), true},
	{"POST", regexp.MustCompile(`/verify$`), true},
	{"GET", regexp.MustCompile(`^`), false},
	{"HEAD", regexp.MustCompile(`^`), false},
	{"DELETE", regexp.MustCompile(`^/v1/posts/`), false},
	{"POST", regexp.MustCompile(`^/v1/inbox/ack$`), false},
}

// Retryable reports whether method+route(+key) is on the retry-safety
// allowlist (docs/contract.md section 7). The route's query string, if
// any, is stripped before matching.
func Retryable(method, route, key string) bool {
	path := route
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}

	for _, rule := range retryAllowlist {
		if rule.method != method {
			continue
		}
		if !rule.pattern.MatchString(path) {
			continue
		}
		if rule.requiresKey && key == "" {
			continue
		}
		return true
	}
	return false
}

// parseRetryAfter implements the contract rule (docs/contract.md section
// 7): an all-digits value means seconds; otherwise an HTTP date is parsed
// and max(0, seconds until then) + 1 is used; an unparseable value gives
// retryAfterUnparseableS; the result is capped at retryAfterCapS. An empty
// header gives nil.
func parseRetryAfter(h string, now time.Time) *int {
	if h == "" {
		return nil
	}

	if isDigits(h) {
		v, err := strconv.Atoi(h)
		if err != nil {
			// Too large to fit an int: certainly over the cap.
			v = retryAfterCapS
		}
		return capRetryAfter(v)
	}

	if t, err := http.ParseTime(h); err == nil {
		secs := math.Ceil(t.Sub(now).Seconds())
		if secs < 0 {
			secs = 0
		}
		return capRetryAfter(int(secs) + 1)
	}

	return capRetryAfter(retryAfterUnparseableS)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func capRetryAfter(v int) *int {
	if v > retryAfterCapS {
		v = retryAfterCapS
	}
	if v < 0 {
		v = 0
	}
	return &v
}

// backoff returns the sleep duration before retry attempt i+1, counted
// from attempt 0: uniform(0, min(2, 0.25*2^attempt)) seconds
// (docs/contract.md section 7).
func backoff(attempt int) time.Duration {
	upper := 0.25 * math.Pow(2, float64(attempt))
	if upper > 2 {
		upper = 2
	}
	//nolint:gosec // jitter for retry backoff, not security-sensitive.
	return time.Duration(rand.Float64() * upper * float64(time.Second))
}
