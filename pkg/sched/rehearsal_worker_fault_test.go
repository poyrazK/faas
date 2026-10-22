// adr: 20261017201012
package sched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type faultRoutingVMM struct {
	*fakeVMM
	owner *rehearsalrecorder.Session
	calls int
	mode  string
}

func (v *faultRoutingVMM) TerminateRehearsalWorkerOnNode(ctx context.Context, _, _, fault string) error {
	v.calls++
	if v.mode == "missing_owner" {
		return errors.New("original recorder unavailable")
	}
	if v.mode == "missing_receipt" {
		return nil
	}
	if err := v.owner.PrepareWorkerTermination(ctx, fault); err != nil {
		return err
	}
	if err := v.owner.FinishWorkerTermination(ctx, fault); err != nil {
		return err
	}
	if v.mode == "lost_reply" {
		return errors.New("RPC reply lost after durable receipt")
	}
	return nil
}

func seedSchedulerWorkerFault(t *testing.T, store *state.MemStore) (rehearsalwork.WorkerFault, *rehearsalrecorder.Session) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@fault.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	run := strings.ReplaceAll(uuid.NewString(), "-", "")
	expires := time.Now().Add(time.Hour)
	members := []state.ScenarioTestMember{}
	apps := map[string]state.App{}
	plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary"}, {Workload: "worker", Async: true}}, WorkerTermination: &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}}
	work, _, err := rehearsalwork.Normalize(plan, []string{"primary", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	sim, _, err := api.NormalizeScenarioSimulationPlan(api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}, []string{"primary", "worker"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"primary", "worker"} {
		a, e := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: name + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: name, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if e != nil {
			t.Fatal(e)
		}
		apps[name] = a
		members = append(members, state.ScenarioTestMember{AppID: a.ID, Workload: name, NetworkMode: "contained", WebhookMode: "capture", SimulationJSON: string(sim), ManagedWorkJSON: string(work)})
	}
	if err = store.RegisterScenarioTestMembers(ctx, account.ID, run, members); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: apps["worker"].ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:fault", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(ctx, dep.AppID, dep.ID, string(state.StateRunning), 128, "node-1", "")
	if err != nil {
		t.Fatal(err)
	}
	id := rehearsalrecorder.Identity{AccountID: account.ID, RunID: run, AppID: dep.AppID, DeploymentID: dep.ID, InstanceID: ins.ID}
	owner, err := rehearsalrecorder.Start(ctx, ctx, store, id)
	if err != nil {
		t.Fatal(err)
	}
	owner.Activate()
	t.Cleanup(owner.Stop)
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: dep.AppID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/import"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimInvocation(ctx, inv.ID, ins.ID, 60); err != nil {
		t.Fatal(err)
	}
	request, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := api.ScenarioSimulationRequest{AccountID: account.ID, AppID: dep.AppID, RunID: run, InstanceID: ins.ID, RecorderRequestID: request, Integration: "checkpoint", Method: "POST", Path: "/after-commit", Body: []byte(`{}`)}
	decision, err := store.BeginScenarioSimulation(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishScenarioSimulation(ctx, req, decision.Attempt.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	checkpointCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	joined := make(chan struct{})
	var connection *rehearsalrecorder.Connection
	connection, err = owner.Attach(func() { <-joined; connection.Detach(false) })
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = owner.HoldWorkerCheckpoint(checkpointCtx, decision.WorkerFaultID); close(joined) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f, e := store.ScenarioWorkerFault(ctx, decision.WorkerFaultID)
		if e != nil {
			t.Fatal(e)
		}
		if f.State == "ready" {
			return f, owner
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("checkpoint was not held")
	return rehearsalwork.WorkerFault{}, nil
}

func TestSchedulerWorkerTerminationUsesReceiptWithoutRepeatingKill(t *testing.T) {
	for _, mode := range []string{"complete", "lost_reply", "receipt_recovery", "missing_owner", "missing_receipt", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			store := state.NewMemStore()
			fault, owner := seedSchedulerWorkerFault(t, store)
			v := &faultRoutingVMM{fakeVMM: &fakeVMM{}, owner: owner, mode: mode}
			var routed RoutedVMM = v
			if mode == "unsupported" {
				routed = &fakeVMM{}
			}
			engine := newEngine(t, store, routed, &fakeNotifier{}, "1.10.0")
			if mode == "receipt_recovery" {
				if _, err := store.ClaimScenarioWorkerFault(t.Context(), fault.ID); err != nil {
					t.Fatal(err)
				}
				if err := v.TerminateRehearsalWorkerOnNode(t.Context(), "node-1", fault.InstanceID, fault.ID); err != nil {
					t.Fatal(err)
				}
				v.calls = 0
			}
			err := engine.RunScenarioWorkerFault(t.Context(), fault.ID)
			valid := mode == "complete" || mode == "lost_reply" || mode == "receipt_recovery"
			if (err == nil) != valid {
				t.Fatalf("%s: %v", mode, err)
			}
			ins, e := store.InstanceByID(t.Context(), fault.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			if (ins.State == string(state.StateStopped)) != valid {
				t.Fatalf("uncertain cleanup retired instance: %+v", ins)
			}
			if valid {
				if err = engine.RunScenarioWorkerFault(t.Context(), fault.ID); err != nil {
					t.Fatal(err)
				}
				want := 1
				if mode == "receipt_recovery" {
					want = 0
				}
				if v.calls != want {
					t.Fatal("termination was repeated")
				}
			}
		})
	}
}
