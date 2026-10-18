// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"testing"
)

// validateTestSchema exercises every keyword validateArgs supports: all
// four types, enum, minimum and maximum, required and
// additionalProperties: false.
var validateTestSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"s": {"type": "string", "enum": ["a", "b"]},
		"b": {"type": "boolean"},
		"i": {"type": "integer", "minimum": 1, "maximum": 5},
		"o": {"type": "object"}
	},
	"required": ["s"],
	"additionalProperties": false
}`)

func TestValidateArgs(t *testing.T) {
	tests := []struct {
		name    string
		schema  json.RawMessage
		args    map[string]any
		wantErr bool
	}{
		{
			name:    "unknown key",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "surprise": 1},
			wantErr: true,
		},
		{
			name:    "missing required key",
			schema:  validateTestSchema,
			args:    map[string]any{},
			wantErr: true,
		},
		{
			name:    "wrong type string",
			schema:  validateTestSchema,
			args:    map[string]any{"s": 5},
			wantErr: true,
		},
		{
			name:    "wrong type boolean",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "b": "nope"},
			wantErr: true,
		},
		{
			name:    "wrong type integer",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "i": "nope"},
			wantErr: true,
		},
		{
			name:    "wrong type object",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "o": "nope"},
			wantErr: true,
		},
		{
			name:    "float in place of integer",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "i": 2.5},
			wantErr: true,
		},
		{
			name:    "below minimum",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "i": float64(0)},
			wantErr: true,
		},
		{
			name:    "above maximum",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "i": float64(6)},
			wantErr: true,
		},
		{
			name:    "bad enum value",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "z"},
			wantErr: true,
		},
		{
			name:    "valid",
			schema:  validateTestSchema,
			args:    map[string]any{"s": "a", "b": true, "i": float64(3), "o": map[string]any{"k": "v"}},
			wantErr: false,
		},
		{
			name:    "nil args treated as empty object, missing required fails",
			schema:  validateTestSchema,
			args:    nil,
			wantErr: true,
		},
		{
			name:    "valid ajent_search",
			schema:  validateArgsSchemaFor(t, "ajent_search"),
			args:    map[string]any{"query": "prior findings"},
			wantErr: false,
		},
		{
			name:   "valid ajent_post",
			schema: validateArgsSchemaFor(t, "ajent_post"),
			args: map[string]any{
				"kind":     "finding",
				"body":     "hello",
				"key":      "idem-key-1",
				"approved": true,
			},
			wantErr: false,
		},
		{
			name:   "valid ajent_goal_update",
			schema: validateArgsSchemaFor(t, "ajent_goal_update"),
			args: map[string]any{
				"id":      "00000000-0000-4000-8000-000000000051",
				"version": float64(1),
				"action":  "update",
				"body":    "progress",
				"key":     "idem-key-2",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArgs(tt.schema, tt.args)
			if tt.wantErr && err == nil {
				t.Fatalf("validateArgs() = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateArgs() = %v, want nil", err)
			}
		})
	}
}

// validateArgsSchemaFor looks up name's registered InputSchema (the actual
// contract schema from defs.go), for cases that must validate against the
// real tool definitions rather than the ad hoc validateTestSchema above.
func validateArgsSchemaFor(t *testing.T, name string) json.RawMessage {
	t.Helper()
	tool, ok := registryLookup(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	return tool.InputSchema
}
