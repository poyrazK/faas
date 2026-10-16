package deploystaging

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectCronReplacementAndPendingReuse(t *testing.T) {
	for _, outcome := range []string{"live", "failed", "operator"} {
		t.Run(outcome, func(t *testing.T) {
			store := state.NewMemStore()
			acct, err := store.CreateAccount(t.Context(), "project@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(acct.Plan)
			old, err := store.CreateCronIfUnderQuota(t.Context(), app.ID, "0 * * * *", "/", true, limits)
			if err != nil {
				t.Fatal(err)
			}
			desired := []api.DeploymentCronConfig{{Schedule: "0 3 * * *", Path: "/", Enabled: true}}
			plan, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, desired)
			if err != nil || !plan.Changed {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if _, err := store.ApplyDeploymentCronStaging(t.Context(), app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: plan.Request, Limits: limits}); err != nil {
				t.Fatal(err)
			}
			ctx := state.ContextWithDeploymentSubmission(t.Context(), plan.Request.SessionID)
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, TrafficPercent: 100})
			if err != nil {
				t.Fatal(err)
			}
			again, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, desired)
			if err != nil || again.Reuse == nil || again.Reuse.ID != dep.ID {
				t.Fatalf("reuse=%+v err=%v", again, err)
			}
			// Reverting to the active declarations must fence the older pending intent.
			revert, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, []api.DeploymentCronConfig{{Schedule: old.Schedule, Path: old.Path, Enabled: true}})
			if err != nil || !revert.Changed || revert.Reuse != nil {
				t.Fatalf("revert=%+v err=%v", revert, err)
			}
			rows, err := store.ListCronsForApp(t.Context(), app.ID)
			if err != nil || len(rows) != 1 || rows[0].ID != old.ID {
				t.Fatalf("early mutation: %+v %v", rows, err)
			}
			status := state.DeployLive
			if outcome == "failed" {
				status = state.DeployFailed
			}
			if outcome == "operator" {
				if _, err := store.CreateCronIfUnderQuota(t.Context(), app.ID, "0 5 * * *", "/operator", true, limits); err != nil {
					t.Fatal(err)
				}
				edited, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, desired)
				if err != nil || edited.Reuse != nil {
					t.Fatalf("operator edit reused: %+v %v", edited, err)
				}
			}
			if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, status, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReconcileDeploymentCrons(t.Context()); err != nil {
				t.Fatal(err)
			}
			rows, err = store.ListCronsForApp(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "live" && (len(rows) != 1 || rows[0].Schedule != desired[0].Schedule) {
				t.Fatalf("replacement=%+v", rows)
			}
			if outcome != "live" {
				found := false
				for _, row := range rows {
					found = found || row.ID == old.ID
				}
				if !found {
					t.Fatalf("lost active schedule: %+v", rows)
				}
			}
		})
	}
}

func TestProjectCronRemovalAndStableIdentity(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "remove@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(acct.Plan)
	old, err := store.CreateCronWithOptions(t.Context(), app.ID, "0 * * * *", "/", true, state.CronOptions{Timezone: "Europe/Istanbul", SkipIfRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	command, err := store.CreateCronWithOptions(t.Context(), app.ID, "0 * * * *", "", true, state.CronOptions{Command: []string{"echo", "tick"}})
	if err != nil {
		t.Fatal(err)
	}
	same, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, []api.DeploymentCronConfig{{Schedule: old.Schedule, Path: old.Path, Enabled: old.Enabled}})
	if err != nil || same.Changed || same.Request.Desired[0].ID != old.ID || same.Request.Desired[0].Timezone != "Europe/Istanbul" || !same.Request.Desired[0].SkipIfRunning {
		t.Fatalf("same=%+v %v", same, err)
	}
	disabled, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, []api.DeploymentCronConfig{{Schedule: old.Schedule, Path: old.Path, Enabled: false}})
	if err != nil || !disabled.Changed || disabled.Request.Desired[0].ID != old.ID {
		t.Fatalf("disabled=%+v %v", disabled, err)
	}
	removed, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, nil)
	if err != nil || !removed.Changed || len(removed.Request.Desired) != 0 {
		t.Fatalf("removed=%+v %v", removed, err)
	}
	if _, err := store.ApplyDeploymentCronStaging(t.Context(), app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: removed.Request, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), removed.Request.SessionID), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileDeploymentCrons(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListCronsForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != command.ID {
		t.Fatalf("removal=%+v %v", rows, err)
	}
}

func TestProjectCronUnchangedPendingDeploymentDoesNotSelectBuild(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "unchanged@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, nil)
	if err != nil || plan.Changed {
		t.Fatalf("initial plan=%+v err=%v", plan, err)
	}
	limits, _ := api.LimitsFor(acct.Plan)
	if _, err := store.ApplyDeploymentCronStaging(t.Context(), app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: plan.Request, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), plan.Request.SessionID), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	again, err := PlanProjectCrons(t.Context(), store, acct.ID, app.ID, nil)
	if err != nil || again.Changed || again.Reuse == nil || again.Reuse.ID != dep.ID {
		t.Fatalf("unchanged pending plan=%+v err=%v", again, err)
	}
}
