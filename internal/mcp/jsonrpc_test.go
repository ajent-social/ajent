// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// fakeDispatcher is a minimal Dispatcher used to exercise ServeRPC without
// depending on any other internal package.
type fakeDispatcher struct {
	afterCount int
}

func (f *fakeDispatcher) Initialize(_ context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "Invalid params"}
	}
	return map[string]any{"protocolVersion": NegotiateProtocol(p.ProtocolVersion)}, nil
}

func (f *fakeDispatcher) ListTools(_ context.Context) (any, error) {
	return map[string]any{"tools": []any{}}, nil
}

func (f *fakeDispatcher) CallTool(_ context.Context, params json.RawMessage) (any, func(), error) {
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Name == "boom" {
		return nil, nil, errors.New("secret detail")
	}
	return map[string]any{"ok": true}, func() { f.afterCount++ }, nil
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is always a repo-relative testdata literal supplied by the calling test, not external input.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func outputLines(out *bytes.Buffer) []string {
	text := strings.TrimRight(out.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func decodeJSON(t *testing.T, line string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return v
}

func runTranscript(t *testing.T, inPath string) (*bytes.Buffer, *fakeDispatcher) {
	t.Helper()
	in, err := os.Open(inPath) //nolint:gosec // G304: inPath is always a repo-relative testdata literal supplied by the calling test, not external input.
	if err != nil {
		t.Fatalf("open %s: %v", inPath, err)
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			t.Errorf("close %s: %v", inPath, closeErr)
		}
	}()

	var out bytes.Buffer
	d := &fakeDispatcher{}
	if err := ServeRPC(context.Background(), in, &out, d); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}
	return &out, d
}

func compareTranscript(t *testing.T, gotBuf *bytes.Buffer, wantPath string) {
	t.Helper()
	got := outputLines(gotBuf)
	want := readLines(t, wantPath)

	if len(got) != len(want) {
		t.Fatalf("line count mismatch: got %d, want %d\ngot: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		gv := decodeJSON(t, got[i])
		wv := decodeJSON(t, want[i])
		if !reflect.DeepEqual(gv, wv) {
			t.Errorf("line %d mismatch:\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
}

func TestServeRPCBasicTranscript(t *testing.T) {
	out, _ := runTranscript(t, "../../testdata/mcp/transport/basic.in.jsonl")
	compareTranscript(t, out, "../../testdata/mcp/transport/basic.out.jsonl")
}

func TestServeRPCErrorTranscript(t *testing.T) {
	out, _ := runTranscript(t, "../../testdata/mcp/transport/errors.in.jsonl")
	compareTranscript(t, out, "../../testdata/mcp/transport/errors.out.jsonl")

	if strings.Contains(out.String(), "secret detail") {
		t.Errorf("output leaked internal error detail: %s", out.String())
	}
}

// oversizedButValidPayload builds a syntactically valid tools/call request
// (id 1, name "noop") whose encoded length exceeds MaxLineBytes by at least
// margin bytes. Using a well-formed, dispatchable request (rather than raw
// junk bytes) matters: it is the only way TestServeRPCOversizedLine can tell
// "rejected for being oversized" apart from "rejected for being invalid
// JSON" -- a size guard that silently stopped doing anything would still
// pass a junk-bytes version of this test, because malformed input hits the
// same -32700 response through the ordinary parse-error path.
func oversizedButValidPayload(t *testing.T, margin int) []byte {
	t.Helper()
	overhead, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "noop", "pad": ""},
	})
	if err != nil {
		t.Fatalf("marshal overhead probe: %v", err)
	}
	pad := MaxLineBytes - len(overhead) + margin
	if pad < 0 {
		pad = 0
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "noop", "pad": strings.Repeat("a", pad)},
	})
	if err != nil {
		t.Fatalf("marshal oversized payload: %v", err)
	}
	if len(payload) <= MaxLineBytes {
		t.Fatalf("test payload not oversized: %d bytes", len(payload))
	}
	return payload
}

func TestServeRPCOversizedLine(t *testing.T) {
	var in bytes.Buffer
	in.Write(oversizedButValidPayload(t, 16))
	in.WriteString("\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	in.WriteString("\n")

	var out bytes.Buffer
	d := &fakeDispatcher{}
	if err := ServeRPC(context.Background(), &in, &out, d); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}

	lines := outputLines(&out)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(lines), lines)
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first line: %v", err)
	}
	errObj, ok := first["error"].(map[string]any)
	if !ok {
		t.Fatalf("first line has no error object (oversized guard did not reject a well-formed, oversized request): %v", first)
	}
	if code, _ := errObj["code"].(float64); int(code) != CodeParseError {
		t.Errorf("first line code = %v, want %d", errObj["code"], CodeParseError)
	}
	if first["id"] != nil {
		t.Errorf("first line id = %v, want nil", first["id"])
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second line: %v", err)
	}
	if id, _ := second["id"].(float64); int(id) != 2 {
		t.Errorf("second line id = %v, want 2", second["id"])
	}
	result, ok := second["result"].(map[string]any)
	if !ok || len(result) != 0 {
		t.Errorf("second line result = %v, want {}", second["result"])
	}
}

func TestServeRPCNotificationWritesNothing(t *testing.T) {
	in := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n" +
			"{\"jsonrpc\":\"2.0\",\"method\":\"notifications/cancelled\"}\n")
	var out bytes.Buffer
	d := &fakeDispatcher{}
	if err := ServeRPC(context.Background(), in, &out, d); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("got %d bytes of output, want 0: %q", out.Len(), out.String())
	}
}

// failingWriter always fails, to exercise the write-error path.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("boom write failure")
}

func TestServeRPCAfterRunsOnlyAfterWrite(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ok"}}` + "\n"

	failD := &fakeDispatcher{}
	if err := ServeRPC(context.Background(), strings.NewReader(in), failingWriter{}, failD); err == nil {
		t.Fatalf("expected write error, got nil")
	}
	if failD.afterCount != 0 {
		t.Errorf("afterCount = %d after failed write, want 0", failD.afterCount)
	}

	okD := &fakeDispatcher{}
	var out bytes.Buffer
	if err := ServeRPC(context.Background(), strings.NewReader(in), &out, okD); err != nil {
		t.Fatalf("ServeRPC: %v", err)
	}
	if okD.afterCount != 1 {
		t.Errorf("afterCount = %d after successful write, want 1", okD.afterCount)
	}
}

func TestNegotiateProtocol(t *testing.T) {
	tests := []struct {
		requested string
		want      string
	}{
		{"2024-11-05", "2024-11-05"},
		{"2025-03-26", "2025-03-26"},
		{"2025-06-18", "2025-06-18"},
		{"2025-11-25", "2025-11-25"},
		{"", "2025-11-25"},
		{"2030-01-01", "2025-11-25"},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			if got := NegotiateProtocol(tt.requested); got != tt.want {
				t.Errorf("NegotiateProtocol(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}

func TestServeRPCOutputIsOnlyJSONLines(t *testing.T) {
	for _, path := range []string{
		"../../testdata/mcp/transport/basic.in.jsonl",
		"../../testdata/mcp/transport/errors.in.jsonl",
	} {
		out, _ := runTranscript(t, path)
		for i, line := range outputLines(out) {
			var v map[string]any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("%s line %d: not valid JSON: %v (%q)", path, i, err, line)
			}
			if v["jsonrpc"] != "2.0" {
				t.Errorf("%s line %d: jsonrpc = %v, want \"2.0\"", path, i, v["jsonrpc"])
			}
		}
	}
}
