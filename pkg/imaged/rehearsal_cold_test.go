package imaged

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestColdStartActivationCannotBeBorrowedByOrdinaryDeployments(t *testing.T) {
	for _, mode := range []string{"enrolled", "ordinary", "expired", "instance_supplied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@cold.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour)
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "cold-" + uuid.NewString()[:8], RAMMB: 128, PreviewOfSlug: "primary", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "ordinary" {
				sim, _, e := api.NormalizeScenarioSimulationPlan(api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}, []string{"primary"})
				if e != nil {
					t.Fatal(e)
				}
				spec, _, e := rehearsalcold.Normalize(rehearsalcold.Spec{Version: 1, WithinMS: 1000}, []string{"primary"})
				if e != nil {
					t.Fatal(e)
				}
				if e = store.RegisterScenarioTestMembers(ctx, account.ID, strings.ReplaceAll(uuid.NewString(), "-", ""), []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", NetworkMode: "contained", WebhookMode: "capture", SimulationJSON: string(sim), ColdStartJSON: string(spec)}}); e != nil {
					t.Fatal(e)
				}
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:cold", Status: state.DeploySnapshotting})
			if err != nil {
				t.Fatal(err)
			}
			ready := deploymentReadyPayload{DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeRequest, NoSnapshotReason: "rehearsal_cold_start"}
			if mode == "instance_supplied" {
				ready.InstanceID = uuid.NewString()
			}
			h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
			if mode == "expired" {
				app.PreviewExpiresAt = new(time.Time)
				if err = h.validateColdStartActivation(ctx, app, dep, ready); err == nil {
					t.Fatal("expired preview borrowed cold activation")
				}
				return
			}
			err = h.handleDeploymentReady(ctx, ready)
			if (err == nil) != (mode == "enrolled") {
				t.Fatalf("activation: %v", err)
			}
			actual, e := store.DeploymentByID(ctx, dep.ID)
			if e != nil {
				t.Fatal(e)
			}
			if (actual.Status == state.DeployLive) != (mode == "enrolled") {
				t.Fatal("unverified cold enrollment changed deployment state")
			}
		})
	}
}
