// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"net/url"
	"strconv"

	"github.com/ajent-social/ajent/internal/api"
)

func init() {
	Handle("ajent_goals", goalsToolsList)
	Handle("ajent_goal", goalsToolsRead)
	Handle("ajent_goal_update", goalsToolsUpdate)
	Handle("ajent_goal_track", goalsToolsTrack)
}

// goalsToolsList implements ajent_goals: GET /v1/goals, plus a before
// cursor when non-empty. The response is returned unchanged; it is not
// list-compacted, matching the reference (docs/tasks/T3.8.md step 2;
// docs/contract.md section 11).
func goalsToolsList(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.API.Require(ctx, c, "goals"); err != nil {
		return nil, err
	}

	route := "/v1/goals"
	if before, ok := args["before"].(string); ok && before != "" {
		route += "?before=" + url.QueryEscape(before)
	}

	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// goalsToolsRead implements ajent_goal: GET /v1/goals/<id>, with before and
// version query parameters when present and non-zero, then fragments the
// response through compactRead, which also carries the goal's
// snapshot_version (docs/tasks/T3.8.md step 3; docs/contract.md section 11,
// "Read fragments").
func goalsToolsRead(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.API.Require(ctx, c, "goals"); err != nil {
		return nil, err
	}

	rawID, _ := args["id"].(string)
	id, err := api.CanonicalUUID(rawID)
	if err != nil {
		return nil, err
	}

	values := url.Values{}
	if before, ok := args["before"].(float64); ok && before != 0 {
		values.Set("before", strconv.Itoa(int(before)))
	}
	if version, ok := args["version"].(float64); ok && version != 0 {
		values.Set("version", strconv.Itoa(int(version)))
	}

	route := "/v1/goals/" + id
	if query := values.Encode(); query != "" {
		route += "?" + query
	}

	var resp map[string]any
	if err := s.API.Do(ctx, c, api.Request{Method: "GET", Route: route}, &resp); err != nil {
		return nil, err
	}

	value, receipts := compactRead("ajent_goal", args, resp, s.Cache, s.Scope)
	return Result{Value: value, Receipts: receipts}, nil
}

// goalsToolsTrackExcluded are the ajent_goal_track arguments never copied
// into the request body (docs/tasks/T3.8.md step 4).
var goalsToolsTrackExcluded = map[string]bool{"key": true}

// goalsToolsTrack implements ajent_goal_track: POST /v1/goals with every
// argument except key, defaulting priority to "normal" when absent, using
// key as the Idempotency-Key (docs/tasks/T3.8.md step 4; docs/contract.md
// section 11).
func goalsToolsTrack(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.API.Require(ctx, c, "goals"); err != nil {
		return nil, err
	}

	body := goalsToolsBody(args, goalsToolsTrackExcluded)
	if _, ok := body["priority"]; !ok {
		body["priority"] = "normal"
	}
	key, _ := args["key"].(string)

	var resp map[string]any
	req := api.Request{Method: "POST", Route: "/v1/goals", Body: body, Key: key}
	if err := s.API.Do(ctx, c, req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// goalsToolsUpdateExcluded are the ajent_goal_update arguments never copied
// into the request body (docs/tasks/T3.8.md step 5).
var goalsToolsUpdateExcluded = map[string]bool{"id": true, "key": true}

// goalsToolsUpdate implements ajent_goal_update: POST
// /v1/goals/<id>/events with every argument except id and key, using key as
// the Idempotency-Key (docs/tasks/T3.8.md step 5; docs/contract.md section
// 11).
func goalsToolsUpdate(ctx context.Context, s *Session, args map[string]any) (any, error) {
	c, err := s.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.API.Require(ctx, c, "goals"); err != nil {
		return nil, err
	}

	rawID, _ := args["id"].(string)
	id, err := api.CanonicalUUID(rawID)
	if err != nil {
		return nil, err
	}

	body := goalsToolsBody(args, goalsToolsUpdateExcluded)
	key, _ := args["key"].(string)

	var resp map[string]any
	req := api.Request{Method: "POST", Route: "/v1/goals/" + id + "/events", Body: body, Key: key}
	if err := s.API.Do(ctx, c, req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// goalsToolsBody copies args into a new map, skipping any key present in
// excluded. Integer arguments (such as version) stay float64 so
// encoding/json marshals them as JSON integers (docs/tasks/T3.8.md step 5).
func goalsToolsBody(args map[string]any, excluded map[string]bool) map[string]any {
	body := make(map[string]any, len(args))
	for k, v := range args {
		if excluded[k] {
			continue
		}
		body[k] = v
	}
	return body
}
