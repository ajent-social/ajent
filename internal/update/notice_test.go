// SPDX-License-Identifier: Apache-2.0

package update

import (
	"net/http"
	"testing"
	"time"
)

// noticeTestNow is the fixed clock every notice_test.go case measures
// relative to.
var noticeTestNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// TestNoticeShown proves Notice prints when a newer release is cached and
// no notice was shown in the last 24 hours (docs/tasks/T6.5.md step 1).
func TestNoticeShown(t *testing.T) {
	st := State{Latest: "0.8.0", LastNotice: noticeTestNow.Add(-25 * time.Hour)}

	text, show := Notice(st, "0.7.0", MethodScript, noticeTestNow)
	if !show {
		t.Fatalf("show = false, want true")
	}
	want := "A new ajent release is available: 0.7.0 -> 0.8.0. Run: ajent update"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

// TestNoticeRateLimited proves Notice stays quiet when a notice already ran
// within the last 24 hours, even though a newer release is cached.
func TestNoticeRateLimited(t *testing.T) {
	st := State{Latest: "0.8.0", LastNotice: noticeTestNow.Add(-1 * time.Hour)}

	text, show := Notice(st, "0.7.0", MethodScript, noticeTestNow)
	if show {
		t.Fatalf("show = true, want false")
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}

// TestNoticeNotNewer proves Notice stays quiet when the cached latest
// version is not newer than current, regardless of LastNotice.
func TestNoticeNotNewer(t *testing.T) {
	tests := []struct {
		name   string
		latest string
	}{
		{"equal version", "0.7.0"},
		{"older version", "0.6.0"},
		{"no cached version", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := State{Latest: tt.latest} // zero LastNotice: never notified
			text, show := Notice(st, "0.7.0", MethodScript, noticeTestNow)
			if show {
				t.Fatalf("show = true, want false")
			}
			if text != "" {
				t.Errorf("text = %q, want empty", text)
			}
		})
	}
}

// TestNoticeManagedCommand proves Notice uses UpgradeCommand(m) for a
// managed install method instead of falling back to "ajent update".
func TestNoticeManagedCommand(t *testing.T) {
	st := State{Latest: "0.8.0"} // zero LastNotice: never notified

	text, show := Notice(st, "0.7.0", MethodHomebrew, noticeTestNow)
	if !show {
		t.Fatalf("show = false, want true")
	}
	want := "A new ajent release is available: 0.7.0 -> 0.8.0. Run: brew upgrade ajent"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

// TestNeedsRefresh proves NeedsRefresh reports true once 24 hours have
// passed since LastCheck, and true for a state that has never been
// checked (zero LastCheck).
func TestNeedsRefresh(t *testing.T) {
	tests := []struct {
		name      string
		lastCheck time.Time
		want      bool
	}{
		{"never checked", time.Time{}, true},
		{"checked just now", noticeTestNow, false},
		{"checked 23h ago", noticeTestNow.Add(-23 * time.Hour), false},
		{"checked exactly 24h ago", noticeTestNow.Add(-24 * time.Hour), true},
		{"checked 25h ago", noticeTestNow.Add(-25 * time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := State{LastCheck: tt.lastCheck}
			if got := NeedsRefresh(st, noticeTestNow); got != tt.want {
				t.Errorf("NeedsRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

// noticeNoNetworkTransport fails the test if it is ever asked to round
// trip a request, proving the code under test made no network call.
type noticeNoNetworkTransport struct{ t *testing.T }

func (rt *noticeNoNetworkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.t.Helper()
	rt.t.Fatalf("unexpected network call: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestDiagnoseFieldsNoNetwork proves DiagnoseFields reads only the local
// cached state and install-method classification, replacing
// http.DefaultTransport with one that fails the test if anything ever
// dials it, then checks the returned fields against a seeded state
// (docs/tasks/T6.5.md step 1).
func TestDiagnoseFieldsNoNetwork(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = &noticeNoNetworkTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = original })

	dir := t.TempDir()
	if err := SaveState(dir, State{Latest: "0.8.0"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	fields := DiagnoseFields(dir, "/tmp/some/unrecognized/path/ajent", "0.7.0")

	if got, want := fields["update_available"], true; got != want {
		t.Errorf("update_available = %v, want %v", got, want)
	}
	if got, want := fields["latest_version"], "0.8.0"; got != want {
		t.Errorf("latest_version = %v, want %v", got, want)
	}
	if got, want := fields["upgrade_command"], "ajent update"; got != want {
		t.Errorf("upgrade_command = %v, want %v", got, want)
	}
}

// TestDiagnoseFieldsUpToDate proves DiagnoseFields reports
// update_available as false, and an empty state.json (never checked) as
// no cached version, without making a network call.
func TestDiagnoseFieldsUpToDate(t *testing.T) {
	dir := t.TempDir() // no state.json written: zero State

	fields := DiagnoseFields(dir, "/tmp/some/unrecognized/path/ajent", "0.7.0")

	if got, want := fields["update_available"], false; got != want {
		t.Errorf("update_available = %v, want %v", got, want)
	}
	if got, want := fields["latest_version"], ""; got != want {
		t.Errorf("latest_version = %v, want %v", got, want)
	}
	if got, want := fields["upgrade_command"], "ajent update"; got != want {
		t.Errorf("upgrade_command = %v, want %v", got, want)
	}
}
