// SPDX-License-Identifier: Apache-2.0

package api

import "testing"

func TestOriginEnv(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		allow   string
		want    string
		wantErr bool
	}{
		{name: "unset uses default", url: "", want: DefaultOrigin},
		{name: "loopback http localhost accepted", url: "http://localhost:9999", want: "http://localhost:9999"},
		{name: "loopback http 127.0.0.1 accepted", url: "http://127.0.0.1:9999", want: "http://127.0.0.1:9999"},
		{name: "loopback http ipv6 accepted", url: "http://[::1]:9999", want: "http://[::1]:9999"},
		{name: "loopback https accepted", url: "https://localhost:9999", want: "https://localhost:9999"},
		{name: "non-loopback http rejected", url: "http://example.com", wantErr: true},
		{name: "non-loopback https rejected without flag", url: "https://example.com", wantErr: true},
		{name: "non-loopback https accepted with flag", url: "https://example.com", allow: "1", want: "https://example.com"},
		{name: "path rejected even with flag", url: "https://example.com/foo", allow: "1", wantErr: true},
		{name: "root path accepted with flag", url: "https://example.com/", allow: "1", want: "https://example.com"},
		{name: "userinfo rejected", url: "https://user@example.com", allow: "1", wantErr: true},
		{name: "query rejected", url: "https://example.com?x=1", allow: "1", wantErr: true},
		{name: "fragment rejected", url: "https://example.com#frag", allow: "1", wantErr: true},
		{name: "unparseable rejected", url: "https://example.com/%zz", allow: "1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AJENT_URL", tt.url)
			t.Setenv("AJENT_ALLOW_CUSTOM_SERVER", tt.allow)

			got, err := Origin()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Origin() = %q, nil; want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Origin() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Origin() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrusted(t *testing.T) {
	t.Setenv("AJENT_URL", "http://127.0.0.1:9999")
	t.Setenv("AJENT_ALLOW_CUSTOM_SERVER", "")

	if !Trusted("http://127.0.0.1:9999") {
		t.Errorf("Trusted(matching origin) = false, want true")
	}
	if Trusted("http://127.0.0.1:9999/") {
		t.Errorf("Trusted(trailing slash) = true, want false")
	}
	if Trusted("https://evil.example") {
		t.Errorf("Trusted(other origin) = true, want false")
	}
	if Trusted("HTTP://127.0.0.1:9999") {
		t.Errorf("Trusted(different case) = true, want false")
	}
}
