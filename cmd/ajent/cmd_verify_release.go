// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/update"
)

func init() {
	cli.Register(cli.Command{
		Name:    "verify-release",
		Hidden:  true,
		Summary: "Verify a release checksums file and its signature",
		Usage:   "ajent verify-release --checksums PATH --sig PATH [--file PATH]",
		Flags:   verifyReleaseFlags,
		Run:     runVerifyRelease,
	})
}

// newVerifyReleaseFlagSet builds verify-release's FlagSet and returns its
// flag values alongside it, so runVerifyRelease and verifyReleaseFlags
// share one definition.
func newVerifyReleaseFlagSet() (fs *flag.FlagSet, checksumsPath, sigPath, filePath *string) {
	fs = flag.NewFlagSet("verify-release", flag.ContinueOnError)
	checksumsPath = fs.String("checksums", "", "path to checksums.txt (required)")
	sigPath = fs.String("sig", "", "path to checksums.txt.sig (required)")
	filePath = fs.String("file", "", "also verify this file's SHA-256 hash against checksums, by its base name")
	return fs, checksumsPath, sigPath, filePath
}

// verifyReleaseFlags returns a fresh FlagSet with verify-release's flag
// definitions, used by "ajent help verify-release".
func verifyReleaseFlags() *flag.FlagSet {
	fs, _, _, _ := newVerifyReleaseFlagSet()
	return fs
}

// runVerifyRelease implements the hidden "ajent verify-release" command
// (docs/tasks/T5.3.md step 7): it checks the Ed25519 signature over
// --checksums against update.PublicKeys, and, when --file is given, also
// checks that file's SHA-256 hash against its entry in --checksums (looked
// up by its base name). It prints "ok" to stdout and returns nil on
// success. Any verification failure is returned as an error whose message
// is "release verification failed: <reason>", which cli.Main reports with
// exit code 1.
func runVerifyRelease(_ context.Context, env *cli.Env, args []string) error {
	fs, checksumsPath, sigPath, filePath := newVerifyReleaseFlagSet()
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *checksumsPath == "" || *sigPath == "" {
		return fmt.Errorf("--checksums and --sig are required: %w", cli.ErrUsage)
	}

	// #nosec G304 -- checksumsPath is the operator-supplied --checksums flag; verify-release's whole purpose is reading a file the caller names.
	checksums, err := os.ReadFile(*checksumsPath)
	if err != nil {
		return fmt.Errorf("release verification failed: reading %s: %w", *checksumsPath, err)
	}
	// #nosec G304 -- sigPath is the operator-supplied --sig flag; verify-release's whole purpose is reading a file the caller names.
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		return fmt.Errorf("release verification failed: reading %s: %w", *sigPath, err)
	}

	if err := update.VerifyChecksums(checksums, sig, update.PublicKeys); err != nil {
		return fmt.Errorf("release verification failed: %w", err)
	}

	if *filePath != "" {
		// #nosec G304 -- filePath is the operator-supplied --file flag; verify-release's whole purpose is reading a file the caller names.
		data, err := os.ReadFile(*filePath)
		if err != nil {
			return fmt.Errorf("release verification failed: reading %s: %w", *filePath, err)
		}
		if err := update.VerifyFile(checksums, filepath.Base(*filePath), data); err != nil {
			return fmt.Errorf("release verification failed: %w", err)
		}
	}

	_, err = fmt.Fprintln(env.Stdout, "ok")
	return err
}
