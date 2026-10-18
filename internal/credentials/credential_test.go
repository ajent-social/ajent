// SPDX-License-Identifier: Apache-2.0

package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestCredentialRoundTripPreservesUnknown(t *testing.T) {
	input := []byte(`{"server":"https://ajent.social","token":"t","future_field":{"x":1},"zzz":true}`)

	var c Credential
	if err := json.Unmarshal(input, &c); err != nil {
		t.Fatalf("Unmarshal(input): %v", err)
	}

	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal(c): %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("Unmarshal(out): %v", err)
	}

	var want map[string]any
	if err := json.Unmarshal(input, &want); err != nil {
		t.Fatalf("Unmarshal(input) into map: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %#v, want %#v", got, want)
	}
}

func TestCredentialMarshalOrder(t *testing.T) {
	c := Credential{
		Server:           "https://ajent.social",
		Token:            "aj_secretvalue",
		Handle:           "h",
		AgentID:          "a1",
		GroupID:          "g1",
		CredentialID:     "c1",
		Pending:          true,
		HumanLinked:      true,
		HumanLinkPending: true,
		DomainNotice:     "n",
		Extra: map[string]json.RawMessage{
			"zzz":    json.RawMessage(`true`),
			"future": json.RawMessage(`1`),
		},
	}

	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(out)

	order := []string{
		`"server"`,
		`"token"`,
		`"handle"`,
		`"agent_id"`,
		`"group_id"`,
		`"credential_id"`,
		`"pending"`,
		`"human_linked"`,
		`"human_link_pending"`,
		`"domain_notice"`,
		`"future"`,
		`"zzz"`,
	}

	last := -1
	for _, key := range order {
		idx := strings.Index(s, key)
		if idx == -1 {
			t.Fatalf("output %s is missing key %s", s, key)
		}
		if idx < last {
			t.Fatalf("key %s is out of order in %s", key, s)
		}
		last = idx
	}
}

func TestCredentialNeverPrintsToken(t *testing.T) {
	const token = "aj_secretvalue"
	c := Credential{
		Server:  "https://ajent.social",
		Token:   token,
		Handle:  "h",
		AgentID: "a1",
	}

	cases := []struct {
		name string
		out  string
	}{
		{"%v", fmt.Sprintf("%v", c)},
		{"%+v", fmt.Sprintf("%+v", c)},
		{"%#v", fmt.Sprintf("%#v", c)},
		{"%s", fmt.Sprintf("%s", c)},
		{"Sprint", fmt.Sprint(c)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.out, token) {
				t.Errorf("output %q contains the token", tc.out)
			}
		})
	}
}

func TestIdentity(t *testing.T) {
	const token = "aj_0123456789abcdef_AAAA"
	c := Credential{Token: token}

	sum := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(sum[:])[:16]

	if got := c.Identity(); got != want {
		t.Errorf("Identity() = %q, want %q", got, want)
	}

	t.Run("golden", func(t *testing.T) {
		path := filepath.Join("..", "..", "testdata", "contract", "golden.json")
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is a fixed repo-relative testdata literal, not external input.
		if err != nil {
			if os.IsNotExist(err) {
				t.Skip("testdata/contract/golden.json not present yet (lands with T2.1)")
			}
			t.Fatalf("ReadFile(%s): %v", path, err)
		}

		// golden.json's own token differs from the short literal above (it
		// is the fixed fake root token T2.1 generated for the Python
		// reference client), so Identity is recomputed from it rather than
		// reused from c: the check is that our Identity() agrees with the
		// Python reference implementation on the same input, not that the
		// two literals happen to match.
		var golden struct {
			Token    string `json:"token"`
			Identity string `json:"identity"`
		}
		if err := json.Unmarshal(data, &golden); err != nil {
			t.Fatalf("Unmarshal(golden.json): %v", err)
		}

		gc := Credential{Token: golden.Token}
		if got := gc.Identity(); got != golden.Identity {
			t.Errorf("Identity() = %q, want golden %q", got, golden.Identity)
		}
	})
}

func TestNewTokenFormat(t *testing.T) {
	re := regexp.MustCompile(`^aj_[0-9a-f]{16}_[A-Za-z0-9_-]{43}$`)

	a, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if !re.MatchString(a) {
		t.Errorf("NewToken() = %q, want match of %s", a, re.String())
	}

	b, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if !re.MatchString(b) {
		t.Errorf("NewToken() = %q, want match of %s", b, re.String())
	}

	if a == b {
		t.Errorf("NewToken() returned the same value twice: %q", a)
	}
}

func TestValidProfile(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase word", "terminal", true},
		{"lowercase with digits and hyphens", "claude-code-1a2b3c4d", true},
		{"too short", "a", false},
		{"leading hyphen", "-x", false},
		{"uppercase", "Upper", false},
		{"too long", strings.Repeat("a", 41), false},
		{"underscore", "x_y", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidProfile(tc.input); got != tc.want {
				t.Errorf("ValidProfile(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
