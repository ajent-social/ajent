// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// PrintJSON marshals v as JSON indented two spaces per level and writes it
// to w, followed by a trailing newline.
func PrintJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("cli: marshaling JSON: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// PrintRawJSON re-indents raw (already-valid JSON, typically a server
// response) two spaces per level, preserving key order, and writes it to w
// followed by a trailing newline.
func PrintRawJSON(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("cli: indenting JSON: %w", err)
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// errNotObject is returned by AddField when raw, once trimmed of
// surrounding whitespace, is not a JSON object.
var errNotObject = errors.New("cli: raw is not a JSON object")

// AddField inserts "key":<marshal(value)> as the last member of the JSON
// object raw, preserving every other byte's order: it appends
// ,"key":<value> before the final "}", or just "key":<value> when the
// object (once trimmed) is empty ("{}"). It returns an error wrapping
// errNotObject when raw, trimmed of leading and trailing whitespace, does
// not start with '{' and end with '}'.
func AddField(raw []byte, key string, value any) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errNotObject
	}

	keyJSON, err := json.Marshal(key)
	if err != nil {
		return nil, fmt.Errorf("cli: marshaling field key %s: %w", key, err)
	}
	valueJSON, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("cli: marshaling field %s: %w", key, err)
	}

	body := trimmed[1 : len(trimmed)-1]
	var out bytes.Buffer
	out.WriteByte('{')
	if len(bytes.TrimSpace(body)) > 0 {
		out.Write(body)
		out.WriteByte(',')
	}
	out.Write(keyJSON)
	out.WriteByte(':')
	out.Write(valueJSON)
	out.WriteByte('}')
	return out.Bytes(), nil
}
