// SPDX-License-Identifier: Apache-2.0

// Package feedback implements ajent's consent-gated local diagnostics and
// help-improve reports (docs/design.md; docs/adr/006-client-trust-boundaries.md,
// "No telemetry by default"). The client records bounded, content-free
// evidence about its own failed requests locally. Nothing leaves the
// machine until the installation owner grants standing permission with
// "ajent feedback set", and that permission can be revoked at any time
// with "ajent feedback revoke" (docs/contract.md section 12).
package feedback

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/ajent-social/ajent/internal/config"
)

// Store is the consent-gated local feedback store rooted at Dir (the ajent
// config directory). Its state lives at Dir/feedback/state.json, guarded by
// the lock file Dir/feedback/lock (docs/contract.md section 4).
type Store struct {
	Dir string

	// now returns the current time; nil means time.Now. Tests inject a
	// fixed or advancing clock so retention and timestamps are
	// deterministic (docs/tasks/T2.11.md step 1).
	now func() time.Time
}

// record is one entry in state.Records: a captured failure, or a frozen
// report or verification draft. It stays a plain map so fields the Python
// reference client wrote (or a newer server response merged in) round-trip
// unchanged even when this package does not know about them
// (docs/tasks/T2.11.md step 1).
type record = map[string]any

// state is the feedback store's on-disk shape (docs/tasks/T2.11.md step 1).
type state struct {
	Policy   map[string]any `json:"policy,omitempty"`
	Records  []record       `json:"records"`
	Counters map[string]int `json:"counters"`
}

const (
	// feedbackDirName is the subdirectory of Store.Dir holding the state
	// file and its lock (docs/contract.md section 4).
	feedbackDirName = "feedback"
	// feedbackStateFileName is the state file's name within
	// feedbackDirName.
	feedbackStateFileName = "state.json"
	// feedbackLockFileName is the lock file's name within
	// feedbackDirName.
	feedbackLockFileName = "lock"

	// feedbackRetention is how long a failure record survives before it
	// is dropped on the next open (docs/contract.md section 12).
	feedbackRetention = 7 * 24 * time.Hour
	// feedbackMaxRecords and feedbackMaxBytes bound the state file: the
	// oldest record is dropped until both hold (docs/contract.md section
	// 12).
	feedbackMaxRecords = 100
	feedbackMaxBytes   = 256 * 1024

	// feedbackCommandLockTimeout is how long a Store operation other
	// than capture waits for the feedback lock before failing
	// (docs/contract.md section 12).
	feedbackCommandLockTimeout = 1 * time.Second
)

// errFeedbackBusy is returned by a Store operation, other than capture,
// that could not acquire the feedback lock before feedbackCommandLockTimeout
// elapsed (docs/contract.md section 12). Its text is the exact wording the
// contract specifies; it is not created with errors.New's usual lower-case,
// unpunctuated convention because this is a user-facing message printed
// verbatim after "ajent: " by internal/cli.Main.
var errFeedbackBusy = feedbackMessage("Feedback buffer busy; continue work and retry at a task boundary.")

// feedbackMessageError is a plain error whose Error method returns its
// message unchanged, used for the small number of feedback errors whose
// exact wording is fixed by docs/contract.md rather than by Go's usual
// error-string conventions.
type feedbackMessageError string

func (e feedbackMessageError) Error() string { return string(e) }

// feedbackMessage builds a feedbackMessageError. internal/feedback cannot
// import internal/cli (docs/interfaces.md's package import graph: feedback
// may import only api, config and credentials), so it cannot return
// cli.UserError directly; cmd/ajent/cmd_feedback.go re-wraps the one such
// message reachable from the CLI (the bad-action error) as cli.UserError
// before it reaches cli.Main, so the two stay byte-identical.
func feedbackMessage(s string) error { return feedbackMessageError(s) }

// clock returns the store's current time.
func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// unixFloatSeconds renders t as Unix seconds with sub-second precision, the
// numeric form docs/contract.md section 12 uses for "created" and
// "granted_at".
func unixFloatSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}

