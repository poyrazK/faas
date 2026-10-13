package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowRecoveryPolicyChangeHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "change-preview")
	path := "/v1/apps/change-preview/recovery-policy"
	req := api.SetWorkflowRecoveryPolicyRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "provider incident"}}
	clear, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-preview", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = clear
	rec := e.do(t, http.MethodPost, path+"/change-preview", req, nil)
	var out api.WorkflowRecoveryPolicyChangePreview
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Current.Version != 0 || out.NextVersion != 1 || out.PendingSuppressed != 0 || out.Entries == nil || out.Candidates == nil || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, path, nil, nil)
	var current api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &current) != nil || current.Version != 0 {
		t.Fatalf("preview wrote policy %d %s", rec.Code, rec.Body.String())
	}
	if denied := e.do(t, http.MethodPut, path, req, nil); denied.Code != 403 {
		t.Fatalf("read preview authorized update %d %s", denied.Code, denied.Body.String())
	}
	req.ExpectedVersion = 1
	if rec = e.do(t, http.MethodPost, path+"/change-preview", req, nil); rec.Code != 409 {
		t.Fatalf("stale %d %s", rec.Code, rec.Body.String())
	}
	req.ExpectedVersion = 0
	req.Policy.Mode = "automatic_retry"
	if rec = e.do(t, http.MethodPost, path+"/change-preview", req, nil); rec.Code != 400 {
		t.Fatalf("unbounded policy %d %s", rec.Code, rec.Body.String())
	}
	req.Policy.Mode = "preview"
	if rec = e.do(t, http.MethodPost, "/v1/apps/absent/recovery-policy/change-preview", req, nil); rec.Code != 404 {
		t.Fatalf("scope %d %s", rec.Code, rec.Body.String())
	}
}
