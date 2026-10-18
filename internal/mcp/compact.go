// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
)

// Response-budget constants shared by the compaction helpers in this file
// (docs/tasks/T3.3.md step 1; docs/contract.md section 11).
const (
	searchMaxBytes = 12000
	listBudget     = MaxResultBytes - 1500
	readBudget     = MaxResultBytes - 1000
	previewChars   = 320
	titleChars     = 160
	readChars      = 1800
)

// ellipsis is U+2026 HORIZONTAL ELLIPSIS, declared as an escape to keep this
// source file ASCII.
const ellipsis = "\u2026"

// errReadToolsSearchResponse is returned by compactSearch when the API
// response does not have a "posts" array.
var errReadToolsSearchResponse = errors.New("mcp: invalid search response")

// errReadToolsSearchPost is returned by compactSearch when a post entry is
// not an object, or its id is not a well-formed UUID.
var errReadToolsSearchPost = errors.New("mcp: invalid search post")

// readToolsSearchIDPattern matches the loosely-shaped UUID string
// compactSearch accepts before lowercasing it (docs/tasks/T3.3.md step 3.3).
var readToolsSearchIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// readToolsPresenceEventTypes are the structured.event_type values that
// mark a status item as connection presence rather than content
// (docs/tasks/T3.3.md step 4).
var readToolsPresenceEventTypes = map[string]bool{
	"agent.joined":       true,
	"agent.left":         true,
	"session.started":    true,
	"session.ended":      true,
	"agent.connected":    true,
	"agent.disconnected": true,
}

// readToolsPresenceTitles are the lowercased, trimmed title strings that
// also mark a status item as presence when structured.event_type is absent
// or unrecognized.
var readToolsPresenceTitles = map[string]bool{
	"connected":       true,
	"disconnected":    true,
	"agent joined":    true,
	"agent left":      true,
	"session started": true,
	"session ended":   true,
	"joining":         true,
	"leaving":         true,
}

// boundedText returns value bounded to at most budget bytes of compact JSON
// encoding. A non-string value, or a string that is blank after
// strings.TrimSpace, returns fallback. A string within budget is returned
// trimmed and unchanged. A longer string is cut to the longest rune prefix
// whose JSON encoding, with the ellipsis appended, still fits the budget.
func boundedText(value any, budget int, fallback string) string {
	s, ok := value.(string)
	if !ok {
		return fallback
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	if len(envelopeEncode(s)) <= budget {
		return s
	}

	runes := []rune(s)
	low, high := 0, len(runes)
	if high > budget {
		high = budget
	}
	for low < high {
		mid := (low + high + 1) / 2
		if len(envelopeEncode(string(runes[:mid])+ellipsis)) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low]) + ellipsis
}

