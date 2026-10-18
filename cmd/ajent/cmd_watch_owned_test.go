// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/channelwatch"
	"github.com/ajent-social/ajent/internal/config"
)

const ownedTestThread = "019cf010-0000-7000-8000-000000000001"
const ownedTestTurn = "019cf010-0000-7000-8000-000000000002"
const ownedNextTurn = "019cf010-0000-7000-8000-000000000003"

// Each fixture owns its pipes and cancellation; no package-global mutable seams.
func ownedFixture(t *testing.T, behavior string) (*watchOwnedSession, <-chan []string) {
	t.Helper()
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	readDone := make(chan struct{})
	seen := make(chan []string, 1)
	s := &watchOwnedSession{stdin: writer, stdout: reader, frames: make(chan watchOwnedFrame, 1), exited: done, readDone: readDone, cancel: func() { cancel(); _ = input.Close(); _ = output.Close() }}
	go func() {
		defer close(readDone)
		defer close(s.frames)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var f watchOwnedFrame
			if json.Unmarshal(scanner.Bytes(), &f) != nil {
				return
			}
			select {
			case s.frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		defer close(done)
		defer func() {
			// Fixture shutdown may race the session's pipe close; both paths intentionally release the writer.
			_ = output.Close()
		}()
		var methods []string
		defer func() { seen <- methods }()
		scanner := bufio.NewScanner(input)
		enc := json.NewEncoder(output)
		turns := 0
		send := func(v any) bool { return enc.Encode(v) == nil }
		completion := func(thread, id, status string) bool {
			return send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": id, "status": status}}})
		}
		for scanner.Scan() {
			var req struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(scanner.Bytes(), &req) != nil {
				return
			}
			methods = append(methods, req.Method)
			var result any = map[string]any{}
			switch req.Method {
			case "initialized":
				continue
			case "thread/start":
				var p map[string]any
				_ = json.Unmarshal(req.Params, &p)
				if p["approvalPolicy"] != "never" || p["sandbox"] != "read-only" || p["ephemeral"] != true || p["model"] != nil || p["effort"] != nil {
					return
				}
				result = map[string]any{"thread": map[string]string{"id": ownedTestThread}}
			case "turn/start":
				turns++
				id := ownedTestTurn
				if turns == 2 {
					id = ownedNextTurn
				}
				switch behavior {
				case "disconnect":
					return
				case "approval":
					send(map[string]any{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
					continue
				case "tool":
					send(map[string]any{"id": 99, "method": "item/tool/call", "params": map[string]any{}})
					continue
				case "wrong response":
					req.ID++
				case "early":
					if !completion(ownedTestThread, id, "completed") {
						return
					}
				}
				result = map[string]any{"turn": map[string]string{"id": id, "status": "inProgress"}}
				if !send(map[string]any{"id": req.ID, "result": result}) {
					return
				}
				switch behavior {
				case "failed":
					completion(ownedTestThread, id, "failed")
				case "interrupted":
					completion(ownedTestThread, id, "interrupted")
				case "wrong thread":
					completion(ownedNextTurn, id, "completed")
				case "wrong turn":
					completion(ownedTestThread, ownedNextTurn, "completed")
				case "hang":
					continue
				case "early":
					continue
				default:
					completion(ownedTestThread, id, "completed")
				}
				continue
			}
			if !send(map[string]any{"id": req.ID, "result": result}) {
				return
			}
		}
	}()
	t.Cleanup(s.close)
	return s, seen
}

func ownedDeliveryFixture(t *testing.T, reconcile string) (*watchOwnedDelivery, string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "channel")
	if err := os.WriteFile(source, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := newWatchCodex(dir, source, "reader", watchCodexOptions{binary: filepath.Join(dir, "codex"), owned: true, reconcile: reconcile}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("codex-cli 0.163.0\n"), nil }
	return &watchOwnedDelivery{base: d, cwd: dir, out: io.Discard}, dir, source
}
func ownedBatch() channelwatch.Batch {
	return channelwatch.Batch{ID: strings.Repeat("a", 64), Consumer: "reader", Lines: []string{"hello"}}
}

func TestWatchOwnedCompletionCases(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{{"normal", true}, {"early", true}, {"failed", false}, {"interrupted", false}, {"wrong thread", false}, {"wrong turn", false}, {"wrong response", false}, {"disconnect", false}, {"approval", false}, {"tool", false}, {"hang", false}} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := ownedDeliveryFixture(t, "")
			session, _ := ownedFixture(t, tc.name)
			d.base.timeout = 100 * time.Millisecond
			d.start = func(context.Context, string, string) (*watchOwnedSession, error) { return session, nil }
			var out bytes.Buffer
			d.out = &out
			err := d.deliver(context.Background(), ownedBatch())
			if (err == nil) != tc.ok {
				t.Fatalf("completion accepted=%v, want %v", err == nil, tc.ok)
			}
			if d.base.state == nil {
				t.Fatal("attempt absent")
			}
			want := "attempting"
			if tc.ok {
				want = "completed"
			}
			if d.base.state.Status != want {
				t.Fatalf("state=%s", d.base.state.Status)
			}
			if !tc.ok && out.Len() != 0 {
				t.Fatal("failure printed output")
			}
			data, err := config.ReadPrivate(d.base.path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("hello")) {
				t.Fatal("state contains raw channel text")
			}
			if err != nil && strings.Contains(err.Error(), "hello") {
				t.Fatal("error contains channel text")
			}
		})
	}
}

