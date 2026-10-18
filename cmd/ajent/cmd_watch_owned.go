// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ajent-social/ajent-client/internal/channelwatch"
	"github.com/ajent-social/ajent-client/internal/config"
)

func ownedWatchError() error {
	return newWatchCodexError("Codex owned delivery stopped; batch may remain ambiguous; inspect private state, then use --codex-reconcile accepted or retry (retry can duplicate)")
}

func validWatchOwnedState(s watchCodexState) bool {
	digest := func(v string) bool {
		b, e := hex.DecodeString(v)
		return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
	}
	if s.Version != 1 || s.Mode != "owned" || !canonicalWatchUUID(s.Target) || !filepath.IsAbs(s.Binary) || filepath.Clean(s.Binary) != s.Binary || s.Qualified != watchCodexVersion || !digest(s.Batch) || !digest(s.Hash) || s.Message != "" {
		return false
	}
	if s.Turn != "" && !canonicalWatchUUID(s.Turn) {
		return false
	}
	return s.Status == "attempting" && !s.OwnerConfirmed || s.Status == "completed" && (s.Turn != "" || s.OwnerConfirmed)
}

// Generic delivery cannot acknowledge a batch whose model acceptance is unresolved.
func refusePendingWatchCodex(dir, source, consumer string) error {
	private := filepath.Join(dir, "watch-codex")
	if _, err := os.Lstat(private); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := config.EnsurePrivateDir(private); err != nil {
		return ownedWatchError()
	}
	key := sha256.Sum256([]byte(filepath.Clean(source) + "\x00" + consumer))
	data, err := config.ReadPrivate(filepath.Join(private, hex.EncodeToString(key[:])+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ownedWatchError()
	}
	var s watchCodexState
	if json.Unmarshal(data, &s) != nil || !validWatchCodexState(s) || s.Status == "attempting" {
		return ownedWatchError()
	}
	return nil
}

type watchOwnedDelivery struct {
	base    *watchCodexDelivery
	cwd     string
	out     io.Writer
	session *watchOwnedSession
	start   func(context.Context, string, string) (*watchOwnedSession, error)
}

func (d *watchOwnedDelivery) close() {
	if d.session != nil {
		d.session.close()
	}
}

func (d *watchOwnedDelivery) run(ctx context.Context, w *channelwatch.Watcher, interval time.Duration, once bool) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if d.session != nil {
			if err := d.session.health(); err != nil {
				return err
			}
		}
		delivered, err := w.Poll(ctx, d.deliver)
		if err != nil {
			return err
		}
		if delivered && once {
			return nil
		}
		var exited, readDone <-chan struct{}
		if d.session != nil {
			exited, readDone = d.session.exited, d.session.readDone
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return ownedWatchError()
		case <-readDone:
			return ownedWatchError()
		case <-ticker.C:
		}
	}
}

func (d *watchOwnedDelivery) receipt(s watchCodexState) error {
	// A failed output write leaves the already durable completion safe to replay.
	return json.NewEncoder(d.out).Encode(struct {
		Batch  string `json:"batch_id"`
		Thread string `json:"thread_id"`
		Turn   string `json:"turn_id"`
		Status string `json:"status"`
	}{s.Batch, s.Target, s.Turn, "completed"})
}

