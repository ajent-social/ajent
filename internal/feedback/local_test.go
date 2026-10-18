// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// feedbackIsJSONNull reports whether v marshals to the JSON literal null.
// It exists because a nil map[string]any wrapped in an any is not equal to
// untyped nil under Go's == (the map's dynamic type makes the interface
// value itself non-nil), even though it marshals to null, matching what a
// CLI caller actually sees.
func feedbackIsJSONNull(t *testing.T, v any) bool {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling %#v: %v", v, err)
	}
	return string(data) == "null"
}

// TestLocalActions walks show, set, preview, stats, revoke and clear in
// sequence against one store, checking each result shape, plus the
// bad-action error (docs/tasks/T2.11.md step 7).
func TestLocalActions(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &Store{Dir: t.TempDir(), now: feedbackFixedClock(now)}

	if _, err := store.Local("bogus"); !errors.Is(err, errFeedbackAction) {
		t.Fatalf("Local(bogus) err = %v, want errFeedbackAction", err)
	}

	show, err := store.Local("show")
	if err != nil {
		t.Fatalf("Local(show) = %v", err)
	}
	showMap := show.(map[string]any)
	if !feedbackIsJSONNull(t, showMap["policy"]) {
		t.Errorf("policy = %#v, want null before set", showMap["policy"])
	}
	if showMap["pending_count"] != 0 {
		t.Errorf("pending_count = %v, want 0", showMap["pending_count"])
	}
	if _, has := showMap["records"]; has {
		t.Errorf("show result has a records key, want none")
	}

	set, err := store.Local("set")
	if err != nil {
		t.Fatalf("Local(set) = %v", err)
	}
	policy := set.(map[string]any)["policy"].(map[string]any)
	if policy["version"] != 1 {
		t.Errorf("policy.version = %v, want 1", policy["version"])
	}
	if policy["revoked"] != false {
		t.Errorf("policy.revoked = %v, want false", policy["revoked"])
	}
	if policy["destination"] != "Ajent product maintainers" {
		t.Errorf("policy.destination = %v, want %q", policy["destination"], "Ajent product maintainers")
	}
	if got := unixFloatSeconds(now); policy["granted_at"] != got {
		t.Errorf("policy.granted_at = %v, want %v", policy["granted_at"], got)
	}

	if err := store.with(0, func(st *state) error {
		st.Records = append(st.Records, record{
			"evidence_id": "a",
			"created":     unixFloatSeconds(now),
			"scope":       "x",
			"evidence":    map[string]any{"route": "/v1/me"},
		})
		return nil
	}); err != nil {
		t.Fatalf("seeding a record: %v", err)
	}

	preview, err := store.Local("preview")
	if err != nil {
		t.Fatalf("Local(preview) = %v", err)
	}
	previewMap := preview.(map[string]any)
	if previewMap["pending_count"] != 1 {
		t.Errorf("pending_count = %v, want 1", previewMap["pending_count"])
	}
	records := previewMap["records"].([]record)
	if len(records) != 1 || records[0]["evidence_id"] != "a" {
		t.Errorf("records = %#v, want one record with evidence_id a", records)
	}

	stats, err := store.Local("stats")
	if err != nil {
		t.Fatalf("Local(stats) = %v", err)
	}
	statsMap := stats.(map[string]any)
	if _, has := statsMap["records"]; has {
		t.Errorf("stats result has a records key, want none")
	}
	if statsMap["pending_count"] != 1 {
		t.Errorf("pending_count = %v, want 1", statsMap["pending_count"])
	}

	revoke, err := store.Local("revoke")
	if err != nil {
		t.Fatalf("Local(revoke) = %v", err)
	}
	if got := revoke.(map[string]any)["policy"].(map[string]any)["revoked"]; got != true {
		t.Errorf("policy.revoked after revoke = %v, want true", got)
	}

	cleared, err := store.Local("clear")
	if err != nil {
		t.Fatalf("Local(clear) = %v", err)
	}
	clearMap := cleared.(map[string]any)
	if clearMap["pending_count"] != 0 {
		t.Errorf("pending_count after clear = %v, want 0", clearMap["pending_count"])
	}
	// revoked must survive clear: clear only empties records.
	if got := clearMap["policy"].(map[string]any)["revoked"]; got != true {
		t.Errorf("policy.revoked after clear = %v, want true (clear must not touch the policy)", got)
	}
}

// TestLocalRevokeWithoutPriorPolicy confirms revoke creates a minimal
// policy object when none exists yet (docs/contract.md section 12).
func TestLocalRevokeWithoutPriorPolicy(t *testing.T) {
	store := &Store{Dir: t.TempDir()}

	result, err := store.Local("revoke")
	if err != nil {
		t.Fatalf("Local(revoke) = %v", err)
	}
	policy, ok := result.(map[string]any)["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy = %#v, want a map", result.(map[string]any)["policy"])
	}
	if policy["revoked"] != true {
		t.Errorf("policy.revoked = %v, want true", policy["revoked"])
	}
}
