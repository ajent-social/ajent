// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// mcpE2ELine is one line this test read from the subprocess's stdout.
type mcpE2ELine struct {
	data []byte
	err  error
}

// mcpE2ESession drives one "ajent mcp" subprocess over its stdin and
// stdout pipes.
type mcpE2ESession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *bytes.Buffer
	lines  chan mcpE2ELine

	mu          sync.Mutex
	receivedRaw [][]byte
	closeOnce   sync.Once
	closeErr    error
}

// mcpE2EProjectDir creates a fresh temporary directory containing an empty
// .git directory, so credentials.ProjectRoot resolves it as a project root.
func mcpE2EProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatalf("mcp e2e: creating .git directory: %v", err)
	}
	return dir
}

// mcpE2EEnv builds the subprocess environment: an isolated config
// directory, the fake server origin, an isolated home directory (both
// HOME and USERPROFILE, since the OS under test is unknown at write time)
// and CI=1. It deliberately does not inherit the host's environment,
// beyond PATH, so the subprocess can never reach real harness
// configuration.
func mcpE2EEnv(configDir, serverURL, home string) []string {
	return []string{
		"AJENT_CONFIG_DIR=" + configDir,
		"AJENT_URL=" + serverURL,
		"HOME=" + home,
		"USERPROFILE=" + home,
		"CI=1",
		"PATH=" + os.Getenv("PATH"),
	}
}

// mcpE2EStart starts "ajentBin mcp --profile claude-code --project-profile"
// with the given working directory and environment, and begins reading its
// stdout in the background. The session is closed automatically at test
// cleanup if the test does not close it explicitly first.
func mcpE2EStart(t *testing.T, dir string, env []string) *mcpE2ESession {
	t.Helper()

	cmd := exec.Command(ajentBin, "mcp", "--profile", "claude-code", "--project-profile")
	cmd.Dir = dir
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("mcp e2e: opening stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("mcp e2e: opening stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("mcp e2e: starting %s: %v", ajentBin, err)
	}

	s := &mcpE2ESession{
		cmd:    cmd,
		stdin:  stdin,
		stderr: &stderr,
		lines:  make(chan mcpE2ELine, 32),
	}
	go mcpE2EReadLines(stdout, s.lines)

	t.Cleanup(func() { _, _ = s.close() })
	return s
}

// mcpE2EReadLines copies newline-delimited messages from r to out until EOF
// or a read error, then closes out.
func mcpE2EReadLines(r io.Reader, out chan<- mcpE2ELine) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		out <- mcpE2ELine{data: line}
	}
	if err := scanner.Err(); err != nil {
		out <- mcpE2ELine{err: err}
	}
	close(out)
}

// send marshals v and writes it as one newline-delimited message.
func (s *mcpE2ESession) send(t *testing.T, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mcp e2e: marshaling request: %v", err)
	}
	s.sendRaw(t, string(b))
}

// sendRaw writes line verbatim as one newline-delimited message, used to
// replay testdata/mcp/e2e_session.in.jsonl exactly as written.
func (s *mcpE2ESession) sendRaw(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.stdin, line+"\n"); err != nil {
		t.Fatalf("mcp e2e: writing request: %v", err)
	}
}

// next waits for the next stdout line, failing the test if the deadline
// passes first, the stream closes early, or the line is not a JSON object.
// Every line read this way is recorded for the stdout-purity check.
func (s *mcpE2ESession) next(t *testing.T, deadline time.Time) map[string]any {
	t.Helper()
	select {
	case line, ok := <-s.lines:
		if !ok {
			t.Fatalf("mcp e2e: stdout closed before a response arrived; stderr:\n%s", s.stderr.String())
		}
		if line.err != nil {
			t.Fatalf("mcp e2e: reading stdout: %v", line.err)
		}
		s.mu.Lock()
		s.receivedRaw = append(s.receivedRaw, line.data)
		s.mu.Unlock()

		var resp map[string]any
		if err := json.Unmarshal(line.data, &resp); err != nil {
			t.Fatalf("mcp e2e: stdout line is not a JSON object: %v\nline: %s", err, line.data)
		}
		return resp
	case <-time.After(time.Until(deadline)):
		t.Fatalf("mcp e2e: timed out waiting for a response; stderr:\n%s", s.stderr.String())
		return nil
	}
}

