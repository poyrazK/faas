// Package apid hosts small read-only HTTP handlers that don't belong on
// cmd/apid (the route table) but are still server-internal.
//
// openapi_handler.go serves the customer-facing OpenAPI spec at
// GET /v1/openapi.{yaml,json}. Both endpoints are anonymous — SDK
// generators and curl users must reach the spec without a Bearer key.
// The spec is embedded at build time so the binary is self-contained
// and a deployed apid always serves the exact spec that matches its
// built-in handler set (the spec_compliance_test.go AST gate keeps them
// in sync at PR time).
package apid

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"

	"gopkg.in/yaml.v3"
)

// openapiYAML is the embedded OpenAPI 3.1 spec for the /v1/* surface.
// The file at pkg/apid/openapi.yaml is generated from api/openapi.yaml
// by `make spec-check` (see Makefile target). The copy is needed because
// `//go:embed` only resolves paths inside the package directory.
//
//go:embed openapi.yaml
var openapiYAML []byte

// OpenAPIYAML returns the raw YAML bytes of the embedded spec. Exported
// so cmd/apid (the route registration) can reuse the same source of
// truth if it ever needs to (e.g. serving the spec from a different
// listener).
func OpenAPIYAML() []byte { return openapiYAML }

// openapiJSON is the embedded spec re-emitted as JSON, computed once at
// process start. The endpoint is anonymous (no s.auth, no rate-limit) and
// amplifiable — SDK generators and curl users hit it freely — so we
// avoid paying YAML→map→JSON cost per request. spec_compliance_test.go
// catches a malformed YAML before deploy, so reaching the error path
// here is a build-time invariant violation.
var (
	openapiJSONOnce sync.Once
	openapiJSON     []byte
)

func init() {
	openapiJSONOnce.Do(func() {
		openapiJSON = mustMarshalJSON(openapiYAML)
	})
}

func mustMarshalJSON(yamlBytes []byte) []byte {
	var doc any
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		// Should be caught at PR time by `make spec-check` (vacuum
		// parse + AST gate). If we land here the spec is malformed;
		// fall back to a structured error envelope so the runtime
		// surfaces it rather than panicking on nil.
		return []byte(`{"error":"openapi spec is malformed at build time"}`)
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return []byte(`{"error":"openapi spec is malformed at build time"}`)
	}
	return body
}

// ServeOpenAPISpec handles GET /v1/openapi.yaml. Anonymous; emits
// application/yaml with a short Cache-Control so SDK codegen caches
// don't pin a stale spec.
func ServeOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapiYAML)
}

// ServeOpenAPISpecJSON handles GET /v1/openapi.json. Anonymous; serves
// the pre-marshalled JSON bytes computed in init(). SDK generators
// (`openapi-generator`, `oapi-codegen`) prefer JSON, and the endpoint
// is amplifiable, so caching the body matters.
//
// The JSON response is deterministic for a given spec — `yaml.v3`
// decodes into `map[string]any` / `[]any`, which json.Marshal renders
// with sorted keys (Go spec). Equivalent specs always produce
// equivalent JSON. See openapi_handler_test.go for the locked-in
// round-trip property.
func ServeOpenAPISpecJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapiJSON)
}