// readToolsRunePrefix returns the first n runes of s, or s unchanged when it
// has n runes or fewer. Unlike boundedText, no ellipsis is appended
// (docs/tasks/T3.3.md step 5.2).
func readToolsRunePrefix(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// isPresence reports whether item is a connection-presence status update
// that compactList suppresses rather than previews (docs/tasks/T3.3.md
// step 4; docs/contract.md section 11, "Feed and inbox compaction").
func isPresence(item map[string]any) bool {
	kind, _ := item["kind"].(string)
	if kind != "status" {
		return false
	}
	if structured, ok := item["structured"].(map[string]any); ok {
		if eventType, ok := structured["event_type"].(string); ok && readToolsPresenceEventTypes[eventType] {
			return true
		}
	}
	title, _ := item["title"].(string)
	title = strings.ToLower(strings.TrimSpace(title))
	return readToolsPresenceTitles[title]
}

// compactSearch projects resp into the compact-findings-v1 shape
// (docs/tasks/T3.3.md step 3; docs/contract.md section 11, "Search
// compaction"). It never returns receipts: search is not cache-filtered.
func compactSearch(resp map[string]any, limit int) (map[string]any, error) {
	postsRaw, ok := resp["posts"].([]any)
	if !ok {
		return nil, errReadToolsSearchResponse
	}

	hasMore, _ := resp["has_more"].(bool)
	hasMore = hasMore || len(postsRaw) > limit

	findings := make([]any, 0, limit)
	data := map[string]any{
		"format":            "compact-findings-v1",
		"findings":          findings,
		"ranking_version":   boundedText(resp["ranking_version"], 80, "unknown"),
		"has_more":          hasMore,
		"max_payload_bytes": searchMaxBytes,
		"guidance":          "Previews only; use ajent_read with an ID for full evidence. Narrow the query if has_more is true. Verify locally.",
	}

	n := len(postsRaw)
	if n > limit {
		n = limit
	}
	for i := 0; i < n; i++ {
		post, ok := postsRaw[i].(map[string]any)
		if !ok {
			return nil, errReadToolsSearchPost
		}
		idRaw, _ := post["id"].(string)
		if !readToolsSearchIDPattern.MatchString(idRaw) {
			return nil, errReadToolsSearchPost
		}
		id := strings.ToLower(idRaw)

		metadata, _ := post["structured"].(map[string]any)

		summary := boundedText(metadata["summary"], 500, "")
		summaryBasis := "author_summary"
		if summary == "" {
			summary = boundedText(post["body"], 500, "No summary provided.")
			summaryBasis = "body_excerpt"
		}

		evidence := boundedText(metadata["evidence"], 240, "")
		evidenceLevel := "author_reported_unverified"
		if evidence == "" {
			evidenceLevel = "not_provided"
			evidence = "No evidence metadata; read the source before relying on it."
		}

		finding := map[string]any{
			"id":             id,
			"title":          boundedText(post["title"], 180, "Untitled post"),
			"kind":           boundedText(post["kind"], 40, "unknown"),
			"summary":        summary,
			"summary_basis":  summaryBasis,
			"applicability":  boundedText(metadata["applicability"], 240, "Not provided; check against the local task."),
			"evidence_level": evidenceLevel,
			"evidence":       evidence,
			"limitations":    boundedText(metadata["limitations"], 240, "Not provided; preview is incomplete and not independently verified."),
			"source": map[string]any{
				"post_id":   id,
				"author_id": boundedText(post["author_id"], 50, ""),
				"handle":    boundedText(post["handle"], 80, ""),
			},
			"scope": map[string]any{
				"audience": boundedText(post["audience"], 30, ""),
				"group_id": boundedText(post["group_id"], 50, ""),
			},
			"expires_at": boundedText(post["expires_at"], 50, ""),
		}

		findings = append(findings, finding)
		data["findings"] = findings

		if len(envelopeEncode(map[string]any{"trust": "untrusted_reference_data", "data": data})) > searchMaxBytes {
			findings = findings[:len(findings)-1]
			data["findings"] = findings
			data["has_more"] = true
			break
		}
	}

	return data, nil
}

// compactList projects an ajent_feed or ajent_inbox response into bounded
// previews, suppressing presence updates and previously delivered items
// (docs/tasks/T3.3.md step 5; docs/contract.md section 11, "Feed and inbox
// compaction"). It returns the compacted value and the delivery-cache
// receipts to remember once the response is written.
func compactList(tool string, args map[string]any, value map[string]any, c *Cache, scope []string) (map[string]any, []string) {
	field := "posts"
	_, hasFindings := value["findings"]
	switch {
	case tool == "ajent_inbox":
		field = "items"
	case hasFindings:
		field = "findings"
	}
	lane := "preview"
	if field == "items" {
		lane = "inbox"
	}

	original, _ := value[field].([]any)

	out := make(map[string]any, len(value)+2)
	for k, v := range value {
		if k == field {
			continue
		}
		out[k] = v
	}
	items := []any{}
	out[field] = items

	repeat, _ := args["repeat"].(bool)

	var receipts []string
	suppressed := 0
	var lastProcessed map[string]any

	for _, rawItem := range original {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}

		if isPresence(item) {
			suppressed++
			lastProcessed = item
			continue
		}

		var preview map[string]any
		if field == "findings" {
			preview = item
		} else {
			preview = readToolsPreview(item)
		}

		key := c.Key(scope, lane, item)
		if !repeat && c.Seen(key) {
			suppressed++
			lastProcessed = item
			continue
		}

		candidateItems := make([]any, len(items)+1)
		copy(candidateItems, items)
		candidateItems[len(items)] = preview
		candidate := make(map[string]any, len(out))
		for k, v := range out {
			candidate[k] = v
		}
		candidate[field] = candidateItems

		if len(envelopeEncode(candidate)) > listBudget {
			out["truncated"] = true
			out["has_more"] = true
			if tool == "ajent_feed" && lastProcessed != nil {
				createdAt, _ := lastProcessed["created_at"].(string)
				lastID, _ := lastProcessed["id"].(string)
				cursor := createdAt + "|" + lastID
				out["next_cursor"] = base64.RawURLEncoding.EncodeToString([]byte(cursor))
			}
			if field == "items" {
				out["acknowledge_through"] = readToolsAcknowledgeThrough(items, value)
			}
			break
		}

		items = candidateItems
		out[field] = items
		receipts = append(receipts, key)
		lastProcessed = item
	}

	out["suppressed"] = suppressed
	if suppressed > 0 {
		out["notice"] = "Previously delivered or presence-only items omitted. repeat=true replays content; acknowledgment remains explicit."
	}

	return out, receipts
}

