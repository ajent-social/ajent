// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/channelwatch"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/config"
)

func TestWatchCodexFlagsAvailable(t *testing.T) {
	fs := watchFlags()
	for _, name := range []string{"codex-thread", "codex-binary", "codex-reconcile"} {
		if fs.Lookup(name) == nil {
			t.Errorf("missing explicit delivery flag %s", name)
		}
	}
}

const testCodexThread = "12345678-1234-1234-1234-123456789abc"
const testCodexMessage = "abcdefab-1234-1234-1234-123456789abc"

func codexTestDelivery(t *testing.T, reconcile string) (*watchCodexDelivery, string, string) {
	t.Helper()
	dir, source := t.TempDir(), filepath.Join(t.TempDir(), "ajent.social")
	d, err := newWatchCodex(dir, source, "reader", watchCodexOptions{thread: testCodexThread, binary: filepath.Join(dir, "codex"), reconcile: reconcile}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return d, dir, source
}
func codexTestBatch() channelwatch.Batch {
	return channelwatch.Batch{ID: strings.Repeat("a", 64), Consumer: "reader", Lines: []string{"quotes: '\"; $(ignored)", "--last\nUnicode: 世界"}}
}
func codexReceipt() []byte {
	return []byte("Queued message " + testCodexMessage + " for thread " + testCodexThread + ".\n")
}
func codexSuccessfulRunner(calls *int) watchCodexRunner {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		(*calls)++
		if args[0] == "--version" {
			return []byte("codex-cli 0.163.0\n"), nil
		}
		return codexReceipt(), nil
	}
}
func codexReload(t *testing.T, d *watchCodexDelivery, dir, source, reconcile string) *watchCodexDelivery {
	t.Helper()
	opts := d.opts
	opts.reconcile = reconcile
	next, err := newWatchCodex(dir, source, "reader", opts, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestWatchCodexModeValidation(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"generic", nil, true},
		{"thread", []string{"--once", "--codex-thread", testCodexThread, "--codex-binary", binary}, true},
		{"continuous", []string{"--codex-thread", testCodexThread}, false},
		{"name", []string{"--once", "--codex-thread", "reviewer"}, false},
		{"last", []string{"--once", "--codex-thread=--last"}, false},
		{"uppercase", []string{"--once", "--codex-thread", strings.ToUpper(testCodexThread)}, false},
		{"bare", []string{"--once", "--codex-thread", strings.ReplaceAll(testCodexThread, "-", "")}, false},
		{"binary only", []string{"--codex-binary", binary}, false},
		{"reconcile only", []string{"--codex-reconcile", "accepted"}, false},
		{"empty thread", []string{"--codex-thread="}, false},
		{"bad recovery", []string{"--once", "--codex-thread", testCodexThread, "--codex-reconcile", "yes"}, false},
		{"handler", []string{"--once", "--codex-thread", testCodexThread, "--", "echo"}, false},
		{"missing executable", []string{"--once", "--codex-thread", testCodexThread, "--codex-binary", filepath.Join(t.TempDir(), "absent")}, false},
		{"directory executable", []string{"--once", "--codex-thread", testCodexThread, "--codex-binary", t.TempDir()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := watchFlags()
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			opts, err := parseWatchCodex(fs)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err != nil && !errors.Is(err, cli.ErrUsage) {
				t.Fatal("expected usage error")
			}
			if opts != nil && (!filepath.IsAbs(opts.binary) || filepath.Clean(opts.binary) != opts.binary) {
				t.Fatal("binary not absolute and clean")
			}
		})
	}
}

