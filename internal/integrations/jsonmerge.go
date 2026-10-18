// SPDX-License-Identifier: Apache-2.0

package integrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent-client/internal/config"
)

// ErrUnparseable is returned when the target file is not plain JSON (for
// example JSONC with comments or trailing commas), or when the section
// exists but is not a JSON object. The file is left unchanged.
var ErrUnparseable = errors.New("integrations: config is not plain JSON; left unchanged")

// ErrForeignEntry is returned when an existing "ajent" entry was not
// written by ajent. The file is left unchanged.
var ErrForeignEntry = errors.New("integrations: an ajent entry with foreign settings exists; left unchanged")

// errNotObject is an internal sentinel for "this JSON value is not an
// object"; callers translate it to ErrUnparseable.
var errNotObject = errors.New("integrations: value is not a JSON object")

// kv is one key of an ordered JSON object. raw holds the value exactly as
// captured from the source document (or freshly marshaled for a new or
// replaced ajent entry); it is never semantically reinterpreted for keys
// ajent does not own.
type kv struct {
	key string
	raw json.RawMessage
}

// MergeJSONServer sets obj[section]["ajent"] = entry in the JSON file at
// path, preserving every other key's order and content. A missing file
// counts as {}. See jsonmerge rules in docs/tasks/T4.17.md.
func MergeJSONServer(path, section string, entry map[string]any) (changed bool, err error) {
	root, existed, mode, err := loadOrdered(path)
	if err != nil {
		return false, err
	}

	si := findKey(root, section)
	var sectionPairs []kv
	if si >= 0 {
		sectionPairs, err = decodeObject(root[si].raw, false)
		if err != nil {
			return false, ErrUnparseable
		}
	}

	ai := findKey(sectionPairs, "ajent")
	switch {
	case ai >= 0:
		existing := unmarshalEntry(sectionPairs[ai].raw)
		if SameEntry(existing, entry) {
			return false, nil
		}
		if !Owned(existing) {
			return false, ErrForeignEntry
		}
		raw, merr := json.Marshal(entry)
		if merr != nil {
			return false, merr
		}
		sectionPairs[ai].raw = raw
	default:
		raw, merr := json.Marshal(entry)
		if merr != nil {
			return false, merr
		}
		sectionPairs = append(sectionPairs, kv{key: "ajent", raw: raw})
	}

	if si >= 0 {
		root[si].raw = rebuildObject(sectionPairs)
	} else {
		root = append(root, kv{key: section, raw: rebuildObject(sectionPairs)})
	}

	out, err := marshalDocument(root)
	if err != nil {
		return false, err
	}
	if err := writeResult(path, out, existed, mode); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveJSONServer deletes section.ajent from the JSON file at path, but
// only when the existing entry is Owned. It leaves an empty section in
// place, returns false, nil when the key is absent, and ErrForeignEntry
// when the entry is foreign.
func RemoveJSONServer(path, section string) (changed bool, err error) {
	root, existed, mode, err := loadOrdered(path)
	if err != nil {
		return false, err
	}
	if !existed {
		return false, nil
	}

	si := findKey(root, section)
	if si < 0 {
		return false, nil
	}

	sectionPairs, err := decodeObject(root[si].raw, false)
	if err != nil {
		return false, ErrUnparseable
	}

	ai := findKey(sectionPairs, "ajent")
	if ai < 0 {
		return false, nil
	}

	existing := unmarshalEntry(sectionPairs[ai].raw)
	if !Owned(existing) {
		return false, ErrForeignEntry
	}

	sectionPairs = append(sectionPairs[:ai], sectionPairs[ai+1:]...)
	root[si].raw = rebuildObject(sectionPairs)

	out, err := marshalDocument(root)
	if err != nil {
		return false, err
	}
	if err := writeResult(path, out, existed, mode); err != nil {
		return false, err
	}
	return true, nil
}

// loadOrdered reads path into an ordered top-level object. A missing file
// counts as {} (existed is false). A symlink returns config.ErrSymlink. A
// file that is not exactly one plain JSON object returns ErrUnparseable.
func loadOrdered(path string) (root []kv, existed bool, mode fs.FileMode, err error) {
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, false, 0, config.ErrSymlink
		}
	case errors.Is(statErr, fs.ErrNotExist):
		return nil, false, 0, nil
	default:
		return nil, false, 0, statErr
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is the harness config file the caller selected; symlinks were refused above.
	if err != nil {
		return nil, false, 0, err
	}
	pairs, derr := decodeObject(data, true)
	if derr != nil {
		return nil, false, 0, ErrUnparseable
	}
	return pairs, true, info.Mode().Perm(), nil
}

// decodeObject parses data as a single JSON object, preserving key order
// and capturing each value's raw bytes without reinterpreting them. When
// strict is true, decodeObject also requires that data holds nothing but
// that one object (no trailing content), which is how the top-level
// document is validated. It returns errNotObject when data's outermost
// value is not an object.
func decodeObject(data []byte, strict bool) ([]kv, error) {
	dec := json.NewDecoder(bytes.NewReader(data))

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, errNotObject
	}

	var pairs []kv
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("integrations: object key is not a string")
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		pairs = append(pairs, kv{key: key, raw: raw})
	}

	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}

	if strict {
		if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
			if err == nil {
				return nil, fmt.Errorf("integrations: trailing content after top-level object")
			}
			return nil, err
		}
	}

	return pairs, nil
}

// rebuildObject serializes pairs back into a syntactically valid (but not
// necessarily indented) JSON object, in order. marshalDocument re-indents
// the result.
func rebuildObject(pairs []kv) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(p.key)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		buf.Write(p.raw)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// marshalDocument renders root as a 2-space indented JSON document ending
// in a single newline. Because indentation is recomputed for the whole
// document from its token stream, a raw value's original whitespace never
// leaks through, and the output is byte-identical to a source file that
// was already 2-space indented in this same style.
func marshalDocument(root []kv) ([]byte, error) {
	compact := rebuildObject(root)
	var buf bytes.Buffer
	if err := json.Indent(&buf, compact, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// findKey returns the index of the last pair in pairs with the given key,
// or -1 when absent. JSON permits duplicate keys; the last one is the
// value an ordinary decode into a map would keep.
func findKey(pairs []kv, key string) int {
	idx := -1
	for i, p := range pairs {
		if p.key == key {
			idx = i
		}
	}
	return idx
}

// unmarshalEntry decodes raw into a map for Owned/SameEntry inspection. A
// value that is not a JSON object (or fails to decode) becomes an empty
// map, which Owned and SameEntry both treat as foreign.
func unmarshalEntry(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

// writeResult writes data to path with config.WritePrivate, creating
// missing parent directories with mode 0700. When the file already
// existed, its previous mode is restored if WritePrivate's 0600 differs
// from it.
func writeResult(path string, data []byte, existed bool, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("integrations: mkdir %s: %w", filepath.Dir(path), err)
	}

	if err := config.WritePrivate(path, data); err != nil {
		return err
	}

	if existed && mode != 0o600 {
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("integrations: chmod %s: %w", path, err)
		}
	}

	return nil
}
