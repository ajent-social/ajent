// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// errPlainServeTest is a plain Go error a test handler returns to prove its
// text never reaches the isError result (docs/tasks/T3.2.md step 7.4).
var errPlainServeTest = errors.New("serve_test: super secret detail")

// serveTestContentText extracts the sole content[0].text string from a
// tools/call result value (as returned by dispatcher.CallTool).
func serveTestContentText(t *testing.T, result any) string {
	t.Helper()
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %#v", result)
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want exactly one entry", m["content"])
	}
	entry, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] = %#v", content[0])
	}
	text, ok := entry["text"].(string)
	if !ok {
		t.Fatalf("content[0].text = %#v, want a string", entry["text"])
	}
	return text
}

func serveTestIsError(t *testing.T, result any) bool {
	t.Helper()
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %#v", result)
	}
	v, ok := m["isError"].(bool)
	if !ok {
		t.Fatalf("isError = %#v, want a bool", m["isError"])
	}
	return v
}

func serveTestCallParams(t *testing.T, name string, args map[string]any) json.RawMessage {
	t.Helper()
	payload := map[string]any{"name": name}
	if args != nil {
		payload["arguments"] = args
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling params: %v", err)
	}
	return raw
}

func TestServeToolErrorPayload(t *testing.T) {
	const toolName = "serve_test_tool_error"
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: returns a ToolError",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Annotations: &Annotations{},
		Handler: func(context.Context, *Session, map[string]any) (any, error) {
			return nil, &ToolError{Payload: map[string]any{"error": "x"}}
		},
	})

	d := &dispatcher{s: &Session{}}
	result, after, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if after != nil {
		t.Errorf("after = non-nil, want nil for a ToolError result")
	}
	if !serveTestIsError(t, result) {
		t.Errorf("isError = false, want true")
	}
	if text := serveTestContentText(t, result); text != `{"error":"x"}` {
		t.Errorf("text = %q, want %q", text, `{"error":"x"}`)
	}
}

