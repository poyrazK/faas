package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestListSidecarTimeline_HappyPathAndLatestStatus(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "sidecar-timeline-app")
	ctx := context.Background()
	appendEvent := func(kind string, payload map[string]any) {
		t.Helper()
		payload["app_id"] = app.ID
		payload["sidecar_name"] = "metrics"
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.AppendEvent(ctx, "vmmd", kind, nil, data); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	appendEvent("wake.sidecar_init_exit", map[string]any{"status": "init_ok", "exit_code": 0})
	appendEvent("wake.sidecar_health", map[string]any{"status": "starting", "reason": "init"})
	appendEvent("wake.sidecar_restart", map[string]any{"attempt": 1, "previous_exit_code": 1})
	appendEvent("wake.sidecar_health", map[string]any{"status": "healthy", "reason": "healthcheck_ok"})

	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/sidecars/metrics/timeline", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.SidecarTimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SidecarName != "metrics" || resp.AppID != app.ID {
		t.Fatalf("identity = %#v, want sidecar metrics and app %s", resp, app.ID)
	}
	if len(resp.Events) != 4 {
		t.Fatalf("events = %d, want 4", len(resp.Events))
	}
	if resp.Latest == nil || resp.Latest.Status != "healthy" || resp.Latest.Reason != "healthcheck_ok" {
		t.Fatalf("latest = %#v, want healthy/healthcheck_ok", resp.Latest)
	}
	if resp.Events[0].Kind != "wake.sidecar_init_exit" || resp.Events[3].Kind != "wake.sidecar_health" {
		t.Fatalf("events not oldest-first: %#v", resp.Events)
	}
}

func TestListSidecarTimeline_CrossAccountRowsAreInvisible(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "sidecar-timeline-forge")
	data := []byte(`{"app_id":"00000000-0000-0000-0000-000000000000","sidecar_name":"metrics","status":"healthy"}`)
	if err := e.store.AppendEvent(context.Background(), "vmmd", "wake.sidecar_health", nil, data); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/sidecars/metrics/timeline", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s, want 404 for foreign-only timeline", rec.Code, rec.Body.String())
	}
}

func TestListSidecarTimeline_FreePlanAndLimitValidation(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, http.MethodGet, "/v1/apps/unknown/sidecars/metrics/timeline", nil, nil)
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanPerAppMetricsNotAllowed)

	e = setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "sidecar-timeline-limit")
	rec = e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/sidecars/metrics/timeline?limit=2000", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}
