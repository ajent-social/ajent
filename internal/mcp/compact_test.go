// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readToolsTestScope is a fixed delivery-cache scope shared by the compact
// helper tests in this file.
var readToolsTestScope = []string{"https://example.test", "agent-1", "group-1"}

// readToolsLoadFixture reads and decodes a JSON object fixture from
// testdata/mcp.
func readToolsLoadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "mcp", name)
	//nolint:gosec // G304: path is a fixed, repo-relative testdata literal, not external input.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return v
}

// readToolsBuildItems returns n synthetic feed/inbox items with ascending
// seq numbers and a preview long enough that many of them together exceed
// listBudget, so tests can force compactList's truncation branch
// deterministically instead of depending on a fixture's exact byte count.
func readToolsBuildItems(n, seqStart int) []any {
	preview := strings.Repeat("x", 300)
	items := make([]any, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]any{
			"id":         fmt.Sprintf("00000000-0000-4000-8000-%012d", 500+i),
			"seq":        float64(seqStart + i),
			"kind":       "finding",
			"title":      fmt.Sprintf("Synthetic item %d", i),
			"preview":    preview,
			"created_at": fmt.Sprintf("2026-01-01T%02d:%02d:00Z", i/60, i%60),
		}
	}
	return items
}

func TestBoundedText(t *testing.T) {
	tests := []struct {
		name       string
		value      any
		budget     int
		fallback   string
		want       string
		wantExact  bool
		wantSuffix string
	}{
		{name: "non-string input", value: 42, budget: 10, fallback: "fallback", want: "fallback", wantExact: true},
		{name: "blank input", value: "   ", budget: 10, fallback: "fallback", want: "fallback", wantExact: true},
		{name: "short string", value: "hello", budget: 50, fallback: "fallback", want: "hello", wantExact: true},
		{name: "long ascii string", value: strings.Repeat("a", 500), budget: 50, fallback: "fallback", wantSuffix: ellipsis},
		{name: "long multibyte string", value: strings.Repeat("日本語", 50), budget: 50, fallback: "fallback", wantSuffix: ellipsis},
		{name: "control characters", value: "\x00\x01Hello\tWorld\x02\x03MoreTrailingText", budget: 20, fallback: "fallback", wantSuffix: ellipsis},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boundedText(tt.value, tt.budget, tt.fallback)
			if tt.wantExact && got != tt.want {
				t.Errorf("boundedText(...) = %q, want %q", got, tt.want)
			}
			if tt.wantSuffix != "" && !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("boundedText(...) = %q, want suffix %q", got, tt.wantSuffix)
			}
			if n := len(envelopeEncode(got)); n > tt.budget {
				t.Errorf("boundedText(...) = %q, JSON length %d exceeds budget %d", got, n, tt.budget)
			}
		})
	}
}

