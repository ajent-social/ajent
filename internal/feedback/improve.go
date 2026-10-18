// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/credentials"
)

const (
	// feedbackContextFieldMax bounds attempted_job, expected, observed
	// and workaround (docs/contract.md section 12).
	feedbackContextFieldMax = 1000
	// feedbackWorkaroundMinutesMax bounds workaround_minutes.
	feedbackWorkaroundMinutesMax = 10080
	// feedbackPayloadMaxBytes bounds a frozen report payload.
	feedbackPayloadMaxBytes = 16384
	// feedbackVerificationEvidenceMax bounds verification evidence.
	feedbackVerificationEvidenceMax = 1000
)

// feedbackContextFields lists the report context fields, in the order
// docs/contract.md section 12 and the Python reference client use.
var feedbackContextFields = []string{"attempted_job", "expected", "observed", "workaround"}

// Validation errors: each is a genuine client-side mistake, equivalent to
// the Python reference's raised ValueError, and is returned as a Go error
// rather than a submission_state result.
var (
	errFeedbackInvalidAction              = errors.New("feedback: invalid improvement action")
	errFeedbackInvalidCategory            = errors.New("feedback: invalid category")
	errFeedbackInvalidContext             = errors.New("feedback: context fields must be strings up to 1000 characters")
	errFeedbackInvalidMinutes             = errors.New("feedback: invalid workaround_minutes")
	errFeedbackFeatureNeedsContext        = errors.New("feedback: feature requests require attempted_job and expected")
	errFeedbackEvidenceNotFound           = errors.New("feedback: evidence not found; preview the local buffer")
	errFeedbackPayloadTooLarge            = errors.New("feedback: submission exceeds 16 KiB")
	errFeedbackReportIDRequired           = errors.New("feedback: report_id is required")
	errFeedbackInvalidOutcome             = errors.New("feedback: invalid verification outcome")
	errFeedbackVerificationEvidenceTooBig = errors.New("feedback: verification evidence exceeds 1000 characters")
)

// HelpImprove implements the report, status, verify and withdraw flows
// docs/contract.md section 12 describes (docs/tasks/T2.11.md step 4).
func (s *Store) HelpImprove(ctx context.Context, c *api.Client, cred credentials.Credential, args map[string]any) (any, error) {
	action := "report"
	if v, present := args["action"]; present {
		str, ok := v.(string)
		if !ok {
			return nil, errFeedbackInvalidAction
		}
		action = str
	}
	switch action {
	case "report", "status", "verify", "withdraw":
	default:
		return nil, errFeedbackInvalidAction
	}

	scope := feedbackScope(cred)

	if action == "report" {
		return s.helpImproveReport(ctx, c, cred, scope, args)
	}
	return s.helpImproveManage(ctx, c, cred, scope, action, args)
}

// feedbackCapabilities looks up the server's advertised capabilities
// leniently: any error (including a capability-check-shaped failure) means
// an empty list, matching the Python reference's broad except clause
// (docs/tasks/T2.11.md step 4, "Capability lookup is lenient").
func feedbackCapabilities(ctx context.Context, c *api.Client, cred credentials.Credential) []string {
	var body struct {
		Capabilities []string `json:"capabilities"`
	}
	req := api.Request{
		Method:    "GET",
		Route:     "/v1/reliability",
		Attempts:  1,
		Timeout:   3 * time.Second,
		NoObserve: true,
	}
	if err := c.Do(ctx, cred, req, &body); err != nil {
		return nil
	}
	return body.Capabilities
}

// feedbackHasCapability reports whether name is present in caps.
func feedbackHasCapability(caps []string, name string) bool {
	for _, c := range caps {
		if c == name {
			return true
		}
	}
	return false
}

// feedbackPermitted reports whether policy grants standing send permission:
// version 1 and not revoked, defaulting revoked to true (not permitted)
// when the field is absent, matching the Python reference's
// policy.get('revoked', True) (docs/contract.md section 12).
func feedbackPermitted(policy map[string]any) bool {
	if policy == nil {
		return false
	}
	v, ok := feedbackAsNumber(policy["version"])
	if !ok || v != 1 {
		return false
	}
	revoked := true
	if r, present := policy["revoked"]; present {
		if b, ok := r.(bool); ok {
			revoked = b
		}
	}
	return !revoked
}

