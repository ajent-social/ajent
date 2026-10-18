// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"testing"

	"github.com/ajent-social/ajent-client/internal/api"
	"github.com/ajent-social/ajent-client/internal/credentials"
	"github.com/ajent-social/ajent-client/internal/testutil/fakeserver"
)

func TestProfileFlagsDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	profile := profileFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse(nil): %v", err)
	}
	if *profile != "terminal" {
		t.Errorf("profile default = %q, want %q", *profile, "terminal")
	}

	fs2 := flag.NewFlagSet("test", flag.ContinueOnError)
	profile2 := profileFlags(fs2)
	if err := fs2.Parse([]string{"--profile", "claude-code", "--project-profile"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *profile2 != "claude-code" {
		t.Errorf("profile = %q, want %q", *profile2, "claude-code")
	}
}

func TestToolCredentialHashesProfile(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _, profiles := newTestEnv(t, srv)

	cred, err := toolCredential(context.Background(), env, "terminal")
	if err != nil {
		t.Fatalf("toolCredential: %v", err)
	}
	if cred.Server != srv.URL {
		t.Errorf("cred.Server = %q, want %q", cred.Server, srv.URL)
	}

	root := credentials.ProjectRoot(env.Cwd, env.Home)
	want := credentials.ProjectProfile("terminal", root)
	got := profiles()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("resolved profiles = %v, want [%q]", got, want)
	}

	t.Run("invalid base yields ErrInvalidProfile", func(t *testing.T) {
		_, err := toolCredential(context.Background(), env, "Not Valid!")
		if !errors.Is(err, credentials.ErrInvalidProfile) {
			t.Fatalf("err = %v, want ErrInvalidProfile", err)
		}
	})
}

func TestRootCredential(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _, _ := newTestEnv(t, srv)

	if _, err := rootCredential(env); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("rootCredential() err = %v, want ErrNoCredential", err)
	}

	want := srv.Credential()
	writeRoot(t, env, want)

	got, err := rootCredential(env)
	if err != nil {
		t.Fatalf("rootCredential: %v", err)
	}
	if got.Server != want.Server || got.Token != want.Token {
		t.Errorf("rootCredential() = %+v, want %+v", got, want)
	}
}

func TestGetAndPrint(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, _, _ := newTestEnv(t, srv)
	srv.Handle(http.MethodGet, "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"handle":"agent-1"}`))
	})

	if err := getAndPrint(context.Background(), env, srv.Credential(), "/v1/me"); err != nil {
		t.Fatalf("getAndPrint: %v", err)
	}
	want := "{\n  \"handle\": \"agent-1\"\n}\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestSendAndPrint(t *testing.T) {
	srv := fakeserver.New(t)
	env, stdout, _, _ := newTestEnv(t, srv)
	srv.Handle(http.MethodPost, "/v1/posts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"p1"}`))
	})

	req := api.Request{Method: "POST", Route: "/v1/posts", Key: "k1"}
	if err := sendAndPrint(context.Background(), env, srv.Credential(), req); err != nil {
		t.Fatalf("sendAndPrint: %v", err)
	}
	want := "{\n  \"id\": \"p1\"\n}\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestJSONArgs(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[string]any
		wantErr bool
	}{
		{
			name: "object",
			in:   `{"a":1,"b":"x"}`,
			want: map[string]any{"a": json.Number("1"), "b": "x"},
		},
		{
			name:    "array",
			in:      `[1,2]`,
			wantErr: true,
		},
		{
			name:    "not json",
			in:      `not json`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jsonArgs(tt.in)
			if tt.wantErr {
				if !errors.Is(err, errJSONArgsNotObject) {
					t.Fatalf("jsonArgs(%q) err = %v, want errJSONArgsNotObject", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("jsonArgs(%q): %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("jsonArgs(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("jsonArgs(%q)[%q] = %v, want %v", tt.in, k, got[k], v)
				}
			}
		})
	}
}
