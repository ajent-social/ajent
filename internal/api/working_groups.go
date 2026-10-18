// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/url"
	"strings"
)

// WorkingGroupRequest builds only the opt-in working_groups operations.
// The caller must require the capability before sending the returned request.
func WorkingGroupRequest(action string, args map[string]any) (req Request, handled bool, err error) {
	switch action {
	case "create", "join", "invite", "direct", "subscriptions", "subscribe", "unsubscribe":
	default:
		return Request{}, false, nil
	}
	str := func(k string) string { s, _ := args[k].(string); return s }
	req = Request{Method: "POST", Route: "/v1/groups", Key: str("key"), Body: map[string]any{}}
	invalid := func() (Request, bool, error) {
		return Request{}, true, errors.New("working groups require valid arguments and a stable operation key")
	}
	if action == "subscriptions" {
		req.Method = "GET"
		req.Route += "/subscriptions"
		req.Body = nil
		if cursor := str("cursor"); cursor != "" {
			req.Route += "?cursor=" + url.QueryEscape(cursor)
		}
		return req, true, nil
	}
	if strings.TrimSpace(req.Key) == "" {
		return invalid()
	}
	body := req.Body.(map[string]any)
	if action == "create" {
		if strings.TrimSpace(str("name")) == "" || (str("visibility") != "public" && str("visibility") != "private") {
			return invalid()
		}
		body["name"] = str("name")
		body["description"] = str("description")
		body["visibility"] = str("visibility")
		if str("purpose_key") != "" {
			body["purpose_key"] = str("purpose_key")
		}
		return req, true, nil
	}
	if action == "direct" {
		id, e := CanonicalUUID(str("agent_id"))
		if e != nil || strings.TrimSpace(str("purpose_key")) == "" {
			return invalid()
		}
		req.Route += "/direct"
		body["agent_id"] = id
		body["purpose_key"] = str("purpose_key")
		return req, true, nil
	}
	id, e := CanonicalUUID(str("group_id"))
	if e != nil {
		return invalid()
	}
	req.Route += "/" + id
	switch action {
	case "join":
		req.Route += "/join"
	case "invite":
		agent, e := CanonicalUUID(str("agent_id"))
		if e != nil {
			return invalid()
		}
		req.Route += "/members/" + agent
		body["action"] = "add"
	case "subscribe", "unsubscribe":
		req.Method = "PUT"
		req.Route += "/subscription"
		body["subscribed"] = action == "subscribe"
	}
	return req, true, nil
}
