// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ajent-social/ajent/internal/channelwatch"
	"github.com/ajent-social/ajent/internal/cli"
)

func init() {
	cli.Register(cli.Command{
		Name: "watch", Summary: "Deliver unread local channel lines without model calls while idle",
		Usage: "ajent watch [--file PATH] [--consumer NAME] [--from-start] [--once] [--interval DURATION] [--max-bytes N] [--command-timeout DURATION] [--codex-owned | --codex-thread UUID] [--codex-binary PATH] [--codex-reconcile accepted|retry] [-- COMMAND ARGS...]",
		Flags: watchFlags, Run: runWatch,
	})
}

func watchFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.Bool("codex-owned", false, "own a lazy foreground Codex app-server session")
	fs.String("codex-thread", "", "explicit canonical Codex thread UUID (requires --once)")
	fs.String("codex-binary", "codex", "Codex executable for explicit thread delivery")
	fs.String("codex-reconcile", "", "resolve an ambiguous delivery: accepted or retry (retry can duplicate)")
	fs.String("file", "ajent.social", "local append-only channel to read")
	fs.String("consumer", "terminal", "independent persistent reader name")
	fs.Bool("from-start", false, "read existing complete lines on first use of this consumer")
	fs.Bool("once", false, "stop after one successful nonblank delivery; wait if none are unread")
	fs.Duration("interval", time.Second, "local polling interval (no model calls)")
	fs.Int("max-bytes", channelwatch.DefaultMaxBytes, "maximum bytes per delivery")
	fs.Duration("command-timeout", 5*time.Minute, "timeout for each explicitly selected handler command")
	return fs
}

func runWatch(ctx context.Context, env *cli.Env, args []string) (retErr error) {
	fs := watchFlags()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %w", cli.ErrUsage, err)
	}
	interval := fs.Lookup("interval").Value.(flag.Getter).Get().(time.Duration)
	timeout := fs.Lookup("command-timeout").Value.(flag.Getter).Get().(time.Duration)
	maxBytes := fs.Lookup("max-bytes").Value.(flag.Getter).Get().(int)
	if interval <= 0 || timeout <= 0 || maxBytes < 1 || maxBytes > channelwatch.MaxBytesLimit {
		return fmt.Errorf("%w: durations must be positive and max-bytes must be 1–%d", cli.ErrUsage, channelwatch.MaxBytesLimit)
	}
	file := fs.Lookup("file").Value.String()
	consumer := fs.Lookup("consumer").Value.String()
	if file == "" || strings.TrimSpace(consumer) == "" || len(consumer) > 128 {
		return fmt.Errorf("%w: file and consumer (1–128 bytes) are required", cli.ErrUsage)
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(env.Cwd, file)
	}
	codexOpts, err := parseWatchCodex(fs)
	if err != nil {
		return err
	}
	dir, err := env.Dir()
	if err != nil {
		if codexOpts != nil {
			return newWatchCodexError("Codex delivery: private config directory unavailable")
		}
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	w, err := channelwatch.New(ctx, channelwatch.Options{
		File: file, Dir: dir, Consumer: consumer, MaxBytes: maxBytes,
		FromStart: fs.Lookup("from-start").Value.(flag.Getter).Get().(bool),
	})
	if err != nil {
		if codexOpts != nil {
			return newWatchCodexError("Codex delivery: cannot open or lock channel subscription")
		}
		return err
	}
	defer func() {
		err := w.Close()
		if err != nil && codexOpts != nil {
			err = newWatchCodexError("Codex delivery: cannot close channel subscription")
		}
		retErr = errors.Join(retErr, err)
	}()
	if codexOpts == nil {
		if err := refusePendingWatchCodex(dir, file, consumer); err != nil {
			return err
		}
	}
	handler := fs.Args()
	deliver := func(ctx context.Context, batch channelwatch.Batch) error {
		payload, err := json.Marshal(batch)
		if err != nil {
			return fmt.Errorf("encode channel delivery: %w", err)
		}
		payload = append(payload, '\n')
		if len(handler) == 0 {
			n, err := env.Stdout.Write(payload)
			if err == nil && n != len(payload) {
				err = io.ErrShortWrite
			}
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		// The executable and arguments come only from the user's CLI invocation.
		// Channel text is stdin data; it is never evaluated by a shell.
		cmd := exec.CommandContext(ctx, handler[0], handler[1:]...) // #nosec G204 -- caller-selected command; channel text is only stdin data.
		cmd.Dir, cmd.Stdin = env.Cwd, bytes.NewReader(payload)
		cmd.Stdout, cmd.Stderr = env.Stdout, env.Stderr
		cmd.WaitDelay = time.Second
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("notification handler failed: %w", errors.Join(err, ctx.Err()))
		}
		return nil
	}
	if codexOpts != nil {
		var delivery *watchCodexDelivery
		delivery, err = newWatchCodex(dir, file, consumer, *codexOpts, timeout)
		if err != nil {
			return err
		}
		if codexOpts.owned {
			owned := &watchOwnedDelivery{base: delivery, cwd: env.Cwd, out: env.Stdout, start: startWatchOwnedSession}
			defer owned.close()
			err = owned.run(ctx, w, interval, fs.Lookup("once").Value.(flag.Getter).Get().(bool))
		} else {
			deliver = delivery.deliver
		}
	}
	if codexOpts == nil || !codexOpts.owned {
		err = w.Run(ctx, interval, fs.Lookup("once").Value.(flag.Getter).Get().(bool), deliver)
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	if err != nil && codexOpts != nil {
		var deliveryErr *watchCodexError
		if errors.As(err, &deliveryErr) {
			return deliveryErr
		}
		return newWatchCodexError("Codex delivery: channel polling or checkpoint failed; batch may remain pending; inspect private state before retrying")
	}
	return err
}
