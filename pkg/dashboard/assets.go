package dashboard

import (
	"embed"
	"net/http"
)

// Vendored third-party scripts the dashboard loads, served from the API host
// so the dashboard's CSP needs no third-party script host. Each file is the
// exact npm release the templates' SRI digest names; TestVendoredAssetsMatch
// TheirIntegrity recomputes the digest from the embedded bytes. Provenance
// and licences: assets/README.md.
//
//go:embed assets/htmx-2.0.4.min.js assets/htmx-ext-sse-2.2.2.js
var assetFS embed.FS

// Paths the templates reference. Versioned names make the responses
// immutable: a new release is a new path.
const (
	HTMXScriptPath    = "/dashboard/assets/htmx-2.0.4.min.js"
	HTMXSSEScriptPath = "/dashboard/assets/htmx-ext-sse-2.2.2.js"
)

var assetFiles = map[string]string{
	"htmx-2.0.4.min.js":     "assets/htmx-2.0.4.min.js",
	"htmx-ext-sse-2.2.2.js": "assets/htmx-ext-sse-2.2.2.js",
}

// ServeAsset serves one vendored script for GET /dashboard/assets/{file}.
// It needs no session: the login and password pages load htmx too.
func ServeAsset(w http.ResponseWriter, r *http.Request) {
	name, ok := assetFiles[r.PathValue("file")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := assetFS.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(body)
}
