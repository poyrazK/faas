package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Cron schedules are not triggers (issue #2757): a cron filter on the trigger
// inventory is refused with a pointer at /v1/crons instead of an empty list,
// and a legacy cron-linked row never appears in the inventory.
func TestTriggerInventoryKeepsCronsOut(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "trigger-inventory")
	rec := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 3 * * *", Path: "/tick"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cron: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := (*e.Store()).CreateTriggerIfUnderQuota(context.Background(), appID, string(api.TriggerKindCron), "legacy-cron",
		true, nil, "", 0, 0, 0, 0, "", api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatalf("seed legacy cron-linked trigger: %v", err)
	}

	rec = e.Do(t, http.MethodGet, "/v1/triggers?kind=cron", nil, nil)
	var problem api.Problem
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeValidation || problem.DocsURL == "" {
		t.Fatalf("GET /v1/triggers?kind=cron = %d %s, want a documented 400 validation_failed", rec.Code, rec.Body.String())
	}

	rec = e.Do(t, http.MethodGet, "/v1/triggers", nil, nil)
	var triggers []api.Trigger
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &triggers) != nil {
		t.Fatalf("GET /v1/triggers = %d %s", rec.Code, rec.Body.String())
	}
	for _, trigger := range triggers {
		if trigger.Kind == api.TriggerKindCron {
			t.Fatalf("the trigger inventory lists a cron-linked row: %+v", trigger)
		}
	}
}
