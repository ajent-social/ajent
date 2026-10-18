// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// HandlerFunc executes one tool call. It returns the value to envelope, or a
// Result to also carry delivery-cache receipts, or an error.
type HandlerFunc func(ctx context.Context, s *Session, args map[string]any) (any, error)

// Annotations are always emitted in full, matching the reference client.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// Tool is one MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *Annotations    `json:"annotations,omitempty"`
	Handler     HandlerFunc     `json:"-"`
}

// ToolError is returned by a handler that needs a specific isError payload.
// Serve writes the JSON of Payload as the text of an isError result, without
// the envelope.
type ToolError struct{ Payload any }

// Error implements the error interface.
func (e *ToolError) Error() string { return "mcp: tool error" }

// Result lets a handler return delivery-cache receipts alongside its value.
// Serve commits the receipts to Session.Cache only after the response line
// was written successfully.
type Result struct {
	Value    any
	Receipts []string
}

var (
	registryMu    sync.RWMutex
	registryTools []Tool
	registryIndex = map[string]int{}
)

// Register adds a tool definition. It panics on a duplicate name.
func Register(t Tool) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registryIndex[t.Name]; exists {
		panic(fmt.Sprintf("mcp: Register: duplicate tool %q", t.Name))
	}
	registryIndex[t.Name] = len(registryTools)
	registryTools = append(registryTools, t)
}

// Handle attaches the handler for an already-registered tool. Tool-group
// files (tools_read.go T3.3, tools_write.go T3.5, tools_goals.go T3.8) call
// it from init(). It panics on an unknown name so a typo fails every test
// immediately, and panics if a handler is already set.
func Handle(name string, h HandlerFunc) {
	registryMu.Lock()
	defer registryMu.Unlock()
	i, ok := registryIndex[name]
	if !ok {
		panic(fmt.Sprintf("mcp: Handle: unknown tool %q", name))
	}
	if registryTools[i].Handler != nil {
		panic(fmt.Sprintf("mcp: Handle: handler already set for %q", name))
	}
	registryTools[i].Handler = h
}

// Tools returns a copy of the registered tools in registration order: the 18
// contract tools of testdata/contract/tools.json, then any later tools
// registered afterward.
func Tools() []Tool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Tool, len(registryTools))
	copy(out, registryTools)
	return out
}

// registryLookup returns the registered tool named name, including its
// Handler, or ok == false when no such tool is registered.
func registryLookup(name string) (Tool, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	i, ok := registryIndex[name]
	if !ok {
		return Tool{}, false
	}
	return registryTools[i], true
}