func TestCompactSearchLimitAndBudget(t *testing.T) {
	resp := readToolsLoadFixture(t, "search_large.json")

	data, err := compactSearch(resp, 5)
	if err != nil {
		t.Fatalf("compactSearch error = %v", err)
	}

	findings, ok := data["findings"].([]any)
	if !ok {
		t.Fatalf("findings = %#v, want []any", data["findings"])
	}
	if len(findings) > 5 {
		t.Errorf("len(findings) = %d, want at most 5", len(findings))
	}

	envelopeSize := len(envelopeEncode(map[string]any{"trust": "untrusted_reference_data", "data": data}))
	if envelopeSize > searchMaxBytes {
		t.Errorf("envelope size = %d, want at most %d", envelopeSize, searchMaxBytes)
	}

	if data["has_more"] != true {
		t.Errorf("has_more = %v, want true (8 posts fixture with limit 5)", data["has_more"])
	}
	if data["format"] != "compact-findings-v1" {
		t.Errorf("format = %v, want compact-findings-v1", data["format"])
	}

	if _, err := compactSearch(map[string]any{"posts": []any{map[string]any{"id": "not-a-uuid"}}}, 5); err == nil {
		t.Errorf("compactSearch with an invalid id returned no error")
	}

	if _, err := compactSearch(map[string]any{}, 5); err == nil {
		t.Errorf("compactSearch with no posts array returned no error")
	}

	// The per-field budgets keep 5 maxed-out findings comfortably under
	// searchMaxBytes (the max_payload_bytes design), so limit=5 alone never
	// exercises compactSearch's own drop-a-finding overflow branch. A
	// caller-supplied higher limit (compactSearch itself has no upper
	// bound; only the ajent_search tool schema caps it at 5) drives enough
	// maxed findings to force it, proving the final envelope-budget check
	// really does bound the result.
	manyPosts := make([]any, 20)
	for i := range manyPosts {
		manyPosts[i] = readToolsMaxedPost(i)
	}
	overflowData, err := compactSearch(map[string]any{"posts": manyPosts}, 20)
	if err != nil {
		t.Fatalf("compactSearch (overflow case) error = %v", err)
	}
	overflowFindings, ok := overflowData["findings"].([]any)
	if !ok {
		t.Fatalf("overflow findings = %#v, want []any", overflowData["findings"])
	}
	if len(overflowFindings) >= len(manyPosts) {
		t.Fatalf("overflow findings = %d, want fewer than the %d requested (the budget must drop some)", len(overflowFindings), len(manyPosts))
	}
	if overflowData["has_more"] != true {
		t.Errorf("overflow has_more = %v, want true", overflowData["has_more"])
	}
	overflowSize := len(envelopeEncode(map[string]any{"trust": "untrusted_reference_data", "data": overflowData}))
	if overflowSize > searchMaxBytes {
		t.Errorf("overflow envelope size = %d, want at most %d", overflowSize, searchMaxBytes)
	}
}

// readToolsMaxedPost returns a synthetic search post whose every
// boundedText-covered field exceeds its budget, so compactSearch's per-field
// truncation always engages.
func readToolsMaxedPost(i int) map[string]any {
	long := strings.Repeat("word ", 400) // well over every field budget below
	return map[string]any{
		"id":         fmt.Sprintf("00000000-0000-4000-8000-%012d", 600+i),
		"title":      long,
		"kind":       long,
		"body":       long,
		"audience":   long,
		"group_id":   long,
		"author_id":  long,
		"handle":     long,
		"expires_at": long,
		"structured": map[string]any{
			"summary":       long,
			"applicability": long,
			"evidence":      long,
			"limitations":   long,
		},
	}
}

func TestCompactListSuppressesPresenceAndSeen(t *testing.T) {
	value := readToolsLoadFixture(t, "feed_mixed.json")
	cache := OpenCache(t.TempDir(), "test-profile")

	first, receipts := compactList("ajent_feed", map[string]any{}, value, cache, readToolsTestScope)
	posts, ok := first["posts"].([]any)
	if !ok {
		t.Fatalf("posts = %#v, want []any", first["posts"])
	}
	if len(posts) != 3 {
		t.Fatalf("first call: len(posts) = %d, want 3 (2 normal + 1 large, 3 presence suppressed)", len(posts))
	}
	if first["suppressed"] != 3 {
		t.Errorf("first call: suppressed = %v, want 3", first["suppressed"])
	}
	if len(receipts) != 3 {
		t.Fatalf("first call: receipts = %v, want 3 entries", receipts)
	}
	cache.Remember(receipts...)

	second, receipts2 := compactList("ajent_feed", map[string]any{}, value, cache, readToolsTestScope)
	posts2, ok := second["posts"].([]any)
	if !ok {
		t.Fatalf("posts = %#v, want []any", second["posts"])
	}
	if len(posts2) != 0 {
		t.Errorf("second call: len(posts) = %d, want 0 (already delivered)", len(posts2))
	}
	if second["suppressed"] != 6 {
		t.Errorf("second call: suppressed = %v, want 6 (3 presence + 3 already delivered)", second["suppressed"])
	}
	if len(receipts2) != 0 {
		t.Errorf("second call: receipts = %v, want none", receipts2)
	}

	third, receipts3 := compactList("ajent_feed", map[string]any{"repeat": true}, value, cache, readToolsTestScope)
	posts3, ok := third["posts"].([]any)
	if !ok {
		t.Fatalf("posts = %#v, want []any", third["posts"])
	}
	if len(posts3) != 3 {
		t.Errorf("repeat=true call: len(posts) = %d, want 3", len(posts3))
	}
	if len(receipts3) != 3 {
		t.Errorf("repeat=true call: receipts = %v, want 3 entries", receipts3)
	}
}

