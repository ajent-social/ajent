// SPDX-License-Identifier: Apache-2.0

// Package version holds build metadata injected at link time and the
// runtime information derived from it.
package version

import "runtime"

// Version, Commit and Date are set at build time with -ldflags, for
// example:
//
//	-X github.com/ajent-social/ajent/internal/version.Version=1.2.3
//
// Their zero values identify a development build.
var (
	// Version is the client's semantic version, or "dev" when unset.
	Version = "dev"
	// Commit is the short VCS commit hash the binary was built from, or
	// "none" when unset.
	Commit = "none"
	// Date is the UTC build timestamp, or "unknown" when unset.
	Date = "unknown"
)

// ClientKind identifies this client implementation to the server.
const ClientKind = "go-cli"

// Info is the JSON-serializable snapshot of build and runtime metadata
// returned by Get.
type Info struct {
	// Version is the client's semantic version.
	Version string `json:"version"`
	// Commit is the short VCS commit hash the binary was built from.
	Commit string `json:"commit"`
	// Date is the UTC build timestamp.
	Date string `json:"date"`
	// ClientKind identifies this client implementation to the server.
	ClientKind string `json:"client_kind"`
	// GoVersion is the Go runtime version that built the binary.
	GoVersion string `json:"go_version"`
	// OS is the target operating system, from runtime.GOOS.
	OS string `json:"os"`
	// Arch is the target architecture, from runtime.GOARCH.
	Arch string `json:"arch"`
}

// Get returns the current build and runtime metadata.
func Get() Info {
	return Info{
		Version:    Version,
		Commit:     Commit,
		Date:       Date,
		ClientKind: ClientKind,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
	}
}

// UserAgent returns the HTTP User-Agent string this client sends,
// formatted as "ajent/<Version> (<GOOS>/<GOARCH>)".
func UserAgent() string {
	return "ajent/" + Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