func TestWatchOwnedSerialAndReplay(t *testing.T) {
	d, dir, source := ownedDeliveryFixture(t, "")
	session, seen := ownedFixture(t, "normal")
	starts := 0
	d.start = func(context.Context, string, string) (*watchOwnedSession, error) { starts++; return session, nil }
	for i := range 2 {
		b := ownedBatch()
		if i == 1 {
			b.ID = strings.Repeat("b", 64)
			b.Lines = []string{"world"}
		}
		if err := d.deliver(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		if err := d.deliver(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 {
		t.Fatal("more than one owned session")
	}
	session.close()
	methods := <-seen
	if strings.Join(methods, ",") != "initialize,initialized,thread/start,turn/start,turn/start" {
		t.Fatalf("unexpected methods: %v", methods)
	}
	// A restarted foreground process skips completed replay without spawning.
	next, err := newWatchCodex(dir, source, "reader", d.base.opts, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	replay := &watchOwnedDelivery{base: next, out: io.Discard, start: func(context.Context, string, string) (*watchOwnedSession, error) {
		t.Fatal("completed replay spawned")
		return nil, nil
	}}
	b := ownedBatch()
	b.ID = strings.Repeat("b", 64)
	b.Lines = []string{"world"}
	if err := replay.deliver(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func TestWatchOwnedRecoveryCases(t *testing.T) {
	for _, mode := range []string{"blocked", "accepted", "retry", "changed batch", "queue mode", "generic"} {
		t.Run(mode, func(t *testing.T) {
			d, dir, source := ownedDeliveryFixture(t, "")
			session, _ := ownedFixture(t, "failed")
			d.start = func(context.Context, string, string) (*watchOwnedSession, error) { return session, nil }
			if d.deliver(context.Background(), ownedBatch()) == nil {
				t.Fatal("failed turn accepted")
			}
			opts := d.base.opts
			switch mode {
			case "queue mode":
				opts.owned = false
				opts.thread = ownedTestThread
			case "generic":
				if refusePendingWatchCodex(dir, source, "reader") == nil {
					t.Fatal("generic bypassed attempt")
				}
				return
			case "accepted", "retry":
				opts.reconcile = mode
			}
			base, err := newWatchCodex(dir, source, "reader", opts, time.Second)
			if mode == "queue mode" {
				if err == nil {
					t.Fatal("queue bypassed attempt")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			replay := &watchOwnedDelivery{base: base, cwd: dir, out: io.Discard}
			replay.base.run = d.base.run
			replay.start = func(context.Context, string, string) (*watchOwnedSession, error) {
				if mode != "retry" {
					t.Fatal("unexpected spawn")
				}
				s, _ := ownedFixture(t, "normal")
				return s, nil
			}
			b := ownedBatch()
			if mode == "changed batch" {
				b.ID = strings.Repeat("c", 64)
			}
			err = replay.deliver(context.Background(), b)
			if (err == nil) != (mode == "accepted" || mode == "retry") {
				t.Fatalf("recovery accepted=%v", err == nil)
			}
		})
	}
}

func TestWatchOwnedIdleNoExecution(t *testing.T) {
	d, dir, source := ownedDeliveryFixture(t, "")
	d.base.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("idle executable call")
		return nil, nil
	}
	d.start = func(context.Context, string, string) (*watchOwnedSession, error) {
		t.Fatal("idle spawn")
		return nil, nil
	}
	w, err := channelwatch.New(context.Background(), channelwatch.Options{Dir: dir, File: source, Consumer: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.run(ctx, w, time.Millisecond, false); err == nil {
		t.Fatal("idle failed to cancel")
	}
}

func TestWatchOwnedPrivateState(t *testing.T) {
	for _, name := range []string{"state symlink", "directory symlink", "queue pending", "unmatched recovery"} {
		t.Run(name, func(t *testing.T) {
			d, dir, source := ownedDeliveryFixture(t, "")
			switch name {
			case "state symlink":
				if err := os.Symlink(source, d.base.path); err != nil {
					t.Skip("symlinks unavailable")
				}
			case "directory symlink":
				if err := os.Remove(filepath.Join(dir, "watch-codex")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, "watch-codex")); err != nil {
					t.Skip("symlinks unavailable")
				}
			case "queue pending":
				s := watchCodexState{Version: 1, Target: ownedTestThread, Binary: d.base.opts.binary, Qualified: watchCodexVersion, Batch: strings.Repeat("a", 64), Hash: strings.Repeat("b", 64), Status: "attempting"}
				if err := config.WriteJSON(d.base.path, s); err != nil {
					t.Fatal(err)
				}
			case "unmatched recovery":
				d.base.opts.reconcile = "accepted"
			}
			if _, err := newWatchCodex(dir, source, "reader", d.base.opts, time.Second); err == nil {
				t.Fatal("invalid private state accepted")
			}
		})
	}
}

// The test binary is a local app-server fixture only for these exact argv and
// explicit test environment. It cannot invoke a model or a real network service.
func init() {
	if len(os.Args) != 3 || os.Args[1] != "app-server" || os.Args[2] != "--stdio" {
		return
	}
	mode := os.Getenv("AJENT_TEST_OWNED_CHILD")
	if mode == "" {
		return
	}
	switch mode {
	case "malformed":
		_, _ = io.WriteString(os.Stdout, "not-json\n")
	case "oversized":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 1024*1024+1)+"\n")
	case "eof":
	case "hang":
		// A pipe read blocks without spinning until the owner closes stdin or kills us.
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	os.Exit(0)
}

func TestWatchOwnedChildLifecycle(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"malformed", "oversized", "eof", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AJENT_TEST_OWNED_CHILD", mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := startWatchOwnedSession(ctx, executable, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hang" {
				cancel()
			} else {
				select {
				case <-s.readDone:
				case <-time.After(3 * time.Second):
					t.Fatal("reader did not stop")
				}
				if s.health() == nil {
					t.Fatal("dead or malformed child appeared healthy")
				}
			}
			closed := make(chan struct{})
			go func() { s.close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("child not reaped")
			}
			select {
			case <-s.exited:
			default:
				t.Fatal("child remains alive")
			}
		})
	}
}

func TestWatchOwnedFlagCases(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		ok   bool
	}{
		{"continuous", []string{"--codex-owned", "--codex-binary", binary}, true},
		{"once", []string{"--codex-owned", "--once", "--codex-binary", binary}, true},
		{"thread", []string{"--codex-owned", "--codex-thread", ownedTestThread}, false},
		{"empty thread", []string{"--codex-owned", "--codex-thread="}, false},
		{"handler", []string{"--codex-owned", "--", "handler"}, false},
		{"bad reconcile", []string{"--codex-owned", "--codex-reconcile=maybe"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := watchFlags()
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			_, err := parseWatchCodex(fs)
			if (err == nil) != tc.ok {
				t.Fatalf("flags accepted=%v", err == nil)
			}
		})
	}
}

func TestWatchOwnedCursorAcceptance(t *testing.T) {
	for _, mode := range []string{"normal", "failed"} {
		t.Run(mode, func(t *testing.T) {
			d, dir, source := ownedDeliveryFixture(t, "")
			session, _ := ownedFixture(t, mode)
			d.start = func(context.Context, string, string) (*watchOwnedSession, error) { return session, nil }
			w, err := channelwatch.New(context.Background(), channelwatch.Options{Dir: dir, File: source, Consumer: "reader", FromStart: true, MaxBytes: 6})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Errorf("close watcher: %v", err)
				}
			}()
			delivered, err := w.Poll(context.Background(), d.deliver)
			if mode == "normal" {
				if err != nil || !delivered {
					t.Fatal("completed turn did not acknowledge")
				}
				delivered, err = w.Poll(context.Background(), d.deliver)
				if err != nil || !delivered {
					t.Fatal("second batch did not complete")
				}
				delivered, err = w.Poll(context.Background(), d.deliver)
				if err != nil || delivered {
					t.Fatal("completed cursor replayed")
				}
			} else {
				if err == nil || delivered {
					t.Fatal("failed turn acknowledged")
				}
				var replay channelwatch.Batch
				_, err = w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error { replay = b; return ownedWatchError() })
				if err == nil || replay.ID != d.base.state.Batch || len(replay.Lines) != 1 || replay.Lines[0] != "hello" {
					t.Fatal("failed turn lost pending batch")
				}
			}
		})
	}
}

