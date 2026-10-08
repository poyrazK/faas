package sched

// adr: 790
import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalCurrentInstanceRequiresOriginalResidentRun(t *testing.T) {
	for _, scenario := range []string{"same", "compact-fields", "run", "app", "deployment", "node", "lookup-spelling", "stopped", "failed", "parked", "warm", "migrating", "snapshotting", "draining", "task"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, _, source, _, ins := regionalAccountingResidentFixture(t, true)
			source.Permit.RunID = ins.WakeID
			switch scenario {
			case "compact-fields":
				ins.AppID = strings.ReplaceAll(ins.AppID, "-", "")
				ins.DeploymentID = strings.ReplaceAll(ins.DeploymentID, "-", "")
				ins.NodeID = strings.ReplaceAll(ins.NodeID, "-", "")
				ins.WakeID = strings.ReplaceAll(ins.WakeID, "-", "")
			case "run":
				ins.WakeID = uuid.NewString()
			case "app":
				ins.AppID = uuid.NewString()
			case "deployment":
				ins.DeploymentID = uuid.NewString()
			case "node":
				ins.NodeID = uuid.NewString()
			case "lookup-spelling":
				ins.ID = source.Permit.InstanceID
			case "stopped":
				ins.State = string(state.StateStopped)
			case "failed":
				ins.State = string(state.StateFailed)
			case "parked":
				ins.State = string(state.StateParked)
			case "warm":
				ins.State = string(state.StateWarm)
			case "migrating":
				ins.State = string(state.StateMigrating)
			case "snapshotting":
				ins.State = string(state.StateSnapshotting)
			case "draining":
				ins.State = string(state.StateDraining)
			case "task":
				source.Permit.Kind = runtimequota.CapacityAppTask
			}
			want := scenario == "same" || scenario == "compact-fields" || scenario == "warm" || scenario == "migrating" || scenario == "snapshotting" || scenario == "draining"
			if got := currentRegionalInstanceIssue(source, ins) == ""; got != want {
				t.Fatal("original current run classification", got, want)
			}
		})
	}
}

func currentTaskSourceFixture(t *testing.T) (runtimequota.LaunchSource, state.AppTask, time.Time) {
	t.Helper()
	_, _, _, source, _ := regionalAccountingFixture(t)
	source.TaskID, source.Permit.Kind = uuid.NewString(), runtimequota.CapacityAppTask
	source.Permit.InstanceID, _ = runtimequota.AppTaskInstanceID(source.TaskID, source.Permit.RunID)
	source.InstanceKey = source.Permit.InstanceID
	at := time.Now()
	expiry := at.Add(time.Minute)
	token := source.Permit.RunID
	owner := "original-worker"
	return source, state.AppTask{ID: source.TaskID, AccountID: source.Permit.Grant.Binding.RegionalAccountID, AppID: source.Permit.Grant.Binding.RegionalAppID, DeploymentID: source.Permit.DeploymentID, Status: state.AppTaskRestoring, LeaseToken: &token, LeaseOwner: &owner, LeaseExpiresAt: &expiry}, at
}

func TestRegionalCurrentTaskRequiresOriginalActiveClaim(t *testing.T) {
	for _, scenario := range []string{"restoring", "running", "compact-fields", "task", "account", "app", "deployment", "replacement-claim", "missing-claim", "missing-owner", "empty-owner", "missing-expiry", "expired", "exact-expiry", "cancelled", "queued", "succeeded", "failed", "timed-out", "terminal-cancelled", "missing-source-task", "zero-time"} {
		t.Run(scenario, func(t *testing.T) {
			source, task, at := currentTaskSourceFixture(t)
			switch scenario {
			case "running":
				task.Status = state.AppTaskRunning
			case "compact-fields":
				task.ID = strings.ReplaceAll(task.ID, "-", "")
				task.AccountID = strings.ReplaceAll(task.AccountID, "-", "")
				task.AppID = strings.ReplaceAll(task.AppID, "-", "")
				task.DeploymentID = strings.ReplaceAll(task.DeploymentID, "-", "")
				token := strings.ReplaceAll(*task.LeaseToken, "-", "")
				task.LeaseToken = &token
			case "task":
				task.ID = uuid.NewString()
			case "account":
				task.AccountID = uuid.NewString()
			case "app":
				task.AppID = uuid.NewString()
			case "deployment":
				task.DeploymentID = uuid.NewString()
			case "replacement-claim":
				token := uuid.NewString()
				task.LeaseToken = &token
			case "missing-claim":
				task.LeaseToken = nil
			case "missing-owner":
				task.LeaseOwner = nil
			case "empty-owner":
				empty := ""
				task.LeaseOwner = &empty
			case "missing-expiry":
				task.LeaseExpiresAt = nil
			case "expired":
				past := at.Add(-time.Second)
				task.LeaseExpiresAt = &past
			case "exact-expiry":
				task.LeaseExpiresAt = &at
			case "cancelled":
				task.CancelRequested = &at
			case "queued":
				task.Status = state.AppTaskQueued
			case "succeeded":
				task.Status = state.AppTaskSucceeded
			case "failed":
				task.Status = state.AppTaskFailed
			case "timed-out":
				task.Status = state.AppTaskTimedOut
			case "terminal-cancelled":
				task.Status = state.AppTaskCancelled
			case "missing-source-task":
				source.TaskID = ""
			case "zero-time":
				at = time.Time{}
			}
			want := scenario == "restoring" || scenario == "running" || scenario == "compact-fields"
			if got := currentRegionalTaskIssue(source, task, at) == ""; got != want {
				t.Fatal("original current claim classification", got, want)
			}
		})
	}
}

