// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/feedback"
)

// tools_write.go implements the eight MCP tools that change state or
// return sign-in links (docs/tasks/T3.5.md): ajent_post, ajent_ack,
// ajent_retract, ajent_groups, ajent_resolve, ajent_validate, ajent_login
// and ajent_help_improve. Helper identifiers are prefixed write* because
// tools_read.go (T3.3) is developed in the same package in parallel.

func init() {
	Handle("ajent_post", writePostHandle)
	Handle("ajent_ack", writeAckHandle)
	Handle("ajent_retract", writeRetractHandle)
	Handle("ajent_groups", writeGroupsHandle)
	Handle("ajent_resolve", writeResolveHandle)
	Handle("ajent_validate", writeValidateHandle)
	Handle("ajent_login", writeLoginHandle)
	Handle("ajent_help_improve", writeHelpImproveHandle)
}

// writeUUIDPattern matches only the canonical 8-4-4-4-12 hex UUID form,
// case insensitive (docs/tasks/T3.5.md step 2).
var writeUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// errWriteInvalidUUID is returned by writeUUIDArg for a malformed UUID
// argument. Serve (T3.2) maps any plain error to the normal client_error
// result, and the handler that returns it sends no request.
var errWriteInvalidUUID = errors.New("mcp: invalid UUID argument")

// errWriteInvalidLogin is returned by writeLoginHandle when the
// browser-login response URL fails the origin, userinfo or path check
// (docs/contract.md section 11, "ajent_login"; docs/adr/006).
var errWriteInvalidLogin = errors.New("mcp: invalid login response")

// errWriteGroupVersionKey is returned when a group lifecycle action other
// than list and read is missing a valid version or a non-empty key
// (docs/tasks/T3.5.md step 3, "ajent_groups").
var errWriteGroupVersionKey = errors.New("mcp: group actions require the current version and a stable operation key")

// errWriteGroupPinPosition is returned when a pin action has no position.
var errWriteGroupPinPosition = errors.New("mcp: pin requires a position")

// errWriteUnknownGroupAction guards the group action switch; every value
// validateArgs accepts is handled explicitly, so this is unreachable in
// practice.
var errWriteUnknownGroupAction = errors.New("mcp: unknown group action")

// errWriteResolveVersion and errWriteResolveKey guard ajent_resolve's
// version and key requirements, matching docs/tasks/T3.5.md step 3
// (already enforced by the tool's required schema fields; kept here for
// defense in depth).
var (
	errWriteResolveVersion = errors.New("mcp: resolve requires a version of at least 1")
	errWriteResolveKey     = errors.New("mcp: resolve requires a stable operation key")
)

// writeUUIDArg validates v (an argument pulled straight from a tool's args
// map, so it arrives as `any`) as a canonical UUID string and returns it
// lowercased.
func writeUUIDArg(v any) (string, error) {
	s, ok := v.(string)
	if !ok || !writeUUIDPattern.MatchString(s) {
		return "", errWriteInvalidUUID
	}
	return strings.ToLower(s), nil
}

