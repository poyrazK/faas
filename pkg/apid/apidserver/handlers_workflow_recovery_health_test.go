package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryPolicyHealthHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "policy-health")
	clear, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-health", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = clear
	path := "/v1/apps/policy-health/recovery-policy/health"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var out api.WorkflowRecoveryPolicyHealth
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Status != "inactive" || out.Queue.Policy.Version != 0 || out.StallAfterSeconds != api.WorkflowRecoveryHealthDefaultStallSeconds || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health %d %s", rec.Code, rec.Body.String())
	}
	for _, query := range []string{"?stall_after_seconds=0", "?stall_after_seconds=86401", "?stall_after_seconds=no", "?stall_after_seconds=", "?stall_after_seconds=1&stall_after_seconds=2"} {
		if rec = e.do(t, http.MethodGet, path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid %s: %d", query, rec.Code)
		}
	}
	if rec = e.do(t, http.MethodGet, "/v1/apps/absent/recovery-policy/health", nil, nil); rec.Code != 404 {
		t.Fatalf("scope %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/policy-health/recovery-policy", nil, nil)
	var policy api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &policy) != nil || policy.Version != 0 || policy.LastEvaluationAt != nil {
		t.Fatalf("health changed policy %d %s", rec.Code, rec.Body.String())
	}
}