// readToolsPreview builds the bounded preview for one feed or inbox item
// (docs/tasks/T3.3.md step 5.2).
func readToolsPreview(item map[string]any) map[string]any {
	keys := [...]string{"id", "seq", "kind", "title", "event_type", "report_id", "deployed_revision", "created_at", "replies", "parent_id"}
	preview := make(map[string]any, len(keys)+1)
	for _, k := range keys {
		if v, ok := item[k]; ok {
			preview[k] = v
		}
	}
	if title, ok := preview["title"].(string); ok {
		preview["title"] = readToolsRunePrefix(title, titleChars)
	}

	previewText, ok := item["preview"].(string)
	if !ok {
		previewText, _ = item["body"].(string)
	}
	preview["preview"] = readToolsRunePrefix(previewText, previewChars)

	return preview
}

// readToolsAcknowledgeThrough computes ajent_inbox's acknowledge_through:
// the highest seq among the items already included when truncation began,
// else the source response's acknowledged_through, else 0
// (docs/tasks/T3.3.md step 5.4).
func readToolsAcknowledgeThrough(included []any, value map[string]any) any {
	maxSeq := 0.0
	found := false
	for _, raw := range included {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		seq, _ := item["seq"].(float64)
		if !found || seq > maxSeq {
			maxSeq = seq
			found = true
		}
	}
	if found {
		return maxSeq
	}
	if ack, ok := value["acknowledged_through"]; ok {
		return ack
	}
	return 0
}

// compactRead projects an ajent_read or ajent_goal response into a
// json_fragment when it is large or an offset was requested, or an
// "unchanged" notice when it was already delivered (docs/tasks/T3.3.md
// step 6; docs/contract.md section 11, "Read fragments"). It returns the
// compacted value and the delivery-cache receipts to remember once the
// response is written.
func compactRead(tool string, args map[string]any, value map[string]any, c *Cache, scope []string) (map[string]any, []string) {
	text := envelopeEncode(value)
	offset := 0
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}

	key := c.Key(scope, "read", []any{offset, text})
	repeat, _ := args["repeat"].(bool)
	if !repeat && c.Seen(key) {
		return map[string]any{
			"id":        args["id"],
			"unchanged": true,
			"notice":    "Already delivered. Use repeat=true to reread.",
		}, nil
	}

	format, _ := value["format"].(string)
	runes := []rune(text)
	total := len(runes)
	if format != "json_fragment" && (len(text) > readBudget || offset > 0) {
		if offset > total {
			offset = total
		}
		end := offset + readChars
		if end > total {
			end = total
		}
		var nextOffset any
		if end < total {
			nextOffset = end
		}
		fragment := map[string]any{
			"id":          args["id"],
			"format":      "json_fragment",
			"content":     string(runes[offset:end]),
			"offset":      offset,
			"next_offset": nextOffset,
			"total_chars": total,
		}
		if tool == "ajent_goal" {
			if goal, ok := value["goal"].(map[string]any); ok {
				if version, ok := goal["version"]; ok {
					fragment["snapshot_version"] = version
				}
			}
			fragment["guidance"] = "Pass snapshot_version as version on every remaining fragment. If the goal changes, restart the read from offset zero."
		}
		return fragment, []string{key}
	}

	return value, []string{key}
}
