// SPDX-License-Identifier: Apache-2.0

// Package mcp implements the ajent Model Context Protocol server.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxLineBytes is the maximum size, in bytes, of a single newline-delimited
// JSON-RPC message this transport will accumulate before treating the line
// as oversized.
const MaxLineBytes = 1 << 20 // 1 MiB per newline-delimited message

// RPCError is a JSON-RPC 2.0 error object. A handler that returns an
// *RPCError controls the exact code and message sent to the client; any
// other error becomes CodeInternalError with the message "Internal error".
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message)
}

// JSON-RPC 2.0 reserved error codes used by this transport.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Dispatcher answers requests. A returned *RPCError becomes a JSON-RPC
// error; any other error becomes CodeInternalError with message
// "Internal error".
type Dispatcher interface {
	Initialize(ctx context.Context, params json.RawMessage) (any, error)
	ListTools(ctx context.Context) (any, error)
	CallTool(ctx context.Context, params json.RawMessage) (result any, after func(), err error) // after runs once the response is written
}

// SupportedProtocols lists the MCP protocol versions this client
// understands, oldest first.
var SupportedProtocols = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// NegotiateProtocol returns requested if it is a supported protocol
// version, else the latest supported version.
func NegotiateProtocol(requested string) string {
	for _, v := range SupportedProtocols {
		if v == requested {
			return requested
		}
	}
	return SupportedProtocols[len(SupportedProtocols)-1]
}

// request is the wire shape of an incoming JSON-RPC 2.0 message. A message
// is a notification when the id member is absent (len(ID) == 0). An
// explicit "id": null is treated as a request with a null id, since it
// decodes to the four bytes "null", not an empty slice.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// nullID is the raw JSON encoding of a null id, used for error responses
// that precede decoding of the request id (parse errors).
var nullID = json.RawMessage("null")

// ServeRPC reads newline-delimited JSON-RPC from r until EOF and writes one
// line per response to w. Messages are processed sequentially, one at a
// time. It returns nil on EOF, or ctx.Err() if the context is cancelled
// between messages.
func ServeRPC(ctx context.Context, r io.Reader, w io.Writer, d Dispatcher) error {
	br := bufio.NewReaderSize(r, 64*1024)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, oversized, eof := readLine(br)

		if oversized {
			if err := writeError(w, nullID, CodeParseError, "Parse error"); err != nil {
				return err
			}
			if eof {
				return nil
			}
			continue
		}

		trimmed := trimLine(line)
		if len(trimmed) != 0 {
			if err := processMessage(ctx, trimmed, w, d); err != nil {
				return err
			}
		}

		if eof {
			return nil
		}
	}
}

// readLine reads one newline-delimited message from br, accumulating across
// multiple ReadSlice calls when the line is longer than the reader's
// internal buffer.
//
// As soon as the accumulated length exceeds MaxLineBytes, it stops
// accumulating and instead discards bytes through the next '\n' (or EOF),
// reporting oversized=true. It never holds more than MaxLineBytes + 64 KiB
// in memory, since the reader's internal buffer (and so each ReadSlice
// chunk) is capped at 64 KiB.
//
// eof reports that the underlying reader is exhausted; when eof is true and
// line is non-empty (or oversized is true), the caller still processes that
// final, un-terminated message before stopping.
func readLine(br *bufio.Reader) (line []byte, oversized, eof bool) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if !oversized {
			buf = append(buf, chunk...)
			if len(buf) > MaxLineBytes {
				oversized = true
			}
		}

		switch {
		case err == nil:
			// chunk ended with '\n'.
			if oversized {
				return nil, true, false
			}
			return buf, false, false
		case errors.Is(err, bufio.ErrBufferFull):
			// No '\n' yet; keep reading (accumulating or discarding).
			continue
		default:
			// EOF or another read error: the stream ended without a
			// trailing newline.
			if oversized {
				return nil, true, true
			}
			if len(buf) == 0 {
				return nil, false, true
			}
			return buf, false, true
		}
	}
}

// trimLine trims a trailing \r\n (or \n) and surrounding ASCII whitespace.
func trimLine(line []byte) []byte {
	line = bytes.TrimRight(line, "\r\n")
	return bytes.TrimFunc(line, isASCIISpace)
}

func isASCIISpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// processMessage decodes and dispatches a single trimmed, non-empty line,
// writing at most one response line to w.
func processMessage(ctx context.Context, line []byte, w io.Writer, d Dispatcher) error {
	var req request
	if err := decodeStrictObject(line, &req); err != nil {
		return writeError(w, nullID, CodeParseError, "Parse error")
	}

	if len(req.ID) == 0 {
		// Notification: never dispatched, produces no output, regardless of
		// whether jsonrpc/method are otherwise well-formed.
		return nil
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		return writeError(w, req.ID, CodeInvalidRequest, "Invalid Request")
	}

	result, after, err := dispatch(ctx, req.Method, req.Params, d)
	if err != nil {
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) {
			return writeError(w, req.ID, rpcErr.Code, rpcErr.Message)
		}
		return writeError(w, req.ID, CodeInternalError, "Internal error")
	}

	if writeErr := writeResult(w, req.ID, result); writeErr != nil {
		return writeErr
	}
	if after != nil {
		after()
	}
	return nil
}

// dispatch routes a method to the Dispatcher, or handles it directly for
// "ping", which never reaches the Dispatcher.
func dispatch(ctx context.Context, method string, params json.RawMessage, d Dispatcher) (result any, after func(), err error) {
	switch method {
	case "initialize":
		result, err = d.Initialize(ctx, params)
		return result, nil, err
	case "ping":
		return map[string]any{}, nil, nil
	case "tools/list":
		result, err = d.ListTools(ctx)
		return result, nil, err
	case "tools/call":
		return d.CallTool(ctx, params)
	default:
		return nil, nil, &RPCError{Code: CodeMethodNotFound, Message: "Method not found"}
	}
}

// decodeStrictObject decodes line into v, requiring that the JSON value be
// an object (not, e.g., an array or scalar).
func decodeStrictObject(line []byte, v any) error {
	trimmed := bytes.TrimLeft(line, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("mcp: not a JSON object")
	}
	return json.Unmarshal(line, v)
}

// writeResult writes a successful JSON-RPC response line.
func writeResult(w io.Writer, id json.RawMessage, result any) error {
	env := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      rawID(id),
		Result:  result,
	}
	return writeLine(w, env)
}

// writeError writes a JSON-RPC error response line.
func writeError(w io.Writer, id json.RawMessage, code int, message string) error {
	env := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   RPCError        `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      rawID(id),
		Error:   RPCError{Code: code, Message: message},
	}
	return writeLine(w, env)
}

// rawID normalizes an absent id to a JSON null so responses always carry an
// id member.
func rawID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return nullID
	}
	return id
}

// writeLine marshals v with HTML-escaping disabled, builds the full line in
// a buffer, and writes it to w with a single Write call.
func writeLine(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
