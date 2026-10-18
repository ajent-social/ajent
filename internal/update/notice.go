// SPDX-License-Identifier: Apache-2.0

package update

import "time"

// noticeInterval is how often an interactive CLI run may print an update
// notice, and how often a background check refreshes release information
// (ADR 005, "Notices").
const noticeInterval = 24 * time.Hour

// Notice reports the one-line update notice an interactive CLI run should
// print on stderr, and whether it should print at all. It returns
// show=false when st.Latest is not newer than current, or when less than
// noticeInterval has passed since st.LastNotice. Otherwise it returns
// show=true and the text "A new ajent release is available: <current> ->
// <latest>. Run: <cmd>", where cmd is UpgradeCommand(m), or "ajent update"
// when that is empty (docs/tasks/T6.5.md step 1).
func Notice(st State, current string, m Method, now time.Time) (text string, show bool) {
	if !Newer(st.Latest, current) {
		return "", false
	}
	if now.Sub(st.LastNotice) < noticeInterval {
		return "", false
	}

	cmd := UpgradeCommand(m)
	if cmd == "" {
		cmd = "ajent update"
	}

	return "A new ajent release is available: " + current + " -> " + st.Latest + ". Run: " + cmd, true
}

// NeedsRefresh reports whether at least noticeInterval has passed since
// st.LastCheck, so a background "ajent update --check" should run
// (docs/tasks/T6.5.md step 1).
func NeedsRefresh(st State, now time.Time) bool {
	return now.Sub(st.LastCheck) >= noticeInterval
}

// DiagnoseFields returns the update-related fields ajent_diagnose merges
// into its response: whether a newer release is cached, its version, and
// the command to run to get it. It loads only the persisted state
// (LoadState) and classifies the install method with Detect, both local
// operations; it never makes a network call (docs/tasks/T6.5.md step 1).
func DiagnoseFields(configDir, executable, current string) map[string]any {
	st, _ := LoadState(configDir)

	cmd := UpgradeCommand(Detect(executable, configDir))
	if cmd == "" {
		cmd = "ajent update"
	}

	return map[string]any{
		"update_available": Newer(st.Latest, current),
		"latest_version":   st.Latest,
		"upgrade_command":  cmd,
	}
}
