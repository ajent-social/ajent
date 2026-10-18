// SPDX-License-Identifier: Apache-2.0

package feedback

// errFeedbackAction is returned by Local for any action other than show,
// preview, stats, set, clear or revoke (docs/contract.md section 12;
// testdata/contract/commands.json "feedback"/"bad_action" fixes this exact
// wording). cmd/ajent/cmd_feedback.go validates the action itself before
// calling Local and returns cli.UserError with the identical text, so the
// CLI output matches the fixture byte for byte even though this package
// cannot import internal/cli (docs/interfaces.md's package import graph).
var errFeedbackAction = feedbackMessage("Use show, set, revoke, preview or clear.")

// feedbackPolicyFields lists the fields disclosed to the user when they
// grant standing feedback permission (docs/contract.md section 12).
var feedbackPolicyFields = []string{
	"registered_route",
	"method",
	"error_category",
	"status",
	"request_id",
	"client_version",
	"attempt_count",
	"timestamps",
	"explicit_context",
}

// Local implements the local feedback-store actions: show, preview, stats,
// set, clear and revoke (docs/contract.md section 12).
func (s *Store) Local(action string) (any, error) {
	switch action {
	case "show", "preview", "stats", "set", "clear", "revoke":
	default:
		return nil, errFeedbackAction
	}

	var result map[string]any
	err := s.with(feedbackCommandLockTimeout, func(st *state) error {
		switch action {
		case "set":
			st.Policy = map[string]any{
				"version":     1,
				"destination": "Ajent product maintainers",
				"granted_at":  unixFloatSeconds(s.clock()),
				"revoked":     false,
				"fields":      feedbackPolicyFields,
			}
		case "revoke":
			if st.Policy == nil {
				st.Policy = map[string]any{}
			}
			st.Policy["revoked"] = true
		case "clear":
			st.Records = []record{}
		}

		pending := 0
		for _, r := range st.Records {
			if _, ok := r["result"]; !ok {
				pending++
			}
		}

		counters := make(map[string]int, len(st.Counters)+1)
		for k, v := range st.Counters {
			counters[k] = v
		}
		counters["capture_failures_process"] = int(captureFailures.Load())

		result = map[string]any{
			"policy":        st.Policy,
			"counters":      counters,
			"pending_count": pending,
		}
		if action == "preview" {
			result["records"] = st.Records
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
