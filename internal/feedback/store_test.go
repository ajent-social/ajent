// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// feedbackFixedClock returns a Store.now func that always reports t.
func feedbackFixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// feedbackSeedRecords appends n records with the given created timestamp to
// the store's state in a single transaction.
func feedbackSeedRecords(t *testing.T, store *Store, prefix string, created float64, n int) {
	t.Helper()
	err := store.with(0, func(st *state) error {
		for i := 0; i < n; i++ {
			st.Records = append(st.Records, record{
				"evidence_id": fmt.Sprintf("%s-%d", prefix, i),
				"created":     created,
				"scope":       "test-scope",
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding records: %v", err)
	}
}

// TestRetentionAndBounds exercises with()'s two eviction rules: records
// older than feedbackRetention are dropped on the next open, and the state
// never keeps more than feedbackMaxRecords records (docs/tasks/T2.11.md
// step 7).
func TestRetentionAndBounds(t *testing.T) {
	t.Run("age", func(t *testing.T) {
		now := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
		store := &Store{Dir: t.TempDir(), now: feedbackFixedClock(now)}

		// Seed the stale and fresh records in a single transaction, so
		// with()'s own retention check (which runs on every open,
		// including this one) does not see them until after this call
		// persists them.
		err := store.with(0, func(st *state) error {
			st.Records = append(st.Records,
				record{"evidence_id": "old-0", "created": unixFloatSeconds(now.Add(-8 * 24 * time.Hour)), "scope": "test-scope"},
				record{"evidence_id": "fresh-0", "created": unixFloatSeconds(now.Add(-time.Hour)), "scope": "test-scope"},
			)
			return nil
		})
		if err != nil {
			t.Fatalf("seeding records: %v", err)
		}

		// The next open, at the same now, drops the now-stale "old"
		// record before running its own (no-op) fn.
		result, err := store.Local("preview")
		if err != nil {
			t.Fatalf("Local(preview) = %v", err)
		}
		m := result.(map[string]any)
		records := m["records"].([]record)
		if len(records) != 1 {
			t.Fatalf("records #2 = %d, want 1", len(records))
		}
		if records[0]["evidence_id"] != "fresh-0" {
			t.Errorf("surviving record = %v, want fresh-0", records[0]["evidence_id"])
		}
		counters := m["counters"].(map[string]int)
		if counters["dropped"] != 1 {
			t.Errorf("dropped = %d, want 1", counters["dropped"])
		}
	})

	t.Run("count", func(t *testing.T) {
		now := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
		store := &Store{Dir: t.TempDir(), now: feedbackFixedClock(now)}

		feedbackSeedRecords(t, store, "fresh", unixFloatSeconds(now.Add(-time.Minute)), feedbackMaxRecords+50)

		result, err := store.Local("preview")
		if err != nil {
			t.Fatalf("Local(preview) = %v", err)
		}
		m := result.(map[string]any)
		records := m["records"].([]record)
		if len(records) != feedbackMaxRecords {
			t.Fatalf("records = %d, want %d", len(records), feedbackMaxRecords)
		}
		counters := m["counters"].(map[string]int)
		if counters["dropped"] != 50 {
			t.Errorf("dropped = %d, want 50", counters["dropped"])
		}

		// A further open with no new records must not drop anything
		// else: bounds are satisfied.
		result, err = store.Local("preview")
		if err != nil {
			t.Fatalf("Local(preview) #2 = %v", err)
		}
		m = result.(map[string]any)
		if got := len(m["records"].([]record)); got != feedbackMaxRecords {
			t.Fatalf("records after #2 = %d, want %d", got, feedbackMaxRecords)
		}
		if got := m["counters"].(map[string]int)["dropped"]; got != 50 {
			t.Errorf("dropped after #2 = %d, want 50 (unchanged)", got)
		}
	})
}

// TestStoreWithBusyLock confirms a contended lock fails fast with
// errFeedbackBusy for a waiting caller, while capture's own 0-wait call
// (exercised via the Observer in capture_test.go) never blocks.
func TestStoreWithBusyLock(t *testing.T) {
	store := &Store{Dir: t.TempDir()}

	// Take the lock directly, simulating a concurrent holder, then try a
	// waiting operation against the same store.
	if err := store.with(0, func(*state) error { return nil }); err != nil {
		t.Fatalf("priming with() = %v", err)
	}

	done := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- store.with(50*time.Millisecond, func(*state) error {
			close(started)
			<-done
			return nil
		})
	}()
	<-started

	err := store.with(10*time.Millisecond, func(*state) error { return nil })
	close(done)
	if !errors.Is(err, errFeedbackBusy) {
		t.Fatalf("with() during contention = %v, want errFeedbackBusy", err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("contended operation after release = %v, want nil", err)
	}
}
