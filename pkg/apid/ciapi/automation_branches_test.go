package ciapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIAutomationLatestOnlyPolicyPreservesGenerationAndDormantCompute(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	store := ci.NewPostgresStore(f.pool)
	policies, err := store.AutomationPolicies(t.Context(), f.account)
	if err != nil || len(policies) != 1 || policies[0].LatestOnly {
		t.Fatal("latest-only was enabled implicitly")
	}
	req := policies[0].AutomationRequest
	req.LatestOnly = true
	var first ci.AutomationPolicy
	for i := range 2 {
		w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", req)
		var policy ci.AutomationPolicy
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &policy) != nil || !policy.LatestOnly || policy.Generation != policies[0].Generation+1 {
			t.Fatalf("explicit latest-only setup: %d %s", w.Code, w.Body)
		}
		if i == 0 {
			first = policy
		} else if first.ID != policy.ID || first.Generation != policy.Generation {
			t.Fatal("same latest-only policy changed admission identity")
		}
	}
	var executions, runs, branches int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs),(SELECT count(*) FROM ci_automation_branches)").Scan(&executions, &runs, &branches); err != nil || executions != 0 || runs != 0 || branches != 0 {
		t.Fatal("policy setup created execution, native intent or branch claims")
	}
	req.LatestOnly = false
	w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", req)
	var disabled ci.AutomationPolicy
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &disabled) != nil || disabled.LatestOnly || disabled.Generation != first.Generation+1 {
		t.Fatal("latest-only changes did not revoke the previous policy generation")
	}
}
