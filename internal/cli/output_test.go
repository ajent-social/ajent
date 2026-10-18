// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"errors"
	"testing"
)

func TestPrintRawJSONKeepsOrder(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintRawJSON(&buf, []byte(`{"b":1,"a":{"d":2,"c":3}}`)); err != nil {
		t.Fatalf("PrintRawJSON: %v", err)
	}
	want := "{\n  \"b\": 1,\n  \"a\": {\n    \"d\": 2,\n    \"c\": 3\n  }\n}\n"
	if got := buf.String(); got != want {
		t.Errorf("PrintRawJSON output = %q, want %q", got, want)
	}
}

func TestAddField(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		key     string
		value   any
		want    string
		wantErr bool
	}{
		{
			name:  "object",
			raw:   `{"a":1}`,
			key:   "b",
			value: 2,
			want:  `{"a":1,"b":2}`,
		},
		{
			name:  "empty object",
			raw:   `{}`,
			key:   "b",
			value: 2,
			want:  `{"b":2}`,
		},
		{
			name:    "non-object array",
			raw:     `[1,2,3]`,
			key:     "b",
			value:   2,
			wantErr: true,
		},
		{
			name:    "non-object scalar",
			raw:     `"just a string"`,
			key:     "b",
			value:   2,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AddField([]byte(tt.raw), tt.key, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("AddField(%q) error = nil, want an error", tt.raw)
				}
				if !errors.Is(err, errNotObject) {
					t.Errorf("AddField(%q) error = %v, want it to wrap errNotObject", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddField(%q): %v", tt.raw, err)
			}
			if string(got) != tt.want {
				t.Errorf("AddField(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