// call sends a tools/call-style JSON-RPC request built from method and
// params, reads the matching response and fails the test if the response
// id does not echo the request id.
func (s *mcpE2ESession) call(t *testing.T, deadline time.Time, id int, method string, params any) map[string]any {
	t.Helper()
	s.send(t, struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params})

	resp := s.next(t, deadline)
	if gotID, _ := resp["id"].(float64); int(gotID) != id {
		t.Fatalf("mcp e2e: response id = %v, want %d (response: %#v)", resp["id"], id, resp)
	}
	return resp
}

// notify sends a JSON-RPC notification (no id), which never produces a
// response line.
func (s *mcpE2ESession) notify(t *testing.T, method string) {
	t.Helper()
	s.send(t, struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}{JSONRPC: "2.0", Method: method})
}

// close closes stdin and waits for the process to exit, killing it if it
// does not exit within 5 seconds. It is idempotent: only the first call
// does any work, and every call returns the same result.
func (s *mcpE2ESession) close() (exitCode int, err error) {
	s.closeOnce.Do(func() {
		_ = s.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		select {
		case werr := <-done:
			s.closeErr = werr
		case <-time.After(5 * time.Second):
			_ = s.cmd.Process.Kill()
			s.closeErr = <-done
		}
	})
	code := -1
	if s.cmd.ProcessState != nil {
		code = s.cmd.ProcessState.ExitCode()
	}
	return code, s.closeErr
}

// stderrText returns everything the subprocess has written to stderr so
// far.
func (s *mcpE2ESession) stderrText() string {
	return s.stderr.String()
}

// mcpE2ECheck is one assertion against a decoded JSON-RPC response, as read
// from testdata/mcp/e2e_session.expect.json.
type mcpE2ECheck struct {
	Path  string `json:"path"`
	Check string `json:"check"` // "equals" or "length"
	Value any    `json:"value"`
}

// mcpE2EExpectEntry is the set of checks for one request id in
// testdata/mcp/e2e_session.expect.json. ToolCall true means the checks run
// against {"envelope": <content[0].text decoded as JSON>} after asserting
// isError is false, instead of against the raw JSON-RPC response.
type mcpE2EExpectEntry struct {
	ID             int           `json:"id"`
	ToolCall       bool          `json:"tool_call"`
	ResponseChecks []mcpE2ECheck `json:"response_checks"`
}

// mcpE2EWalk descends into root following the dot-separated path,
// indexing arrays by their decimal position and objects by key.
func mcpE2EWalk(root any, path string) (any, error) {
	cur := root
	for _, seg := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return nil, fmt.Errorf("segment %q: no such key (have %v)", seg, mcpE2EKeys(v))
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("segment %q: not a valid index into a %d-element array", seg, len(v))
			}
			cur = v[idx]
		default:
			return nil, fmt.Errorf("segment %q: cannot descend into %T", seg, cur)
		}
	}
	return cur, nil
}

// mcpE2EKeys returns the sorted keys of m, for a readable error message.
func mcpE2EKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// mcpE2ELength reports the length of v when it is a JSON array, object or
// string.
func mcpE2ELength(v any) (int, bool) {
	switch x := v.(type) {
	case []any:
		return len(x), true
	case map[string]any:
		return len(x), true
	case string:
		return len(x), true
	default:
		return 0, false
	}
}

