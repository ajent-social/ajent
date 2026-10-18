// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// errorsFixture mirrors the parts of testdata/contract/errors.json this
// file cross-checks the hard-coded table against.
type errorsFixture struct {
	Statuses []struct {
		Status json.RawMessage `json:"status"`
		Code   string          `json:"code"`
		Action string          `json:"action"`
	} `json:"statuses"`
}

func loadErrorsFixture(t *testing.T) errorsFixture {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "contract", "errors.json")
	//nolint:gosec // path is fixed and points at the module's own
	// testdata/contract, not external input.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var f errorsFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("Unmarshal(errors.json): %v", err)
	}
	return f
}

func TestErrorTable(t *testing.T) {
	fixture := loadErrorsFixture(t)

	for _, row := range fixture.Statuses {
		var raw string
		if err := json.Unmarshal(row.Status, &raw); err == nil {
			// "5xx" or "other": exercised separately below.
			continue
		}

		var status int
		if err := json.Unmarshal(row.Status, &status); err != nil {
			t.Fatalf("status %s: %v", row.Status, err)
		}

		t.Run(strconv.Itoa(status), func(t *testing.T) {
			e := NewError(status, "", nil)
			if e.Code != row.Code {
				t.Errorf("NewError(%d).Code = %q, want %q", status, e.Code, row.Code)
			}
			if e.Action != row.Action {
				t.Errorf("NewError(%d).Action = %q, want %q", status, e.Action, row.Action)
			}
			wantText := "ajent: " + row.Code + ". " + row.Action
			if got := e.Error(); got != wantText {
				t.Errorf("NewError(%d).Error() = %q, want %q", status, got, wantText)
			}
			if e.Status != status {
				t.Errorf("NewError(%d).Status = %d, want %d", status, e.Status, status)
			}
		})
	}

	// The "5xx" row: any status >= 500 not otherwise in the table.
	find := func(code string) (string, bool) {
		for _, row := range fixture.Statuses {
			var raw string
			if err := json.Unmarshal(row.Status, &raw); err == nil && raw == code {
				return row.Action, true
			}
		}
		return "", false
	}

	t.Run("5xx", func(t *testing.T) {
		action, ok := find("5xx")
		if !ok {
			t.Fatal("errors.json missing 5xx row")
		}
		for _, status := range []int{500, 502, 503, 520, 599} {
			e := NewError(status, "", nil)
			if e.Code != "service_unavailable" {
				t.Errorf("NewError(%d).Code = %q, want service_unavailable", status, e.Code)
			}
			if e.Action != action {
				t.Errorf("NewError(%d).Action = %q, want %q", status, e.Action, action)
			}
		}
	})

	t.Run("other", func(t *testing.T) {
		action, ok := find("other")
		if !ok {
			t.Fatal("errors.json missing other row")
		}
		for _, status := range []int{301, 302, 400, 405, 451} {
			e := NewError(status, "", nil)
			if e.Code != "invalid_request" {
				t.Errorf("NewError(%d).Code = %q, want invalid_request", status, e.Code)
			}
			if e.Action != action {
				t.Errorf("NewError(%d).Action = %q, want %q", status, e.Action, action)
			}
		}
	})
}

func TestRequestIDFilter(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{"valid lowercase uuid", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000001"},
		{"valid 36 hex-and-dash chars", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"empty", "", ""},
		{"uppercase rejected", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", ""},
		{"wrong length rejected", "00000000-0000-4000-8000-00000000000", ""},
		{"non-hex characters rejected", "gggggggg-0000-4000-8000-000000000001", ""},
		{"free text rejected", "not-a-request-id", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewError(500, tt.id, nil)
			if e.RequestID != tt.want {
				t.Errorf("NewError(500, %q, nil).RequestID = %q, want %q", tt.id, e.RequestID, tt.want)
			}
		})
	}
}

func TestNewErrorRetryAfter(t *testing.T) {
	if got := NewError(429, "", nil).RetryAfter; got != nil {
		t.Errorf("RetryAfter = %v, want nil", got)
	}
	v := 5
	got := NewError(429, "", &v)
	if got.RetryAfter == nil || *got.RetryAfter != 5 {
		t.Errorf("RetryAfter = %v, want pointer to 5", got.RetryAfter)
	}
	// The returned pointer must not alias the caller's.
	v = 6
	if *got.RetryAfter != 5 {
		t.Errorf("NewError aliased the caller's RetryAfter pointer")
	}
}

func TestCanonicalUUID(t *testing.T) {
	const canonical = "12345678-90ab-cdef-1234-567890abcdef"

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"canonical lowercase", "12345678-90ab-cdef-1234-567890abcdef", canonical, false},
		{"canonical uppercase", "12345678-90AB-CDEF-1234-567890ABCDEF", canonical, false},
		{"bare hex lowercase", "1234567890abcdef1234567890abcdef", canonical, false},
		{"bare hex uppercase", "1234567890ABCDEF1234567890ABCDEF", canonical, false},
		{"braced canonical", "{12345678-90ab-cdef-1234-567890abcdef}", canonical, false},
		{"braced bare hex", "{1234567890abcdef1234567890abcdef}", canonical, false},
		{"urn prefix", "urn:uuid:12345678-90ab-cdef-1234-567890abcdef", canonical, false},
		{"urn prefix uppercase", "URN:UUID:12345678-90AB-CDEF-1234-567890ABCDEF", canonical, false},
		{"too short", "12345678-90ab-cdef-1234-567890abcde", "", true},
		{"not hex", "zzzzzzzz-90ab-cdef-1234-567890abcdef", "", true},
		{"empty", "", "", true},
		{"garbage", "not-a-uuid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalUUID(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CanonicalUUID(%q) = %q, nil; want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalUUID(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("CanonicalUUID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