type regionalAttemptReadingStore struct {
	state.Store
	instance func(state.Instance) (state.Instance, error)
	app      func(state.App) (state.App, error)
}

func (s regionalAttemptReadingStore) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	ins, err := s.Store.InstanceByID(ctx, id)
	if err != nil {
		return ins, err
	}
	if s.instance != nil {
		return s.instance(ins)
	}
	return ins, nil
}
func (s regionalAttemptReadingStore) AppByID(ctx context.Context, id string) (state.App, error) {
	app, err := s.Store.AppByID(ctx, id)
	if err != nil {
		return app, err
	}
	if s.app != nil {
		return s.app(app)
	}
	return app, nil
}

func TestRegionalInventorySeparatesCurrentAttemptsFromRetainedPhysicalHolds(t *testing.T) {
	for _, scenario := range []string{"current", "replaced", "collected", "wrong-account", "changed-org", "read-error", "cancelled-read", "cancel-after-read", "native-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, source, v := startupRowBindingFixture(t)
			if err := e.SeedLedger(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := s.RegionalRuntimeCapacity(t.Context(), source.Permit.Grant.Binding.RegionalAppID, source.Permit.ID)
			if err != nil {
				t.Fatal(err)
			}
			ram, vcpu, cpu := e.ledger.ResidentRAM(), e.ledger.UsedVCPUForNode(source.Permit.NodeID), e.ledger.UsedCPUMillicoresForNode(source.Permit.NodeID)
			observed := regionalAttemptReadingStore{Store: s}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "replaced":
				observed.instance = func(i state.Instance) (state.Instance, error) { i.WakeID = uuid.NewString(); return i, nil }
			case "collected":
				if err := s.DeleteInstance(t.Context(), source.InstanceKey); err != nil {
					t.Fatal(err)
				}
			case "wrong-account":
				observed.app = func(a state.App) (state.App, error) { a.AccountID = uuid.NewString(); return a, nil }
			case "changed-org":
				observed.app = func(a state.App) (state.App, error) { a.OrgID = uuid.NewString(); return a, nil }
			case "read-error":
				observed.instance = func(state.Instance) (state.Instance, error) { return state.Instance{}, errors.New("read unavailable") }
			case "cancelled-read":
				observed.instance = func(state.Instance) (state.Instance, error) { return state.Instance{}, context.Canceled }
			case "cancel-after-read":
				observed.instance = func(i state.Instance) (state.Instance, error) { cancel(); return i, nil }
			case "native-unavailable":
				v.err = errors.New("native unavailable")
			}
			e.store = observed
			calls := v.calls
			cursor, report, err := e.ObserveRegionalRuntimeInventoryPage(ctx, state.RegionalRuntimeRetirementCursor{})
			unavailable := scenario == "read-error" || scenario == "cancelled-read" || scenario == "cancel-after-read" || scenario == "native-unavailable"
			physical := 1
			if scenario == "native-unavailable" {
				physical = 0
			}
			current := 0
			if scenario == "current" {
				current = 1
			}
			if (err != nil) != unavailable || cursor.PermitID != source.Permit.ID || len(report.Matches) != physical || len(report.CurrentAttempts) != current || len(report.CurrentIssues) != physical-current || v.calls != calls+1 {
				t.Fatal(cursor, report, err)
			}
			after, err := s.RegionalRuntimeCapacity(t.Context(), source.Permit.Grant.Binding.RegionalAppID, source.Permit.ID)
			if err != nil || !reflect.DeepEqual(before, after) || e.ledger.ResidentRAM() != ram || e.ledger.UsedVCPUForNode(source.Permit.NodeID) != vcpu || e.ledger.UsedCPUMillicoresForNode(source.Permit.NodeID) != cpu {
				t.Fatal("current observation changed original accounting", err)
			}
			head, err := s.RegionalRuntimeQuotaHead(t.Context(), source.Permit.Grant.Binding.RegionalAppID)
			if err != nil || head.AdmissionEnabled {
				t.Fatal("observation activated head", err)
			}
		})
	}
}

