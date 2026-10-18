// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goOnlyTools lists every tool name Tools() may return besides the 18
// contract tools of testdata/contract/tools.json. Empty after ADR 009
// retired the skills catalog tools.
var goOnlyTools = []string{}

// registryFixtureTool decodes one entry of testdata/contract/tools.json's
// "tools" array, keeping only the fields that make up the wire tool
// definition (Tool's JSON shape); the fixture's other fields (credential,
// requests, response, owner_task) are documentation for humans, not part
// of what a client sees, and are ignored here.
type registryFixtureTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *Annotations    `json:"annotations"`
}

// registryLoadContractTools reads and decodes the "tools" array of
// testdata/contract/tools.json.
func registryLoadContractTools(t *testing.T) []registryFixtureTool {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "contract", "tools.json")
	//nolint:gosec // G304: path is a fixed, repo-relative testdata literal, not external input.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var fixture struct {
		Tools []registryFixtureTool `json:"tools"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return fixture.Tools
}

// registrySchemaEqual reports whether two inputSchema byte strings decode
// to the same JSON value, ignoring formatting and key order.
func registrySchemaEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return jsonValuesEqual(av, bv)
}

// jsonValuesEqual compares two values produced by unmarshaling JSON into
// any (so maps, slices, strings, float64, bool and nil), recursively and
// order-independently for object keys.
func jsonValuesEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			bvv, ok := bv[k]
			if !ok || !jsonValuesEqual(v, bvv) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonValuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// TestToolsMatchContract proves that the 18 registered contract tools equal
// testdata/contract/tools.json byte-for-byte-equivalent, in the fixture's
// order, and that no other tool is registered beyond goOnlyTools.
func TestToolsMatchContract(t *testing.T) {
	fixture := registryLoadContractTools(t)

	fixtureByName := make(map[string]registryFixtureTool, len(fixture))
	fixtureOrder := make([]string, len(fixture))
	for i, f := range fixture {
		fixtureByName[f.Name] = f
		fixtureOrder[i] = f.Name
	}

	allowExtra := make(map[string]bool, len(goOnlyTools))
	for _, n := range goOnlyTools {
		allowExtra[n] = true
	}

	got := Tools()
	var contractOrder []string
	for _, tool := range got {
		f, isContract := fixtureByName[tool.Name]
		if !isContract {
			if !allowExtra[tool.Name] {
				t.Errorf("unexpected registered tool %q: not one of the 18 contract tools and not in goOnlyTools", tool.Name)
			}
			continue
		}
		contractOrder = append(contractOrder, tool.Name)

		if tool.Description != f.Description {
			t.Errorf("tool %s: description mismatch\n got:  %q\n want: %q", tool.Name, tool.Description, f.Description)
		}
		if !registrySchemaEqual(tool.InputSchema, f.InputSchema) {
			t.Errorf("tool %s: inputSchema mismatch\n got:  %s\n want: %s", tool.Name, tool.InputSchema, f.InputSchema)
		}
		switch {
		case tool.Annotations == nil || f.Annotations == nil:
			t.Errorf("tool %s: missing annotations (got %v, want %v)", tool.Name, tool.Annotations, f.Annotations)
		case *tool.Annotations != *f.Annotations:
			t.Errorf("tool %s: annotations mismatch: got %+v, want %+v", tool.Name, *tool.Annotations, *f.Annotations)
		}
	}

	if len(contractOrder) != len(fixtureOrder) {
		t.Fatalf("got %d contract tools registered, want %d\n got:  %v\n want: %v", len(contractOrder), len(fixtureOrder), contractOrder, fixtureOrder)
	}
	for i, name := range fixtureOrder {
		if contractOrder[i] != name {
			t.Fatalf("contract tool order mismatch at index %d: got %q, want %q\n got:  %v\n want: %v", i, contractOrder[i], name, contractOrder, fixtureOrder)
		}
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register did not panic on a duplicate name")
		}
	}()
	Register(Tool{Name: "ajent_search"})
}

func TestHandleUnknownPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Handle did not panic on an unknown name")
		}
		msg, ok := r.(string)
		if !ok || msg != `mcp: Handle: unknown tool "registry_test_no_such_tool"` {
			t.Errorf("panic value = %v, want the documented unknown-tool message", r)
		}
	}()
	Handle("registry_test_no_such_tool", func(context.Context, *Session, map[string]any) (any, error) {
		return nil, nil
	})
}

// registryTestRegister registers a temporary tool and removes it when the test
// ends, so tests stay independent of order (go test -shuffle=on).
func registryTestRegister(t *testing.T, tool Tool) {
	t.Helper()
	Register(tool)
	t.Cleanup(func() {
		registryMu.Lock()
		defer registryMu.Unlock()
		i, ok := registryIndex[tool.Name]
		if !ok {
			return
		}
		registryTools = append(registryTools[:i], registryTools[i+1:]...)
		delete(registryIndex, tool.Name)
		for j := i; j < len(registryTools); j++ {
			registryIndex[registryTools[j].Name] = j
		}
	})
}