// mcpE2EApplyCheck evaluates one fixture check against root.
func mcpE2EApplyCheck(t *testing.T, root any, c mcpE2ECheck) {
	t.Helper()
	got, err := mcpE2EWalk(root, c.Path)
	if err != nil {
		t.Errorf("mcp e2e: path %s: %v", c.Path, err)
		return
	}
	switch c.Check {
	case "equals":
		if !reflect.DeepEqual(got, c.Value) {
			t.Errorf("mcp e2e: path %s = %#v, want %#v", c.Path, got, c.Value)
		}
	case "length":
		n, ok := mcpE2ELength(got)
		want, _ := c.Value.(float64)
		if !ok || n != int(want) {
			t.Errorf("mcp e2e: path %s length = %v (ok=%v), want %v", c.Path, n, ok, c.Value)
		}
	default:
		t.Errorf("mcp e2e: unknown check kind %q for path %s", c.Check, c.Path)
	}
}

// mcpE2EToolEnvelope decodes a tools/call response's content[0].text as
// JSON, after asserting isError is false.
func mcpE2EToolEnvelope(t *testing.T, id int, resp map[string]any) map[string]any {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("mcp e2e: request %d: response has no result object: %#v", id, resp)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("mcp e2e: request %d: isError = true, want false (result: %#v)", id, result)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("mcp e2e: request %d: content is empty", id)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("mcp e2e: request %d: content[0].text is not JSON: %v\ntext: %s", id, err, text)
	}
	return envelope
}

// mcpE2ERecorded returns the first recorded request matching method and
// path, or nil.
func mcpE2ERecorded(reqs []fakeserver.Recorded, method, path string) *fakeserver.Recorded {
	for i := range reqs {
		if reqs[i].Method == method && reqs[i].Path == path {
			return &reqs[i]
		}
	}
	return nil
}

