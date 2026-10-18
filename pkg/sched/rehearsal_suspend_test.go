// adr: 20261017201015
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
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type suspendRoutingVMM struct {
	*fakeVMM
	owner    *rehearsalrecorder.Session
	store    *state.MemStore
	app      string
	calls    int
	mode     string
	during   func()
	heldDone chan struct{}
	heldErr  error
}

func (v *suspendRoutingVMM) RehearseSuspendResumeOnNode(ctx context.Context, _, _, receipt string) error {
	v.calls++
	if v.during != nil {
		v.during()
	}
	if v.mode == "missing_owner" {
		return errors.New("original collector lost")
	}
	if v.mode == "missing_receipt" {
		return nil
	}
	if _, err := v.owner.PrepareSuspend(ctx, receipt); err != nil {
		return err
	}
	if err := v.owner.RecordSuspended(ctx, receipt); err != nil {
		return err
	}
	if err := v.owner.BeginResume(ctx, receipt, 1); err != nil {
		return err
	}
	if err := v.owner.RecordResumed(ctx, receipt, 10); err != nil {
		return err
	}
	if v.mode == "config_changed" {
		ram := 256
		if _, err := v.store.UpdateApp(ctx, v.app, state.UpdateAppParams{RAMMB: &ram}); err != nil {
			return err
		}
	}
	if v.mode == "lost_reply" {
		return errors.New("reply lost after durable physical receipt")
	}
	return nil
}

