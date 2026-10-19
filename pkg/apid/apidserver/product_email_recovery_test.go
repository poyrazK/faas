package apidserver_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlEmailRecoveryDefaultOff(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	s := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	for _, path := range []string{"/v1/product-control/customer/exports/email-recovery", "/v1/product-control/customer/exports/email-recoveries", "/v1/apps/exports/product/email-recoveries", "/dashboard/apps/exports/product-control/email-recoveries"} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest(method, "https://example.test"+path, nil))
			if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(path, method, w.Code, w.Body.String())
			}
		}
	}
}
