// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"math"
)

// maxSafeInteger is the default upper bound for an "integer" schema
// property: 2^53-1, the largest integer a float64 (how JSON numbers arrive
// in Go) represents exactly.
const maxSafeInteger = float64(1<<53 - 1)

// argError is returned by validateArgs. Its message is a short, fixed
// reason and never echoes argument values.
type argError struct{ reason string }

func (e *argError) Error() string { return e.reason }

// argSchema is the JSON Schema subset validateArgs supports: the types,
// keywords and nesting the tool fixtures in testdata/contract/tools.json
// actually use.
type argSchema struct {
	Type                 string               `json:"type"`
	Properties           map[string]argSchema `json:"properties"`
	Required             []string             `json:"required"`
	AdditionalProperties *bool                `json:"additionalProperties"`
	Enum                 []any                `json:"enum"`
	Minimum              *float64             `json:"minimum"`
	Maximum              *float64             `json:"maximum"`
}

// validateArgs validates args against schema before any tool handler runs.
// nil args are treated as an empty object. It supports type (object,
// string, boolean, integer), properties, required,
// additionalProperties: false, enum, minimum and maximum. Integers arrive
// from JSON as float64 and are accepted only when they are integral and
// within range (default 0 to 2^53-1).
func validateArgs(schema json.RawMessage, args map[string]any) error {
	var s argSchema
	if err := json.Unmarshal(schema, &s); err != nil {
		return &argError{"invalid tool schema"}
	}
	if args == nil {
		args = map[string]any{}
	}
	return validateObject(s, args)
}

// validateObject checks args against an object schema: unknown properties,
// missing required properties, then each present property's value.
func validateObject(s argSchema, args map[string]any) error {
	if s.AdditionalProperties != nil && !*s.AdditionalProperties {
		for k := range args {
			if _, ok := s.Properties[k]; !ok {
				return &argError{"unknown argument"}
			}
		}
	}
	for _, name := range s.Required {
		if _, ok := args[name]; !ok {
			return &argError{"missing required argument"}
		}
	}
	for k, v := range args {
		prop, ok := s.Properties[k]
		if !ok {
			continue
		}
		if err := validateValue(prop, v); err != nil {
			return err
		}
	}
	return nil
}

// validateValue checks one argument value against its property schema.
func validateValue(s argSchema, v any) error {
	switch s.Type {
	case "string":
		sv, ok := v.(string)
		if !ok {
			return &argError{"wrong argument type"}
		}
		if len(s.Enum) > 0 && !enumContains(s.Enum, sv) {
			return &argError{"invalid enum value"}
		}
		return nil
	case "boolean":
		if _, ok := v.(bool); !ok {
			return &argError{"wrong argument type"}
		}
		return nil
	case "integer":
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return &argError{"wrong argument type"}
		}
		lower := 0.0
		if s.Minimum != nil {
			lower = *s.Minimum
		}
		upper := maxSafeInteger
		if s.Maximum != nil {
			upper = *s.Maximum
		}
		if f < lower || f > upper {
			return &argError{"integer out of range"}
		}
		return nil
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return &argError{"wrong argument type"}
		}
		if len(s.Properties) > 0 || len(s.Required) > 0 || s.AdditionalProperties != nil {
			return validateObject(s, obj)
		}
		return nil
	default:
		return &argError{"unsupported schema type"}
	}
}

func enumContains(enum []any, v string) bool {
	for _, e := range enum {
		if s, ok := e.(string); ok && s == v {
			return true
		}
	}
	return false
}
