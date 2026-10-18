// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeBudgetSmallValueIntact(t *testing.T) {
	text, ok := Envelope("ajent_me", map[string]any{"hello": "world"})
	if !ok {
		t.Fatalf("Envelope returned ok=false for a small value")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("Envelope text is not valid JSON: %v", err)
	}
	if decoded["trust"] != "untrusted_reference_data" {
		t.Errorf("trust = %v, want untrusted_reference_data", decoded["trust"])
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok || data["hello"] != "world" {
		t.Errorf("data = %v, want {hello: world}", decoded["data"])
	}
	if _, has := decoded["authority_model"]; has {
		t.Errorf("non-goal tool envelope carries authority_model")
	}
}

func TestEnvelopeBudgetExceeded(t *testing.T) {
	big := strings.Repeat("a", MaxResultBytes*2)
	text, ok := Envelope("ajent_search", map[string]any{"padding": big})
	if ok {
		t.Fatalf("Envelope returned ok=true for an oversized value")
	}
	if len(text) > MaxResultBytes {
		t.Errorf("omission envelope itself is %d bytes, want <= %d", len(text), MaxResultBytes)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("Envelope text is not valid JSON: %v", err)
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %v", decoded["data"])
	}
	if data["response_omitted"] != true {
		t.Errorf("response_omitted = %v, want true", data["response_omitted"])
	}
	if data["reason"] != "response_budget" {
		t.Errorf("reason = %v, want response_budget", data["reason"])
	}
	if data["operation_completed"] != true {
		t.Errorf("operation_completed = %v, want true", data["operation_completed"])
	}
	wantNotice := "Result exceeded 12000 bytes. Use a narrower query or explicit post read."
	if data["notice"] != wantNotice {
		t.Errorf("notice = %v, want %q", data["notice"], wantNotice)
	}
}

func TestEnvelopeGoalToolCarriesAuthorityModel(t *testing.T) {
	for _, name := range []string{"ajent_goal", "ajent_goals", "ajent_goal_update", "ajent_goal_track"} {
		text, ok := Envelope(name, map[string]any{"x": 1})
		if !ok {
			t.Fatalf("%s: Envelope returned ok=false", name)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			t.Fatalf("%s: not valid JSON: %v", name, err)
		}
		am, _ := decoded["authority_model"].(string)
		if am == "" {
			t.Errorf("%s: authority_model missing or empty", name)
		}
	}
	// A non-goal tool must never carry it.
	text, ok := Envelope("ajent_read", map[string]any{"x": 1})
	if !ok {
		t.Fatalf("Envelope returned ok=false")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, has := decoded["authority_model"]; has {
		t.Errorf("ajent_read envelope carries authority_model, want absent")
	}
}