func schedulerSuspendFixture(t *testing.T, running ...bool) (*Engine, *state.MemStore, *suspendRoutingVMM, rehearsalresume.Receipt, state.Instance) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@suspend.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "suspend-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, PreviewOfSlug: "primary", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	run := strings.ReplaceAll(uuid.NewString(), "-", "")
	managed := len(running) > 0 && running[0]
	names := []string{"primary"}
	simulation := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	resumeSpec := rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"primary"}, PauseMS: 1, WithinMS: 1000}
	workJSON := ""
	if managed {
		names = append(names, "origin")
		simulation.Fixtures[0] = api.ScenarioSimulationFixture{Workload: "primary", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}
		resumeSpec.Checkpoint = "managed_job"
		resumeSpec.WorkerCheckpoint = "after-commit"
		raw, _, normalizeErr := rehearsalwork.Normalize(rehearsalwork.Plan{Version: 1, Primary: "origin", Participants: []rehearsalwork.Participant{{Workload: "origin"}, {Workload: "primary", Async: true}}}, names)
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		workJSON = string(raw)
	}
	sim, _, err := api.NormalizeScenarioSimulationPlan(simulation, names)
	if err != nil {
		t.Fatal(err)
	}
	spec, _, err := rehearsalresume.Normalize(resumeSpec, names)
	if err != nil {
		t.Fatal(err)
	}
	members := []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", NetworkMode: "contained", WebhookMode: "capture", SimulationJSON: string(sim), SuspendResumeJSON: string(spec), ManagedWorkJSON: workJSON}}
	if managed {
		origin, createErr := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "origin-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "origin", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if createErr != nil {
			t.Fatal(createErr)
		}
		member := members[0]
		member.AppID = origin.ID
		member.Workload = "origin"
		members = append(members, member)
	}
	if err = store.RegisterScenarioTestMembers(ctx, acct.ID, run, members); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:suspend", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "waking", app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := rehearsalrecorder.Start(ctx, ctx, store, rehearsalrecorder.Identity{AccountID: acct.ID, AppID: app.ID, RunID: run, DeploymentID: dep.ID, InstanceID: ins.ID})
	if err != nil {
		t.Fatal(err)
	}
	owner.Activate()
	t.Cleanup(owner.Stop)
	values, err := store.RuntimeAppValuesForDeployment(ctx, acct.ID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	ins, err = store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: acct.ID, AppID: app.ID, InstanceID: ins.ID, NodeID: node.ID, WakeID: ins.WakeID, ExpectedState: "waking", Fence: fence.SecretFence, ConfigFence: fence, Netns: "fc-" + ins.ID, HostIP: "10.100.0.2", GuestUID: 20001})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &suspendRoutingVMM{fakeVMM: &fakeVMM{}, owner: owner, store: store, app: app.ID}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	limits := api.MustLimitsFor(api.PlanPro)
	if err = e.Ledger().Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro, RAMMB: 128, VCPU: limits.VCPU, CPUMillicores: 1000, MaxConcurrency: 1, NodeID: node.ID}); err != nil {
		t.Fatal(err)
	}
	if managed {
		job, enqueueErr := store.EnqueueInvocation(ctx, state.Invocation{AccountID: acct.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/import", Payload: []byte(`{}`)})
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		if _, err = store.ClaimInvocation(ctx, job.ID, ins.ID, 60); err != nil {
			t.Fatal(err)
		}
		request, beginErr := owner.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		req := api.ScenarioSimulationRequest{AccountID: acct.ID, AppID: app.ID, RunID: run, InstanceID: ins.ID, RecorderRequestID: request, Integration: "checkpoint", Method: "POST", Path: "/after-commit", Body: []byte(`{}`)}
		d, beginErr := store.BeginScenarioSimulation(ctx, req)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if err = store.FinishScenarioSimulation(ctx, req, d.Attempt.ID, "completed"); err != nil {
			t.Fatal(err)
		}
		var conn *rehearsalrecorder.Connection
		conn, err = owner.Attach(func() { conn.Detach(false) })
		if err != nil {
			t.Fatal(err)
		}
		holdCtx, cancel := context.WithCancel(ctx)
		vmm.heldDone = make(chan struct{})
		go func() {
			vmm.heldErr = owner.HoldSuspendCheckpoint(holdCtx, d.SuspendResumeID, conn)
			close(vmm.heldDone)
		}()
		t.Cleanup(func() {
			cancel()
			conn.Detach(false)
			select {
			case <-vmm.heldDone:
			case <-time.After(3 * time.Second):
				t.Error("held checkpoint did not join")
			}
		})
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			receipt, readErr := store.ScenarioSuspendResume(ctx, d.SuspendResumeID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if receipt.State == "armed" {
				return e, store, vmm, receipt, ins
			}
			select {
			case <-deadline.C:
				t.Fatal("checkpoint not armed")
			case <-tick.C:
			}
		}
	}
	proof, err := store.BeginScenarioSuspendResume(ctx, acct.ID, run)
	if err != nil {
		t.Fatal(err)
	}
	return e, store, vmm, proof.Receipts[0], ins
}

func TestSuspendResumeSchedulerRequiresHostProofAndDoesNotRepeatPause(t *testing.T) {
	for _, mode := range []string{"complete", "lost_reply", "missing_owner", "missing_receipt", "config_changed", "lost_claim"} {
		t.Run(mode, func(t *testing.T) {
			e, store, vmm, r, ins := schedulerSuspendFixture(t)
			vmm.mode = mode
			charged := e.Ledger().AdmissionUsedMB(ins.NodeID)
			vmm.during = func() {
				if got := e.Ledger().AdmissionUsedMB(ins.NodeID); got != charged {
					t.Errorf("pause released or reduced full reservation: %d/%d", got, charged)
				}
			}
			if mode == "lost_claim" {
				if _, err := store.ClaimScenarioSuspendResume(t.Context(), r.ID); err != nil {
					t.Fatal(err)
				}
			}
			err := e.RunScenarioSuspendResume(t.Context(), r.ID)
			success := mode == "complete" || mode == "lost_reply"
			if (err == nil) != success {
				t.Fatalf("%s: %v", mode, err)
			}
			proof, err := store.ScenarioSuspendResume(t.Context(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if success {
				current, err := store.InstanceByID(t.Context(), ins.ID)
				if err != nil || current.State != "running" || proof.State != "complete" || current.DeploymentID != ins.DeploymentID {
					t.Fatal("same VM was not republished with complete proof")
				}
				if err = e.RunScenarioSuspendResume(t.Context(), r.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if proof.State != "failed" {
					t.Fatal("uncertain lifecycle not fenced")
				}
				_ = e.RunScenarioSuspendResume(t.Context(), r.ID)
			}
			want := 1
			if mode == "lost_claim" {
				want = 0
			}
			if vmm.calls != want {
				t.Fatal("uncertain physical pause repeated")
			}
		})
	}
}

func TestRunningSuspendSchedulerPublishesSameAttemptWithoutRepeatingPause(t *testing.T) {
	for _, mode := range []string{"complete", "lost_reply", "missing_receipt", "config_changed", "lost_claim", "changed_attempt"} {
		t.Run(mode, func(t *testing.T) {
			e, store, vmm, r, ins := schedulerSuspendFixture(t, true)
			vmm.mode = mode
			job, err := store.InvocationByID(t.Context(), r.InvocationID)
			if err != nil {
				t.Fatal(err)
			}
			charged := e.Ledger().AdmissionUsedMB(ins.NodeID)
			vmm.during = func() {
				if e.Ledger().AdmissionUsedMB(ins.NodeID) != charged {
					t.Error("running-job pause released its full reservation")
				}
				if mode == "changed_attempt" {
					if err := store.FailInvocation(t.Context(), job.ID, "dispatch ended", time.Nanosecond, 3, state.WithInvocationClaim(job)); err != nil {
						t.Error(err)
					}
				}
			}
			if mode == "lost_claim" {
				if _, err = store.ClaimScenarioSuspendResume(t.Context(), r.ID); err != nil {
					t.Fatal(err)
				}
			}
			err = e.RunScenarioSuspendResume(t.Context(), r.ID)
			success := mode == "complete" || mode == "lost_reply"
			if (err == nil) != success {
				t.Fatalf("%s: %v", mode, err)
			}
			select {
			case <-vmm.heldDone:
				if (vmm.heldErr == nil) != success {
					t.Fatalf("response release: %v", vmm.heldErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("held response did not settle")
			}
			if success {
				current, readErr := store.InvocationByID(t.Context(), job.ID)
				if readErr != nil || current.Attempts != 1 || current.State != state.InvocationDispatching || current.InstanceID != ins.ID {
					t.Fatal("pause changed its original managed dispatch")
				}
			}
			_ = e.RunScenarioSuspendResume(t.Context(), r.ID)
			want := 1
			if mode == "lost_claim" {
				want = 0
			}
			if vmm.calls != want {
				t.Fatal("uncertain running-job pause was repeated")
			}
		})
	}
}
