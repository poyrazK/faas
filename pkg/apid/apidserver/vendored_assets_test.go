package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/docsassets"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// The dashboard and the API reference load htmx and Swagger UI from apid
// itself, so the CSP names no package CDN. Login and /docs are anonymous, so
// the assets must be too.
func TestVendoredScriptAssetsAreServedAnonymously(t *testing.T) {
	ops := wire.NewOpsMetrics("apid_vendored_assets_test")
	srv := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", apidserver.NoopNotifier{}).WithOpsMetrics(context.Background(), ops)
	h := srv.Handler()
	for _, path := range []string{dashboard.HTMXScriptPath, dashboard.HTMXSSEScriptPath, docsassets.BundlePath, docsassets.CSSPath} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("GET %s = %d (%d bytes)", path, rec.Code, rec.Body.Len())
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("GET %s Cache-Control = %q", path, rec.Header().Get("Cache-Control"))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if body := rec.Body.String(); strings.Contains(body, "unpkg.com") || !strings.Contains(body, docsassets.BundlePath) {
		t.Errorf("API reference page does not load the vendored Swagger UI:\n%s", body)
	}
}
