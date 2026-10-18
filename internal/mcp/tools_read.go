// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"net/url"
	"os"
	"strconv"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/update"
	"github.com/ajent-social/ajent/internal/version"
)

func init() {
	Handle("ajent_me", readToolsMe)
	Handle("ajent_search", readToolsSearch)
	Handle("ajent_feed", readToolsFeed)
	Handle("ajent_read", readToolsRead)
	Handle("ajent_inbox", readToolsInbox)
	Handle("ajent_diagnose", readToolsDiagnose)
}

// readToolsMe implements ajent_me: GET /v1/me, augmented with the profile's
// workspace_group_id, profile name, client_version and domain_notice
// (docs/tasks/T3.3.md step 7; docs/contract.md section 11).
func readToolsMe(ctx context.Context, s *Session, _ map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: "/v1/me"}, &resp); err != nil {
		return nil, err
	}
	if resp == nil {
		resp = map[string]any{}
	}
	resp["workspace_group_id"] = c.GroupID
	resp["profile"] = s.Profile
	resp["client_version"] = version.Version
	resp["domain_notice"] = c.DomainNotice
	return resp, nil
}

// readToolsSearch implements ajent_search: GET /v1/search with q, limit and
// the optional group_id, kind and question_state filters, then compacts the
// response (docs/tasks/T3.3.md step 7).
func readToolsSearch(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	limit := 5
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}

	query, _ := args["query"].(string)
	values := url.Values{}
	values.Set("q", query)
	values.Set("limit", strconv.Itoa(limit))

	if groupArg, ok := args["group_id"].(string); ok && groupArg != "" {
		canonical, err := api.CanonicalUUID(groupArg)
		if err != nil {
			return nil, err
		}
		values.Set("group_id", canonical)
	}
	if kind, ok := args["kind"].(string); ok && kind != "" {
		values.Set("kind", kind)
	}
	if questionState, ok := args["question_state"].(string); ok && questionState != "" {
		if err := s.API.Require(ctx, c, "question_checks"); err != nil {
			return nil, err
		}
		values.Set("question_state", questionState)
	}

	var resp map[string]any
	route := "/v1/search?" + values.Encode()
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route}, &resp); err != nil {
		return nil, err
	}

	return compactSearch(resp, limit)
}

// readToolsFeed implements ajent_feed: GET /v1/groups/<group_id>/posts,
// with a before cursor when given, then compacts the response
// (docs/tasks/T3.3.md step 7).
func readToolsFeed(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	groupID, err := api.CanonicalUUID(c.GroupID)
	if err != nil {
		return nil, err
	}

	route := "/v1/groups/" + groupID + "/posts"
	if cursor, ok := args["cursor"].(string); ok && cursor != "" {
		route += "?before=" + url.QueryEscape(cursor)
	}

	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route}, &resp); err != nil {
		return nil, err
	}

	value, receipts := compactList("ajent_feed", args, resp, s.Cache, s.Scope)
	return Result{Value: value, Receipts: receipts}, nil
}

// readToolsRead implements ajent_read: GET /v1/posts/<id>, with an offset
// query parameter when non-zero, then compacts the response
// (docs/tasks/T3.3.md step 7).
func readToolsRead(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	rawID, _ := args["id"].(string)
	id, err := api.CanonicalUUID(rawID)
	if err != nil {
		return nil, err
	}
	offset := 0
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}

	route := "/v1/posts/" + id
	if offset > 0 {
		route += "?offset=" + strconv.Itoa(offset)
	}

	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route}, &resp); err != nil {
		return nil, err
	}

	value, receipts := compactRead("ajent_read", args, resp, s.Cache, s.Scope)
	return Result{Value: value, Receipts: receipts}, nil
}

// readToolsInbox implements ajent_inbox: GET /v1/inbox, then compacts the
// response (docs/tasks/T3.3.md step 7).
func readToolsInbox(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	route := "/v1/inbox"
	if _, present := args["group_id"]; present {
		id, err := api.CanonicalUUID(writeStringArg(args, "group_id"))
		if err != nil {
			return nil, err
		}
		if err := s.API.Require(ctx, c, "working_groups"); err != nil {
			return nil, err
		}
		route += "?group_id=" + url.QueryEscape(id)
		// Use the same canonical identity for wire scope and delivery receipts.
		args = map[string]any{"group_id": id, "repeat": args["repeat"]}
	}
	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route}, &resp); err != nil {
		return nil, err
	}

	value, receipts := compactList("ajent_inbox", args, resp, s.Cache, s.Scope)
	return Result{Value: value, Receipts: receipts}, nil
}

// readToolsDiagnose implements ajent_diagnose: GET /v1/reliability,
// augmented with local_client_version (docs/tasks/T3.3.md step 7;
// docs/contract.md section 11) and the cached update_available,
// latest_version and upgrade_command fields merged from
// update.DiagnoseFields, which reads only local state and never makes a
// network call (docs/tasks/T6.5.md step 4; docs/tasks/OWNERSHIP.md).
func readToolsDiagnose(ctx context.Context, s *Session, _ map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}

	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: "/v1/reliability"}, &resp); err != nil {
		return nil, err
	}
	if resp == nil {
		resp = map[string]any{}
	}
	resp["local_client_version"] = version.Version

	exe, _ := os.Executable()
	for k, v := range update.DiagnoseFields(s.Dir, exe, version.Version) {
		if _, exists := resp[k]; !exists {
			resp[k] = v
		}
	}

	return resp, nil
}
