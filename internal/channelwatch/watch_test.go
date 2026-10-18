// SPDX-License-Identifier: Apache-2.0

package channelwatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ajent-social/ajent-client/internal/channelwatch"
)

func fixture(t *testing.T, contents string, fromStart bool, limit int) channelwatch.Options {
	t.Helper()
	opts := channelwatch.Options{File: filepath.Join(t.TempDir(), "ajent.social"),
		Dir: t.TempDir(), Consumer: "reader", FromStart: fromStart, MaxBytes: limit}
	write(t, opts.File, contents)
	return opts
}

func write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, opts channelwatch.Options) *channelwatch.Watcher {
	t.Helper()
	w, err := channelwatch.New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}

func TestPollCompleteLines(t *testing.T) {
	tests := []struct {
		name, before, after string
		fromStart           bool
		limit               int
		want                []string
	}{
		{name: "skip backlog while idle", before: "old\n", after: "old\n"},
		{name: "new complete line", before: "old\n", after: "old\nnew\n", want: []string{"new"}},
		{name: "partial line waits", before: "old\n", after: "old\npartial"},
		{name: "initial partial finishes", before: "old\npar", after: "old\npartial\n", want: []string{"partial"}},
		{name: "blank lines acknowledged silently", before: "", after: " \r\n\n"},
		{name: "CRLF", before: "one\r\ntwo\r\n", after: "one\r\ntwo\r\n", fromStart: true, want: []string{"one", "two"}},
		{name: "bounded batch", before: "one\ntwo\nend\n", after: "one\ntwo\nend\n", fromStart: true, limit: 8, want: []string{"one", "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := fixture(t, tt.before, tt.fromStart, tt.limit)
			w := open(t, opts)
			write(t, opts.File, tt.after)
			var got []string
			delivered, err := w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
				got = b.Lines
				if len(b.ID) != 64 || b.To <= b.From || b.Guidance == "" || b.Type != "ajent.social.unread" {
					t.Fatalf("invalid delivery envelope: %#v", b)
				}
				return nil
			})
			if err != nil || delivered != (tt.want != nil) || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("delivered=%v, lines=%#v, err=%v; want %#v", delivered, got, err, tt.want)
			}
		})
	}
}

func TestPollRetryAndRestart(t *testing.T) {
	opts := fixture(t, "one\n", true, 0)
	w, err := channelwatch.New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if w != nil {
			if err := w.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	failure := errors.New("handler failed")
	var first channelwatch.Batch
	delivered, err := w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
		first = b
		return failure
	})
	if delivered || !errors.Is(err, failure) {
		t.Fatalf("failed delivery: delivered=%v, err=%v", delivered, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w = nil
	w = open(t, opts)
	var replay channelwatch.Batch
	delivered, err = w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
		replay = b
		return nil
	})
	if err != nil || !delivered || !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay differs: %#v vs %#v; delivered=%v err=%v", first, replay, delivered, err)
	}
	// open owns the cleanup for this handle.
	w = nil
}

func TestPollAcknowledgedRestart(t *testing.T) {
	opts := fixture(t, "first\n", true, 0)
	w, err := channelwatch.New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	_, pollErr := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error { return nil })
	if err := errors.Join(pollErr, w.Close()); err != nil {
		t.Fatal(err)
	}
	w = open(t, opts)
	for range 3 {
		delivered, err := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error {
			t.Fatal("acknowledged lines invoked handler after restart")
			return nil
		})
		if delivered || err != nil {
			t.Fatalf("idle delivery=%v, err=%v", delivered, err)
		}
	}
	write(t, opts.File, "first\nsecond\n")
	_, err = w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
		if !reflect.DeepEqual(b.Lines, []string{"second"}) {
			t.Fatalf("got lines %#v", b.Lines)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPollRotationAndTruncation(t *testing.T) {
	for _, mode := range []string{"rename", "truncate", "same-size replacement", "briefly missing"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			opts := fixture(t, "old\n", false, 0)
			w := open(t, opts)
			if mode == "rename" || mode == "briefly missing" {
				if err := os.Rename(opts.File, opts.File+".old"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "briefly missing" {
				delivered, err := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error {
					t.Fatal("missing channel invoked handler")
					return nil
				})
				if delivered || err != nil {
					t.Fatalf("missing channel: delivered=%v err=%v", delivered, err)
				}
			}
			if mode == "truncate" {
				write(t, opts.File, "")
				if delivered, err := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error {
					t.Fatal("empty truncated channel invoked handler")
					return nil
				}); delivered || err != nil {
					t.Fatalf("empty truncation: delivered=%v err=%v", delivered, err)
				}
			}
			write(t, opts.File, "new\n")
			delivered, err := w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
				if !b.Replaced || b.From != 0 || !reflect.DeepEqual(b.Lines, []string{"new"}) {
					t.Fatalf("invalid replacement delivery: %#v", b)
				}
				return nil
			})
			if !delivered || err != nil {
				t.Fatalf("rotation: delivered=%v, err=%v", delivered, err)
			}
		})
	}
}

