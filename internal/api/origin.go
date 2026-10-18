// SPDX-License-Identifier: Apache-2.0

// Package api sends requests to the hosted Ajent service with
// Python-compatible headers, the retry-safety allowlist and backoff, typed
// errors, redirect refusal and origin pinning (docs/contract.md sections 6
// to 8, ADR 006).
package api

import (
	"errors"
	"net/url"
	"os"
)

// DefaultOrigin is the hosted service origin used when AJENT_URL is unset.
const DefaultOrigin = "https://ajent.social"

// errInvalidOrigin is returned when AJENT_URL cannot be trusted.
var errInvalidOrigin = errors.New("api: AJENT_URL must be a loopback URL or https with AJENT_ALLOW_CUSTOM_SERVER=1")

// Origin returns the configured origin: $AJENT_URL when it is loopback
// http(s), or any https origin when AJENT_ALLOW_CUSTOM_SERVER=1, else
// DefaultOrigin. It never reads project files. The result is the
// normalized form scheme://host[:port] with no trailing slash.
func Origin() (string, error) {
	raw := os.Getenv("AJENT_URL")
	if raw == "" {
		return DefaultOrigin, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", errInvalidOrigin
	}
	if u.User != nil {
		return "", errInvalidOrigin
	}
	if u.Path != "" && u.Path != "/" {
		return "", errInvalidOrigin
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errInvalidOrigin
	}

	loopback := isLoopbackHost(u.Hostname())

	switch {
	case (u.Scheme == "http" || u.Scheme == "https") && loopback:
	case u.Scheme == "https" && os.Getenv("AJENT_ALLOW_CUSTOM_SERVER") == "1":
	default:
		return "", errInvalidOrigin
	}

	return u.Scheme + "://" + u.Host, nil
}

// Trusted reports whether server exactly equals the configured origin
// (scheme+host+port, no path, no userinfo).
func Trusted(server string) bool {
	o, err := Origin()
	return err == nil && server == o
}

// isLoopbackHost reports whether host is one of the loopback hostnames
// accepted for plain http (a Go addition for tests and development;
// docs/contract.md section 6).
func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
