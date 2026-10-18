// SPDX-License-Identifier: Apache-2.0

package mcp

import "testing"

// TestAllToolsHaveHandlers proves every registered tool has a handler
// attached, so a tool can never ship half-wired. It is complete once
// tools_read.go (T3.3), tools_write.go (T3.5), tools_goals.go (T3.8) and
// tools_skills.go (T3.10) have all registered their handlers from their own
// package init() functions (docs/tasks/T3.7.md).
func TestAllToolsHaveHandlers(t *testing.T) {
	tools := Tools()
	if len(tools) == 0 {
		t.Fatal("Tools() returned no tools")
	}

	var missing []string
	for _, tool := range tools {
		if tool.Handler == nil {
			missing = append(missing, tool.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("tools registered without a handler: %v", missing)
	}
}
