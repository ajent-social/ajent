// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/version"
)

// captureFailures counts Observe calls that could not record locally (lock
// contention, disk errors), process-local only. It is reported as
// "capture_failures_process" by Local (docs/contract.md section 12).
var captureFailures atomic.Int64

// feedbackObserver adapts a Store to api.Observer.
type feedbackObserver struct{ store *Store }

// Observer returns an api.Observer that records bounded, content-free
// evidence about failed requests locally, gated on the routes and methods
// docs/contract.md section 12 lists. It never blocks: capture uses a
// non-waiting lock, and any failure (including lock contention) only
// increments the process-local captureFailures counter.
func (s *Store) Observer() api.Observer {
	return &feedbackObserver{store: s}
}

// Observe implements api.Observer.
func (o *feedbackObserver) Observe(obs api.Observation) {
	if !feedbackCaptureEligible(obs) {
		return
	}

	err := o.store.with(0, func(st *state) error {
		st.Counters["logical_operations"]++

		if obs.Err == nil {
			st.Counters["successes"]++
			if obs.RouteTemplate == "/v1/search" && feedbackEmptySearchResult(obs.Result) {
				st.Counters["empty_search_observations"]++
			}
			if obs.Attempts > 1 {
				st.Counters["successful_after_retry"]++
			}
			return nil
		}

		st.Counters["logical_failures"]++
		st.Counters["candidates"]++

		id, genErr := feedbackUUIDv4()
		if genErr != nil {
			return genErr
		}
		st.Records = append(st.Records, record{
			"evidence_id": id,
			"created":     unixFloatSeconds(o.store.clock()),
			"scope":       feedbackScope(obs.Credential),
			"evidence": map[string]any{
				"logical_operation_id": id,
				"route":                obs.RouteTemplate,
				"method":               obs.Method,
				"error_code":           obs.Err.Code,
				"status":               obs.Err.Status,
				"request_id":           obs.Err.RequestID,
				"client_version":       version.Version,
				"attempt_count":        obs.Attempts,
				"observed_at":          o.store.clock().UTC().Format(time.RFC3339),
			},
		})
		return nil
	})
	if err != nil {
		captureFailures.Add(1)
	}
}

// feedbackCaptureEligible reports whether obs is a candidate for capture:
// it has a route template, its method is GET, POST, DELETE or HEAD, and its
// template does not contain "/improvements" (docs/contract.md section 12).
func feedbackCaptureEligible(obs api.Observation) bool {
	if obs.RouteTemplate == "" {
		return false
	}
	switch obs.Method {
	case "GET", "POST", "DELETE", "HEAD":
	default:
		return false
	}
	return !strings.Contains(obs.RouteTemplate, "/improvements")
}

// feedbackEmptySearchResult reports whether result is a decoded response
// whose "posts" field is an empty array. internal/api hands the Observer a
// map[string]any (the dereferenced *map[string]any it decoded into), but
// this also accepts a *map[string]any defensively.
func feedbackEmptySearchResult(result any) bool {
	var m map[string]any
	switch v := result.(type) {
	case map[string]any:
		m = v
	case *map[string]any:
		if v == nil {
			return false
		}
		m = *v
	default:
		return false
	}

	posts, ok := m["posts"]
	if !ok {
		return false
	}
	arr, ok := posts.([]any)
	return ok && len(arr) == 0
}

// feedbackScope returns the scope identity hex(sha256(server + "\x00" +
// token)) for cred (docs/contract.md section 12).
func feedbackScope(cred credentials.Credential) string {
	sum := sha256.Sum256([]byte(cred.Server + "\x00" + cred.Token))
	return hex.EncodeToString(sum[:])
}
