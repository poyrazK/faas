// adr: 521 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Use the real ownership, adoption, preparation and attempt fences. Fake VMM
// tests establish scheduler ordering, not native readiness or snapshot proof.
func qualificationExecutionFixture(t *testing.T, mode string, duration time.Duration) (*state.MemStore, state.EnvironmentGitSource, state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	ctx, store := t.Context(), state.NewMemStore()
	account, err := store.CreateAccount(ctx, "qualification@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environment.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	workloadClass := state.WorkloadClass("")
	if mode == api.ExecutionModeWorker {
		workloadClass = state.WorkloadClassWorker
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 512, CPUMillicores: 250, MaxConcurrency: 1, WorkloadClass: workloadClass,
		Manifest: state.AppManifest{ExecutionMode: mode, Port: 8079, StartupDeadlineS: 10}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, ImageDigest: "registry.example/shop@sha256:" + strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug,
			Source:    &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/shop@sha256:" + strings.Repeat("d", 64)},
			Runtime:   json.RawMessage(fmt.Sprintf(`{"port":8087,"healthz":"/reviewed-ready","startup_deadline_s":25,"execution_mode":%q}`, mode)),
			Variables: map[string]string{"MODE": "production"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID,
		ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "account-owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(ctx, account.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, account.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "preparer", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := func() environmentsync.Plan {
		observed, err := store.ObserveEnvironmentGitOps(ctx, lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		result, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
			Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation,
			Prune: source.Spec.Prune, Now: time.Now(), Overrides: observed.Overrides})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if _, err := store.ApplyEnvironmentGitOps(ctx, lease, plan()); err != nil {
		t.Fatal(err)
	}
	reviewed := plan()
	candidates, err := store.PrepareEnvironmentGitOpsImageCandidates(ctx, lease, reviewed)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidate: %+v %v", candidates, err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidates[0].DeploymentID, "/reviewed.ext4", "reviewed", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, candidates[0].DeploymentID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if graph, err := store.ReconcileEnvironmentGitOpsPreparation(ctx, lease, reviewed); err != nil || graph.Phase != "prepared" {
		t.Fatalf("graph: %+v %v", graph, err)
	}
	requests, err := store.QueueEnvironmentGitOpsQualification(ctx, lease, reviewed)
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests: %+v %v", requests, err)
	}
	claimed, err := store.ClaimEnvironmentWorkloadQualification(ctx, requests[0].ID, "scheduler", duration)
	if err != nil {
		t.Fatal(err)
	}
	return store, source, claimed
}

func assertQualificationRetired(t *testing.T, store *state.MemStore, e *Engine, request state.EnvironmentWorkloadQualificationRequest, vmm *qualificationRuntimeVMM, destroys int) {
	t.Helper()
	ins, err := store.InstanceByID(t.Context(), request.ReservedInstanceID)
	if err != nil || ins.State != string(state.StateStopped) || ins.TerminalAt == nil || e.ledger.ResidentRAM() != 0 || e.ledger.UsedVCPU() != 0 {
		t.Fatalf("retirement: %+v %v ram=%d vcpu=%d", ins, err, e.ledger.ResidentRAM(), e.ledger.UsedVCPU())
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys != destroys || vmm.lastDestroyContextErr != nil {
		t.Fatalf("physical retirement: destroys=%d ctx=%v", vmm.destroys, vmm.lastDestroyContextErr)
	}
}

func TestEngineEnvironmentQualificationRuntimeUsesFrozenInputsWithoutActivation(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeWorker, api.ExecutionModeService} {
		t.Run(mode, func(t *testing.T) {
			store, source, request := qualificationExecutionFixture(t, mode, time.Minute)
			vmm, notif := newQualificationRuntimeVMM(&fakeVMM{}), &fakeNotifier{}
			e := newEngine(t, store, vmm, notif, "test-fc")
			visited := false
			err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(ctx context.Context, ins state.Instance) error {
				visited = true
				proof, exists, err := store.InstanceRuntimeConfigReceipt(ctx, ins.ID)
				if err != nil || !exists || proof.Scope != "production" || proof.Variables["MODE"] != "production" || ins.State != string(state.StateRunning) ||
					ins.ID != request.ReservedInstanceID || ins.FrameworkReadyAt != nil || ins.StartupCPUBoostUntil == nil || !ins.StartupCPUBoostUntil.Equal(*request.LeaseUntil) || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
					t.Fatalf("bound execution: %+v %+v %v", ins, proof, err)
				}
				if err := store.MarkDeploymentLive(ctx, request.DeploymentID); err == nil {
					t.Fatal("runtime became activation authority")
				}
				return nil
			})
			if err != nil || !visited {
				t.Fatalf("execution: visited=%v %v", visited, err)
			}
			if vmm.lastColdBootSpec.Port != 8087 || vmm.lastColdBootSpec.HealthcheckPath != "/reviewed-ready" || vmm.lastColdBootSpec.StartupDeadlineS != 25 || vmm.lastColdBootSpec.ExecutionMode != mode || vmm.coldBoots != 1 || vmm.snapshots != 0 {
				t.Fatalf("frozen boot: %+v boots=%d snapshots=%d", vmm.lastColdBootSpec, vmm.coldBoots, vmm.snapshots)
			}
			if notif.count(db.NotifyDeploymentReady) != 0 {
				t.Fatal("qualification emitted ordinary activation handoff")
			}
			current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
			if err != nil || current.AppliedRevisionID != "" {
				t.Fatal("execution claimed applied revision", err)
			}
			assertQualificationRetired(t, store, e, request, vmm, 1)
			if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { t.Fatal("replayed visitor"); return nil }); !errors.Is(err, state.ErrConflict) || vmm.coldBoots != 1 {
				t.Fatalf("retired reservation replayed VM: %v boots=%d", err, vmm.coldBoots)
			}
		})
	}
}