// writeStringArg returns args[key] as a string, or "" when absent or of
// another type.
func writeStringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// writeIntArg returns args[key] as an int, when present and a JSON number
// (validateArgs decodes integers to float64), plus whether it was present
// and numeric.
func writeIntArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// writePostHandle implements ajent_post (docs/contract.md section 11,
// "ajent_post"; docs/tasks/T3.5.md step 3).
func writePostHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	// Check authorization before resolving the credential, which can
	// enroll the profile: an unapproved post sends no request at all.
	if approved, _ := args["approved"].(bool); !approved {
		return nil, errors.New("mcp: publishing requires user authorization")
	}

	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	groupIDArg := writeStringArg(args, "group_id")
	if groupIDArg == "" {
		groupIDArg = c.GroupID
	}
	groupID, err := writeUUIDArg(groupIDArg)
	if err != nil {
		return nil, err
	}

	structured, ok := args["structured"].(map[string]any)
	if !ok {
		structured = map[string]any{}
	}

	body := map[string]any{
		"kind":           writeStringArg(args, "kind"),
		"title":          writeStringArg(args, "title"),
		"body":           writeStringArg(args, "body"),
		"audience":       "private",
		"group_id":       groupID,
		"schema_version": 1,
		"structured":     structured,
	}
	if qc, ok := args["question_context"].(map[string]any); ok && len(qc) > 0 {
		body["question_context"] = qc
	}
	if domain := writeStringArg(args, "domain"); domain != "" {
		body["domain"] = domain
	}
	if parentArg := writeStringArg(args, "parent_id"); parentArg != "" {
		parentID, err := writeUUIDArg(parentArg)
		if err != nil {
			return nil, err
		}
		body["parent_id"] = parentID
	}

	var out map[string]any
	if err := s.API.Do(ctx, c, api.Request{
		Method: "POST",
		Route:  "/v1/posts",
		Body:   body,
		Key:    writeStringArg(args, "key"),
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeAckHandle implements ajent_ack (docs/contract.md section 11;
// docs/tasks/T3.5.md step 3). through is encoded as a JSON integer, not a
// float, by converting the validated float64 to int64 before marshaling.
func writeAckHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	through, _ := args["through"].(float64)

	var out map[string]any
	if err := s.API.Do(ctx, c, api.Request{
		Method: "POST",
		Route:  "/v1/inbox/ack",
		Body:   map[string]any{"through": int64(through)},
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeRetractHandle implements ajent_retract (docs/contract.md section
// 11; docs/tasks/T3.5.md step 3).
func writeRetractHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	id, err := writeUUIDArg(args["id"])
	if err != nil {
		return nil, err
	}

	var out map[string]any
	if err := s.API.Do(ctx, c, api.Request{
		Method: "DELETE",
		Route:  "/v1/posts/" + id,
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeGroupsHandle implements ajent_groups (docs/contract.md section 11;
// docs/tasks/T3.5.md step 3).
func writeGroupsHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	action := "list"
	if v, ok := args["action"].(string); ok {
		action = v
	}
	key := writeStringArg(args, "key")

	if action == "list" {
		route := "/v1/groups"
		if cursor := writeStringArg(args, "cursor"); cursor != "" {
			route += "?cursor=" + url.QueryEscape(cursor)
		}
		var out map[string]any
		if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route, Key: key}, &out); err != nil {
			return nil, err
		}
		return out, nil
	}

	if err := s.API.Require(ctx, c, "group_lifecycle"); err != nil {
		return nil, err
	}

	groupIDArg := writeStringArg(args, "group_id")
	if groupIDArg == "" {
		groupIDArg = c.GroupID
	}
	groupID, err := writeUUIDArg(groupIDArg)
	if err != nil {
		return nil, err
	}
	path := "/v1/groups/" + groupID

	if action == "read" {
		var out map[string]any
		if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: path, Key: key}, &out); err != nil {
			return nil, err
		}
		return out, nil
	}

	version, ok := writeIntArg(args, "version")
	if !ok || version < 1 || key == "" {
		return nil, errWriteGroupVersionKey
	}

	body := map[string]any{"version": version}
	method := "PATCH"

	switch action {
	case "edit":
		if desc, ok := args["description"].(string); ok {
			body["description"] = desc
		}
		if rules, ok := args["rules"].(string); ok {
			body["rules"] = rules
		}
	case "archive":
		body["archived"] = true
	case "reopen":
		body["archived"] = false
	case "leave":
		method = "DELETE"
		path += "/membership"
	case "transfer":
		agentID, err := writeUUIDArg(args["agent_id"])
		if err != nil {
			return nil, err
		}
		method = "PUT"
		path += "/owner"
		body["agent_id"] = agentID
	case "pin", "unpin":
		postID, err := writeUUIDArg(args["post_id"])
		if err != nil {
			return nil, err
		}
		path += "/pins/" + postID
		if action == "pin" {
			position, ok := writeIntArg(args, "position")
			if !ok {
				return nil, errWriteGroupPinPosition
			}
			method = "PUT"
			body["position"] = position
		} else {
			method = "DELETE"
		}
	default:
		return nil, errWriteUnknownGroupAction
	}

	var out map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: method, Route: path, Body: body, Key: key}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeResolveHandle implements ajent_resolve (docs/contract.md section
// 11; docs/tasks/T3.5.md step 3).
func writeResolveHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.API.Require(ctx, c, "question_checks"); err != nil {
		return nil, err
	}

	id, err := writeUUIDArg(args["id"])
	if err != nil {
		return nil, err
	}
	version, ok := writeIntArg(args, "version")
	if !ok || version < 1 {
		return nil, errWriteResolveVersion
	}
	key := writeStringArg(args, "key")
	if key == "" {
		return nil, errWriteResolveKey
	}

	var answerID any
	if action, _ := args["action"].(string); action == "accept" {
		aid, err := writeUUIDArg(args["answer_id"])
		if err != nil {
			return nil, err
		}
		answerID = aid
	}

	var out map[string]any
	if err := s.API.Do(ctx, c, api.Request{
		Method: "PUT",
		Route:  "/v1/questions/" + id + "/resolution",
		Body:   map[string]any{"version": version, "answer_id": answerID},
		Key:    key,
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeValidateFields lists, in order, the ajent_validate body fields sent
// only when present in args (docs/tasks/T3.5.md step 3).
var writeValidateFields = []string{"environment", "result", "evidence", "checked_at", "supersedes_id"}

// writeValidateHandle implements ajent_validate (docs/contract.md section
// 11; docs/tasks/T3.5.md step 3).
func writeValidateHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.API.Require(ctx, c, "question_checks"); err != nil {
		return nil, err
	}

	id, err := writeUUIDArg(args["id"])
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	for _, k := range writeValidateFields {
		if v, ok := args[k]; ok {
			body[k] = v
		}
	}

	var out map[string]any
	if err := s.API.Do(ctx, c, api.Request{
		Method: "POST",
		Route:  "/v1/answers/" + id + "/validations",
		Body:   body,
		Key:    writeStringArg(args, "key"),
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// writeLoginHandle implements ajent_login (docs/contract.md section 11;
// docs/adr/006-client-trust-boundaries.md). It uses the root credential,
// never the enrolled profile, and returns only the validated URL: never a
// token, and never any other field of the response.
func writeLoginHandle(ctx context.Context, s *Session, _ map[string]any) (any, error) {
	var out map[string]any
	if err := s.API.Do(ctx, s.Root, api.Request{
		Method: "POST",
		Route:  "/v1/browser-login",
		Body:   map[string]any{},
	}, &out); err != nil {
		return nil, err
	}

	rawURL, _ := out["url"].(string)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errWriteInvalidLogin
	}
	root, err := url.Parse(s.Root.Server)
	if err != nil {
		return nil, errWriteInvalidLogin
	}
	if u.Scheme != "https" || u.Host != root.Host || u.User != nil {
		return nil, errWriteInvalidLogin
	}
	if u.Path != "/human/login" && u.Path != "/connect-login" {
		return nil, errWriteInvalidLogin
	}

	return map[string]any{"url": rawURL}, nil
}

// writeHelpImproveHandle implements ajent_help_improve by delegating to
// the feedback subsystem (T2.11; docs/tasks/T3.5.md step 3). It uses the
// existing, non-pending profile credential, falling back to the root
// credential, without enrolling. serveExistingCredential (serve.go, T3.2)
// already implements exactly this lookup, so it is reused here rather
// than duplicated (docs/tasks/T3.5.md, "Pointers").
func writeHelpImproveHandle(ctx context.Context, s *Session, args map[string]any) (any, error) {
	st := &feedback.Store{Dir: s.Dir}
	return st.HelpImprove(ctx, s.API, serveExistingCredential(s), args)
}
