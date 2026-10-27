// adr: 20261020150000
package state

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

func TestReleaseTaskRunsDeploymentRelease(t *testing.T) {
	dep := Deployment{ID: uuid.NewString(), AppID: uuid.NewString(), ReleaseCommand: []string{"bin/migrate", "--up"}}
	envelope, err := rehearsalcommand.ContainedRelease(strings.Repeat("a", 32), dep.ID, dep.ReleaseCommand, false)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := rehearsalcommand.ContainedRelease(strings.Repeat("a", 32), dep.ID, []string{"bin/migrate", "--down"}, false)
	release := AppTask{Kind: AppTaskKindRelease, AppID: dep.AppID, DeploymentID: dep.ID}
	for _, tc := range []struct {
		name string
		task func(AppTask) AppTask
		want bool
	}{
		{"verbatim release", func(a AppTask) AppTask { a.Command = dep.ReleaseCommand; return a }, true},
		{"contained release envelope", func(a AppTask) AppTask { a.Command = envelope; return a }, true},
		{"envelope of another command", func(a AppTask) AppTask { a.Command = other; return a }, false},
		{"envelope as shell", func(a AppTask) AppTask { a.Command, a.CommandShell = envelope, true; return a }, false},
		{"manual task", func(a AppTask) AppTask { a.Command, a.Kind = envelope, AppTaskKindManual; return a }, false},
		{"another deployment", func(a AppTask) AppTask { a.Command, a.DeploymentID = envelope, uuid.NewString(); return a }, false},
	} {
		if got := ReleaseTaskRunsDeploymentRelease(tc.task(release), dep); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A contained candidate's release is admitted only as its own pinned
// contained command; the raw release, another command, or a manual envelope
// on a not-yet-live deployment are refused durably (ADR-20261020150000).
func TestContainedReleaseAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var store Store = NewMemStore()
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(ctx, pool); err != nil {
					t.Fatal(err)
				}
				store = NewPgStore(pool)
			}
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@release.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour)
			app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "release-" + uuid.NewString()[:8], Type: AppTypeApp, Runtime: "node22", RAMMB: 256,
				PreviewOfSlug: "source", PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
			body, _, err := api.NormalizeScenarioSimulationPlan(plan, []string{"primary"})
			if err != nil {
				t.Fatal(err)
			}
			run := strings.Repeat("e", 32)
			if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: ScenarioWebhookCaptureMode, NetworkMode: ScenarioNetworkContained, SimulationJSON: string(body)}}); err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:release", Status: DeploySnapshotting, ReleaseCommand: []string{"bin/migrate", "--up"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/tmp/release.ext4", "apps/release/candidate.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			envelope, err := rehearsalcommand.ContainedRelease(run, dep.ID, dep.ReleaseCommand, false)
			if err != nil {
				t.Fatal(err)
			}
			other, _ := rehearsalcommand.ContainedRelease(run, dep.ID, []string{"bin/migrate", "--down"}, false)
			base := CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: AppTaskKindRelease, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}
			for _, tc := range []struct {
				name   string
				mutate func(*CreateAppTaskParams)
			}{
				{"raw release", func(p *CreateAppTaskParams) { p.Command, p.MaxOutputBytes = dep.ReleaseCommand, 0 }},
				{"envelope of another command", func(p *CreateAppTaskParams) { p.Command = other }},
				{"manual envelope before live", func(p *CreateAppTaskParams) { p.Command, p.Kind = envelope, AppTaskKindManual }},
			} {
				params := base
				tc.mutate(&params)
				if _, err := store.CreateAppTask(ctx, params); !IsScenarioCommandAdmissionRefused(err) {
					t.Fatalf("%s admitted: %v", tc.name, err)
				}
			}
			params := base
			params.Command = envelope
			task, err := store.CreateAppTask(ctx, params)
			if err != nil {
				t.Fatalf("pinned contained release refused: %v", err)
			}
			dep, err = store.DeploymentByID(ctx, dep.ID)
			if err != nil || !ReleaseTaskRunsDeploymentRelease(task, dep) {
				t.Fatalf("admitted release does not satisfy its deployment: %+v %v", task, err)
			}
		})
	}
}
