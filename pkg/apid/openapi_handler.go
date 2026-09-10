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
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/httpsec"
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

// Convert the embedded spec only when the JSON endpoint is requested, then
// share the result across requests. This package is also linked into vmmd's
// jail mount helper: eager YAML conversion added about 82 ms to every helper
// process on the SSD compute node, even though it never serves this endpoint.
var (
	openapiJSONOnce sync.Once
	openapiJSON     []byte
)

const (
	swaggerUIDistVersion = "5.17.14"
	swaggerUIBundleSRI   = "sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep"
	swaggerUICSSSRI      = "sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn"
)

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

func openAPIJSONBytes() []byte {
	openapiJSONOnce.Do(func() {
		openapiJSON = mustMarshalJSON(openapiYAML)
	})
	return openapiJSON
}

// OpenAPIJSONSHA256 returns the hex SHA-256 checksum of the exact bytes served
// by GET /v1/openapi.json. The docs page publishes this value so clients and
// CI can verify that Swagger UI is rendering the deployed document.
func OpenAPIJSONSHA256() string {
	sum := sha256.Sum256(openAPIJSONBytes())
	return hex.EncodeToString(sum[:])
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
// JSON bytes computed once on first use. SDK generators
// (`openapi-generator`, `oapi-codegen`) prefer JSON, and the endpoint
// is amplifiable, so caching the body matters.
//
// The JSON response is deterministic for a given spec — `yaml.v3`
// decodes into `map[string]any` / `[]any`, which json.Marshal renders
// with sorted keys (Go spec). Equivalent specs always produce
// equivalent JSON. See openapi_handler_test.go for the locked-in
// round-trip property.
func ServeOpenAPISpecJSON(w http.ResponseWriter, _ *http.Request) {
	body := openAPIJSONBytes()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ServeDocs handles the anonymous human-readable API reference. Swagger UI is
// loaded from a pinned, SRI-protected CDN asset and renders the same JSON bytes
// served by ServeOpenAPISpecJSON. Authorization is intentionally in-memory in
// Swagger UI (persistAuthorization=false); the server never receives or logs
// a token from this page.
func ServeDocs(w http.ResponseWriter, r *http.Request) {
	nonce := httpsec.NonceFromContext(r.Context())
	checksum := OpenAPIJSONSHA256()
	inlineScript := `window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "/v1/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    displayRequestDuration: true,
    persistAuthorization: false,
    tryItOutEnabled: false
  });
});`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="gregale-openapi-sha256" content="%s">
  <title>Gregale API reference</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@%s/swagger-ui.css"
    integrity="%s" crossorigin="anonymous">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@%s/swagger-ui-bundle.js"
    integrity="%s" crossorigin="anonymous" defer></script>
  <script%s>%s</script>
</body>
</html>
`, checksum, swaggerUIDistVersion, swaggerUICSSSRI, swaggerUIDistVersion, swaggerUIBundleSRI, nonceAttribute(nonce), inlineScript)
}

func nonceAttribute(nonce string) string {
	if nonce == "" {
		return ""
	}
	return ` nonce="` + nonce + `"`
}
