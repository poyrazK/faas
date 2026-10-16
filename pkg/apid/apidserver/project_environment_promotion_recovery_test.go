package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPromotionRecoveryAPIResumesWithoutReplayingSQL(t *testing.T) {
	oldDelay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = oldDelay })
	ctx := t.Context()
	srv, store, account, project, app, baseline := newPromotionHealthAPIFixture(t, true)
	now := time.Now().UTC()
	store.SetClockForTest(func() time.Time { return now })
	srv.WithPromotionHealthEnabled(true).WithPromotionMigrationsEnabled(true)
	srv.routeProbes = promotionHealthAPISender{baseline: baseline.Members[0].DeploymentID}
	gate, err := store.GetRouteHealthGate(ctx, account.ID, app.ID)
	mustSnapshotPromotionAPI(t, err)
	_, err = store.SetRouteHealthGate(ctx, account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
	mustSnapshotPromotionAPI(t, err)
	plan, problem := srv.buildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "production", "staging", true, true, true)
	if problem != nil || !plan.Preview.CanPromote {
		t.Fatal(problem, plan.Preview)
	}
	body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
	req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
	req.SetPathValue("environment", "staging")
	req.Header.Set("Idempotency-Key", "recovery-api")
	srv.promoteProjectEnvironment(rec, req, account)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admission=%d %s", rec.Code, rec.Body)
	}
	var accepted api.ProjectEnvironmentPromotionResponse
	mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &accepted))
	job := state.PromotionHealthJob{AccountID: account.ID, ProjectSlug: project.Slug, ToEnvironment: "staging", PromotionID: accepted.PromotionID}
	run := func() {
		a := srv.promotionHealthActions()
		a.Now = func() time.Time { return now }
		mustSnapshotPromotionAPI(t, promotionapi.ReconcileHealth(ctx, store, job, a))
	}
	run()
	at := time.Now().UTC().Add(time.Millisecond)
	task, err := store.ClaimNextAppTask(ctx, "recovery", at, time.Minute)
	mustSnapshotPromotionAPI(t, err)
	values, err := store.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, task.DeploymentID)
	mustSnapshotPromotionAPI(t, err)
	fence, err := state.NewRuntimeAppConfigFence(values)
	mustSnapshotPromotionAPI(t, err)
	mustSnapshotPromotionAPI(t, store.BindProjectMigrationTaskRuntime(ctx, task, fence))
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	mustSnapshotPromotionAPI(t, err)
	_, err = store.RegisterPromotionMigrationRuntime(ctx, task, node.ID)
	mustSnapshotPromotionAPI(t, err)
	_, err = store.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(time.Millisecond))
	mustSnapshotPromotionAPI(t, err)
	_, err = store.SweepExpiredAppTasks(ctx, at.Add(2*time.Minute))
	mustSnapshotPromotionAPI(t, err)
	run()
	h, err := store.ProjectEnvironmentPromotionHealth(ctx, account.ID, job.PromotionID)
	mustSnapshotPromotionAPI(t, err)
	recovery := api.ProjectPromotionMigrationRecoveryRequest{RequestID: strings.Repeat("a", 32), ExpectedTaskID: task.ID, ExpectedGeneration: h.Generation, Action: "resume", DatabaseStateChecked: true, SchemaCompatible: true, EvidenceHash: strings.Repeat("b", 64)}
	// Closing an incident remains available after migration admission is disabled.
	srv.WithPromotionMigrationsEnabled(false)
	recoveryBody, _ := json.Marshal(recovery)
	req, rec = projectRequest(http.MethodPost, "/recover", project.Slug, recoveryBody)
	req.SetPathValue("environment", "staging")
	req.SetPathValue("promotion", job.PromotionID)
	srv.recoverProjectPromotionMigration(rec, req, account)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("recovery=%d %s", rec.Code, rec.Body)
	}
	run()
	h, err = store.ProjectEnvironmentPromotionHealth(ctx, account.ID, job.PromotionID)
	mustSnapshotPromotionAPI(t, err)
	if h.Phase != "failed" || h.Recovery == nil || h.Recovery.Status != "quiescing" {
		t.Fatal("recovery bypassed teardown", h)
	}
	jobs, err := store.PendingPromotionMigrationQuiescence(ctx)
	mustSnapshotPromotionAPI(t, err)
	if len(jobs) != 1 {
		t.Fatal("missing teardown intent")
	}
	mustSnapshotPromotionAPI(t, store.AcknowledgePromotionMigrationQuiescence(ctx, jobs[0]))
	run()
	h, err = store.ProjectEnvironmentPromotionHealth(ctx, account.ID, job.PromotionID)
	mustSnapshotPromotionAPI(t, err)
	if h.Phase != "preflight" || h.Recovery.Status != "resumed" || h.Migration.Status != "uncertain" {
		t.Fatal("recovery did not preserve outcome and restart checks", h)
	}
	run()
	now = now.Add(api.ProjectReleaseVerificationInterval)
	run()
	run()
	run()
	now = now.Add(api.ProjectReleaseVerificationInterval)
	run()
	run()
	p, _, err := store.ProjectEnvironmentPromotionByID(ctx, account.ID, project.Slug, "staging", job.PromotionID)
	mustSnapshotPromotionAPI(t, err)
	if p.Status != "succeeded" {
		t.Fatal("recovered promotion did not complete", p)
	}
	task, err = store.AppTaskByID(ctx, account.ID, app.ID, task.ID)
	mustSnapshotPromotionAPI(t, err)
	if task.Status != state.AppTaskFailed || task.ExitCode != nil {
		t.Fatal("original outcome overwritten")
	}
	// Acknowledgement replay returns the original resolution even after completion.
	req, rec = projectRequest(http.MethodPost, "/recover", project.Slug, recoveryBody)
	req.SetPathValue("environment", "staging")
	req.SetPathValue("promotion", job.PromotionID)
	srv.recoverProjectPromotionMigration(rec, req, account)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay=%d %s", rec.Code, rec.Body)
	}
}