func TestWatchOwnedEnvelopeAndPrivatePermissions(t *testing.T) {
	for _, mode := range []string{"oversized envelope", "private completion"} {
		t.Run(mode, func(t *testing.T) {
			d, _, _ := ownedDeliveryFixture(t, "")
			b := ownedBatch()
			if mode == "oversized envelope" {
				b.Lines = []string{strings.Repeat("x", watchCodexMessageLimit)}
				d.base.run = func(context.Context, string, ...string) ([]byte, error) {
					t.Fatal("oversized batch executed")
					return nil, nil
				}
				if d.deliver(context.Background(), b) == nil {
					t.Fatal("oversized envelope accepted")
				}
				return
			}
			session, _ := ownedFixture(t, "normal")
			d.start = func(context.Context, string, string) (*watchOwnedSession, error) { return session, nil }
			if err := d.deliver(context.Background(), b); err != nil {
				t.Fatal(err)
			}
			file, err := os.Stat(d.base.path)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := os.Stat(filepath.Dir(d.base.path))
			if err != nil {
				t.Fatal(err)
			}
			// Windows permissions are enforced by config's platform implementation.
			if os.PathSeparator != '\\' && (file.Mode().Perm() != 0o600 || directory.Mode().Perm() != 0o700) {
				t.Fatal("state permissions are not private")
			}
		})
	}
}

type ownedReceiptSignal struct{ written chan struct{} }

func (w ownedReceiptSignal) Write(p []byte) (int, error) {
	close(w.written)
	return len(p), nil
}

func TestWatchOwnedIdleChildFailureWakes(t *testing.T) {
	d, dir, source := ownedDeliveryFixture(t, "")
	w, err := channelwatch.New(context.Background(), channelwatch.Options{Dir: dir, File: source, Consumer: "reader", FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	}()
	session, _ := ownedFixture(t, "normal")
	d.start = func(context.Context, string, string) (*watchOwnedSession, error) { return session, nil }
	written := make(chan struct{})
	d.out = ownedReceiptSignal{written: written}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- d.run(ctx, w, time.Hour, false) }()
	// Receipt is emitted only after the initial health check and successful turn.
	// Closing the child after this signal cannot race ahead of that health check.
	select {
	case <-written:
		session.cancel()
	case <-ctx.Done():
		<-result
		t.Fatal("first batch did not complete")
	}
	select {
	case err := <-result:
		if err == nil || ctx.Err() != nil {
			t.Fatal("child failure did not promptly stop idle watcher")
		}
	case <-ctx.Done():
		<-result
		t.Fatal("idle child failure waited for the polling interval")
	}
}
