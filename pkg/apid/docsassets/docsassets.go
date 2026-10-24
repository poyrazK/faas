// Package docsassets serves the vendored Swagger UI release behind the API
// reference page (/docs), so its CSP needs no third-party host. It is a
// package of its own because pkg/apid is linked into small helpers (vmmd's
// jail mount helper) that must not carry 1.6 MB of browser code; only apid
// imports this one.
//
// The files are byte-identical to swagger-ui-dist@5.17.14 from npm; the page
// pins the same SRI digests (pkg/apid/openapi_handler.go) and the tests
// recompute them from the embedded bytes.
package docsassets

import (
	"embed"
	"net/http"
)

//go:embed swagger-ui-bundle-5.17.14.js swagger-ui-5.17.14.css
var files embed.FS

// Paths the API reference page references.
const (
	BundlePath = "/docs/assets/swagger-ui-bundle-5.17.14.js"
	CSSPath    = "/docs/assets/swagger-ui-5.17.14.css"
)

var served = map[string]struct{ file, contentType string }{
	"swagger-ui-bundle-5.17.14.js": {"swagger-ui-bundle-5.17.14.js", "text/javascript; charset=utf-8"},
	"swagger-ui-5.17.14.css":       {"swagger-ui-5.17.14.css", "text/css; charset=utf-8"},
}

// Serve answers GET /docs/assets/{file}. Versioned names make the responses
// immutable.
func Serve(w http.ResponseWriter, r *http.Request) {
	asset, ok := served[r.PathValue("file")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := files.ReadFile(asset.file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(body)
}
