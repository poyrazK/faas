package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func wantCronProblem(t *testing.T, rec interface {
	Result() *http.Response
}, body []byte, status int, code string) {
	t.Helper()
	if got := rec.Result().StatusCode; got != status {
		t.Fatalf("status = %d, want %d; body=%s", got, status, body)
	}
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil || problem.Code != code {
		t.Fatalf("problem = %+v (%v), want %s", problem, err, code)
	}
}

// An app has one cron per schedule and path. A second one, by create or by
// update, is a 409 that says how to run the job twice; it was a 503 "could
// not create cron" (crons audit 2026-11-01).
func TestCronWithTakenScheduleAndPathIsConflict(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "cron-conflict")
	rec := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 9 * * *", Path: "/report", Timezone: "Europe/Istanbul"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 9 * * *", Path: "/report", Timezone: "America/New_York"}, nil)
	wantCronProblem(t, rec, rec.Body.Bytes(), http.StatusConflict, api.CodeConflict)

	rec = e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 9 * * *", Path: "/report?tz=newyork", Timezone: "America/New_York"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with a distinct query string: status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var second api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	taken := "/report"
	rec = e.Do(t, http.MethodPatch, "/v1/crons/"+second.ID, api.UpdateCronRequest{Path: &taken}, nil)
	wantCronProblem(t, rec, rec.Body.Bytes(), http.StatusConflict, api.CodeConflict)
}

// "Local" is whatever timezone the scheduler's host runs in, not a place.
func TestCronTimezoneLocalIsRefused(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "cron-local")
	rec := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: appID, Schedule: "0 9 * * *", Path: "/", Timezone: "Local"}, nil)
	wantCronProblem(t, rec, rec.Body.Bytes(), http.StatusBadRequest, api.CodeCronInvalid)
}

// A suspended cron is not run by hand: the API says so at once instead of
// accepting a request schedd then fails.
func TestSuspendedCronManualRunIsConflict(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	cronID, appID := seedCron(t, e, "cron-suspended-run", "0 3 * * *")
	if _, err := (*e.Store()).SuspendCronsForApp(context.Background(), appID, state.CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/crons/"+cronID+"/run", nil, nil)
	wantCronProblem(t, rec, rec.Body.Bytes(), http.StatusConflict, api.CodeConflict)
}

// Status reports an enabled cron the plan's limits stop as not ready; it read
// "fires on schedule" (crons audit 2026-11-01). Hobby runs 5 crons per app.
func TestCronStatusReportsPlanLimit(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	appID := apidserver.MustSeedApp(t, e, "cron-status-limit")
	limit := api.MustLimitsFor(api.PlanHobby).CronLimitPerApp
	var ids []string
	for i := range limit + 1 {
		c, err := (*e.Store()).CreateCron(context.Background(), appID, "0 3 * * *", fmt.Sprintf("/job-%d", i), true)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	ready := func(cronID string) api.Condition {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/crons/"+cronID+"/status", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: %d; body=%s", rec.Code, rec.Body.String())
		}
		var status api.ResourceStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		for _, c := range status.Conditions {
			if c.Type == api.ConditionReady {
				return c
			}
		}
		t.Fatalf("no Ready condition in %+v", status)
		return api.Condition{}
	}
	if c := ready(ids[0]); c.Status != api.ConditionTrue {
		t.Fatalf("oldest cron Ready = %+v, want True", c)
	}
	if c := ready(ids[limit]); c.Status != api.ConditionFalse || c.Reason != "plan_limit" {
		t.Fatalf("cron over the limit Ready = %+v, want False plan_limit", c)
	}
}