func TestEngineEnvironmentQualificationRuntimeRevocationAndFailureCleanup(t *testing.T) {
	for _, change := range []string{"before_boot", "during_boot", "during_visit", "cancel", "boot_failure", "visit_failure", "destroy_failure"} {
		t.Run(change, func(t *testing.T) {
			store, source, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
			vmm := newQualificationRuntimeVMM(&fakeVMM{})
			e := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			revoke := func() {
				if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"}); err != nil {
					t.Fatal(err)
				}
			}
			injected := errors.New("injected effect failure")
			switch change {
			case "before_boot":
				revoke()
			case "during_boot":
				vmm.coldBootHook = revoke
			case "boot_failure":
				vmm.wakeErr = injected
			case "destroy_failure":
				vmm.destroyErr = injected
			}
			visited := false
			err := e.WithEnvironmentWorkloadQualificationRuntime(ctx, request, func(context.Context, state.Instance) error {
				visited = true
				switch change {
				case "during_visit":
					revoke()
				case "cancel":
					cancel()
				case "visit_failure":
					return injected
				}
				return nil
			})
			if err == nil {
				t.Fatal("failed or revoked execution returned success")
			}
			if change == "before_boot" {
				if vmm.coldBoots != 0 || vmm.destroys != 0 || visited {
					t.Fatal("stale attempt reached a VM effect")
				}
				return
			}
			if change == "during_boot" || change == "boot_failure" {
				if visited {
					t.Fatal("unacknowledged boot reached visitor")
				}
				if _, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), request.ReservedInstanceID); err != nil || exists {
					t.Fatal("failed boot published runtime proof", err)
				}
			}
			if change == "destroy_failure" {
				ins, err := store.InstanceByID(t.Context(), request.ReservedInstanceID)
				if err != nil || ins.State != string(state.StateRunning) || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
					t.Fatalf("uncertain VM lost reservation: %+v %v", ins, err)
				}
				restarted := newEngine(t, store, vmm, nil, "test-fc")
				if err := restarted.SeedLedger(t.Context()); err != nil {
					t.Fatal(err)
				}
				if restarted.ledger.ResidentRAM() != 512+api.PerVMOverheadMB || restarted.ledger.UsedCPUMillicoresForNode(ins.NodeID) != startupCPUBoostQuota(api.PlanPro, 250) {
					t.Fatal("restart lost uncertain VM memory or startup CPU reservation")
				}
				return
			}
			assertQualificationRetired(t, store, e, request, vmm, 1)
		})
	}
}

func TestEngineEnvironmentQualificationRuntimeLeaseDeadlineCancelsBlockedBoot(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Second)
	vmm := newQualificationRuntimeVMM(&fakeVMM{bootStarted: make(chan struct{}, 1), bootRelease: make(chan struct{})})
	e := newEngine(t, store, vmm, nil, "test-fc")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := e.WithEnvironmentWorkloadQualificationRuntime(ctx, request, func(context.Context, state.Instance) error { t.Fatal("expired boot reached visitor"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired execution: %v", err)
	}
	assertQualificationRetired(t, store, e, request, vmm, 1)
	if _, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "recovery", time.Minute); err != nil {
		t.Fatal("confirmed retirement prevented attempt recovery", err)
	}
}