func TestCompactListFeedCursor(t *testing.T) {
	items := readToolsBuildItems(60, 1)
	value := map[string]any{"posts": items, "has_more": false}
	cache := OpenCache(t.TempDir(), "test-profile")

	out, receipts := compactList("ajent_feed", map[string]any{}, value, cache, readToolsTestScope)

	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
	if out["has_more"] != true {
		t.Errorf("has_more = %v, want true", out["has_more"])
	}
	included, ok := out["posts"].([]any)
	if !ok {
		t.Fatalf("posts = %#v, want []any", out["posts"])
	}
	if len(included) == 0 || len(included) >= len(items) {
		t.Fatalf("included %d of %d items, want a partial prefix", len(included), len(items))
	}
	if len(receipts) != len(included) {
		t.Errorf("receipts = %d, want %d (one per included item)", len(receipts), len(included))
	}

	last, ok := included[len(included)-1].(map[string]any)
	if !ok {
		t.Fatalf("last included item is not a map: %#v", included[len(included)-1])
	}
	wantCursor := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%v|%v", last["created_at"], last["id"])))
	if out["next_cursor"] != wantCursor {
		t.Errorf("next_cursor = %v, want %v", out["next_cursor"], wantCursor)
	}
}

func TestCompactListInboxAcknowledgeThrough(t *testing.T) {
	t.Run("from included items", func(t *testing.T) {
		items := readToolsBuildItems(60, 100)
		value := map[string]any{"items": items, "acknowledged_through": float64(0)}
		cache := OpenCache(t.TempDir(), "test-profile")

		out, _ := compactList("ajent_inbox", map[string]any{}, value, cache, readToolsTestScope)

		if out["truncated"] != true {
			t.Fatalf("truncated = %v, want true", out["truncated"])
		}
		included, ok := out["items"].([]any)
		if !ok || len(included) == 0 {
			t.Fatalf("items = %#v, want a non-empty partial prefix", out["items"])
		}
		last, ok := included[len(included)-1].(map[string]any)
		if !ok {
			t.Fatalf("last included item is not a map: %#v", included[len(included)-1])
		}
		if out["acknowledge_through"] != last["seq"] {
			t.Errorf("acknowledge_through = %v, want %v (seq of the last included item)", out["acknowledge_through"], last["seq"])
		}
	})

	t.Run("falls back to acknowledged_through when no item fits", func(t *testing.T) {
		value := map[string]any{
			"items":                readToolsBuildItems(5, 1),
			"acknowledged_through": float64(42),
			"filler":               strings.Repeat("z", listBudget+300),
		}
		cache := OpenCache(t.TempDir(), "test-profile")

		out, receipts := compactList("ajent_inbox", map[string]any{}, value, cache, readToolsTestScope)

		if out["truncated"] != true {
			t.Fatalf("truncated = %v, want true", out["truncated"])
		}
		included, ok := out["items"].([]any)
		if !ok || len(included) != 0 {
			t.Fatalf("items = %#v, want an empty slice (the filler field alone exceeds the budget)", out["items"])
		}
		if len(receipts) != 0 {
			t.Errorf("receipts = %v, want none", receipts)
		}
		if out["acknowledge_through"] != float64(42) {
			t.Errorf("acknowledge_through = %v, want 42", out["acknowledge_through"])
		}
	})

	t.Run("falls back to zero when acknowledged_through is absent", func(t *testing.T) {
		value := map[string]any{
			"items":  readToolsBuildItems(5, 1),
			"filler": strings.Repeat("z", listBudget+300),
		}
		cache := OpenCache(t.TempDir(), "test-profile")

		out, _ := compactList("ajent_inbox", map[string]any{}, value, cache, readToolsTestScope)

		if out["acknowledge_through"] != 0 {
			t.Errorf("acknowledge_through = %v, want 0", out["acknowledge_through"])
		}
	})
}

