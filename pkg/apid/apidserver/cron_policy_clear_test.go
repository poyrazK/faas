package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// PATCH /v1/crons/{id} removes a schedule policy with clear_schedule_policy;
// sending the policy and its clear together is a 400. A manual run of a
// disabled cron is refused before a request is queued (crons audit
// 2026-11-03).
func TestCronPolicyClearAndDisabledRun(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "policy-clear"})
	if err != nil {
		t.Fatal(err)
	}
	cron, err := store.CreateCron(ctx, app.ID, "0 3 * * *", "/nightly", true)
	if err != nil {
		t.Fatal(err)
	}
	policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "skip"}
	if r := e.Do(t, http.MethodPatch, "/v1/crons/"+cron.ID, api.UpdateCronRequest{SchedulePolicy: policy}, nil); r.Code != http.StatusOK {
		t.Fatalf("set policy = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPatch, "/v1/crons/"+cron.ID, api.UpdateCronRequest{SchedulePolicy: policy, ClearSchedulePolicy: true}, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("set and clear = %d, want 400: %s", r.Code, r.Body)
	}
	r := e.Do(t, http.MethodPatch, "/v1/crons/"+cron.ID, api.UpdateCronRequest{ClearSchedulePolicy: true}, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("clear policy = %d %s", r.Code, r.Body)
	}
	var got api.CronResponse
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil || got.SchedulePolicy != nil {
		t.Fatalf("cleared cron = %s (%v), want no schedule policy", r.Body, err)
	}

	disabled := false
	if r := e.Do(t, http.MethodPatch, "/v1/crons/"+cron.ID, api.UpdateCronRequest{Enabled: &disabled}, nil); r.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/crons/"+cron.ID+"/run", nil, nil); r.Code != http.StatusConflict {
		t.Fatalf("run a disabled cron = %d, want 409: %s", r.Code, r.Body)
	}
}
