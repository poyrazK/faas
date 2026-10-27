//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 1447 — a rolled-back canary is explained with the request telemetry
// behind its decision, and debug status reports the rollback.
func TestDebugRolloutAndStatusPG(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	ctx := t.Context()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "rollout-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	newDeployment := func(digit string) state.Deployment {
		t.Helper()
		d, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat(digit, 64), Scope: "default", Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	stable := newDeployment("1")
	if err = (*e.Store()).SetDeploymentRootfs(ctx, stable.ID, "/test/"+stable.ID, "test/"+stable.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = (*e.Store()).MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	candidate := newDeployment("2")

	now := time.Now().UTC()
	windowStart, windowEnd := now.Add(-20*time.Minute), now.Add(-5*time.Minute)
	decision, _ := json.Marshal(api.RolloutDecision{
		Kind: api.RolloutDecisionAbort, Rule: api.RolloutRuleAbort5xx, Summary: "5xx regression (candidate 83.3%, stable 0.0%)",
		TrafficPercent: 10, WindowStart: windowStart.Format(time.RFC3339Nano), WindowEnd: windowEnd.Format(time.RFC3339Nano),
		StableDeploymentID: stable.ID, DecidedAt: windowEnd.Format(time.RFC3339Nano),
	})
	if _, err = (*e.Pool()).Exec(ctx, `UPDATE deployments
		   SET canary_total_steps = 4, canary_step = 0, canary_step_started_at = $2, traffic_percent = 0,
		       rollout_state = 'aborted', rollout_started_at = $2, rollout_aborted_at = $3,
		       rollout_aborted_reason = '5xx error rate regression', rollout_decision = $4::jsonb
		 WHERE id = $1`, candidate.ID, windowStart, windowEnd, string(decision)); err != nil {
		t.Fatal(err)
	}

	insert := func(deploymentID string, status int32, latency int32, at time.Time) {
		t.Helper()
		if err := (*e.Store()).InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse((*e.Acct()).ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
			DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(deploymentID), Valid: true}, ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true},
			Route: "/checkout", Method: "GET", Status: status, LatencyMs: latency, Count: 1,
			UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		at := windowStart.Add(time.Duration(i+1) * time.Minute)
		status := int32(500)
		if i == 0 {
			status = 200
		}
		insert(candidate.ID, status, 40, at)
		insert(stable.ID, 200, 30, at)
	}

	rec := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/debug/rollout", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollout = %d %s", rec.Code, rec.Body)
	}
	var rollout api.DebugRolloutExplanation
	if err := json.Unmarshal(rec.Body.Bytes(), &rollout); err != nil {
		t.Fatal(err)
	}
	if rollout.State != api.DebugRolloutAborted || rollout.DeploymentID != candidate.ID || rollout.StableDeploymentID != stable.ID || rollout.Decision == nil {
		t.Fatalf("rollout = %+v", rollout)
	}
	if len(rollout.Routes) != 1 || rollout.Routes[0].CandidateServerErrors != 5 || rollout.Routes[0].CandidateRequests != 6 || rollout.Routes[0].StableRequests != 6 {
		t.Fatalf("routes = %+v", rollout.Routes)
	}
	if len(rollout.Examples) != api.DebugRolloutMaxExamples || rollout.Examples[0].Status != 500 || rollout.Examples[0].DeploymentID != candidate.ID {
		t.Fatalf("examples = %+v", rollout.Examples)
	}
	if !strings.Contains(rollout.Headline, "Worst route /checkout: 5 of 6 requests failed with a server error, vs 0 of 6 on the stable release.") {
		t.Fatalf("headline = %q", rollout.Headline)
	}

	// ADR-121: the window's OOM kills of this app, newest first; another
	// app's kill and one before the window are left out.
	for _, kill := range []struct {
		at    time.Time
		appID string
		peak  int
	}{
		{now.Add(-10 * time.Minute), app.ID, 150},
		{now.Add(-2 * time.Minute), app.ID, 160},
		{now.Add(-3 * time.Minute), uuid.NewString(), 999},
		{now.Add(-2 * time.Hour), app.ID, 999},
	} {
		data, _ := json.Marshal(map[string]any{"app_id": kill.appID, "deployment_id": stable.ID, "instance_id": uuid.NewString(),
			"peak_mb": kill.peak, "plan_mb": 128, "deployment_failed": false})
		if _, err = (*e.Pool()).Exec(ctx, `INSERT INTO events (at, actor, kind, data) VALUES ($1, 'schedd', 'instances.workload_oom_failed', $2::jsonb)`,
			kill.at, string(data)); err != nil {
			t.Fatal(err)
		}
	}

	rec = e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/debug/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var status api.DebugStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Verdict != api.DebugStatusRollback || status.Since != "1h" || status.Rollout == nil || status.Headline != rollout.Headline {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Deployments) != 1 || status.Deployments[0].ID != stable.ID || status.Health.Requests != 6 || status.Health.ServerErrors != 0 {
		t.Fatalf("serving health = %+v deployments = %+v", status.Health, status.Deployments)
	}
	if status.OOMKillsTotal != 2 || len(status.OOMKills) != 2 || status.OOMKills[0].PeakMB != 160 || status.OOMKills[1].PeakMB != 150 ||
		status.OOMKills[0].PlanMB != 128 || status.OOMKills[0].DeploymentID != stable.ID || !strings.Contains(status.OOMFix, "at least 168 MB") {
		t.Fatalf("OOM kills = %d %+v fix %q", status.OOMKillsTotal, status.OOMKills, status.OOMFix)
	}
}
