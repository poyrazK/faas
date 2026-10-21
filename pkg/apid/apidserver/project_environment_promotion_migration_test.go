package apidserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	promotionapi "github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPromotionMigrationAPI(t *testing.T) {
	oldDelay := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = oldDelay })
	for _, scenario := range []string{"success", "rollback", "disabled", "requires_verify", "token_opt_in"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			srv, store, account, project, app, baseline := newPromotionHealthAPIFixture(t, true)
			now := time.Now().UTC()
			store.SetClockForTest(func() time.Time { return now })
			srv.WithPromotionHealthEnabled(true).WithPromotionMigrationsEnabled(scenario != "disabled")
			srv.routeProbes = promotionHealthAPISender{baseline: baseline.Members[0].DeploymentID}
			gate, err := store.GetRouteHealthGate(ctx, account.ID, app.ID)
			mustSnapshotPromotionAPI(t, err)
			_, err = store.SetRouteHealthGate(ctx, account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
			mustSnapshotPromotionAPI(t, err)
			plan, problem := srv.buildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "production", "staging", true, scenario != "requires_verify", true)
			if problem != nil {
				t.Fatal(problem)
			}
			if scenario == "disabled" || scenario == "requires_verify" {
				if plan.Preview.CanPromote {
					t.Fatal("unguarded migration preview admitted")
				}
				return
			}
			if !plan.Preview.CanPromote || plan.Preview.Health == nil || plan.Preview.Health.Migration == nil {
				t.Fatalf("migration preview=%+v", plan.Preview)
			}
			wire, err := decodeProjectEnvironmentPromotionToken(plan.Preview.PromotionToken)
			mustSnapshotPromotionAPI(t, err)
			if !wire.Migrate || !wire.Verify {
				t.Fatal("token lost migration contract")
			}
			if scenario == "token_opt_in" {
				wire.Migrate = false
				raw, _ := json.Marshal(wire)
				plan.Preview.PromotionToken = base64.RawURLEncoding.EncodeToString(raw)
			}
			body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
			req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
			req.SetPathValue("environment", "staging")
			req.Header.Set("Idempotency-Key", "migration-api")
			srv.promoteProjectEnvironment(rec, req, account)
			if scenario == "token_opt_in" {
				if rec.Code == http.StatusAccepted || rec.Code == http.StatusOK {
					t.Fatal("changed migration token was accepted")
				}
				return
			}
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
			h, err := store.ProjectEnvironmentPromotionHealth(ctx, account.ID, job.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if h.Phase != "migrating" || h.Migration.TaskID == "" || len(h.Evidence) != 0 {
				t.Fatalf("probed before migration: %+v", h)
			}
			taskID := h.Migration.TaskID
			// Admission switches do not strand already admitted work after a restart.
			srv.WithPromotionMigrationsEnabled(false)
			run()
			h, err = store.ProjectEnvironmentPromotionHealth(ctx, account.ID, job.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if h.Migration.TaskID != taskID {
				t.Fatal("worker restart replayed task")
			}
			at := time.Now().UTC().Add(time.Millisecond)
			task, err := store.ClaimNextAppTask(ctx, "migration-api", at, time.Minute)
			mustSnapshotPromotionAPI(t, err)
			values, err := store.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, task.DeploymentID)
			mustSnapshotPromotionAPI(t, err)
			fence, err := state.NewRuntimeAppConfigFence(values)
			mustSnapshotPromotionAPI(t, err)
			mustSnapshotPromotionAPI(t, store.BindProjectMigrationTaskRuntime(ctx, task, fence))
			node, nodeErr := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			mustSnapshotPromotionAPI(t, nodeErr)
			_, nodeErr = store.RegisterPromotionMigrationRuntime(ctx, task, node.ID)
			mustSnapshotPromotionAPI(t, nodeErr)
			_, err = store.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, at.Add(time.Millisecond))
			mustSnapshotPromotionAPI(t, err)
			zero := 0
			_, err = store.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, FinishedAt: at.Add(2 * time.Millisecond)})
			mustSnapshotPromotionAPI(t, err)
			run()
			run()
			now = now.Add(api.ProjectReleaseVerificationInterval)
			run()
			run()
			if scenario == "rollback" {
				srv.routeProbes = promotionHealthAPISender{fail: true, baseline: baseline.Members[0].DeploymentID}
			}
			run()
			now = now.Add(api.ProjectReleaseVerificationInterval)
			run()
			run()
			p, _, err := store.ProjectEnvironmentPromotionByID(ctx, account.ID, project.Slug, "staging", job.PromotionID)
			mustSnapshotPromotionAPI(t, err)
			if scenario == "success" && p.Status != "succeeded" || scenario == "rollback" && (p.Status != "failed" || p.RollbackStatus != "rolled_back") {
				t.Fatalf("unexpected completion: %+v", p)
			}
			h, err = store.ProjectEnvironmentPromotionHealth(ctx, account.ID, p.ID)
			mustSnapshotPromotionAPI(t, err)
			if h.Migration.TaskID != taskID || h.Migration.Status != "succeeded" {
				t.Fatal("rollback discarded migration receipt")
			}
		})
	}
}
