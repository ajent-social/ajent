// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"context"
	"reflect"
	"testing"
)

// fakeWriter is a minimal Writer used only to exercise the registry.
type fakeWriter struct {
	name string
}

func (f fakeWriter) Target() Target                                 { return Target{Name: f.name} }
func (f fakeWriter) Detect(Params) bool                             { return false }
func (f fakeWriter) Paths(Params) []string                          { return nil }
func (f fakeWriter) Apply(context.Context, Params) (Result, error)  { return Result{}, nil }
func (f fakeWriter) Remove(context.Context, Params) (Result, error) { return Result{}, nil }

func TestRegistryOrderAndDuplicatePanics(t *testing.T) {
	defer ResetForTest()()

	names := []string{"vscode", "unknownb", "claude", "unknowna", "codex", "gemini", "copilot", "cursor", "agentskills"}
	for _, n := range names {
		Register(fakeWriter{name: n})
	}

	got := make([]string, 0, len(names))
	for _, w := range Writers() {
		got = append(got, w.Target().Name)
	}

	want := []string{"claude", "codex", "cursor", "gemini", "copilot", "vscode", "agentskills", "unknowna", "unknownb"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Writers() order = %v, want %v", got, want)
	}

	// Writers() returns a copy: mutating it must not affect the registry.
	got[0] = "mutated"
	again := Writers()
	if again[0].Target().Name != "claude" {
		t.Fatalf("Writers() returned a shared slice: got %v", again[0].Target().Name)
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("Register duplicate: want panic, got none")
			}
		}()
		Register(fakeWriter{name: "claude"})
	}()
}