func TestWatchCodexDeliveryStateMachine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		output   []byte
		queueErr error
	}{
		{"accepted", codexReceipt(), nil},
		{"malformed", []byte("success\n"), nil},
		{"wrong thread", bytes.ReplaceAll(codexReceipt(), []byte(testCodexThread), []byte(testCodexMessage)), nil},
		{"bad message uuid", bytes.ReplaceAll(codexReceipt(), []byte(testCodexMessage), []byte(strings.Repeat("x", 36))), nil},
		{"extra output", append(codexReceipt(), 'x'), nil},
		{"overflow", bytes.Repeat([]byte("x"), 5000), nil},
		{"failed", nil, errors.New("child private data must not leak")},
		{"timeout", nil, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			calls := 0
			d.run = func(_ context.Context, binary string, args ...string) ([]byte, error) {
				calls++
				if binary != d.opts.binary {
					t.Fatal("wrong binary")
				}
				if args[0] == "--version" {
					return []byte("codex-cli 0.163.0\n"), nil
				}
				if len(args) != 5 || args[0] != "queue" || args[1] != "--thread" || args[2] != testCodexThread || args[3] != "--message" {
					t.Fatal("incorrect queue argv")
				}
				encoded, _ := json.Marshal(codexTestBatch())
				if args[4] != watchCodexGuidance+string(encoded)+"\nEND UNTRUSTED CHANNEL BATCH\n" {
					t.Fatal("full batch was changed in argv")
				}
				var marker watchCodexState
				if err := config.ReadJSON(d.path, &marker); err != nil || marker.Status != "attempting" {
					t.Fatal("queue ran without durable attempting marker")
				}
				return tc.output, tc.queueErr
			}
			err := d.deliver(context.Background(), codexTestBatch())
			success := tc.name == "accepted"
			if (err == nil) != success || calls != 2 {
				t.Fatalf("success=%v calls=%d err=%v", success, calls, err)
			}
			if err != nil && strings.Contains(err.Error(), "private data") {
				t.Fatal("child output leaked")
			}
			next := codexReload(t, d, dir, source, "")
			next.run = func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("replay must not invoke a command")
				return nil, nil
			}
			err = next.deliver(context.Background(), codexTestBatch())
			if (err == nil) != success {
				t.Fatalf("replay success=%v, err=%v", success, err)
			}
		})
	}
}

func TestWatchCodexRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "retry"} {
		t.Run(mode, func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			calls := 0
			d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				calls++
				if args[0] == "--version" {
					return []byte("codex-cli 0.163.0\n"), nil
				}
				return nil, errors.New("lost response")
			}
			if err := d.deliver(context.Background(), codexTestBatch()); err == nil {
				t.Fatal("expected ambiguity")
			}
			next := codexReload(t, d, dir, source, mode)
			calls = 0
			next.run = codexSuccessfulRunner(&calls)
			if err := next.deliver(context.Background(), codexTestBatch()); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if mode == "retry" {
				expected = 2
			}
			if calls != expected {
				t.Fatalf("calls=%d", calls)
			}
			if next.state.Status != "accepted" || next.state.OwnerConfirmed != (mode == "accepted") {
				t.Fatal("incorrect acceptance state")
			}
			replay := codexReload(t, next, dir, source, "")
			replay.run = func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("accepted recovery replay invoked a command")
				return nil, nil
			}
			if err := replay.deliver(context.Background(), codexTestBatch()); err != nil {
				t.Fatal(err)
			}

			opts := next.opts
			opts.reconcile = mode
			if _, err := newWatchCodex(dir, source, "reader", opts, time.Second); err == nil {
				t.Fatal("accepted state must reject reconciliation")
			}
		})
	}
}

func TestWatchCodexRefusals(t *testing.T) {
	for _, tc := range []string{"unsupported version", "version failure", "oversized", "missing recovery", "corrupt", "cross target", "cross binary", "batch mismatch", "hash mismatch", "new accepted batch"} {
		t.Run(tc, func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			batch := codexTestBatch()
			calls := 0
			d.run = codexSuccessfulRunner(&calls)
			switch tc {
			case "unsupported version", "version failure":
				d.run = func(context.Context, string, ...string) ([]byte, error) {
					calls++
					if tc == "version failure" {
						return nil, errors.New("secret")
					}
					return []byte("codex-cli 0.164.0\n"), nil
				}
				if err := d.deliver(context.Background(), batch); err == nil || calls != 1 || d.state != nil {
					t.Fatal("unqualified version queued or saved")
				}
				return
			case "oversized":
				batch.Lines = []string{strings.Repeat("界", watchCodexMessageLimit)}
				if err := d.deliver(context.Background(), batch); err == nil || calls != 0 || d.state != nil {
					t.Fatal("oversized batch was submitted")
				}
				return
			case "missing recovery":
				opts := d.opts
				opts.reconcile = "retry"
				if _, err := newWatchCodex(dir, source, "reader", opts, time.Second); err == nil {
					t.Fatal("missing state accepted recovery")
				}
				return
			case "corrupt":
				if err := config.WritePrivate(d.path, []byte(`{"status":"accepted"}`)); err != nil {
					t.Fatal(err)
				}
				if _, err := newWatchCodex(dir, source, "reader", d.opts, time.Second); err == nil {
					t.Fatal("corrupt state accepted")
				}
				return
			}
			if err := d.deliver(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			if tc == "cross target" || tc == "cross binary" {
				opts := d.opts
				if tc == "cross target" {
					opts.thread = testCodexMessage
				} else {
					opts.binary = filepath.Join(dir, "other")
				}
				if _, err := newWatchCodex(dir, source, "reader", opts, time.Second); err == nil {
					t.Fatal("binding bypassed")
				}
				return
			}
			if tc == "new accepted batch" {
				batch.ID = strings.Repeat("b", 64)
				if err := d.deliver(context.Background(), batch); err != nil || calls != 4 {
					t.Fatal("new accepted batch not delivered")
				}
				return
			}
			// An unresolved attempt must retain both batch identity and message digest.
			state := *d.state
			state.Status = "attempting"
			state.Message = ""
			if err := d.save(state); err != nil {
				t.Fatal(err)
			}
			next := codexReload(t, d, dir, source, "retry")
			next.run = func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("mismatched pending must not run")
				return nil, nil
			}
			if tc == "batch mismatch" {
				batch.ID = strings.Repeat("b", 64)
			} else {
				batch.Lines = append(batch.Lines, "changed")
			}
			if err := next.deliver(context.Background(), batch); err == nil {
				t.Fatal("mismatched pending accepted")
			}
		})
	}
}

