// adr: 20261022194510
package state_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/state"
)

// A contained rehearsal app's command-cron fire-now becomes the run's pinned
// contained command: its own argv on the live deployment, no retries. Any
// other cron-kind envelope is refused durably.
func TestContainedCommandCronFireNow(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(ctx, pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@cron.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour)
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "cron-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256,
				PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend",
				Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
			body, _, err := api.NormalizeScenarioSimulationPlan(plan, []string{"primary"})
			if err != nil {
				t.Fatal(err)
			}
			run := strings.Repeat("c", 32)
			if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary",
				WebhookMode: state.ScenarioWebhookCaptureMode, NetworkMode: state.ScenarioNetworkContained, SimulationJSON: string(body)}}); err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:cron", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/tmp/cron.ext4", "apps/cron/live.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			cron, err := store.CreateCronWithOptions(ctx, app.ID, "0 0 1 1 *", "", true, state.CronOptions{Command: []string{"bin/sync", "--once"}, RetryMax: 2, RetryBackoffSeconds: 30})
			if err != nil {
				t.Fatal(err)
			}

			// A cron-kind envelope of another command is refused.
			other, _ := rehearsalcommand.ContainedRelease(run, dep.ID, []string{"bin/drop"}, false)
			if _, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, CronID: cron.ID,
				Kind: state.AppTaskKindCron, Command: other, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}); !state.IsScenarioCommandAdmissionRefused(err) {
				t.Fatalf("cron envelope of another command admitted: %v", err)
			}

			requestID, err := store.InsertFireNowRequest(ctx, cron.ID, account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := store.ClaimPendingFireNowRequest(ctx); err != nil || claimed.ID != requestID {
				t.Fatalf("claim fire-now = %+v %v", claimed, err)
			}
			task, err := store.CreateManualCronAppTaskForFireNow(ctx, requestID, time.Now().UTC())
			if err != nil {
				t.Fatalf("contained command-cron fire-now refused: %v", err)
			}
			spec, err := rehearsalcommand.Decode(task.Command)
			if err != nil || task.Kind != state.AppTaskKindCron || task.CronID != cron.ID || task.DeploymentID != dep.ID || task.CommandShell || task.RetryMax != 0 ||
				task.MaxOutputBytes != rehearsalcommand.MaxReceiptBytes || spec.RunID != run || !slices.Equal(spec.Command, []string{"bin/sync", "--once"}) ||
				len(spec.Env) != 0 || len(spec.Files) != 0 {
				t.Fatalf("contained cron task = %+v (%+v) %v", task, spec, err)
			}
			if runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, ""); err != nil || len(runs) != 1 || runs[0].ID != task.ID {
				t.Fatalf("cron runs = %+v %v", runs, err)
			}
		})
	}
}
