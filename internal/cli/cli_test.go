// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/credentials"
)

// reset clears the command registry. It exists only for tests: each test
// that registers commands calls it first so tests never see each other's
// registrations.
func reset() {
	commands = make(map[string]Command)
	aliases = make(map[string]string)
}

// newBufEnv returns a minimal Env with buffered Stdout and Stderr.
func newBufEnv() (env *Env, stdout, stderr *bytes.Buffer) {
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	return &Env{Stdout: stdout, Stderr: stderr}, stdout, stderr
}

func noopRun(context.Context, *Env, []string) error { return nil }

func TestUnknownCommandExit2(t *testing.T) {
	reset()
	env, stdout, stderr := newBufEnv()

	code := Main(context.Background(), env, []string{"nope"})

	if code != 2 {
		t.Fatalf("Main() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `ajent: unknown command "nope"`) {
		t.Errorf("stderr = %q, want it to contain the unknown-command message", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage: ajent <command> [flags]") {
		t.Errorf("stderr = %q, want it to contain the usage listing", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestUsageErrorExit2(t *testing.T) {
	reset()
	Register(Command{
		Name:  "search",
		Usage: "ajent search [--group UUID] QUERY...",
		Run: func(context.Context, *Env, []string) error {
			return fmt.Errorf("missing QUERY argument: %w", ErrUsage)
		},
	})
	env, _, stderr := newBufEnv()

	code := Main(context.Background(), env, []string{"search"})

	if code != 2 {
		t.Fatalf("Main() code = %d, want 2", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "ajent: missing QUERY argument\n") {
		t.Errorf("stderr = %q, want it to contain the stripped usage message", out)
	}
	if !strings.Contains(out, "Usage: ajent search [--group UUID] QUERY...\n") {
		t.Errorf("stderr = %q, want it to contain the command's Usage line", out)
	}
}

func TestRuntimeErrorExit1Format(t *testing.T) {
	t.Run("bare error gets the ajent: prefix", func(t *testing.T) {
		reset()
		Register(Command{
			Name: "boom",
			Run: func(context.Context, *Env, []string) error {
				return errors.New("boom")
			},
		})
		env, _, stderr := newBufEnv()

		code := Main(context.Background(), env, []string{"boom"})

		if code != 1 {
			t.Fatalf("Main() code = %d, want 1", code)
		}
		if got, want := stderr.String(), "ajent: boom\n"; got != want {
			t.Errorf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("already-prefixed error is not double-prefixed", func(t *testing.T) {
		reset()
		Register(Command{
			Name: "boom",
			Run: func(context.Context, *Env, []string) error {
				return errors.New("ajent: already prefixed")
			},
		})
		env, _, stderr := newBufEnv()

		code := Main(context.Background(), env, []string{"boom"})

		if code != 1 {
			t.Fatalf("Main() code = %d, want 1", code)
		}
		if got, want := stderr.String(), "ajent: already prefixed\n"; got != want {
			t.Errorf("stderr = %q, want %q", got, want)
		}
	})
}

func TestAliasDispatch(t *testing.T) {
	reset()
	var gotArgs []string
	Register(Command{
		Name:    "setup",
		Aliases: []string{"install"},
		Run: func(_ context.Context, _ *Env, args []string) error {
			gotArgs = args
			return nil
		},
	})
	env, _, _ := newBufEnv()

	code := Main(context.Background(), env, []string{"install", "--agent-only"})

	if code != 0 {
		t.Fatalf("Main() code = %d, want 0", code)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "--agent-only" {
		t.Errorf("Run args = %v, want [--agent-only]", gotArgs)
	}
}

func TestDuplicateRegisterPanics(t *testing.T) {
	t.Run("duplicate name", func(t *testing.T) {
		reset()
		Register(Command{Name: "dup", Run: noopRun})
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("Register did not panic on a duplicate name")
			}
		}()
		Register(Command{Name: "dup", Run: noopRun})
	})

	t.Run("duplicate alias", func(t *testing.T) {
		reset()
		Register(Command{Name: "one", Aliases: []string{"shared"}, Run: noopRun})
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("Register did not panic on a duplicate alias")
			}
		}()
		Register(Command{Name: "two", Aliases: []string{"shared"}, Run: noopRun})
	})

	t.Run("alias collides with a command name", func(t *testing.T) {
		reset()
		Register(Command{Name: "shared", Run: noopRun})
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("Register did not panic on an alias colliding with a command name")
			}
		}()
		Register(Command{Name: "two", Aliases: []string{"shared"}, Run: noopRun})
	})
}

func TestHelpListsCommandsSorted(t *testing.T) {
	reset()
	Register(Command{Name: "zeta", Summary: "Z command", Run: noopRun})
	Register(Command{Name: "alpha", Summary: "A command", Run: noopRun})
	Register(Command{Name: "hidden", Summary: "should not appear", Hidden: true, Run: noopRun})

	tests := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"-h", []string{"-h"}},
		{"--help", []string{"--help"}},
		{"help", []string{"help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, stdout, stderr := newBufEnv()
			code := Main(context.Background(), env, tt.args)
			if code != 0 {
				t.Fatalf("Main() code = %d, want 0", code)
			}
			if stderr.String() != "" {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			out := stdout.String()
			ia, iz := strings.Index(out, "alpha"), strings.Index(out, "zeta")
			if ia == -1 || iz == -1 || ia > iz {
				t.Errorf("stdout = %q, want alpha listed before zeta", out)
			}
			if strings.Contains(out, "hidden") {
				t.Errorf("stdout = %q, want the hidden command omitted", out)
			}
			if !strings.HasPrefix(out, "Usage: ajent <command> [flags]\n") {
				t.Errorf("stdout = %q, want it to start with the usage line", out)
			}
		})
	}
}

func TestCredentialNilResolve(t *testing.T) {
	env := &Env{}
	_, err := env.Credential(context.Background(), "terminal")
	if !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("Credential() err = %v, want ErrNoCredential", err)
	}
}

func TestUserErrorPrintedVerbatim(t *testing.T) {
	Register(Command{Name: "usererror-probe", Usage: "ajent usererror-probe", Run: func(context.Context, *Env, []string) error {
		return UserError("Supply a goal UUID.")
	}})
	var stdout, stderr strings.Builder
	code := Main(context.Background(), &Env{Stdout: &stdout, Stderr: &stderr}, []string{"usererror-probe"})
	if code != 1 || stderr.String() != "ajent: Supply a goal UUID.\n" {
		t.Fatalf("Main() = %d, stderr %q; want 1, %q", code, stderr.String(), "ajent: Supply a goal UUID.\n")
	}
}
