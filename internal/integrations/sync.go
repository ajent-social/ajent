// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"path/filepath"
	"time"

	"github.com/ajent-social/ajent/internal/config"
)

// syncExampleFile and syncStatusFile are always snapshotted and rewritten by
// Sync, in addition to every detected writer's Paths (docs/tasks/T4.6.md
// step 4).
const (
	syncExampleFile = "mcp-example.json"
	syncStatusFile  = "integration-status.json"
)

// syncStatus is the JSON shape written to
// <ConfigDir>/integration-status.json.
type syncStatus struct {
	Updated string   `json:"updated"`
	Backup  string   `json:"backup"`
	Results []Result `json:"results"`
}

// Sync snapshots every path any detected writer may modify (plus
// mcp-example.json and integration-status.json) before making any change,
// applies each detected writer in Writers() order, and records
// mcp-example.json and integration-status.json (docs/tasks/T4.6.md step 4;
// docs/adr/007-harness-integration-model.md).
func Sync(ctx context.Context, p Params) ([]Result, Backup, error) {
	ws := Writers()

	var detected []Writer
	seen := map[string]bool{}
	var paths []string
	for _, w := range ws {
		if !w.Detect(p) {
			continue
		}
		detected = append(detected, w)
		for _, path := range w.Paths(p) {
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}

	examplePath := filepath.Join(p.ConfigDir, syncExampleFile)
	statusPath := filepath.Join(p.ConfigDir, syncStatusFile)
	if !seen[examplePath] {
		paths = append(paths, examplePath)
	}
	if !seen[statusPath] {
		paths = append(paths, statusPath)
	}

	backup, err := Snapshot(p.ConfigDir, paths)
	if err != nil {
		return nil, Backup{}, err
	}

	results := make([]Result, 0, len(detected))
	for _, w := range detected {
		res, applyErr := w.Apply(ctx, p)
		if applyErr != nil {
			results = append(results, Result{Name: w.Target().Name, Status: "incomplete", Detail: applyErr.Error()})
			continue
		}
		results = append(results, res)
	}

	entry := Entry(p, "custom-agent")
	example := map[string]any{"mcpServers": map[string]any{"ajent": entry}}
	if err := config.WriteJSON(examplePath, example); err != nil {
		return results, backup, err
	}

	status := syncStatus{
		Updated: time.Now().UTC().Format(time.RFC3339),
		Backup:  backup.ID,
		Results: results,
	}
	if err := config.WriteJSON(statusPath, status); err != nil {
		return results, backup, err
	}

	return results, backup, nil
}