// TestMCPTranscript builds the real ajent binary, starts "ajent mcp" as a
// subprocess against a fake server, and replays
// testdata/mcp/e2e_session.in.jsonl verbatim: initialize, tools/list,
// search, read, post, inbox and ack. It checks each response against
// testdata/mcp/e2e_session.expect.json, then checks the requests the fake
// server actually received, stdout purity and that no token reached
// stderr.
func TestMCPTranscript(t *testing.T) {
	root, err := e2eModuleRoot()
	if err != nil {
		t.Fatalf("mcp e2e: %v", err)
	}

	inPath := filepath.Join(root, "testdata", "mcp", "e2e_session.in.jsonl")
	inBytes, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatalf("mcp e2e: reading %s: %v", inPath, err)
	}

	expectPath := filepath.Join(root, "testdata", "mcp", "e2e_session.expect.json")
	expectBytes, err := os.ReadFile(expectPath)
	if err != nil {
		t.Fatalf("mcp e2e: reading %s: %v", expectPath, err)
	}
	var expect []mcpE2EExpectEntry
	if err := json.Unmarshal(expectBytes, &expect); err != nil {
		t.Fatalf("mcp e2e: parsing %s: %v", expectPath, err)
	}
	expectByID := make(map[int]mcpE2EExpectEntry, len(expect))
	for _, e := range expect {
		expectByID[e.ID] = e
	}

	srv := fakeserver.New(t)
	cred := srv.Credential()
	configDir := t.TempDir()
	if err := credentials.SaveRoot(configDir, cred); err != nil {
		t.Fatalf("mcp e2e: saving root credential: %v", err)
	}

	projectDir := mcpE2EProjectDir(t)
	home := t.TempDir()
	env := mcpE2EEnv(configDir, srv.URL, home)

	sess := mcpE2EStart(t, projectDir, env)
	deadline := time.Now().Add(10 * time.Second)

	seenIDs := map[int]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(inBytes), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var probe struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("mcp e2e: fixture line is not JSON: %v\nline: %s", err, line)
		}

		sess.sendRaw(t, line)
		if len(probe.ID) == 0 {
			continue // notification: no response expected
		}

		var id int
		if err := json.Unmarshal(probe.ID, &id); err != nil {
			t.Fatalf("mcp e2e: fixture request id %s is not an integer: %v", probe.ID, err)
		}
		resp := sess.next(t, deadline)
		if gotID, _ := resp["id"].(float64); int(gotID) != id {
			t.Fatalf("mcp e2e: response id = %v, want %d (response: %#v)", resp["id"], id, resp)
		}
		seenIDs[id] = true

		entry, ok := expectByID[id]
		if !ok {
			continue
		}

		var checkRoot any = resp
		if entry.ToolCall {
			checkRoot = map[string]any{"envelope": mcpE2EToolEnvelope(t, id, resp)}
		}
		for _, c := range entry.ResponseChecks {
			mcpE2EApplyCheck(t, checkRoot, c)
		}
	}
	for id := range expectByID {
		if !seenIDs[id] {
			t.Errorf("mcp e2e: expect fixture has id %d, but the session never sent it", id)
		}
	}

	exitCode, waitErr := sess.close()
	if waitErr != nil {
		t.Errorf("mcp e2e: ajent mcp exited with an error: %v; stderr:\n%s", waitErr, sess.stderrText())
	}
	if exitCode != 0 {
		t.Errorf("mcp e2e: ajent mcp exit code = %d, want 0; stderr:\n%s", exitCode, sess.stderrText())
	}

	reqs := srv.Requests()

	// Exactly one lazy-enrollment POST /v1/fleet, made before the search
	// tool's own GET /v1/search.
	fleetCount := 0
	fleetIdx, searchIdx := -1, -1
	for i, r := range reqs {
		if r.Method == "POST" && r.Path == "/v1/fleet" {
			fleetCount++
			if fleetIdx == -1 {
				fleetIdx = i
			}
		}
		if r.Method == "GET" && r.Path == "/v1/search" && searchIdx == -1 {
			searchIdx = i
		}
	}
	if fleetCount != 1 {
		t.Errorf("mcp e2e: POST /v1/fleet recorded %d times, want exactly 1", fleetCount)
	}
	if fleetIdx == -1 || searchIdx == -1 || fleetIdx >= searchIdx {
		t.Errorf("mcp e2e: POST /v1/fleet (index %d) did not happen before GET /v1/search (index %d)", fleetIdx, searchIdx)
	}

	// ajent_post carried the agent-supplied idempotency key.
	postReq := mcpE2ERecorded(reqs, "POST", "/v1/posts")
	if postReq == nil {
		t.Fatal("mcp e2e: no POST /v1/posts recorded")
	}
	if got := postReq.Header.Get("Idempotency-Key"); got != "e2e-1" {
		t.Errorf("mcp e2e: POST /v1/posts Idempotency-Key = %q, want %q", got, "e2e-1")
	}

	// ajent_ack sent through as a JSON integer, matching the seq recorded
	// in testdata/contract/responses/inbox.json.
	ackReq := mcpE2ERecorded(reqs, "POST", "/v1/inbox/ack")
	if ackReq == nil {
		t.Fatal("mcp e2e: no POST /v1/inbox/ack recorded")
	}
	if !bytes.Contains(ackReq.Body, []byte(`"through":7`)) {
		t.Errorf("mcp e2e: POST /v1/inbox/ack body = %s, want it to contain %q", ackReq.Body, `"through":7`)
	}

	// stdout purity: every line this test read parses as a JSON-RPC 2.0
	// object.
	sess.mu.Lock()
	rawLines := append([][]byte(nil), sess.receivedRaw...)
	sess.mu.Unlock()
	if len(rawLines) == 0 {
		t.Error("mcp e2e: no stdout lines were read")
	}
	for i, raw := range rawLines {
		var generic map[string]any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Errorf("mcp e2e: stdout line %d is not a JSON object: %v\nline: %s", i, err, raw)
			continue
		}
		if generic["jsonrpc"] != "2.0" {
			t.Errorf("mcp e2e: stdout line %d is missing \"jsonrpc\":\"2.0\": %s", i, raw)
		}
	}

	// No token substring ever reaches stderr.
	if stderrText := sess.stderrText(); strings.Contains(stderrText, cred.Token) {
		t.Errorf("mcp e2e: stderr contains the credential token:\n%s", stderrText)
	}
}

