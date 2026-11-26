package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestObsControlPlaneReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/control-plane", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var r api.ObsControlPlaneReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Source != "not_configured" || r.State != "unknown" || len(r.Checks) == 0 || r.Pools == nil || r.Statements == nil || r.FailedQueries == nil || r.WindowSeconds != 3600 {
		t.Fatalf("report = %+v", r)
	}
	if rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/control-plane?window=1h", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("query param status %d", rec.Code)
	}
}
