// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajent-social/ajent-client/internal/cli"
)

func TestWatchFromStart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ajent.social")
	if err := os.WriteFile(file, []byte("A checked finding.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env := &cli.Env{Cwd: dir, ConfigDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := cli.Main(ctx, env, []string{"watch", "--file", file, "--consumer", "reviewer", "--from-start", "--once"})
	if code != 0 {
		t.Fatalf("watch exit = %d; stderr = %q", code, stderr.String())
	}
	var batch struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &batch); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(batch.Lines) != 1 || batch.Lines[0] != "A checked finding." {
		t.Fatalf("unexpected lines: %#v", batch.Lines)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "A checked finding.\n" {
		t.Fatalf("watch changed channel: %q, %v", got, err)
	}
}

func TestWatchRejectInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--interval", "0s"}, {"--interval", "-1s"}, {"--max-bytes", "0"},
		{"--max-bytes", "1048577"}, {"--file", ""}, {"--consumer", ""}, {"--consumer", " "},
		{"--command-timeout", "0s"}, {"--unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := &cli.Env{Cwd: t.TempDir(), ConfigDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr}
			if code := cli.Main(context.Background(), env, append([]string{"watch"}, args...)); code != 2 {
				t.Fatalf("exit=%d, stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestWatchCommandDelivery(t *testing.T) {
	// A standalone fixture has no model, network or test-binary startup hooks.
	// Its timeout case exercises cancellation without an orphaned build process.
	helperDir := t.TempDir()
	source := filepath.Join(helperDir, "main.go")
	executable := filepath.Join(helperDir, "handler.exe")
	const helper = `// SPDX-License-Identifier: Apache-2.0
package main
import ("io"; "os"; "os/signal")
func main() {
  switch os.Args[1] {
  case "fail": os.Exit(19)
  case "timeout":
    ch := make(chan os.Signal, 1)
    signal.Notify(ch, os.Interrupt)
    <-ch
    return
  }
  if _, err := io.Copy(os.Stdout, os.Stdin); err != nil { os.Exit(20) }
}
`
	if err := os.WriteFile(source, []byte(helper), 0o600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", executable, source)
	build.Dir = helperDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build delivery fixture: %v: %s", err, out)
	}
	for _, mode := range []string{"ok", "fail", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "ajent.social")
			if err := os.WriteFile(file, []byte("$(do-not-execute)\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			env := &cli.Env{Cwd: dir, ConfigDir: t.TempDir(), Stdout: &stdout, Stderr: &stderr}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			timeout := "5s"
			if mode == "timeout" {
				timeout = "100ms"
			}
			code := cli.Main(ctx, env, []string{"watch", "--file", file, "--from-start", "--once",
				"--command-timeout", timeout, "--", executable, mode})
			if mode == "ok" {
				if code != 0 || !strings.Contains(stdout.String(), "$(do-not-execute)") {
					t.Fatalf("handler exit=%d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
				}
				return
			}
			if code != 1 {
				t.Fatalf("failed handler exit=%d, stderr=%q", code, stderr.String())
			}
			if mode == "timeout" && !strings.Contains(stderr.String(), "context deadline exceeded") {
				t.Fatalf("handler was not stopped by its timeout: %q", stderr.String())
			}
			stdout.Reset()
			stderr.Reset()
			if code := cli.Main(ctx, env, []string{"watch", "--file", file, "--once"}); code != 0 || !strings.Contains(stdout.String(), "$(do-not-execute)") {
				t.Fatalf("retry exit=%d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}
