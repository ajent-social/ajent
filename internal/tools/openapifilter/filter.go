// SPDX-License-Identifier: Apache-2.0

// This file implements the OpenAPI filtering used by the openapifilter
// command (main.go): it trims a public OpenAPI 3.0 description down to the
// agent-facing routes the ajent client calls. It keeps the transform
// generic (a decoded map[string]any) rather than a typed OpenAPI model,
// since it only needs to select operations, scrub a few extension keys and
// prune unreferenced components.
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Route matches one entry of testdata/contract/routes.json: an HTTP method
// and an anchored regular expression over the request path.
type Route struct {
	Method  string
	Pattern string
}

// openAPIFilterSampleID replaces every "{name}" path template segment when
// building a sample path to match against a Route's Pattern.
const openAPIFilterSampleID = "00000000-0000-4000-8000-000000000000"

// openAPIFilterHTTPMethods lists the OpenAPI path-item keys that are
// operations rather than shared metadata (parameters, summary, ...).
var openAPIFilterHTTPMethods = map[string]bool{
	"get":     true,
	"put":     true,
	"post":    true,
	"delete":  true,
	"options": true,
	"head":    true,
	"patch":   true,
	"trace":   true,
}

// openAPIFilterExtensionKeys names the keys removed at any depth: operator
// and abuse-control details that never belong in the public description.
var openAPIFilterExtensionKeys = map[string]bool{
	"x-ratelimit":  true,
	"x-rate-limit": true,
	"x-abuse":      true,
}

// openAPIFilterLimitText matches a number immediately followed by one of
// "requests", "per minute", "per hour" or "per day", the signal that a
// description or summary states an operational limit.
var openAPIFilterLimitText = regexp.MustCompile(`[0-9]+\s*(requests|per minute|per hour|per day)`)

// openAPIFilterPathTemplate matches a single "{name}" path template
// segment.
var openAPIFilterPathTemplate = regexp.MustCompile(`\{[^{}]*\}`)

// Filter decodes src as an OpenAPI 3.0 document, keeps only the operations
// that some entry of routes also matches, pins servers to
// https://ajent.social, scrubs rate-limit extensions and text, prunes
// unreferenced components and returns the result marshaled with two-space
// indentation and a trailing newline.
func Filter(src []byte, routes []Route) ([]byte, error) {
	var spec map[string]any
	if err := json.Unmarshal(src, &spec); err != nil {
		return nil, fmt.Errorf("openapifilter: decode source: %w", err)
	}

	compiled, err := openAPIFilterCompileRoutes(routes)
	if err != nil {
		return nil, err
	}

	if err := openAPIFilterKeepMatchingPaths(spec, compiled); err != nil {
		return nil, err
	}

	spec["servers"] = []any{map[string]any{"url": "https://ajent.social"}}

	openAPIFilterScrub(spec)

	openAPIFilterPruneComponents(spec)

	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapifilter: encode result: %w", err)
	}
	out = append(out, '\n')
	return out, nil
}

// openAPIFilterCompiledRoute is a Route with its pattern already compiled.
type openAPIFilterCompiledRoute struct {
	method string
	regex  *regexp.Regexp
}

func openAPIFilterCompileRoutes(routes []Route) ([]openAPIFilterCompiledRoute, error) {
	compiled := make([]openAPIFilterCompiledRoute, 0, len(routes))
	for _, r := range routes {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("openapifilter: compile route pattern %q: %w", r.Pattern, err)
		}
		compiled = append(compiled, openAPIFilterCompiledRoute{method: strings.ToUpper(r.Method), regex: re})
	}
	return compiled, nil
}

// openAPIFilterSamplePath builds the sample path used to test a route
// pattern against an OpenAPI path template, replacing every "{name}"
// segment with a fixed placeholder UUID.
func openAPIFilterSamplePath(path string) string {
	return openAPIFilterPathTemplate.ReplaceAllString(path, openAPIFilterSampleID)
}

// openAPIFilterMatchesAny reports whether some compiled route matches
// method (case-insensitively) and the sample path.
func openAPIFilterMatchesAny(compiled []openAPIFilterCompiledRoute, method, sample string) bool {
	method = strings.ToUpper(method)
	for _, r := range compiled {
		if r.method == method && r.regex.MatchString(sample) {
			return true
		}
	}
	return false
}