func TestWatchCodexIdle(t *testing.T) {
	for _, contents := range []string{"", "\n \t\n", "partial"} {
		t.Run(fmt.Sprintf("bytes-%d", len(contents)), func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			d.run = func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("idle command invoked")
				return nil, nil
			}
			w, err := channelwatch.New(context.Background(), channelwatch.Options{Dir: dir, File: source, Consumer: "reader", FromStart: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Error(err)
				}
			}()
			delivered, err := w.Poll(context.Background(), d.deliver)
			if err != nil || delivered {
				t.Fatalf("idle delivered=%v err=%v", delivered, err)
			}
			if _, err := os.Stat(d.path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("idle wrote delivery state")
			}
		})
	}
}

func TestWatchCodexCaptureBound(t *testing.T) {
	var capture watchCodexCapture
	for _, size := range []int{4000, 200, 10000} {
		n, err := capture.Write(bytes.Repeat([]byte("x"), size))
		if n != size || err != nil {
			t.Fatal("capture write failed")
		}
	}
	if len(capture.data) != 4096 || !capture.overflow {
		t.Fatal("capture not bounded")
	}
}

// Handle subprocess fixtures before TestMain, which otherwise builds another CLI.
// This is private to the test binary and does not change the production runner.
func init() {
	// This test binary doubles as an unsupported Codex fixture. Intercept before
	// TestMain to avoid its CLI build, and never contact a real Codex process.
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Print("codex-cli 0.164.0\n")
		os.Exit(0)
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--watch-codex-helper" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "ok":
		fmt.Print("bounded response\n")
	case "fail":
		fmt.Fprint(os.Stderr, "private child error")
		os.Exit(19)
	case "overflow":
		fmt.Print(strings.Repeat("x", 4097))
	case "stderr overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 4097))
	case "timeout":
		<-time.After(time.Hour)
	}
	os.Exit(0)
}

func TestWatchCodexCommandExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ok", "fail", "overflow", "stderr overflow", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 5 * time.Second
			if mode == "timeout" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out, err := runWatchCodexCommand(ctx, executable, "-test.run=^$", "--", "--watch-codex-helper", mode)
			if mode == "ok" {
				if err != nil || string(out) != "bounded response\n" {
					t.Fatal("child response not captured")
				}
			} else if err == nil || len(out) != 0 || strings.Contains(err.Error(), "private child error") {
				t.Fatal("unsafe child failure response")
			}
		})
	}
}

func TestWatchCodexWatcherPendingRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "retry"} {
		t.Run(mode, func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			if err := os.WriteFile(source, []byte("first batch\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			options := channelwatch.Options{Dir: dir, File: source, Consumer: "reader", FromStart: true}
			open := func() *channelwatch.Watcher {
				t.Helper()
				w, err := channelwatch.New(context.Background(), options)
				if err != nil {
					t.Fatal(err)
				}
				return w
			}
			closeWatcher := func(w *channelwatch.Watcher) {
				t.Helper()
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			}
			w := open()
			d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "--version" {
					return []byte("codex-cli 0.163.0\n"), nil
				}
				return nil, context.DeadlineExceeded
			}
			if delivered, err := w.Poll(context.Background(), d.deliver); delivered || err == nil {
				t.Fatal("failed queue advanced cursor")
			}
			pendingID := d.state.Batch
			closeWatcher(w)
			if err := os.WriteFile(source, []byte("first batch\nsecond batch\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w = open()
			next := codexReload(t, d, dir, source, "")
			next.run = func(context.Context, string, ...string) ([]byte, error) { t.Fatal("blind resend"); return nil, nil }
			if delivered, err := w.Poll(context.Background(), next.deliver); delivered || err == nil {
				t.Fatal("ambiguous queue replay advanced cursor")
			}
			closeWatcher(w)
			w = open()
			recovered := codexReload(t, d, dir, source, mode)
			calls := 0
			recovered.run = codexSuccessfulRunner(&calls)
			if delivered, err := w.Poll(context.Background(), recovered.deliver); !delivered || err != nil {
				t.Fatalf("recovery failed: %v", err)
			}
			if recovered.state.Batch != pendingID {
				t.Fatal("pending batch changed after append")
			}
			expected := 0
			if mode == "retry" {
				expected = 2
			}
			if calls != expected {
				t.Fatal("unexpected recovery commands")
			}
			// Appended lines remain a separate unread batch after recovery.
			if delivered, err := w.Poll(context.Background(), func(_ context.Context, batch channelwatch.Batch) error {
				if len(batch.Lines) != 1 || batch.Lines[0] != "second batch" || batch.ID == pendingID {
					t.Fatal("pending recovery consumed appended lines")
				}
				return nil
			}); !delivered || err != nil {
				t.Fatal("appended batch lost")
			}
			closeWatcher(w)
		})
	}
}

func TestWatchCodexPrivateStateRefusal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require Windows privileges")
	}
	for _, kind := range []string{"config directory", "state directory", "state file"} {
		t.Run(kind, func(t *testing.T) {
			dir, source := t.TempDir(), filepath.Join(t.TempDir(), "ajent.social")
			opts := watchCodexOptions{thread: testCodexThread, binary: filepath.Join(dir, "codex")}
			target := t.TempDir()
			switch kind {
			case "config directory":
				dir = filepath.Join(dir, "linked")
				if err := os.Symlink(target, dir); err != nil {
					t.Fatal(err)
				}
			case "state directory":
				if err := os.Symlink(target, filepath.Join(dir, "watch-codex")); err != nil {
					t.Fatal(err)
				}
			case "state file":
				d, err := newWatchCodex(dir, source, "reader", opts, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(target, "state"), d.path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := newWatchCodex(dir, source, "reader", opts, time.Second); err == nil {
				t.Fatal("symlink state accepted")
			}
		})
	}
}

func TestWatchCodexAcceptedWriteFailure(t *testing.T) {
	d, _, _ := codexTestDelivery(t, "")
	d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "--version" {
			return []byte("codex-cli 0.163.0\n"), nil
		}
		// Keep the attempting marker intact but force the acceptance write to fail.
		// A directory at the configured destination cannot be replaced by WriteJSON.
		d.path = filepath.Dir(d.path)
		return codexReceipt(), nil
	}
	if err := d.deliver(context.Background(), codexTestBatch()); err == nil {
		t.Fatal("acceptance succeeded without durable state")
	}
	if d.state.Status != "attempting" {
		t.Fatal("failed acceptance changed in-memory state")
	}
}

func TestWatchCodexVersionDeadline(t *testing.T) {
	for _, limit := range []time.Duration{time.Second, time.Minute} {
		t.Run(limit.String(), func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			var err error
			d, err = newWatchCodex(dir, source, "reader", d.opts, limit)
			if err != nil {
				t.Fatal(err)
			}
			d.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > min(limit, 30*time.Second) {
					t.Fatal("command deadline not bounded")
				}
				return nil, context.DeadlineExceeded
			}
			if err := d.deliver(context.Background(), codexTestBatch()); err == nil {
				t.Fatal("timed-out qualification accepted")
			}
		})
	}
}