// TestMCPTranscriptRepeatSuppression checks that ajent_inbox suppresses
// items already delivered to this profile, that the suppression survives a
// subprocess restart (cache persistence, T3.4), and that repeat=true
// replays the suppressed items.
func TestMCPTranscriptRepeatSuppression(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	configDir := t.TempDir()
	if err := credentials.SaveRoot(configDir, cred); err != nil {
		t.Fatalf("mcp e2e: saving root credential: %v", err)
	}

	projectDir := mcpE2EProjectDir(t)
	home := t.TempDir()
	env := mcpE2EEnv(configDir, srv.URL, home)

	// First subprocess: initialize, then call ajent_inbox twice.
	sess1 := mcpE2EStart(t, projectDir, env)
	deadline := time.Now().Add(10 * time.Second)

	sess1.call(t, deadline, 1, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	sess1.notify(t, "notifications/initialized")

	first := mcpE2EInboxData(t, sess1.call(t, deadline, 2, "tools/call", map[string]any{
		"name": "ajent_inbox", "arguments": map[string]any{},
	}))
	if suppressed, _ := first["suppressed"].(float64); suppressed != 0 {
		t.Errorf("mcp e2e: first ajent_inbox call: suppressed = %v, want 0", suppressed)
	}
	if items, _ := first["items"].([]any); len(items) != 1 {
		t.Errorf("mcp e2e: first ajent_inbox call: items length = %d, want 1", len(items))
	}

	second := mcpE2EInboxData(t, sess1.call(t, deadline, 3, "tools/call", map[string]any{
		"name": "ajent_inbox", "arguments": map[string]any{},
	}))
	if suppressed, _ := second["suppressed"].(float64); suppressed <= 0 {
		t.Errorf("mcp e2e: second ajent_inbox call (same process): suppressed = %v, want > 0", suppressed)
	}
	if items, _ := second["items"].([]any); len(items) != 0 {
		t.Errorf("mcp e2e: second ajent_inbox call (same process): items length = %d, want 0", len(items))
	}

	if _, err := sess1.close(); err != nil {
		t.Errorf("mcp e2e: first subprocess exited with an error: %v; stderr:\n%s", err, sess1.stderrText())
	}

	// Second subprocess, same config dir and project dir: the persisted
	// cache still suppresses the item.
	sess2 := mcpE2EStart(t, projectDir, env)
	deadline = time.Now().Add(10 * time.Second)

	sess2.call(t, deadline, 1, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	sess2.notify(t, "notifications/initialized")

	third := mcpE2EInboxData(t, sess2.call(t, deadline, 2, "tools/call", map[string]any{
		"name": "ajent_inbox", "arguments": map[string]any{},
	}))
	if suppressed, _ := third["suppressed"].(float64); suppressed <= 0 {
		t.Errorf("mcp e2e: ajent_inbox call after restart: suppressed = %v, want > 0 (cache persistence)", suppressed)
	}
	if items, _ := third["items"].([]any); len(items) != 0 {
		t.Errorf("mcp e2e: ajent_inbox call after restart: items length = %d, want 0", len(items))
	}

	fourth := mcpE2EInboxData(t, sess2.call(t, deadline, 3, "tools/call", map[string]any{
		"name": "ajent_inbox", "arguments": map[string]any{"repeat": true},
	}))
	if items, _ := fourth["items"].([]any); len(items) != 1 {
		t.Errorf("mcp e2e: ajent_inbox call with repeat=true: items length = %d, want 1", len(items))
	}

	if _, err := sess2.close(); err != nil {
		t.Errorf("mcp e2e: second subprocess exited with an error: %v; stderr:\n%s", err, sess2.stderrText())
	}
}

// mcpE2EInboxData decodes an ajent_inbox tools/call response down to its
// envelope's "data" object.
func mcpE2EInboxData(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	gotID, _ := resp["id"].(float64)
	envelope := mcpE2EToolEnvelope(t, int(gotID), resp)
	data, _ := envelope["data"].(map[string]any)
	if data == nil {
		t.Fatalf("mcp e2e: envelope has no data object: %#v", envelope)
	}
	return data
}