// feedbackUUIDv4 returns a random RFC 4122 version-4 UUID in canonical
// lowercase 8-4-4-4-12 form, generated with crypto/rand
// (docs/tasks/T2.11.md step 2).
func feedbackUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("feedback: generating uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// dir returns the feedback subdirectory of s.Dir.
func (s *Store) dir() string { return filepath.Join(s.Dir, feedbackDirName) }

// statePath returns the state file's path.
func (s *Store) statePath() string { return filepath.Join(s.dir(), feedbackStateFileName) }

// lockPath returns the lock file's path.
func (s *Store) lockPath() string { return filepath.Join(s.dir(), feedbackLockFileName) }

// load reads the state file, returning an empty state when it does not yet
// exist.
func (s *Store) load() (state, error) {
	st := state{Counters: map[string]int{}, Records: []record{}}

	data, err := config.ReadPrivate(s.statePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return st, nil
		}
		return state{}, fmt.Errorf("feedback: reading state: %w", err)
	}

	var loaded state
	if err := json.Unmarshal(data, &loaded); err != nil {
		return state{}, fmt.Errorf("feedback: decoding state: %w", err)
	}
	if loaded.Counters == nil {
		loaded.Counters = map[string]int{}
	}
	if loaded.Records == nil {
		loaded.Records = []record{}
	}
	return loaded, nil
}

// recordCreated returns record r's "created" field as float64 seconds, or
// ok == false when the field is missing or not numeric.
func recordCreated(r record) (float64, bool) {
	switch v := r["created"].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

// stateSize returns the exact byte length WriteJSON would write for st.
func stateSize(st state) (int, error) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("feedback: measuring state size: %w", err)
	}
	return len(data), nil
}

// with is the store's single read-modify-write transaction: it creates the
// feedback directory, takes the lock (waiting up to wait, or not waiting at
// all when wait is 0), loads the state, drops records older than
// feedbackRetention, runs fn, then evicts the oldest records until the
// state is within bounds, and finally persists it (docs/tasks/T2.11.md step
// 1). When fn returns an error, with propagates it without persisting any
// change (including the retention drop), mirroring the Python reference's
// context-manager semantics: an exception skips the write-back, a normal
// return does not.
func (s *Store) with(wait time.Duration, fn func(*state) error) (err error) {
	if dirErr := config.EnsurePrivateDir(s.dir()); dirErr != nil {
		return fmt.Errorf("feedback: preparing state directory: %w", dirErr)
	}

	lock, lockErr := config.Acquire(s.lockPath(), wait)
	if lockErr != nil {
		if errors.Is(lockErr, config.ErrLockTimeout) {
			return errFeedbackBusy
		}
		return fmt.Errorf("feedback: acquiring lock: %w", lockErr)
	}
	defer func() {
		if relErr := lock.Release(); relErr != nil && err == nil {
			err = fmt.Errorf("feedback: releasing lock: %w", relErr)
		}
	}()

	st, loadErr := s.load()
	if loadErr != nil {
		return loadErr
	}

	cutoff := unixFloatSeconds(s.clock().Add(-feedbackRetention))
	kept := make([]record, 0, len(st.Records))
	dropped := 0
	for _, r := range st.Records {
		created, ok := recordCreated(r)
		if ok && created <= cutoff {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	st.Records = kept
	if dropped > 0 {
		st.Counters["dropped"] += dropped
	}

	if fnErr := fn(&st); fnErr != nil {
		return fnErr
	}

	for {
		size, sizeErr := stateSize(st)
		if sizeErr != nil {
			return sizeErr
		}
		if len(st.Records) <= feedbackMaxRecords && size <= feedbackMaxBytes {
			break
		}
		if len(st.Records) == 0 {
			break
		}
		st.Records = st.Records[1:]
		st.Counters["dropped"]++
	}

	if writeErr := config.WriteJSON(s.statePath(), st); writeErr != nil {
		return fmt.Errorf("feedback: writing state: %w", writeErr)
	}
	return nil
}
