package apidserver

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"testing"
)

func TestWorkflowRecoveryReportHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "report")
	path := "/v1/apps/report/recovery-policy/report"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var out api.WorkflowRecoveryReport
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Groups == nil || len(out.CoverageNotes) == 0 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("report %d %s", rec.Code, rec.Body.String())
	}
	for _, q := range []string{"?limit=1", "?after=bad", "?run_id=bad", "?policy_version=-1", "?workflow=a&workflow=b", "?created_after=bad", "?created_before=2099-01-01T00:00:00Z", "?created_after=2026-01-01T00:00:00Z&created_before=2026-03-01T00:00:00Z"} {
		if rec = e.do(t, http.MethodGet, path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("query %s %d", q, rec.Code)
		}
	}
	if rec = e.do(t, http.MethodGet, "/v1/apps/absent/recovery-policy/report", nil, nil); rec.Code != 404 {
		t.Fatalf("scope %d", rec.Code)
	}
}
