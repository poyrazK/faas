package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The gateway traffic report reads only Prometheus (ADR-20261124093000).
// The test server has none, so the report is not_configured with every
// section present and empty, and a bad window is a 400.
func TestObsGatewayTrafficReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/gateway-traffic?window=6h", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var r api.ObsGatewayTrafficReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Source != "not_configured" || r.WindowSeconds != 6*3600 || r.Nodes == nil || r.Apps == nil || r.FailedQueries == nil {
		t.Fatalf("report = %+v", r)
	}
	for _, bad := range []string{"1m", "200h", "soon"} {
		if rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/gateway-traffic?window="+bad, nil, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("window=%s status %d", bad, rec.Code)
		}
	}
}
