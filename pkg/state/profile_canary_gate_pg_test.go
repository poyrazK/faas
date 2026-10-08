package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 801 — qualify the durable gate reader and atomic traffic/audit transitions.
// These fixtures represent retained stage certificates; qualification and
// distinct-window counting are separately exercised by the gate state tests.
func TestProfileCanaryGatePostgresTrafficAndAudit(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	for _, mode := range []string{"passed", "override", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "gate-" + uuid.NewString(), Runtime: "node24", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			stable, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable", Scope: "default"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
				t.Fatal(err)
			}
			candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: "default", CanaryPreset: "balanced", CanaryTotalSteps: 4, TrafficPercent: 10})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC().Truncate(time.Microsecond).Add(-5 * time.Minute)
			if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent=90,rollout_state='complete' WHERE id=$1`, stable.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE deployments SET status='live',traffic_percent=10,rollout_state='rolling_out',canary_step_started_at=$2 WHERE id=$1`, candidate.ID, started); err != nil {
				t.Fatal(err)
			}
			options := api.DefaultProfileRegressionOptions()
			options.Routes = []string{"POST /checkout"}
			zero := int64(0)
			policy, err := store.SaveProfileDeploymentPolicy(ctx, account.ID, app.ID, api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &zero, Config: api.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: "node24", WindowSeconds: 60, Options: options, CanaryGate: &api.ProfileCanaryGatePolicy{Confirmations: 2, TimeoutSeconds: 1800, OnTimeout: "hold", AutoRollback: mode == "rollback"}}})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := store.ReadProfileCanaryGate(ctx, account.ID, app.ID, candidate.ID)
			if err != nil || decision.Status != "collecting" || decision.StableDeploymentID != stable.ID {
				t.Fatalf("initial gate: %+v %v", decision, err)
			}
			params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 50, ProfileGateDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "account:" + account.ID}}
			if _, _, err := store.AdvanceCanary(ctx, candidate.ID, params); err == nil {
				t.Fatal("collecting gate advanced")
			} else {
				var blocked *state.ProfileGateBlockedError
				if !errors.As(err, &blocked) {
					t.Fatal(err)
				}
			}
			completed := time.Now().UTC()
			baseline := api.ProfileQuery{DeploymentID: stable.ID, Runtime: "node24", Start: completed.Add(-time.Minute), End: completed}
			query := baseline
			query.DeploymentID = candidate.ID
			status, gateStatus := "regressed", "regressed"
			if mode == "passed" {
				status, gateStatus = "no_regression_detected", "passed"
			}
			signal := api.CanaryProfileSignal{Mode: "gate", Status: status, CanaryStep: 0, CanaryStepStartedAt: started, PolicyRevision: policy.Revision, WindowSeconds: 60, Options: options, Baseline: &baseline, Candidate: &query, CompletedAt: &completed, Gate: &api.ProfileCanaryGateState{Policy: *policy.Config.CanaryGate, Status: gateStatus, Reason: "Qualified retained stage evidence", Deadline: policy.UpdatedAt.Add(30 * time.Minute), Windows: 2}}
			body, err := json.Marshal(signal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO profile_canary_checks(deployment_id,app_id,account_id,canary_step,canary_step_started_at,policy_revision,data,status,completed_at) VALUES($1,$2,$3,0,$4,$5,$6,$7,$8)`, candidate.ID, app.ID, account.ID, started, policy.Revision, body, status, completed); err != nil {
				t.Fatal(err)
			}
			decision, err = store.ReadProfileCanaryGate(ctx, account.ID, app.ID, candidate.ID)
			if err != nil || decision.Status != gateStatus || decision.Signal == nil {
				t.Fatalf("retained gate: %+v %v", decision, err)
			}
			if mode == "override" {
				params.ProfileGateOverride = &api.ProfileGateOverride{ExpectedPolicyRevision: policy.Revision, Reason: "Reviewed the retained regression and accepted its cost."}
			}
			if mode == "rollback" {
				params.ProfileGateRollback = true
				params.RequireSafeReleaseLease = true
				params.Audit.Actor = "meterd:canary_progression"
				if err := store.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
					t.Fatal(err)
				}
			}
			got, auditID, err := store.AdvanceCanary(ctx, candidate.ID, params)
			if err != nil || auditID == 0 {
				t.Fatalf("transition: %+v %d %v", got, auditID, err)
			}
			prior, err := store.DeploymentByID(ctx, stable.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "rollback" {
				if got.TrafficPercent != 0 || got.RolloutState != "aborted" || prior.TrafficPercent != 100 || decision.Status != "rolled_back" {
					t.Fatalf("rollback: %+v %+v %+v", got, prior, decision)
				}
			} else if got.TrafficPercent != 50 || got.CanaryStep != 1 || prior.TrafficPercent != 50 {
				t.Fatalf("advance: %+v %+v", got, prior)
			}
			audits, err := store.ListDeploymentAudit(ctx, candidate.ID, 20)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, audit := range audits {
				if audit.ID == auditID {
					var data struct {
						Gate api.ProfileCanaryGateDecision `json:"profile_gate"`
					}
					if err := json.Unmarshal(audit.Data, &data); err != nil {
						t.Fatal(err)
					}
					found = data.Gate.Status == decision.Status && data.Gate.PolicyRevision == policy.Revision && audit.Actor == params.Audit.Actor
				}
			}
			if !found {
				t.Fatal("transition and gate decision were not audited together")
			}
		})
	}
}
