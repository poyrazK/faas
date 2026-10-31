package apid

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/docsassets"
)

// The API reference page names the vendored Swagger UI by path without
// importing docsassets; the two must agree or the page loads nothing.
func TestDocsPageReferencesTheVendoredSwaggerUI(t *testing.T) {
	if swaggerUIBundlePath != docsassets.BundlePath || swaggerUICSSPath != docsassets.CSSPath {
		t.Fatalf("docs page paths %s, %s; docsassets serves %s, %s", swaggerUIBundlePath, swaggerUICSSPath, docsassets.BundlePath, docsassets.CSSPath)
	}
}