func (d *watchOwnedDelivery) deliver(ctx context.Context, b channelwatch.Batch) error {
	payload, err := json.Marshal(b)
	if err != nil {
		return ownedWatchError()
	}
	message := watchCodexGuidance + string(payload) + "\nEND UNTRUSTED CHANNEL BATCH\n"
	if len(message) > watchCodexMessageLimit {
		return newWatchCodexError("Codex delivery: full envelope exceeds 16 KiB; reduce --max-bytes; batch remains pending")
	}
	hash := sha256.Sum256([]byte(message))
	h := hex.EncodeToString(hash[:])
	if old := d.base.state; old != nil {
		same := old.Batch == b.ID && old.Hash == h
		if old.Batch == b.ID && !same || old.Status == "attempting" && !same {
			return ownedWatchError()
		}
		if old.Status == "completed" && same {
			return d.receipt(*old)
		}
		if old.Status == "attempting" {
			switch d.base.opts.reconcile {
			case "accepted":
				s := *old
				s.Status = "completed"
				s.OwnerConfirmed = true
				if err := d.base.save(s); err != nil {
					return err
				}
				d.base.opts.reconcile = ""
				return d.receipt(s)
			case "retry":
				d.base.opts.reconcile = ""
			default:
				return ownedWatchError()
			}
		}
	}
	if d.session == nil {
		probeCtx, cancel := context.WithTimeout(ctx, min(d.base.timeout, 30*time.Second))
		version, err := d.base.run(probeCtx, d.base.opts.binary, "--version")
		cancel()
		if err != nil || string(version) != "codex-cli "+watchCodexVersion+"\n" {
			return newWatchCodexError("Codex delivery: requires qualified codex-cli 0.163.0; select that version with --codex-binary")
		}
		s, err := d.start(ctx, d.base.opts.binary, d.cwd)
		if err != nil {
			return ownedWatchError()
		}
		d.session = s
		initCtx, cancel := context.WithTimeout(ctx, d.base.timeout)
		defer cancel()
		if err := s.initialize(initCtx, d.cwd); err != nil {
			return err
		}
	}
	s := watchCodexState{Version: 1, Mode: "owned", Target: d.session.thread, Binary: d.base.opts.binary, Qualified: watchCodexVersion, Batch: b.ID, Hash: h, Status: "attempting"}
	if !validWatchOwnedState(s) {
		return ownedWatchError()
	}
	if err := d.base.save(s); err != nil {
		return err
	}
	turnCtx, cancel := context.WithTimeout(ctx, d.base.timeout)
	defer cancel()
	turn, err := d.session.turn(turnCtx, message, func(id string) error { s.Turn = id; return d.base.save(s) })
	if err != nil {
		return err
	}
	s.Turn = turn
	s.Status = "completed"
	if err := d.base.save(s); err != nil {
		return err
	}
	return d.receipt(s)
}

type watchOwnedFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type watchOwnedCompletion struct {
	Thread string `json:"threadId"`
	Turn   struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

type watchOwnedSession struct {
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	frames   chan watchOwnedFrame
	readDone chan struct{}
	exited   chan struct{}
	cancel   context.CancelFunc
	next     int
	thread   string
	pending  *watchOwnedCompletion
}

func startWatchOwnedSession(ctx context.Context, binary, cwd string) (*watchOwnedSession, error) {
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, binary, "app-server", "--stdio") // #nosec G204 -- explicit executable and fixed argv, no shell.
	cmd.Dir = cwd
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, ownedWatchError()
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, ownedWatchError()
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, ownedWatchError()
	}
	s := &watchOwnedSession{stdin: stdin, stdout: stdout, frames: make(chan watchOwnedFrame, 1), readDone: make(chan struct{}), exited: make(chan struct{}), cancel: cancel}
	go func() { defer close(s.exited); _ = cmd.Wait() }()
	go func() {
		defer close(s.readDone)
		defer close(s.frames)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var f watchOwnedFrame
			if err := json.Unmarshal(scanner.Bytes(), &f); err != nil || (f.Method == "" && len(f.ID) == 0) {
				cancel()
				return
			}
			select {
			case s.frames <- f:
			case <-childCtx.Done():
				return
			}
		}
		// EOF and oversized output stop idle watching as well as in-flight work.
		cancel()
	}()
	return s, nil
}

func (s *watchOwnedSession) close() {
	s.cancel()
	_ = s.stdin.Close()
	_ = s.stdout.Close()
	<-s.exited
	<-s.readDone
}
func (s *watchOwnedSession) health() error {
	select {
	case <-s.exited:
		return ownedWatchError()
	default:
	}
	select {
	case <-s.readDone:
		return ownedWatchError()
	default:
	}
	// Drain bounded unsolicited messages while idle; server requests fail closed.
	for range 128 {
		select {
		case f, ok := <-s.frames:
			if !ok || f.Method == "" || len(f.ID) != 0 || f.Method == "turn/completed" {
				return ownedWatchError()
			}
		default:
			return nil
		}
	}
	return ownedWatchError()
}