func TestServeMissingHandlerIsError(t *testing.T) {
	const toolName = "serve_test_missing_handler"
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: registered with no handler",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Annotations: &Annotations{},
	})

	d := &dispatcher{s: &Session{}}
	result, after, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if after != nil {
		t.Errorf("after = non-nil, want nil")
	}
	if !serveTestIsError(t, result) {
		t.Errorf("isError = false, want true")
	}
	want := envelopeEncode(serveClientError)
	if text := serveTestContentText(t, result); text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestServeUnknownToolIsError(t *testing.T) {
	d := &dispatcher{s: &Session{}}
	result, _, err := d.CallTool(context.Background(), serveTestCallParams(t, "serve_test_does_not_exist", nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if !serveTestIsError(t, result) {
		t.Errorf("isError = false, want true")
	}
}

func TestServeCallToolInvalidParamsIsRPCError(t *testing.T) {
	d := &dispatcher{s: &Session{}}

	tests := []struct {
		name   string
		params json.RawMessage
	}{
		{"params is an array", json.RawMessage(`[1,2,3]`)},
		{"params is a string", json.RawMessage(`"nope"`)},
		{"name is missing", json.RawMessage(`{"arguments":{}}`)},
		{"name is a number", json.RawMessage(`{"name":5}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := d.CallTool(context.Background(), tt.params)
			var rpcErr *RPCError
			if err == nil {
				t.Fatalf("CallTool error = nil, want *RPCError")
			}
			if !errors.As(err, &rpcErr) {
				t.Fatalf("CallTool error = %v (%T), want *RPCError", err, err)
			}
			if rpcErr.Code != CodeInvalidParams {
				t.Errorf("code = %d, want %d", rpcErr.Code, CodeInvalidParams)
			}
		})
	}
}

func TestServeInvalidArgsNoHTTP(t *testing.T) {
	srv := fakeserver.New(t)
	const toolName = "serve_test_invalid_args_network"
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: would call the network if invoked",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
		Annotations: &Annotations{},
		Handler: func(ctx context.Context, s *Session, _ map[string]any) (any, error) {
			var out map[string]any
			err := s.API.Do(ctx, srv.Credential(), api.Request{Method: "GET", Route: "/v1/reliability"}, &out)
			return out, err
		},
	})

	s := &Session{API: &api.Client{}}
	d := &dispatcher{s: s}
	// Missing the required "id" argument: validateArgs must reject this
	// before the handler (and so before any HTTP call) ever runs.
	result, _, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, map[string]any{}))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if !serveTestIsError(t, result) {
		t.Errorf("isError = false, want true")
	}
	if got := srv.Requests(); len(got) != 0 {
		t.Errorf("fakeserver recorded %d requests, want 0: %+v", len(got), got)
	}
}

func TestServeAPIErrorMapping(t *testing.T) {
	const toolName = "serve_test_api_error"
	five := 5
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: returns a rate-limited api.Error",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Annotations: &Annotations{},
		Handler: func(context.Context, *Session, map[string]any) (any, error) {
			return nil, api.NewError(429, "", &five)
		},
	})

	d := &dispatcher{s: &Session{}}
	result, _, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if !serveTestIsError(t, result) {
		t.Fatalf("isError = false, want true")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(serveTestContentText(t, result)), &decoded); err != nil {
		t.Fatalf("text is not valid JSON: %v", err)
	}
	if decoded["error"] != "rate_limited" {
		t.Errorf("error = %v, want rate_limited", decoded["error"])
	}
	if decoded["http_status"] != float64(429) {
		t.Errorf("http_status = %v, want 429", decoded["http_status"])
	}
	if decoded["retry_after_seconds"] != float64(5) {
		t.Errorf("retry_after_seconds = %v, want 5", decoded["retry_after_seconds"])
	}
	if _, ok := decoded["action"]; !ok {
		t.Errorf("action missing from diagnostic")
	}
	if _, ok := decoded["request_id"]; !ok {
		t.Errorf("request_id missing from diagnostic")
	}
}

func TestServeUnsupportedCapabilityMapping(t *testing.T) {
	const toolName = "serve_test_unsupported_capability"
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: returns an UnsupportedCapabilityError",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Annotations: &Annotations{},
		Handler: func(context.Context, *Session, map[string]any) (any, error) {
			return nil, &api.UnsupportedCapabilityError{Capability: "goals"}
		},
	})

	d := &dispatcher{s: &Session{}}
	result, _, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if !serveTestIsError(t, result) {
		t.Fatalf("isError = false, want true")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(serveTestContentText(t, result)), &decoded); err != nil {
		t.Fatalf("text is not valid JSON: %v", err)
	}
	if decoded["error"] != "unsupported_capability" {
		t.Errorf("error = %v, want unsupported_capability", decoded["error"])
	}
	if decoded["capability"] != "goals" {
		t.Errorf("capability = %v, want goals", decoded["capability"])
	}
}

func TestServeGenericErrorNeverLeaksGoText(t *testing.T) {
	const toolName = "serve_test_generic_error"
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: returns a plain Go error",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Annotations: &Annotations{},
		Handler: func(context.Context, *Session, map[string]any) (any, error) {
			return nil, errPlainServeTest
		},
	})

	d := &dispatcher{s: &Session{}}
	result, _, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	text := serveTestContentText(t, result)
	if strings.Contains(text, "super secret detail") {
		t.Fatalf("text leaked the underlying Go error: %q", text)
	}
	want := envelopeEncode(serveClientError)
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestServeReceiptsCommittedAfterWrite(t *testing.T) {
	const toolName = "serve_test_receipts"
	registryTestRegister(t, Tool{
		Name:        toolName,
		Description: "serve_test: returns a Result carrying receipts",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Annotations: &Annotations{},
		Handler: func(context.Context, *Session, map[string]any) (any, error) {
			return Result{Value: map[string]any{"ok": true}, Receipts: []string{"serve-test-receipt-1", "serve-test-receipt-2"}}, nil
		},
	})

	cache := OpenCache(t.TempDir(), "terminal")
	s := &Session{Cache: cache}
	d := &dispatcher{s: s}

	_, after, err := d.CallTool(context.Background(), serveTestCallParams(t, toolName, nil))
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if after == nil {
		t.Fatalf("after = nil, want a func committing the receipts")
	}
	if cache.Seen("serve-test-receipt-1") {
		t.Fatalf("receipt already committed before after() ran")
	}

	after()

	if !cache.Seen("serve-test-receipt-1") || !cache.Seen("serve-test-receipt-2") {
		t.Errorf("receipts not committed after after() ran")
	}
}

func TestHelpImproveVerifyEnumWithCapability(t *testing.T) {
	srv := fakeserver.New(t)
	s := &Session{Dir: t.TempDir(), Profile: "terminal", Root: srv.Credential(), API: &api.Client{}}
	d := &dispatcher{s: s}

	result, err := d.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	enum := serveTestHelpImproveEnum(t, result)
	if !serveTestContainsString(enum, "verify") {
		t.Errorf("enum = %v, want verify present (fakeserver's default reliability fixture advertises improvement_verification)", enum)
	}
}

func TestHelpImproveVerifyEnumWithoutCapability(t *testing.T) {
	srv := fakeserver.New(t)
	srv.Handle("GET", "^/v1/reliability$", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","capabilities":[]}`))
	})
	s := &Session{Dir: t.TempDir(), Profile: "terminal", Root: srv.Credential(), API: &api.Client{}}
	d := &dispatcher{s: s}

	result, err := d.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	enum := serveTestHelpImproveEnum(t, result)
	if serveTestContainsString(enum, "verify") {
		t.Errorf("enum = %v, want verify absent", enum)
	}
	if len(enum) != 3 {
		t.Errorf("enum = %v, want exactly the 3 base actions", enum)
	}
}

// serveTestHelpImproveEnum extracts ajent_help_improve's "action" enum from
// a ListTools result.
func serveTestHelpImproveEnum(t *testing.T, listToolsResult any) []string {
	t.Helper()
	m, ok := listToolsResult.(map[string]any)
	if !ok {
		t.Fatalf("ListTools result is not a map: %#v", listToolsResult)
	}
	tools, ok := m["tools"].([]Tool)
	if !ok {
		t.Fatalf("tools = %#v, want []Tool", m["tools"])
	}
	for _, tool := range tools {
		if tool.Name != "ajent_help_improve" {
			continue
		}
		var schema struct {
			Properties struct {
				Action struct {
					Enum []string `json:"enum"`
				} `json:"action"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("decoding ajent_help_improve schema: %v", err)
		}
		return schema.Properties.Action.Enum
	}
	t.Fatal("ajent_help_improve not present in ListTools result")
	return nil
}

func serveTestContainsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestServeInitializeGolden(t *testing.T) {
	srv := fakeserver.New(t)
	s := &Session{
		Profile: "terminal",
		Dir:     t.TempDir(), // no profile credential on disk: the startup inbox check reports "unavailable"
		Root:    srv.Credential(),
		API:     &api.Client{},
	}

	line := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
	var out bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(line), &out, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	got := strings.TrimRight(out.String(), "\n")
	if got == "" {
		t.Fatalf("Serve wrote no output")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("decoding response line: %v\nline: %s", err, got)
	}

	path := filepath.Join("..", "..", "testdata", "mcp", "initialize.golden.json")
	//nolint:gosec // G304: path is a fixed, repo-relative testdata literal, not external input.
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var gotValue, wantValue any
	if err := json.Unmarshal(envelope.Result, &gotValue); err != nil {
		t.Fatalf("decoding got result: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decoding golden file: %v", err)
	}
	if !jsonValuesEqual(gotValue, wantValue) {
		t.Errorf("initialize result mismatch:\n got:  %s\n want: %s", envelope.Result, want)
	}
}
