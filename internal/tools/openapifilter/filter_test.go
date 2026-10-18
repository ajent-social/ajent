// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// openAPIFilterTestRoutes is the two-route list used against
// testdata/source.json: the two agent paths it contains.
func openAPIFilterTestRoutes() []Route {
	return []Route{
		{Method: "GET", Pattern: "^/v1/me$"},
		{Method: "GET", Pattern: "^/v1/feed$"},
	}
}

func openAPIFilterTestSource(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/source.json")
	if err != nil {
		t.Fatalf("read testdata/source.json: %v", err)
	}
	return data
}

func TestFilterKeepsOnlyAgentRoutes(t *testing.T) {
	out, err := Filter(openAPIFilterTestSource(t), openAPIFilterTestRoutes())
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(out, &spec); err != nil {
		t.Fatalf("Filter output is not valid JSON: %v", err)
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("spec[paths] = %v, want an object", spec["paths"])
	}
	for _, want := range []string{"/v1/me", "/v1/feed"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("paths is missing agent path %q", want)
		}
	}
	if _, ok := paths["/v1/improvements/metrics"]; ok {
		t.Error("paths still has operator path /v1/improvements/metrics")
	}

	rendered := string(out)
	if strings.Contains(rendered, "x-ratelimit") {
		t.Error("output still has an x-ratelimit key")
	}
	if strings.Contains(rendered, "120 requests") {
		t.Error("output still has the rate-limit sentence")
	}
}

func TestFilterPrunesComponents(t *testing.T) {
	out, err := Filter(openAPIFilterTestSource(t), openAPIFilterTestRoutes())
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(out, &spec); err != nil {
		t.Fatalf("Filter output is not valid JSON: %v", err)
	}

	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatalf("spec[components] = %v, want an object", spec["components"])
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("spec[components][schemas] = %v, want an object", components["schemas"])
	}

	for _, want := range []string{"Agent", "Handle"} {
		if _, ok := schemas[want]; !ok {
			t.Errorf("schemas is missing referenced schema %q", want)
		}
	}
	for _, unwanted := range []string{"Unused", "Metric"} {
		if _, ok := schemas[unwanted]; ok {
			t.Errorf("schemas still has unreferenced schema %q", unwanted)
		}
	}
}

func TestSpecMatchesRoutes(t *testing.T) {
	specData, err := os.ReadFile("../../../docs/api/openapi.json")
	if err != nil {
		t.Fatalf("read docs/api/openapi.json: %v", err)
	}
	routesData, err := os.ReadFile("../../../testdata/contract/routes.json")
	if err != nil {
		t.Fatalf("read testdata/contract/routes.json: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("docs/api/openapi.json is not valid JSON: %v", err)
	}
	var routeFiles []openAPIFilterRouteFixture
	if err := json.Unmarshal(routesData, &routeFiles); err != nil {
		t.Fatalf("testdata/contract/routes.json is not valid JSON: %v", err)
	}
	routes := make([]Route, 0, len(routeFiles))
	for _, f := range routeFiles {
		routes = append(routes, Route(f))
	}
	compiled, err := openAPIFilterCompileRoutes(routes)
	if err != nil {
		t.Fatalf("compile routes: %v", err)
	}

	type operation struct {
		method string
		path   string
	}
	var ops []operation
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("spec[paths] = %v, want an object", spec["paths"])
	}
	for path, itemRaw := range paths {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		for method := range item {
			if openAPIFilterHTTPMethods[strings.ToLower(method)] {
				ops = append(ops, operation{method: strings.ToUpper(method), path: path})
			}
		}
	}

	for i, r := range routes {
		route := compiled[i]
		matched := false
		for _, op := range ops {
			if op.method != route.method {
				continue
			}
			if route.regex.MatchString(openAPIFilterSamplePath(op.path)) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("route %s %s matches no operation in docs/api/openapi.json", r.Method, r.Pattern)
		}
	}

	for _, op := range ops {
		if !openAPIFilterMatchesAny(compiled, op.method, openAPIFilterSamplePath(op.path)) {
			t.Errorf("operation %s %s in docs/api/openapi.json matches no route in testdata/contract/routes.json", op.method, op.path)
		}
	}

	servers, ok := spec["servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("spec[servers] = %v, want exactly one entry", spec["servers"])
	}
	server, ok := servers[0].(map[string]any)
	if !ok || server["url"] != "https://ajent.social" {
		t.Fatalf("spec[servers][0] = %v, want {\"url\":\"https://ajent.social\"}", servers[0])
	}
}

func TestFilterKeepsSecuritySchemes(t *testing.T) {
	src := []byte(`{
  "openapi": "3.1.0",
  "paths": {
    "/v1/me": {"get": {"security": [{"bearerAuth": []}], "responses": {}}},
    "/v1/improvements/metrics": {"get": {"security": [{"ownerAuth": []}], "responses": {}}}
  },
  "components": {"securitySchemes": {
    "bearerAuth": {"type": "http", "scheme": "bearer"},
    "ownerAuth": {"type": "http", "scheme": "bearer"}
  }}
}`)
	out, err := Filter(src, []Route{{Method: "GET", Pattern: "^/v1/me$"}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	var spec struct {
		Components struct {
			SecuritySchemes map[string]any `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(out, &spec); err != nil {
		t.Fatalf("Filter output is not valid JSON: %v", err)
	}
	tests := []struct {
		name string
		want bool
	}{
		{"bearerAuth", true},
		{"ownerAuth", false},
	}
	for _, tt := range tests {
		if _, got := spec.Components.SecuritySchemes[tt.name]; got != tt.want {
			t.Errorf("securitySchemes[%q] present = %v, want %v", tt.name, got, tt.want)
		}
	}
}
