package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The egress report reads only Prometheus (ADR-20261127100000). The test
// server has none, so the report is not_configured with every section
// present and empty, the default window is 24h, and a bad window is a 400.
func TestObsEgressReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/egress", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var r api.ObsEgressReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Source != "not_configured" || r.WindowSeconds != 24*3600 || r.Classes == nil || r.Apps == nil || r.Denylist == nil || r.FailedQueries == nil {
		t.Fatalf("report = %+v", r)
	}
	for _, tc := range []struct {
		window          string
		limit, observed int64 // seconds; 0 when the value did not parse
	}{{"1m", 300, 60}, {"200h", 604800, 720000}, {"soon", 0, 0}} {
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/egress?window="+tc.window, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("window=%s status %d", tc.window, rec.Code)
		}
		var p api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.DocsURL != api.ObsEgressDocsURL || !strings.Contains(p.Detail, `"`+tc.window+`"`) || !strings.Contains(p.Detail, "5m to 168h") {
			t.Fatalf("window=%s problem = %+v", tc.window, p)
		}
		if tc.limit == 0 {
			if p.Limit != nil || p.Observed != nil {
				t.Fatalf("window=%s limit set on a parse error: %+v", tc.window, p)
			}
		} else if p.Limit == nil || *p.Limit != tc.limit || p.Observed == nil || *p.Observed != tc.observed {
			t.Fatalf("window=%s limit/observed = %v/%v", tc.window, p.Limit, p.Observed)
		}
	}
}
