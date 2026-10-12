package apidserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryHistoryHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "history")
	path := "/v1/apps/history/recovery-policy/history"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var out api.WorkflowRecoveryHistory
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Entries == nil || !out.RetainedObservations || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("history %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"?limit=101", "?limit=bad", "?run_id=bad", "?policy_version=-1", "?after=invalid", "?limit=1&limit=2", "?created_after=bad"} {
		if rec = e.do(t, http.MethodGet, path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid %s %d", query, rec.Code)
		}
	}
	if rec = e.do(t, http.MethodGet, "/v1/apps/absent/recovery-policy/history", nil, nil); rec.Code != 404 {
		t.Fatalf("scope %d", rec.Code)
	}
}