// openAPIFilterKeepMatchingPaths drops every operation under spec["paths"]
// that no compiled route matches, then drops any path left with no
// operations.
func openAPIFilterKeepMatchingPaths(spec map[string]any, compiled []openAPIFilterCompiledRoute) error {
	pathsRaw, ok := spec["paths"]
	if !ok {
		return nil
	}
	paths, ok := pathsRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("openapifilter: %q is not an object", "paths")
	}

	for path, itemRaw := range paths {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			delete(paths, path)
			continue
		}
		sample := openAPIFilterSamplePath(path)
		for method := range item {
			if !openAPIFilterHTTPMethods[strings.ToLower(method)] {
				continue
			}
			if !openAPIFilterMatchesAny(compiled, method, sample) {
				delete(item, method)
			}
		}
		if !openAPIFilterHasOperation(item) {
			delete(paths, path)
		}
	}
	return nil
}

func openAPIFilterHasOperation(item map[string]any) bool {
	for method := range item {
		if openAPIFilterHTTPMethods[strings.ToLower(method)] {
			return true
		}
	}
	return false
}

// openAPIFilterScrub walks v, removing any key named in
// openAPIFilterExtensionKeys and blanking any "description" or "summary"
// string value that states a numeric request limit.
func openAPIFilterScrub(v any) {
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			if openAPIFilterExtensionKeys[k] {
				delete(val, k)
				continue
			}
			if k == "description" || k == "summary" {
				if s, ok := sub.(string); ok && openAPIFilterLimitText.MatchString(s) {
					val[k] = ""
					continue
				}
			}
			openAPIFilterScrub(sub)
		}
	case []any:
		for _, sub := range val {
			openAPIFilterScrub(sub)
		}
	}
}

// openAPIFilterPruneComponents keeps only the members of spec["components"]
// that the remaining spec["paths"] reference, directly or transitively
// through "$ref" strings.
func openAPIFilterPruneComponents(spec map[string]any) {
	componentsRaw, ok := spec["components"]
	if !ok {
		return
	}
	components, ok := componentsRaw.(map[string]any)
	if !ok {
		return
	}

	kept := make(map[string]map[string]bool)
	queue := openAPIFilterCollectRefs(spec["paths"])
	queue = append(queue, openAPIFilterCollectSecurity(spec)...)
	visited := make(map[string]bool)

	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if visited[ref] {
			continue
		}
		visited[ref] = true

		kind, name, ok := openAPIFilterParseComponentRef(ref)
		if !ok {
			continue
		}
		if kept[kind] == nil {
			kept[kind] = make(map[string]bool)
		}
		kept[kind][name] = true

		kindMap, ok := components[kind].(map[string]any)
		if !ok {
			continue
		}
		member, ok := kindMap[name]
		if !ok {
			continue
		}
		queue = append(queue, openAPIFilterCollectRefs(member)...)
	}

	for kind, memberRaw := range components {
		memberMap, ok := memberRaw.(map[string]any)
		if !ok {
			continue
		}
		for name := range memberMap {
			if !kept[kind][name] {
				delete(memberMap, name)
			}
		}
		if len(memberMap) == 0 {
			delete(components, kind)
		}
	}
	if len(components) == 0 {
		delete(spec, "components")
	}
}

// openAPIFilterCollectRefs returns every "$ref" string found anywhere
// within v.
func openAPIFilterCollectRefs(v any) []string {
	var refs []string
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			if k == "$ref" {
				if s, ok := sub.(string); ok {
					refs = append(refs, s)
				}
			}
			refs = append(refs, openAPIFilterCollectRefs(sub)...)
		}
	case []any:
		for _, sub := range val {
			refs = append(refs, openAPIFilterCollectRefs(sub)...)
		}
	}
	return refs
}

// openAPIFilterCollectSecurity returns a securitySchemes reference for every
// scheme named in a security requirement, at the top level or on a kept
// operation. Security requirements name schemes directly, not by "$ref".
func openAPIFilterCollectSecurity(spec map[string]any) []string {
	var refs []string
	addReqs := func(v any) {
		reqs, ok := v.([]any)
		if !ok {
			return
		}
		for _, r := range reqs {
			req, ok := r.(map[string]any)
			if !ok {
				continue
			}
			for name := range req {
				refs = append(refs, "#/components/securitySchemes/"+name)
			}
		}
	}
	addReqs(spec["security"])
	paths, _ := spec["paths"].(map[string]any)
	for _, itemRaw := range paths {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		for _, opRaw := range item {
			if op, ok := opRaw.(map[string]any); ok {
				addReqs(op["security"])
			}
		}
	}
	return refs
}

// openAPIFilterParseComponentRef splits a local "#/components/<kind>/<name>"
// reference into its kind and name.
func openAPIFilterParseComponentRef(ref string) (kind, name string, ok bool) {
	const prefix = "#/components/"
	if !strings.HasPrefix(ref, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