func TestCompactReadFragments(t *testing.T) {
	value := readToolsLoadFixture(t, "post_large.json")
	wantText := envelopeEncode(value)
	cache := OpenCache(t.TempDir(), "test-profile")

	var content strings.Builder
	offset := 0
	fragments := 0
	for {
		args := map[string]any{"id": "post-large-test", "offset": float64(offset)}
		frag, receipts := compactRead("ajent_read", args, value, cache, readToolsTestScope)
		cache.Remember(receipts...)
		fragments++

		if frag["format"] != "json_fragment" {
			t.Fatalf("offset %d: format = %v, want json_fragment", offset, frag["format"])
		}
		part, ok := frag["content"].(string)
		if !ok {
			t.Fatalf("offset %d: content = %#v, want a string", offset, frag["content"])
		}
		content.WriteString(part)

		// docs/contract.md section 11: each fragment carries 1800
		// characters (runes), except the last.
		total := len([]rune(wantText))
		wantRunes := min(1800, total-offset)
		if got := len([]rune(part)); got != wantRunes {
			t.Fatalf("offset %d: fragment has %d runes, want %d", offset, got, wantRunes)
		}
		if frag["total_chars"] != total {
			t.Fatalf("offset %d: total_chars = %#v, want %d", offset, frag["total_chars"], total)
		}

		next := frag["next_offset"]
		if next == nil {
			break
		}
		nextOffset, ok := next.(int)
		if !ok {
			t.Fatalf("offset %d: next_offset = %#v, want an int", offset, next)
		}
		if nextOffset <= offset {
			t.Fatalf("offset %d: next_offset = %d, want forward progress", offset, nextOffset)
		}
		offset = nextOffset

		if fragments > 1000 {
			t.Fatal("did not terminate after 1000 fragments")
		}
	}

	if fragments < 2 {
		t.Fatalf("fragments = %d, want more than 1 for a ~30KB post", fragments)
	}
	if content.String() != wantText {
		t.Errorf("concatenated content length = %d, want %d (original JSON text)", len(content.String()), len(wantText))
	}
}

func TestCompactReadUnchanged(t *testing.T) {
	cache := OpenCache(t.TempDir(), "test-profile")
	value := map[string]any{
		"id":    "11111111-1111-4111-8111-111111111111",
		"kind":  "finding",
		"title": "Small post",
		"body":  "Small body, well under the read budget.",
	}
	args := map[string]any{"id": "11111111-1111-4111-8111-111111111111"}

	first, receipts := compactRead("ajent_read", args, value, cache, readToolsTestScope)
	if _, unchanged := first["unchanged"]; unchanged {
		t.Fatalf("first read reported unchanged: %#v", first)
	}
	if len(receipts) != 1 {
		t.Fatalf("first read receipts = %v, want exactly one key", receipts)
	}
	cache.Remember(receipts...)

	second, receipts2 := compactRead("ajent_read", args, value, cache, readToolsTestScope)
	if second["unchanged"] != true {
		t.Errorf("second read unchanged = %v, want true", second["unchanged"])
	}
	wantNotice := "Already delivered. Use repeat=true to reread."
	if second["notice"] != wantNotice {
		t.Errorf("second read notice = %v, want %q", second["notice"], wantNotice)
	}
	if second["id"] != args["id"] {
		t.Errorf("second read id = %v, want %v", second["id"], args["id"])
	}
	if len(receipts2) != 0 {
		t.Errorf("second read receipts = %v, want none", receipts2)
	}

	repeatArgs := map[string]any{"id": args["id"], "repeat": true}
	third, receipts3 := compactRead("ajent_read", repeatArgs, value, cache, readToolsTestScope)
	if _, unchanged := third["unchanged"]; unchanged {
		t.Errorf("repeat=true read reported unchanged: %#v", third)
	}
	if len(receipts3) != 1 {
		t.Errorf("repeat=true read receipts = %v, want exactly one key", receipts3)
	}
}