func (s *watchOwnedSession) write(ctx context.Context, v any) error {
	// Cancellation kills the child and closes its pipes, unblocking a full stdin.
	stop := context.AfterFunc(ctx, func() { s.cancel(); _ = s.stdin.Close(); _ = s.stdout.Close() })
	defer stop()
	if ctx.Err() != nil {
		return ownedWatchError()
	}
	if err := json.NewEncoder(s.stdin).Encode(v); err != nil {
		return ownedWatchError()
	}
	return nil
}
func (s *watchOwnedSession) notify(ctx context.Context, method string) error {
	return s.write(ctx, map[string]any{"method": method})
}
func (s *watchOwnedSession) receive(ctx context.Context) (watchOwnedFrame, error) {
	select {
	case <-ctx.Done():
		return watchOwnedFrame{}, ownedWatchError()
	case f, ok := <-s.frames:
		if !ok {
			return f, ownedWatchError()
		}
		if len(f.ID) != 0 && f.Method != "" {
			return f, ownedWatchError()
		}
		return f, nil
	}
}
func (s *watchOwnedSession) completion(f watchOwnedFrame) error {
	var c watchOwnedCompletion
	if json.Unmarshal(f.Params, &c) != nil || c.Thread != s.thread || !canonicalWatchUUID(c.Turn.ID) || s.pending != nil {
		return ownedWatchError()
	}
	if c.Turn.Status != "completed" && c.Turn.Status != "failed" && c.Turn.Status != "interrupted" {
		return ownedWatchError()
	}
	s.pending = &c
	return nil
}
func (s *watchOwnedSession) request(ctx context.Context, method string, params any, allowCompletion bool) (json.RawMessage, error) {
	s.next++
	id := s.next
	if err := s.write(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	// Bound ignored notifications even when a malicious child floods stdout.
	for range 4096 {
		f, err := s.receive(ctx)
		if err != nil {
			return nil, err
		}
		if f.Method != "" {
			if f.Method == "turn/completed" {
				if !allowCompletion {
					return nil, ownedWatchError()
				}
				if err := s.completion(f); err != nil {
					return nil, err
				}
			}
			continue
		}
		var got int
		if json.Unmarshal(f.ID, &got) != nil || got != id || len(f.Error) != 0 || len(f.Result) == 0 || string(f.Result) == "null" {
			return nil, ownedWatchError()
		}
		return f.Result, nil
	}
	return nil, ownedWatchError()
}
func (s *watchOwnedSession) initialize(ctx context.Context, cwd string) error {
	if _, err := s.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "ajent-watch", "version": "1"}}, false); err != nil {
		return err
	}
	if err := s.notify(ctx, "initialized"); err != nil {
		return err
	}
	result, err := s.request(ctx, "thread/start", map[string]any{"cwd": cwd, "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": true}, false)
	if err != nil {
		return err
	}
	var r struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &r) != nil || !canonicalWatchUUID(r.Thread.ID) {
		return ownedWatchError()
	}
	s.thread = r.Thread.ID
	return nil
}
func (s *watchOwnedSession) turn(ctx context.Context, message string, save func(string) error) (string, error) {
	if s.pending != nil {
		return "", ownedWatchError()
	}
	result, err := s.request(ctx, "turn/start", map[string]any{"threadId": s.thread, "input": []any{map[string]any{"type": "text", "text": message, "text_elements": []any{}}}}, true)
	if err != nil {
		return "", err
	}
	var r struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	if json.Unmarshal(result, &r) != nil || !canonicalWatchUUID(r.Turn.ID) || (r.Turn.Status != "inProgress" && r.Turn.Status != "completed" && r.Turn.Status != "failed" && r.Turn.Status != "interrupted") {
		return "", ownedWatchError()
	}
	if err := save(r.Turn.ID); err != nil {
		return "", err
	}
	if r.Turn.Status == "failed" || r.Turn.Status == "interrupted" {
		return "", ownedWatchError()
	}
	for range 4096 {
		if s.pending != nil {
			c := s.pending
			s.pending = nil
			if c.Turn.ID != r.Turn.ID || c.Turn.Status != "completed" {
				return "", ownedWatchError()
			}
			return r.Turn.ID, nil
		}
		f, err := s.receive(ctx)
		if err != nil {
			return "", err
		}
		if f.Method == "" {
			return "", ownedWatchError()
		}
		if f.Method == "turn/completed" {
			if err := s.completion(f); err != nil {
				return "", err
			}
		}
	}
	return "", ownedWatchError()
}