func TestWatchCodexCorruptState(t *testing.T) {
	for _, name := range []string{"version", "qualified version", "status", "batch", "digest", "target", "binary", "missing receipt", "receipt and confirmation", "attempt receipt", "unknown field", "trailing json"} {
		t.Run(name, func(t *testing.T) {
			d, dir, source := codexTestDelivery(t, "")
			calls := 0
			d.run = codexSuccessfulRunner(&calls)
			if err := d.deliver(context.Background(), codexTestBatch()); err != nil {
				t.Fatal(err)
			}
			state := *d.state
			switch name {
			case "version":
				state.Version = 2
			case "qualified version":
				state.Qualified = "0.164.0"
			case "status":
				state.Status = "done"
			case "batch":
				state.Batch = "bad"
			case "digest":
				state.Hash = "bad"
			case "target":
				state.Target = "--last"
			case "binary":
				state.Binary = "relative"
			case "missing receipt":
				state.Message = ""
			case "receipt and confirmation":
				state.OwnerConfirmed = true
			case "attempt receipt":
				state.Status = "attempting"
			}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if name == "unknown field" {
				data = append(data[:len(data)-1], []byte(`,"extra":true}`)...)
			}
			if name == "trailing json" {
				data = append(data, []byte(" {}")...)
			}
			if err := config.WritePrivate(d.path, data); err != nil {
				t.Fatal(err)
			}
			if _, err := newWatchCodex(dir, source, "reader", d.opts, time.Second); err == nil {
				t.Fatal("corrupt state accepted")
			}
		})
	}
}

// Exercise the actual CLI boundary: Poll wraps callback errors before Run
// returns them, so checking only direct adapter errors misses this regression.
func TestWatchCodexCLIDiagnostics(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"ambiguous replay", []string{"--codex-reconcile accepted or retry", "retry can duplicate"}},
		{"unsupported version", []string{"requires qualified codex-cli 0.163.0", "--codex-binary"}},
		{"oversized envelope", []string{"16 KiB", "reduce --max-bytes"}},
		{"private source failure", []string{"cannot open or lock channel subscription"}},
		{"private checkpoint failure", []string{"cannot open or lock channel subscription"}},
		{"private polling failure", []string{"channel polling or checkpoint failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(t.TempDir(), "private-source-marker")
			contents := "private-channel-content-marker\n"
			if tc.name == "oversized envelope" {
				contents = strings.Repeat("x", watchCodexMessageLimit) + "\n"
			}
			if tc.name == "private polling failure" {
				contents = strings.Repeat("x", channelwatch.DefaultMaxBytes+1) + "\n"
			}
			if tc.name != "private source failure" {
				if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "ambiguous replay" || tc.name == "private checkpoint failure" {
				w, err := channelwatch.New(context.Background(), channelwatch.Options{Dir: dir, File: source, Consumer: "reader", FromStart: true})
				if err != nil {
					t.Fatal(err)
				}
				d, err := newWatchCodex(dir, source, "reader", watchCodexOptions{thread: testCodexThread, binary: executable}, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
					if args[0] == "--version" {
						return []byte("codex-cli 0.163.0\n"), nil
					}
					return nil, errors.New("lost response private-child-marker")
				}
				delivered, pollErr := w.Poll(context.Background(), d.deliver)
				if closeErr := w.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if delivered || pollErr == nil || d.state == nil || d.state.Status != "attempting" {
					t.Fatal("did not seed a real pending attempt")
				}
				if tc.name == "private checkpoint failure" {
					key := sha256.Sum256([]byte(filepath.Clean(source) + "\x00reader"))
					checkpoint := filepath.Join(dir, "watch", hex.EncodeToString(key[:])+".json")
					// A JSON parse failure embeds this real private checkpoint path.
					if err := config.WritePrivate(checkpoint, []byte("not JSON")); err != nil {
						t.Fatal(err)
					}
				}
			}
			var stdout, stderr bytes.Buffer
			env := &cli.Env{Cwd: filepath.Dir(source), ConfigDir: dir, Stdout: &stdout, Stderr: &stderr}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			code := cli.Main(ctx, env, []string{"watch", "--file", source, "--consumer", "reader", "--from-start", "--once", "--codex-thread", testCodexThread, "--codex-binary", executable})
			if code != 1 {
				t.Fatalf("exit=%d; stderr=%q", code, stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("missing actionable diagnostic %q in %q", want, stderr.String())
				}
			}
			for _, secret := range []string{source, filepath.Dir(source), dir, executable, "private-channel-content-marker", "private-child-marker"} {
				if strings.Contains(stderr.String(), secret) || strings.Contains(stdout.String(), secret) {
					t.Error("private filesystem or child data leaked")
				}
			}
			if stdout.Len() != 0 {
				t.Error("failed Codex delivery wrote to stdout")
			}
		})
	}
}
