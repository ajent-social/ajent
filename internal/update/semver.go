// SPDX-License-Identifier: Apache-2.0

package update

import (
	"strconv"
	"strings"
)

// semver is a parsed MAJOR.MINOR.PATCH[-PRERELEASE] version. Build metadata
// (a trailing "+...") is discarded during parsing and never affects
// precedence.
type semver struct {
	major, minor, patch int
	// pre holds the dot-separated prerelease identifiers, or nil when the
	// version has no prerelease component.
	pre []string
}

// parseSemver parses v, stripping one leading "v" and any build metadata
// after "+". It reports false when v does not match
// MAJOR.MINOR.PATCH[-PRERELEASE] with non-negative, non-zero-padded core
// parts and non-empty dot-separated prerelease identifiers.
func parseSemver(v string) (semver, bool) {
	v = strings.TrimPrefix(v, "v")

	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}

	core := v
	var pre []string
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core = v[:i]
		rest := v[i+1:]
		if rest == "" {
			return semver{}, false
		}
		pre = strings.Split(rest, ".")
		for _, id := range pre {
			if id == "" {
				return semver{}, false
			}
		}
	}

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}

	nums := make([]int, 3)
	for i, p := range parts {
		n, ok := parseCorePart(p)
		if !ok {
			return semver{}, false
		}
		nums[i] = n
	}

	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, true
}

// parseCorePart parses one MAJOR, MINOR or PATCH component: a non-negative
// decimal integer without leading zeros, except the literal "0".
func parseCorePart(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Newer reports whether a is a strictly newer semantic version than b.
// It returns false when either a or b fails to parse.
func Newer(a, b string) bool {
	av, aok := parseSemver(a)
	bv, bok := parseSemver(b)
	if !aok || !bok {
		return false
	}

	if av.major != bv.major {
		return av.major > bv.major
	}
	if av.minor != bv.minor {
		return av.minor > bv.minor
	}
	if av.patch != bv.patch {
		return av.patch > bv.patch
	}

	aHasPre := av.pre != nil
	bHasPre := bv.pre != nil
	if aHasPre != bHasPre {
		// A version without a prerelease outranks one with a prerelease.
		return !aHasPre
	}
	if !aHasPre {
		return false // equal
	}

	return comparePrerelease(av.pre, bv.pre) > 0
}

// Valid reports whether v parses as MAJOR.MINOR.PATCH[-PRERELEASE], with an
// optional leading "v" and optional build metadata.
func Valid(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// comparePrerelease compares two non-nil prerelease identifier lists per
// semver precedence rules, returning a negative number, zero or a positive
// number as a is less than, equal to or greater than b.
func comparePrerelease(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := compareIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) == len(b):
		return 0
	case len(a) < len(b):
		return -1
	default:
		return 1
	}
}

// compareIdentifier compares two dot-separated prerelease identifiers.
// Numeric identifiers (digits only) compare numerically and always rank
// lower than alphanumeric identifiers, which compare in ASCII order.
func compareIdentifier(a, b string) int {
	an, aNum := numericIdentifier(a)
	bn, bNum := numericIdentifier(b)

	switch {
	case aNum && bNum:
		switch {
		case an == bn:
			return 0
		case an < bn:
			return -1
		default:
			return 1
		}
	case aNum && !bNum:
		return -1
	case !aNum && bNum:
		return 1
	default:
		switch {
		case a == b:
			return 0
		case a < b:
			return -1
		default:
			return 1
		}
	}
}

// numericIdentifier reports whether id consists only of ASCII digits and,
// if so, its integer value.
func numericIdentifier(id string) (int, bool) {
	for _, c := range id {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0, false
	}
	return n, true
}