// feedbackAsNumber normalizes a decoded JSON number (float64, from a
// loaded state file), a Go int (freshly set in this process) or a
// json.Number (from a --json CLI argument decoded with UseNumber) to a
// float64.
func feedbackAsNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// feedbackDiagnostic renders e's JSON fields (docs/contract.md section 8)
// as a map, so they can be merged into a submission_state result.
func feedbackDiagnostic(e *api.Error) map[string]any {
	data, err := json.Marshal(e)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// feedbackRecordByID returns the record in records with the given scope and
// evidence_id, or nil.
func feedbackRecordByID(records []record, scope, id string) record {
	for _, r := range records {
		if s, _ := r["scope"].(string); s != scope {
			continue
		}
		if eid, _ := r["evidence_id"].(string); eid == id {
			return r
		}
	}
	return nil
}

// feedbackPendingReportRecord returns the first record in this scope with
// no result and no verification_payload (a report draft, not a
// verification), or nil.
func feedbackPendingReportRecord(records []record, scope string) record {
	for _, r := range records {
		if s, _ := r["scope"].(string); s != scope {
			continue
		}
		if _, has := r["result"]; has {
			continue
		}
		if _, has := r["verification_payload"]; has {
			continue
		}
		return r
	}
	return nil
}

// helpImproveReport implements the "report" action (docs/tasks/T2.11.md
// step 4; docs/contract.md section 12).
func (s *Store) helpImproveReport(ctx context.Context, c *api.Client, cred credentials.Credential, scope string, args map[string]any) (any, error) {
	category, err := feedbackCategory(args)
	if err != nil {
		return nil, err
	}
	context, err := feedbackContext(args)
	if err != nil {
		return nil, err
	}
	minutes, err := feedbackWorkaroundMinutes(args)
	if err != nil {
		return nil, err
	}
	if category == "feature" && (context["attempted_job"] == "" || context["expected"] == "") {
		return nil, errFeedbackFeatureNeedsContext
	}

	var (
		outcome      any
		payload      map[string]any
		occurrenceID string
	)
	err = s.with(feedbackCommandLockTimeout, func(st *state) error {
		var rec record
		if evidenceID, ok := args["evidence_id"].(string); ok && evidenceID != "" {
			rec = feedbackRecordByID(st.Records, scope, evidenceID)
			if rec == nil {
				return errFeedbackEvidenceNotFound
			}
		} else {
			rec = feedbackPendingReportRecord(st.Records, scope)
		}

		if rec == nil {
			if context["attempted_job"] == "" {
				outcome = map[string]any{
					"submission_state": "empty",
					"next_action":      "Continue your task; no captured friction needs reporting.",
				}
				return nil
			}
			id, genErr := feedbackUUIDv4()
			if genErr != nil {
				return genErr
			}
			rec = record{
				"evidence_id": id,
				"created":     unixFloatSeconds(s.clock()),
				"scope":       scope,
				"evidence":    map[string]any{},
			}
			st.Records = append(st.Records, rec)
		}

		if result, ok := rec["result"]; ok {
			outcome = result
			return nil
		}

		evidenceID, _ := rec["evidence_id"].(string)

		pl, ok := rec["payload"].(map[string]any)
		if !ok {
			evidence, _ := rec["evidence"].(map[string]any)
			pl = map[string]any{
				"occurrence_id":      evidenceID,
				"policy_version":     1,
				"category":           category,
				"evidence":           evidence,
				"attempted_job":      context["attempted_job"],
				"expected":           context["expected"],
				"observed":           context["observed"],
				"workaround":         context["workaround"],
				"workaround_minutes": minutes,
			}
			rec["payload"] = pl
		}

		if feedbackReportChanged(args, category, minutes, pl) {
			outcome = map[string]any{
				"submission_state": "queued_original",
				"evidence_id":      evidenceID,
				"next_action":      "The original draft is frozen for safe replay. Retry without changed context, or inspect and clear the draft before creating a new occurrence.",
			}
			return nil
		}

		size, sizeErr := feedbackJSONSize(pl)
		if sizeErr != nil {
			return sizeErr
		}
		if size > feedbackPayloadMaxBytes {
			return errFeedbackPayloadTooLarge
		}

		if !feedbackPermitted(st.Policy) {
			outcome = map[string]any{
				"submission_state": "local_draft",
				"evidence_id":      evidenceID,
				"next_action":      "Feedback remains local. Set installation feedback policy once to share with Ajent product maintainers.",
			}
			return nil
		}

		payload = pl
		occurrenceID, _ = pl["occurrence_id"].(string)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if outcome != nil {
		return outcome, nil
	}

	if !feedbackHasCapability(feedbackCapabilities(ctx, c, cred), "improvements") {
		return map[string]any{
			"submission_state": "unsupported",
			"evidence_id":      occurrenceID,
			"next_action":      "Server support unavailable; the exact draft remains queued.",
		}, nil
	}

	var blocked bool
	err = s.with(feedbackCommandLockTimeout, func(st *state) error {
		blocked = !feedbackPermitted(st.Policy)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if blocked {
		return map[string]any{"submission_state": "local_draft"}, nil
	}

	var result map[string]any
	sendErr := c.Do(ctx, cred, api.Request{
		Method:    "POST",
		Route:     "/v1/improvements",
		Body:      payload,
		Key:       occurrenceID,
		NoObserve: true,
	}, &result)
	if sendErr != nil {
		var apiErr *api.Error
		if errors.As(sendErr, &apiErr) {
			diag := feedbackDiagnostic(apiErr)
			diag["submission_state"] = "queued"
			diag["evidence_id"] = occurrenceID
			return diag, nil
		}
		return nil, sendErr
	}

	err = s.with(feedbackCommandLockTimeout, func(st *state) error {
		if rec := feedbackRecordByID(st.Records, scope, occurrenceID); rec != nil {
			rec["result"] = result
		}
		st.Counters["submitted_occurrences"]++
		if feedbackTruthy(result["replayed"]) || feedbackEquals(result["submission_state"], "replayed") {
			st.Counters["replayed_occurrences"]++
		}
		if feedbackTruthy(result["deduplicated"]) {
			st.Counters["deduplicated_occurrences"]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// feedbackCategory validates args["category"], defaulting to "bug".
func feedbackCategory(args map[string]any) (string, error) {
	v, present := args["category"]
	if !present {
		return "bug", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errFeedbackInvalidCategory
	}
	switch s {
	case "bug", "feature", "friction":
		return s, nil
	default:
		return "", errFeedbackInvalidCategory
	}
}

// feedbackContext validates the four report context fields, defaulting
// each to "" when absent.
func feedbackContext(args map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(feedbackContextFields))
	for _, key := range feedbackContextFields {
		v, present := args[key]
		if !present {
			out[key] = ""
			continue
		}
		s, ok := v.(string)
		if !ok || utf8.RuneCountInString(s) > feedbackContextFieldMax {
			return nil, errFeedbackInvalidContext
		}
		out[key] = s
	}
	return out, nil
}

// feedbackWorkaroundMinutes validates args["workaround_minutes"]: absent or
// null returns (nil, nil); otherwise it must be a whole number from 0 to
// 10080.
func feedbackWorkaroundMinutes(args map[string]any) (any, error) {
	v, present := args["workaround_minutes"]
	if !present || v == nil {
		return nil, nil
	}
	f, ok := feedbackAsNumber(v)
	if !ok || f != math.Trunc(f) || f < 0 || f > feedbackWorkaroundMinutesMax {
		return nil, errFeedbackInvalidMinutes
	}
	return int(f), nil
}

// feedbackJSONSize returns the compact JSON encoding length of v.
func feedbackJSONSize(v any) (int, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// feedbackTruthy reports whether v is the JSON boolean true.
func feedbackTruthy(v any) bool {
	b, _ := v.(bool)
	return b
}

// feedbackEquals reports whether v is the string s.
func feedbackEquals(v any, s string) bool {
	str, ok := v.(string)
	return ok && str == s
}

// feedbackReportChanged reports whether any of args's report fields that
// are actually present differ from the frozen payload pl, matching the
// Python reference's "key in args and args[key] != payload.get(key)" gate:
// a field absent from args never counts as changed, even if its computed
// default differs from the frozen value.
func feedbackReportChanged(args map[string]any, category string, minutes any, pl map[string]any) bool {
	for _, key := range feedbackContextFields {
		v, present := args[key]
		if !present {
			continue
		}
		s, _ := v.(string)
		plVal, _ := pl[key].(string)
		if s != plVal {
			return true
		}
	}
	if v, present := args["category"]; present {
		s, _ := v.(string)
		if s != category {
			return true
		}
		plVal, _ := pl["category"].(string)
		if category != plVal {
			return true
		}
	}
	if _, present := args["workaround_minutes"]; present {
		if !feedbackNumberOrNilEqual(minutes, pl["workaround_minutes"]) {
			return true
		}
	}
	return false
}

// feedbackNumberOrNilEqual reports whether a and b are both nil, or both
// numeric with the same value.
func feedbackNumberOrNilEqual(a, b any) bool {
	af, aOK := feedbackAsNumber(a)
	bf, bOK := feedbackAsNumber(b)
	if aOK != bOK {
		return false
	}
	if !aOK {
		return true
	}
	return af == bf
}

// helpImproveManage implements the "status", "withdraw" and "verify"
// actions, which all begin with the same capability check
// (docs/tasks/T2.11.md step 4).
func (s *Store) helpImproveManage(ctx context.Context, c *api.Client, cred credentials.Credential, scope, action string, args map[string]any) (any, error) {
	needed := "improvements"
	if action == "verify" {
		needed = "improvement_verification"
	}
	if !feedbackHasCapability(feedbackCapabilities(ctx, c, cred), needed) {
		return map[string]any{
			"submission_state": "unsupported",
			"next_action":      "Upgrade the Ajent server; keep working.",
		}, nil
	}

	reportID, err := feedbackReportID(args)
	if err != nil {
		return nil, err
	}
	path := "/v1/improvements/" + reportID

	if action != "verify" {
		method := "GET"
		if action == "withdraw" {
			method = "DELETE"
		}
		var out map[string]any
		if reqErr := c.Do(ctx, cred, api.Request{Method: method, Route: path}, &out); reqErr != nil {
			return nil, reqErr
		}
		return out, nil
	}

	return s.helpImproveVerify(ctx, c, cred, scope, path, args)
}

// feedbackReportID validates and canonicalizes args["report_id"].
func feedbackReportID(args map[string]any) (string, error) {
	v, ok := args["report_id"].(string)
	if !ok || v == "" {
		return "", errFeedbackReportIDRequired
	}
	return api.CanonicalUUID(v)
}

// feedbackVerificationOutcomes lists the valid verification outcomes
// (docs/contract.md section 12).
var feedbackVerificationOutcomes = map[string]bool{
	"reproduced":       true,
	"not_reproduced":   true,
	"unable_to_verify": true,
	"deferred":         true,
	"dismissed":        true,
}

// helpImproveVerify implements the "verify" action, following the Python
// reference sequence: the local_draft check, the frozen verification
// record per report and deployed revision, the queued_original check, a
// second local_draft re-check immediately before sending, then the send
// itself (docs/tasks/T2.11.md step 4).
func (s *Store) helpImproveVerify(ctx context.Context, c *api.Client, cred credentials.Credential, scope, path string, args map[string]any) (any, error) {
	outcome, ok := args["outcome"].(string)
	if !ok || !feedbackVerificationOutcomes[outcome] {
		return nil, errFeedbackInvalidOutcome
	}
	reportID, _ := args["report_id"].(string)

	var draft1 bool
	err := s.with(feedbackCommandLockTimeout, func(st *state) error {
		draft1 = !feedbackPermitted(st.Policy)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if draft1 {
		return map[string]any{
			"submission_state": "local_draft",
			"next_action":      "Set installation feedback policy to send verification.",
		}, nil
	}

	var (
		queuedOriginal map[string]any
		payload        map[string]any
	)
	err = s.with(feedbackCommandLockTimeout, func(st *state) error {
		op := feedbackFindVerifyRecord(st.Records, scope, reportID, args["deployed_revision"])
		if op == nil {
			verificationID, idErr := feedbackVerificationID(args)
			if idErr != nil {
				return idErr
			}
			evidence, _ := args["verification_evidence"].(string)
			if utf8.RuneCountInString(evidence) > feedbackVerificationEvidenceMax {
				return errFeedbackVerificationEvidenceTooBig
			}
			deployedRevision, _ := args["deployed_revision"].(string)
			pl := map[string]any{
				"verification_id":   verificationID,
				"deployed_revision": deployedRevision,
				"outcome":           outcome,
				"evidence":          evidence,
			}
			op = record{
				"evidence_id":          verificationID,
				"created":              unixFloatSeconds(s.clock()),
				"verify_report":        reportID,
				"verification_payload": pl,
				"scope":                scope,
			}
			st.Records = append(st.Records, op)
		}

		pl, _ := op["verification_payload"].(map[string]any)
		changed := false
		plOutcome, _ := pl["outcome"].(string)
		if plOutcome != outcome {
			changed = true
		}
		if !changed {
			if v, present := args["verification_evidence"]; present {
				s2, _ := v.(string)
				plEv, _ := pl["evidence"].(string)
				if s2 != plEv {
					changed = true
				}
			}
		}
		if !changed {
			if v, present := args["verification_id"]; present {
				s2, _ := v.(string)
				plID, _ := pl["verification_id"].(string)
				if s2 != plID {
					changed = true
				}
			}
		}
		if changed {
			queuedOriginal = map[string]any{
				"submission_state": "queued_original",
				"verification_id":  pl["verification_id"],
				"next_action":      "An earlier verification is pending. Retry its original outcome and evidence before submitting a different result.",
			}
			return nil
		}

		payload = pl
		return nil
	})
	if err != nil {
		return nil, err
	}
	if queuedOriginal != nil {
		return queuedOriginal, nil
	}

	var draft2 bool
	err = s.with(feedbackCommandLockTimeout, func(st *state) error {
		draft2 = !feedbackPermitted(st.Policy)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if draft2 {
		return map[string]any{"submission_state": "local_draft"}, nil
	}

	verificationID, _ := payload["verification_id"].(string)
	var result map[string]any
	sendErr := c.Do(ctx, cred, api.Request{
		Method:    "POST",
		Route:     path + "/verify",
		Body:      payload,
		Key:       verificationID,
		NoObserve: true,
	}, &result)
	if sendErr != nil {
		var apiErr *api.Error
		if errors.As(sendErr, &apiErr) {
			diag := feedbackDiagnostic(apiErr)
			diag["submission_state"] = "queued"
			diag["verification_id"] = verificationID
			return diag, nil
		}
		return nil, sendErr
	}

	err = s.with(feedbackCommandLockTimeout, func(st *state) error {
		if rec := feedbackRecordByID(st.Records, scope, verificationID); rec != nil {
			rec["result"] = result
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// feedbackFindVerifyRecord returns the frozen verification record for
// scope, reportID and deployedRevision that has no result yet, or nil. It
// compares deployedRevision (args["deployed_revision"], possibly absent)
// against the frozen payload's stored value with no default, matching the
// Python reference exactly: a replay that omits deployed_revision does not
// match a record created with one.
func feedbackFindVerifyRecord(records []record, scope, reportID string, deployedRevision any) record {
	for _, r := range records {
		if s, _ := r["scope"].(string); s != scope {
			continue
		}
		if vr, _ := r["verify_report"].(string); vr != reportID {
			continue
		}
		if _, has := r["result"]; has {
			continue
		}
		pl, _ := r["verification_payload"].(map[string]any)
		var dr any
		if pl != nil {
			dr = pl["deployed_revision"]
		}
		if dr != deployedRevision {
			continue
		}
		return r
	}
	return nil
}

// feedbackVerificationID returns args["verification_id"] canonicalized, or
// a fresh UUID v4 when absent or empty.
func feedbackVerificationID(args map[string]any) (string, error) {
	if v, ok := args["verification_id"].(string); ok && v != "" {
		return api.CanonicalUUID(v)
	}
	return feedbackUUIDv4()
}