func TestPollAppendsDuringDelivery(t *testing.T) {
	opts := fixture(t, "first\n", true, 0)
	w := open(t, opts)
	_, err := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error {
		write(t, opts.File, "first\nsecond\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
		if !reflect.DeepEqual(b.Lines, []string{"second"}) {
			t.Fatalf("concurrent append was lost: %#v", b.Lines)
		}
		return nil
	})
	if !delivered || err != nil {
		t.Fatalf("pending append: delivered=%v err=%v", delivered, err)
	}
}

func TestPollRejectInvalidLines(t *testing.T) {
	for _, tt := range []struct{ name, data string }{
		{"oversized", "0123456789\n"}, {"invalid UTF8", "\xff\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := fixture(t, tt.data, true, 8)
			w := open(t, opts)
			for range 2 {
				delivered, err := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error {
					t.Fatal("invalid lines invoked handler")
					return nil
				})
				if delivered || err == nil {
					t.Fatalf("invalid lines: delivered=%v err=%v", delivered, err)
				}
			}
		})
	}
}

func TestNewLocksPerConsumer(t *testing.T) {
	opts := fixture(t, "", false, 0)
	open(t, opts)
	if other, err := channelwatch.New(context.Background(), opts); err == nil {
		if closeErr := other.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("duplicate subscription acquired the lock")
	}
	opts.Consumer = "another-reader"
	open(t, opts)
}

func TestRunCancellationAndOnce(t *testing.T) {
	for _, once := range []bool{false, true} {
		t.Run(map[bool]string{true: "once", false: "cancel"}[once], func(t *testing.T) {
			w := open(t, fixture(t, "pending\n", true, 0))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := w.Run(ctx, time.Hour, once, func(context.Context, channelwatch.Batch) error {
				calls++
				if !once {
					cancel()
				}
				return nil
			})
			if calls != 1 || (once && err != nil) || (!once && !errors.Is(err, context.Canceled)) {
				t.Fatalf("calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestPollRetryAfterAppend(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "same watcher", true: "restart"}[restart], func(t *testing.T) {
			opts := fixture(t, "one\n", true, 0)
			w, err := channelwatch.New(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Error(err)
				}
			}()
			var first channelwatch.Batch
			_, err = w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error { first = b; return errors.New("reject") })
			if err == nil {
				t.Fatal("expected rejection")
			}
			write(t, opts.File, "one\ntwo\n")
			if restart {
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w, err = channelwatch.New(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
				if !reflect.DeepEqual(first, b) {
					t.Fatalf("replay changed: first=%#v retry=%#v", first, b)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = w.Poll(context.Background(), func(_ context.Context, b channelwatch.Batch) error {
				if !reflect.DeepEqual(b.Lines, []string{"two"}) {
					t.Fatalf("next batch=%#v", b.Lines)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPollPendingWhitespaceRewrite(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "same watcher", true: "restart"}[restart], func(t *testing.T) {
			opts := fixture(t, "one\n", true, 0)
			w, err := channelwatch.New(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Error(err)
				}
			}()
			_, err = w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error { return errors.New("reject") })
			if err == nil {
				t.Fatal("expected rejection")
			}
			write(t, opts.File, "   \n")
			if restart {
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				w, err = channelwatch.New(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				delivered, err := w.Poll(context.Background(), func(context.Context, channelwatch.Batch) error {
					t.Fatal("rewritten pending text was delivered")
					return nil
				})
				if err == nil || delivered {
					t.Fatalf("rewritten pending text acknowledged: delivered=%v err=%v", delivered, err)
				}
			}
		})
	}
}