func TestEngineEnvironmentQualificationRuntimeBoundsAdmissionLockWait(t *testing.T) {
	for _, deadline := range []string{"caller", "attempt"} {
		t.Run(deadline, func(t *testing.T) {
			duration := time.Minute
			if deadline == "attempt" {
				duration = time.Second
			}
			store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, duration)
			vmm := newQualificationRuntimeVMM(&fakeVMM{})
			e := newEngine(t, store, vmm, nil, "test-fc")
			release := e.lockApp(request.AppID)
			defer release()
			ctx := t.Context()
			if deadline == "caller" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			result := make(chan error, 1)
			go func() {
				result <- e.WithEnvironmentWorkloadQualificationRuntime(ctx, request, func(context.Context, state.Instance) error { return errors.New("unexpected visitor") })
			}()
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("contended admission: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("admission wait ignored its deadline")
			}
			if _, err := store.InstanceByID(t.Context(), request.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) || e.ledger.ResidentRAM() != 0 || vmm.coldBoots != 0 {
				t.Fatalf("expired admission had effects: %v ram=%d boots=%d", err, e.ledger.ResidentRAM(), vmm.coldBoots)
			}
		})
	}
}

func TestEngineEnvironmentQualificationRuntimeBoundsRetirementLockWait(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Second)
	vmm := newQualificationRuntimeVMM(&fakeVMM{})
	e := newEngine(t, store, vmm, nil, "test-fc")
	locked := make(chan func(), 1)
	result := make(chan error, 1)
	go func() {
		result <- e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error {
			locked <- e.lockApp(request.AppID)
			return nil
		})
	}()
	select {
	case release := <-locked:
		defer release()
	case <-time.After(5 * time.Second):
		t.Fatal("visitor never acquired serving app lock")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("contended retirement: %v", err)
		}
	case <-time.After(2*DestroyTimeout + 5*time.Second):
		t.Fatal("retirement lock wait ignored its cleanup deadline")
	}
	ins, err := store.InstanceByID(t.Context(), request.ReservedInstanceID)
	if err != nil || ins.State != string(state.StateRunning) || ins.TerminalAt != nil || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
		t.Fatalf("uncommitted retirement released reservation: %+v %v ram=%d", ins, err, e.ledger.ResidentRAM())
	}
	if _, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "recovery", time.Minute); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired attempt replaced unretired reservation: %v", err)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys != 1 || vmm.lastDestroyContextErr != nil {
		t.Fatalf("physical retirement did not precede lock wait: destroys=%d ctx=%v", vmm.destroys, vmm.lastDestroyContextErr)
	}
}

func TestEngineEnvironmentQualificationRuntimeRevokesBlockedBoot(t *testing.T) {
	store, source, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	vmm := newQualificationRuntimeVMM(&fakeVMM{bootStarted: make(chan struct{}, 1), bootRelease: make(chan struct{})})
	e := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc")
	result := make(chan error, 1)
	go func() {
		result <- e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { return errors.New("unexpected visitor") })
	}()
	select {
	case <-vmm.bootStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("boot never started")
	}
	lockAcquired := make(chan struct{})
	go func() {
		release := e.lockApp(request.AppID)
		release()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		close(vmm.bootRelease)
		<-result
		t.Fatal("qualification blocked serving wakes behind its VM RPC")
	}
	if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, state.ErrConflict) || !errors.Is(err, context.Canceled) {
			t.Fatalf("lost revocation cause: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation did not cancel the VM RPC")
	}
	assertQualificationRetired(t, store, e, request, vmm, 1)
}

func TestEngineEnvironmentQualificationRuntimeRejectsUnavailableCapacityAndExpiredAttempt(t *testing.T) {
	for _, failure := range []string{"capacity", "expired"} {
		t.Run(failure, func(t *testing.T) {
			store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
			vmm := newQualificationRuntimeVMM(&fakeVMM{})
			e := newEngine(t, store, vmm, nil, "test-fc")
			if failure == "expired" {
				past := time.Now().Add(-time.Second)
				request.LeaseUntil = &past
			} else {
				// Fill ledger concurrency without inventing a deployment overlap.
				for i := 0; i < 2; i++ {
					if err := e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: request.AppID, DeploymentID: "different", Plan: api.PlanPro, RAMMB: 512, VCPU: 1, MaxConcurrency: 2, NodeID: e.defaultLocalNodeID}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { t.Fatal("refusal ran visitor"); return nil }); err == nil {
				t.Fatal("refusal returned success")
			}
			if vmm.coldBoots != 0 || vmm.destroys != 0 {
				t.Fatal("refusal invoked VM")
			}
			if failure == "capacity" {
				ins, err := store.InstanceByID(t.Context(), request.ReservedInstanceID)
				if err != nil || ins.State != string(state.StateStopped) || e.ledger.ResidentRAM() != 2*(512+api.PerVMOverheadMB) {
					t.Fatalf("refusal damaged capacity: %+v %v", ins, err)
				}
			}
		})
	}
}