func TestRegionalInventoryCurrentTaskFollowsDurableClaimLifecycle(t *testing.T) {
	for _, phase := range []string{"restoring", "running", "cancel-requested", "terminal"} {
		t.Run(phase, func(t *testing.T) {
			s, acct, app, dep := regionalQuotaSchedulerFixture(t)
			if err := s.SetDeploymentRootfs(t.Context(), dep.ID, "/tmp/task.ext4", "apps/task/deployment.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			task, err := s.CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{"bin/task"}})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now()
			claimed, err := s.ClaimNextAppTask(t.Context(), "original-worker", at, time.Hour)
			if err != nil {
				t.Fatal(err)
			}

			if phase != "restoring" {
				if _, err := s.MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, at.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "current-task", TargetURL: "unix:///run/faas/current-task.sock", Active: true, AdmissionCeilingMB: 1024, VCPUBudget: 8, VPCPUs: 8})
			if err != nil {
				t.Fatal(err)
			}
			freezeRegionalSchedulerApp(t, s, acct, app)
			head, err := s.RegionalRuntimeQuotaHead(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := runtimequota.AppTaskInstanceID(task.ID, *claimed.LeaseToken)
			if err != nil {
				t.Fatal(err)
			}
			source := runtimequota.LaunchSource{TaskID: task.ID, InstanceKey: instance, Permit: runtimequota.CapacityPermit{Grant: head.Grant, ID: uuid.NewString(), InstanceID: instance, RunID: *claimed.LeaseToken, DeploymentID: uuid.MustParse(dep.ID).String(), NodeID: node.ID, HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityAppTask}}
			resources := state.RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: 1000}
			if err := s.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: source.Permit}, source, resources); err != nil {
				t.Fatal(err)
			}
			_, sample := nativeReconciliationFixture(t)
			sample.Host = runtimequota.HostEpoch{NodeID: source.Permit.NodeID, HostEpochID: source.Permit.HostEpochID}
			owner := &sample.Inventory.Owners[0]
			owner.Instance, owner.Lease.Instance = instance, instance
			owner.Placement = &runtimequota.Placement{Permit: source.Permit}
			owner.OriginalSource = &source
			owner.OriginalResources = &resources
			owner.Machine = &fcvm.Machine{MemSizeMib: resources.RAMMB, VcpuCount: resources.VCPU}
			mem := uint64(api.BillableRAMMB(resources.RAMMB)) << 20
			owner.Cgroup.MemoryMaxBytes = &mem
			owner.Cgroup.Path = fcvm.ParentCgroupFor(owner.Lease.Plan) + "/" + instance
			sample.Inventory.Cgroups[0] = *owner.Cgroup
			v := &inventoryReadingVMM{fakeVMM: &fakeVMM{}, sample: sample}
			e := newEngine(t, s, v, &fakeNotifier{}, "1.10.0")
			if err := e.SeedLedger(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := s.RegionalRuntimeCapacity(t.Context(), app.ID, source.Permit.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case "cancel-requested":
				if _, err := s.RequestAppTaskCancellation(t.Context(), acct.ID, app.ID, task.ID, at.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
			case "terminal":
				if _, err := s.CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskCancelled, FinishedAt: at.Add(3 * time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
			_, report, err := e.ObserveRegionalRuntimeInventoryPage(t.Context(), state.RegionalRuntimeRetirementCursor{})
			current := 0
			if phase == "restoring" || phase == "running" {
				current = 1
			}
			if err != nil || len(report.Matches) != 1 || len(report.CurrentAttempts) != current || len(report.CurrentIssues) != 1-current {
				t.Fatal(report, err)
			}
			after, err := s.RegionalRuntimeCapacity(t.Context(), app.ID, source.Permit.ID)
			if err != nil || !reflect.DeepEqual(before, after) || e.ledger.ResidentRAM() != api.BillableRAMMB(resources.RAMMB) {
				t.Fatal("task intent released physical runtime", err)
			}
		})
	}
}
