package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestDeploymentConfigurationMemory(t *testing.T) {
	testDeploymentConfiguration(t, func(t *testing.T) state.Store { return state.NewMemStore() })
}
func testDeploymentConfiguration(t *testing.T, newStore func(*testing.T) state.Store) {
	for _, mode := range []string{"ready", "operator-edit", "quota", "identity-conflict", "late-canceled-intent", "failed", "superseded-intent", "partial-traffic", "rollout", "dark", "preview"} {
		t.Run(mode, func(t *testing.T) {
			store := newStore(t)
			app, p, command := cronStageFixture(t, store)
			staging := store.(state.DeploymentCronStagingStore)
			reader := store.(state.DeploymentConfigurationStore)
			worker := store.(state.DeploymentCronActivationStore)
			p.Request.Operation = "prepare"
			if mode == "identity-conflict" {
				p.Request.Desired[1].ID = command.ID
			}
			if _, err := staging.ApplyDeploymentCronStaging(t.Context(), app.ID, p); err != nil {
				t.Fatal(err)
			}
			if mode == "late-canceled-intent" {
				cancel := p
				cancel.Request.Operation = "cancel"
				if _, err := staging.ApplyDeploymentCronStaging(t.Context(), app.ID, cancel); err != nil {
					t.Fatal(err)
				}
			}
			candidate := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 100}
			if mode == "partial-traffic" {
				candidate.TrafficPercent = 50
				candidate.TrafficPercentExplicit = true
			}
			if mode == "rollout" {
				candidate.CanaryTotalSteps = 2
				candidate.RolloutState = "rolling_out"
			}
			if mode == "dark" {
				candidate.TrafficPercent = 0
				candidate.TrafficPercentExplicit = true
			}
			if mode == "preview" {
				candidate.Scope = "preview"
			}
			dep, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), p.Request.SessionID), candidate)
			if err != nil {
				t.Fatal(err)
			}
			before, err := staging.DeploymentCronStagingSnapshot(t.Context(), p.AccountID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			status, err := reader.DeploymentConfiguration(t.Context(), p.AccountID, dep.ID)
			if err != nil || status.Complete || status.Crons.SessionID != p.Request.SessionID || status.Crons.Revision != p.Request.ExpectedRevision+1 {
				t.Fatalf("pending=%+v err=%v", status, err)
			}
			if _, err := reader.DeploymentConfiguration(t.Context(), "dddddddd-dddd-4ddd-addd-dddddddddddd", dep.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("ownership error=%v", err)
			}
			after, err := staging.DeploymentCronStagingSnapshot(t.Context(), p.AccountID, app.ID)
			if err != nil || after.Revision != before.Revision || !api.DeploymentCronsEqual(after.Crons, before.Crons) {
				t.Fatal("configuration read mutated schedules or intent")
			}
			expectedStatus, expectedReason := "activated", ""
			switch mode {
			case "late-canceled-intent":
				expectedStatus, expectedReason = "discarded", "activation_discarded"
			case "identity-conflict":
				expectedStatus, expectedReason = "conflicted", "identity_conflict"
			case "operator-edit":
				if _, err := store.CreateCron(t.Context(), app.ID, "0 5 * * *", "/operator", true); err != nil {
					t.Fatal(err)
				}
				expectedStatus, expectedReason = "conflicted", "cron_changed"
			case "quota":
				if err := store.UpdateAccountPlan(t.Context(), p.AccountID, api.PlanFree); err != nil {
					t.Fatal(err)
				}
				expectedStatus, expectedReason = "conflicted", "quota_changed"
			case "failed":
				expectedStatus, expectedReason = "discarded", "deployment_failed"
			case "superseded-intent":
				expectedStatus, expectedReason = "discarded", "intent_superseded"
			case "partial-traffic":
				expectedStatus, expectedReason = "pending", "awaiting_full_traffic"
			case "rollout":
				expectedStatus, expectedReason = "pending", "awaiting_rollout"
			case "dark", "preview":
				expectedStatus, expectedReason = "discarded", "non_production"
			}
			statusValue := state.DeployLive
			if mode == "failed" {
				statusValue = state.DeployFailed
			}
			if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, statusValue, ""); err != nil {
				t.Fatal(err)
			}
			prepareNext := func() {
				t.Helper()
				snapshot, err := staging.DeploymentCronStagingSnapshot(t.Context(), p.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				next := p
				next.Request = api.DeploymentCronStagingRequest{Operation: "prepare", SessionID: strings.Repeat("c", 32), ExpectedAppID: app.ID, ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision, Before: snapshot.Crons, Desired: snapshot.Crons}
				next.Limits = api.MustLimitsFor(api.PlanPro)
				if _, err := staging.ApplyDeploymentCronStaging(t.Context(), app.ID, next); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "superseded-intent" {
				prepareNext()
			} else {
				if _, err := worker.ReconcileDeploymentCrons(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			assertResult := func() {
				t.Helper()
				result, err := reader.DeploymentConfiguration(t.Context(), p.AccountID, dep.ID)
				if err != nil || result.Crons.Status != expectedStatus || result.Crons.Reason != expectedReason || result.Complete != (expectedStatus == "activated") {
					t.Fatalf("result=%+v err=%v want=%s/%s", result, err, expectedStatus, expectedReason)
				}
				if result.Crons.Message == "" || !result.Complete && result.Crons.Hint == "" {
					t.Fatalf("missing diagnostic=%+v", result)
				}
			}
			assertResult()
			// Replacing the app's current intent must retain terminal outcomes.
			if mode == "ready" || mode == "operator-edit" || mode == "quota" || mode == "failed" || mode == "late-canceled-intent" {
				if mode == "quota" {
					if err := store.UpdateAccountPlan(t.Context(), p.AccountID, api.PlanPro); err != nil {
						t.Fatal(err)
					}
				}
				prepareNext()
				assertResult()
			}
			deployments, err := store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
			if err != nil || len(deployments) != 1 {
				t.Fatalf("read created deployments=%v err=%v", deployments, err)
			}
		})
	}
}
