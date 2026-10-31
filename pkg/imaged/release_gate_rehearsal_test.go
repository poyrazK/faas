// adr: 20261020150000
package imaged

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedContainedRelease enrolls a fresh preview app in a contained rehearsal
// and gives it a snapshotting candidate that declares a release command.
func seedContainedRelease(t *testing.T, commandsEnabled, simulation bool) (releaseGateFixture, string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@release.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "release-" + uuid.NewString()[:8], RAMMB: 256, Status: state.AppActive,
		PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	member := state.ScenarioTestMember{AppID: app.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode, NetworkMode: state.ScenarioNetworkContained}
	if simulation {
		plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
		body, _, err := api.NormalizeScenarioSimulationPlan(plan, []string{"primary"})
		if err != nil {
			t.Fatal(err)
		}
		member.SimulationJSON = string(body)
	}
	run := strings.Repeat("d", 32)
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []state.ScenarioTestMember{member}); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate",
		Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate", "--up"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, "/tmp/release.ext4", "apps/release/candidate.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if candidate, err = store.DeploymentByID(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	handler := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger()).WithReleasePhaseEnabled(true).WithRehearsalCommandsEnabled(commandsEnabled)
	return releaseGateFixture{store: store, handler: handler, account: account, app: app, candidate: candidate}, run
}

func TestContainedRehearsalReleaseRunsAsPinnedContainedCommand(t *testing.T) {
	fx, run := seedContainedRelease(t, true, true)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatal(err)
	}
	task, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := rehearsalcommand.Decode(task.Command)
	if err != nil || task.Kind != state.AppTaskKindRelease || task.CommandShell || task.MaxOutputBytes != rehearsalcommand.MaxReceiptBytes ||
		spec.RunID != run || !rehearsalcommand.IsContainedReleaseFor(task.Command, fx.candidate.ID, fx.candidate.ReleaseCommand, false) {
		t.Fatalf("release task = %+v (spec %+v, %v); want the pinned contained release", task, spec, err)
	}
	dep, err := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if err != nil || dep.Status != state.DeploySnapshotting {
		t.Fatalf("candidate = %+v, %v; want it waiting on its release", dep, err)
	}
	if !state.ReleaseTaskRunsDeploymentRelease(task, dep) {
		t.Fatal("the prime gate would not accept the contained release")
	}
}

func TestContainedRehearsalReleaseFailsClosedWhenItCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		commandsEnabled, simulation bool
	}{
		{"command execution disabled", false, true},
		{"run refuses contained commands", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, _ := seedContainedRelease(t, tc.commandsEnabled, tc.simulation)
			ctx := context.Background()
			if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
				t.Fatal(err)
			}
			dep, err := fx.store.DeploymentByID(ctx, fx.candidate.ID)
			if err != nil || dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeReleasePhaseUnavailable {
				t.Fatalf("candidate = %+v, %v; want failed with %s instead of a stalled deployment", dep, err, api.CodeReleasePhaseUnavailable)
			}
			if task, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err == nil {
				t.Fatalf("an uncontained release task was admitted: %+v", task)
			}
		})
	}
}
