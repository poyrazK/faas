package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The disk pressure report reads only Prometheus (ADR-20261124150000). The
// test server has none, so the report is not_configured with every section
// present and empty, and query parameters are rejected.
func TestObsDiskPressureReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/disk-pressure", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var r api.ObsDiskPressureReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Source != "not_configured" || r.Nodes == nil || r.Filesystems == nil || r.Guests == nil || r.FailedQueries == nil || r.LowFreePercent != 15 {
		t.Fatalf("report = %+v", r)
	}
	if rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/disk-pressure?window=1h", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("query param status %d", rec.Code)
	}
}
