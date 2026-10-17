package sched

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

type coldRoutingVMM struct {
	*fakeVMM
	owner *rehearsalrecorder.Session
	calls int
	mode  string
}

func (v *coldRoutingVMM) RetireRehearsalColdOnNode(ctx context.Context, _, _, prep string) error {
	v.calls++
	if v.mode == "missing_owner" {
		return errors.New("original owner lost")
	}
	if v.mode == "missing_receipt" {
		return nil
	}
	if err := v.owner.PrepareColdRetirement(ctx, prep); err != nil {
		return err
	}
	if err := v.owner.FinishColdRetirement(ctx, prep); err != nil {
		return err
	}
	if v.mode == "lost_reply" {
		return errors.New("reply lost after cleanup receipt")
	}
	return nil
}

func seedSchedulerCold(t *testing.T, store *state.MemStore) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@cold.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "cold-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "primary", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	sim, _, err := api.NormalizeScenarioSimulationPlan(api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	spec, _, err := rehearsalcold.Normalize(rehearsalcold.Spec{Version: 1, WithinMS: 1000}, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RegisterScenarioTestMembers(ctx, account.ID, strings.ReplaceAll(uuid.NewString(), "-", ""), []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", NetworkMode: "contained", WebhookMode: "capture", SimulationJSON: string(sim), ColdStartJSON: string(spec)}}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:cold", RootfsKey: "cold-layer.ext4", Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	return app, dep
}

func TestColdStartPrimeDefersExecutionAndStillVerifiesArtifact(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "bad_artifact"}[reject], func(t *testing.T) {
			store := state.NewMemStore()
			app, dep := seedSchedulerCold(t, store)
			vmm := &fakeVMM{}
			notifier := &fakeNotifier{}
			verifier := &initialBurstVerifier{}
			if reject {
				verifier.err = errors.New("signature check failed")
			}
			e := newEngine(t, store, vmm, notifier, "1.10.0").WithVerifier(verifier)
			err := e.Prime(t.Context(), app.ID, dep.ID)
			if (err != nil) != reject {
				t.Fatalf("prime: %v", err)
			}
			instances, err := store.ListInstancesForApp(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(instances) != 0 || verifier.calls.Load() != 1 {
				t.Fatal("cold prime executed serving code or skipped signature verification")
			}
			notifier.mu.Lock()
			defer notifier.mu.Unlock()
			seen := false
			for _, event := range notifier.events {
				seen = seen || strings.Contains(event.payload, `"no_snapshot_reason":"rehearsal_cold_start"`)
			}
			if seen == reject {
				t.Fatal("activation did not match artifact verification")
			}
		})
	}
}

func TestColdStartSchedulerNeverRepeatsUncertainRetirement(t *testing.T) {
	for _, mode := range []string{"complete", "lost_reply", "receipt_recovery", "missing_owner", "missing_receipt", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore()
			app, dep := seedSchedulerCold(t, store)
			member, err := store.ScenarioTestMemberByApp(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			ins, err := store.CreateInstance(t.Context(), app.ID, dep.ID, "running", 128, "node-1", "")
			if err != nil {
				t.Fatal(err)
			}
			owner, err := rehearsalrecorder.Start(t.Context(), t.Context(), store, rehearsalrecorder.Identity{AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, InstanceID: ins.ID, RunID: member.RunID})
			if err != nil {
				t.Fatal(err)
			}
			owner.Activate()
			t.Cleanup(owner.Stop)
			prep, err := store.BeginScenarioColdPreparation(t.Context(), app.AccountID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			parked := state.AppEvictedCold
			if _, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
				t.Fatal(err)
			}
			vmm := &coldRoutingVMM{fakeVMM: &fakeVMM{}, owner: owner, mode: mode}
			var routed RoutedVMM = vmm
			if mode == "unsupported" {
				routed = &fakeVMM{}
			}
			if mode == "receipt_recovery" {
				if _, err = store.ClaimScenarioColdPreparation(t.Context(), app.ID); err != nil {
					t.Fatal(err)
				}
				if err = vmm.RetireRehearsalColdOnNode(t.Context(), ins.NodeID, ins.ID, prep.ID); err != nil {
					t.Fatal(err)
				}
				vmm.calls = 0
			}
			e := newEngine(t, store, routed, &fakeNotifier{}, "1.10.0")
			_, err = e.ParkApp(t.Context(), app.ID)
			valid := mode == "complete" || mode == "lost_reply" || mode == "receipt_recovery"
			if (err == nil) != valid {
				t.Fatalf("park: %v", err)
			}
			i, err := store.InstanceByID(t.Context(), ins.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (i.State == "stopped") != valid {
				t.Fatal("uncertain cleanup retired scheduler row")
			}
			_, _ = e.ParkApp(t.Context(), app.ID)
			want := 1
			if mode == "receipt_recovery" || mode == "unsupported" {
				want = 0
			}
			if vmm.calls != want {
				t.Fatalf("retirement calls=%d want=%d", vmm.calls, want)
			}
		})
	}
}
