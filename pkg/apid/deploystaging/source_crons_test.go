package deploystaging

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestSourceCronPreparationActivation(t *testing.T) {
	for _, mode := range []string{"ready", "failed", "operator-edit", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore()
			acct, err := store.CreateAccount(t.Context(), "source-cron@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(acct.Plan)
			existing, err := store.CreateCronIfUnderQuota(t.Context(), app.ID, "0 * * * *", "/existing", false, limits)
			if err != nil {
				t.Fatal(err)
			}
			manifest := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{
				{App: app.Slug, Kind: gregalemanifest.TriggerKindCron, Schedule: "0 * * * *", Path: "/existing"},
				{App: app.Slug, Kind: gregalemanifest.TriggerKindCron, Schedule: "0 3 * * *", Path: "/new"},
				{App: app.Slug, Kind: gregalemanifest.TriggerKindCron, Schedule: "0 3 * * *", Path: "/new"},
				{App: "another", Kind: gregalemanifest.TriggerKindCron, Schedule: "0 4 * * *", Path: "/other"},
			}}
			ctx, intent, err := PrepareSourceCrons(t.Context(), store, acct, app, manifest)
			if err != nil || intent == nil {
				t.Fatalf("prepare=%v err=%v", intent, err)
			}
			if len(intent.Desired) != 2 || intent.Desired[0].ID != existing.ID || intent.Desired[0].Enabled {
				t.Fatalf("additive intent=%+v", intent)
			}
			assertCount := func(want int) {
				t.Helper()
				rows, err := store.ListCronsForApp(t.Context(), app.ID)
				if err != nil || len(rows) != want {
					t.Fatalf("crons=%+v err=%v want=%d", rows, err, want)
				}
			}
			assertCount(1)
			duplicateCtx, duplicate, err := PrepareSourceCrons(ctx, store, acct, app, manifest)
			if err != nil || duplicate != nil || state.DeploymentSubmissionAttempt(duplicateCtx) != intent.SessionID {
				t.Fatalf("duplicate=%v err=%v", duplicate, err)
			}
			if mode == "rejected" {
				intent.Operation = "cancel"
				if _, err := store.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: *intent}); err != nil {
					t.Fatal(err)
				}
				assertCount(1)
				return
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub, TrafficPercent: 100})
			if err != nil {
				t.Fatal(err)
			}
			if receipt, err := store.DeploymentSubmission(t.Context(), app.ID, intent.SessionID); err != nil || receipt.DeploymentID != dep.ID {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if changes, err := store.ReconcileDeploymentCrons(t.Context()); err != nil || len(changes) != 0 {
				t.Fatalf("pending changes=%v err=%v", changes, err)
			}
			assertCount(1)
			status := state.DeployLive
			if mode == "failed" {
				status = state.DeployFailed
			}
			if mode == "operator-edit" {
				if _, err := store.CreateCronIfUnderQuota(t.Context(), app.ID, "0 5 * * *", "/operator", true, limits); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, status, ""); err != nil {
				t.Fatal(err)
			}
			changes, err := store.ReconcileDeploymentCrons(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			count := 1
			if mode == "ready" {
				want = 1
				count = 2
			}
			if mode == "operator-edit" {
				count = 2
			}
			if len(changes) != want {
				t.Fatalf("changes=%v", changes)
			}
			assertCount(count)
			changes, err = store.ReconcileDeploymentCrons(t.Context())
			if err != nil || len(changes) != 0 {
				t.Fatalf("repeat changes=%v err=%v", changes, err)
			}
		})
	}
}

func TestSourcePreparationPreservesLegacyJournal(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "legacy-cron@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(acct.Plan)
	r := api.DeploymentCronStagingRequest{SessionID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Operation: "stage", ExpectedAppID: app.ID, Before: []api.DeploymentCronConfig{}, Desired: []api.DeploymentCronConfig{{ID: "bbbbbbbb-bbbb-4bbb-abbb-bbbbbbbbbbbb", Schedule: "0 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}}}
	if _, err := store.ApplyDeploymentCronStaging(t.Context(), app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: r, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	ctx := state.ContextWithDeploymentSubmission(t.Context(), r.SessionID)
	manifest := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{{App: app.Slug, Kind: gregalemanifest.TriggerKindCron, Schedule: "0 * * * *", Path: "/tick"}}}
	if _, intent, err := PrepareSourceCrons(ctx, store, acct, app, manifest); err != nil || intent != nil {
		t.Fatalf("intent=%v err=%v", intent, err)
	}
	r.Operation = "restore"
	if _, err := store.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: r, Limits: limits}); err != nil {
		t.Fatalf("legacy recovery fenced: %v", err)
	}
}
