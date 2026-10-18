// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/cli"
	"github.com/ajent-social/ajent-client/internal/config"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/integrations"
)

// init appends the Python-migration offer to PreSetup, so "ajent setup"
// offers it automatically (docs/tasks/T4.9.md step 4).
func init() {
	PreSetup = append(PreSetup, migrateOfferDuringSetup)
}

// errMigrateForeignServer is returned by PlanMigration (and therefore by
// Migrate, which computes the plan first) when the Python-era root
// credential belongs to a server other than the one currently configured
// (docs/tasks/T4.9.md step 2.2).
var errMigrateForeignServer = errors.New("setup: the saved credential belongs to another server; migration is not supported for it")

// migrateHintLater is the line printed whenever the migration offer is
// declined, or cannot be asked interactively, or has already been acted on
// (docs/tasks/T4.9.md steps 4 and 5).
const migrateHintLater = "Run ajent migrate later to move your coding-tool configuration."

// Plan is the set of changes PlanMigration would make; Migrate makes them
// (docs/interfaces.md, internal/setup).
type Plan struct{ Changes []string }

// DetectPython reports whether configDir holds an earlier Python
// installation: a regular file named client.py (docs/tasks/T4.9.md step 1).
func DetectPython(configDir string) bool {
	info, err := os.Lstat(filepath.Join(configDir, "client.py"))
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// PlanMigration describes, without changing anything, what Migrate would do
// for env: keep the root credential and its tool profiles unchanged,
// rewrite every detected harness entry that still launches the Python
// client, and leave the Python client files in place
// (docs/tasks/T4.9.md step 2). It returns an empty Plan and a nil error
// when there is nothing to migrate.
func PlanMigration(_ context.Context, env *cli.Env) (Plan, error) {
	if !DetectPython(env.ConfigDir) {
		return Plan{}, nil
	}

	root, err := credentials.LoadRoot(env.ConfigDir)
	if err != nil {
		return Plan{}, err
	}
	if !api.Trusted(root.Server) {
		return Plan{}, errMigrateForeignServer
	}

	profileCount, err := migrateCountProfiles(credentials.ProfileDir(env.ConfigDir, root))
	if err != nil {
		return Plan{}, err
	}

	var plan Plan
	plan.Changes = append(plan.Changes, fmt.Sprintf("keep credential and %d tool profiles unchanged", profileCount))

	p, _ := integrations.ParamsFromEnv(env.Home, env.ConfigDir, env.Getenv)
	for _, w := range integrations.Writers() {
		if !w.Detect(p) {
			continue
		}
		if migratePathsMentionClientPy(w.Paths(p)) {
			plan.Changes = append(plan.Changes, fmt.Sprintf("rewrite %s entry to launch %s", w.Target().Name, p.Binary))
		}
	}

	plan.Changes = append(plan.Changes, fmt.Sprintf("keep Python client files in %s", env.ConfigDir))

	return plan, nil
}

// migrateCountProfiles counts the profile credential files (".json",
// excluding ".lock" files) directly under dir. A missing directory counts
// as zero (docs/tasks/T4.9.md step 2.3).
func migrateCountProfiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("setup: reading profile directory %s: %w", dir, err)
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count, nil
}

// migratePathsMentionClientPy reports whether any file in paths exists and
// its bytes contain "client.py" (docs/tasks/T4.9.md step 2.4). A path that
// does not exist, or cannot be read, is silently skipped: PlanMigration
// only describes what Sync would find, it never fails on a harness file it
// cannot yet see.
func migratePathsMentionClientPy(paths []string) bool {
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from a registered Writer's own Paths(p) (docs/interfaces.md), the same paths Sync and Snapshot already trust.
		if err != nil {
			continue
		}
		if bytes.Contains(data, []byte("client.py")) {
			return true
		}
	}
	return false
}

// Migrate moves an earlier Python installation to this binary
// (docs/tasks/T4.9.md step 3): it reuses the root credential and tool
// profiles unchanged, applies every registered Writer through Sync (which
// already treats a Python-era entry as ajent-owned and replaces it,
// T4.17's Owned), replaces the Python-era shared skill, and records the
// migration so "ajent setup" offers it only once and it can be rolled back
// with the returned Backup's ID.
func Migrate(ctx context.Context, env *cli.Env) (integrations.Backup, error) {
	if _, err := PlanMigration(ctx, env); err != nil {
		return integrations.Backup{}, err
	}

	p, err := integrations.ParamsFromEnv(env.Home, env.ConfigDir, env.Getenv)
	if err != nil {
		return integrations.Backup{}, err
	}

	results, backup, err := integrations.Sync(ctx, p)
	if err != nil {
		return integrations.Backup{}, err
	}

	migration := map[string]any{
		"from":        "python",
		"migrated_at": time.Now().UTC().Format(time.RFC3339),
		"backup":      backup.ID,
	}
	if err := config.WriteJSON(filepath.Join(env.ConfigDir, "migration.json"), migration); err != nil {
		return integrations.Backup{}, err
	}

	migratePrintResults(env, results)
	if _, err := fmt.Fprintf(env.Stdout, "Migration complete. Roll back with: ajent rollback %s\n", backup.ID); err != nil {
		return integrations.Backup{}, err
	}
	if _, err := fmt.Fprintln(env.Stdout, "Reload MCP connections or start new agent sessions."); err != nil {
		return integrations.Backup{}, err
	}

	return backup, nil
}

// migratePrintResults prints one "<name>: <status>" line per result to
// env.Stdout, appending ": <detail>" when Detail is set
// (docs/tasks/T4.9.md step 3.4).
func migratePrintResults(env *cli.Env, results []integrations.Result) {
	for _, r := range results {
		if r.Detail != "" {
			_, _ = fmt.Fprintf(env.Stdout, "%s: %s: %s\n", r.Name, r.Status, r.Detail)
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "%s: %s\n", r.Name, r.Status)
		}
	}
}

// migrateOfferDuringSetup offers to migrate a detected Python installation
// during "ajent setup", before enrollment (docs/tasks/T4.9.md step 4). It
// changes nothing when there is no Python installation to migrate, or when
// a migration has already run (<ConfigDir>/migration.json exists). In a
// non-interactive session, or on any answer other than empty, "y" or
// "yes", it prints a hint to run "ajent migrate" later instead of
// migrating.
func migrateOfferDuringSetup(ctx context.Context, env *cli.Env, _ Options) error {
	if !DetectPython(env.ConfigDir) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(env.ConfigDir, "migration.json")); err == nil {
		return nil
	}

	proceed := false
	if env.IsTerminal(0) {
		if _, err := fmt.Fprint(env.Stdout, "An earlier Python installation was found. Move your coding-tool configuration to ajent now? [Y/n] "); err != nil {
			return err
		}
		answer, err := migrateReadYesNo(env.Stdin)
		if err != nil {
			return err
		}
		proceed = answer
	}

	if proceed {
		_, err := Migrate(ctx, env)
		return err
	}

	_, err := fmt.Fprintln(env.Stdout, migrateHintLater)
	return err
}

// migrateReadYesNo reads one line from r and reports whether it is empty,
// "y" or "yes" (case-insensitive after trimming), the migration offer's
// affirmative answers (docs/tasks/T4.9.md step 4).
func migrateReadYesNo(r io.Reader) (bool, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("setup: reading migration prompt answer: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes", nil
}
