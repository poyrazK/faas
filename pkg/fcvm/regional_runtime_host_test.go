package fcvm

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

func regionalHostFixture(t *testing.T, vmm VMM) (*Manager, *runtimequotahost.Journal, WakeRequest) {
	t.Helper()
	j, err := runtimequotahost.Open(filepath.Join(t.TempDir(), "journal"), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := j.Close(); err != nil {
			t.Error(err)
		}
	})
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2,
		Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "host-test", APIBase: "https://cell.example.com"}}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: j.Identity().NodeID, HostEpochID: j.Identity().EpochID, Kind: runtimequota.CapacityServing}
	req := wakeReq(p.InstanceID, nil)
	req.Plan = policy.Plan
	req.AppID, req.AccountID, req.DeploymentID = g.Binding.RegionalAppID, g.Binding.RegionalAccountID, p.DeploymentID
	req.RegionalRuntime = &runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
	m := newTestManager(&fakeRunner{}, vmm).WithRuntimeHostJournal(j).WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { return nil })
	return m, j, req
}

type fencedBootVMM struct {
	*fakeVMM
	bootEntered, bootContinue       chan struct{}
	cleanupEntered, cleanupContinue chan struct{}
}

func (v *fencedBootVMM) BootColdBoot(ctx context.Context, l Lease, spec ColdBootSpec) error {
	close(v.bootEntered)
	<-v.bootContinue
	return v.fakeVMM.BootColdBoot(ctx, l, spec)
}
func (v *fencedBootVMM) Kill(ctx context.Context, l Lease) error {
	close(v.cleanupEntered)
	<-v.cleanupContinue
	return v.fakeVMM.Kill(ctx, l)
}

func TestRegionalHostFenceCountsLateBootThroughCleanup(t *testing.T) {
	v := &fencedBootVMM{fakeVMM: &fakeVMM{}, bootEntered: make(chan struct{}), bootContinue: make(chan struct{}), cleanupEntered: make(chan struct{}), cleanupContinue: make(chan struct{})}
	m, j, req := regionalHostFixture(t, v)
	result := make(chan error, 1)
	go func() { _, err := m.Wake(t.Context(), req); result <- err }()
	select {
	case <-v.bootEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("boot never reached host boundary")
	}
	if _, err := j.FencePermit(req.RegionalRuntime.Permit, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	close(v.bootContinue)
	select {
	case <-v.cleanupEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("fenced late boot never began cleanup")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	if err := j.WaitPermit(ctx, req.RegionalRuntime.Permit); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late boot disappeared before cleanup: %v", err)
	}
	cancel()
	close(v.cleanupContinue)
	if err := <-result; !errors.Is(err, runtimequotahost.ErrFenced) {
		t.Fatalf("late publication: %v", err)
	}
	if err := j.WaitPermit(t.Context(), req.RegionalRuntime.Permit); err != nil {
		t.Fatal(err)
	}
	if m.LeasedCount() != 0 || len(m.LiveInstances()) != 0 {
		t.Fatal("fenced publication retained Manager resources")
	}
	// Wait only drained operations; these portable mocks do not establish
	// absence of native resources or justify an operational stop signature.
}

func TestRegionalHostMetadataCannotBypassAuthority(t *testing.T) {
	for _, scenario := range []string{"no-owner", "no-journal", "wrong-app", "wrong-account", "wrong-deployment", "wrong-instance", "builder", "execution", "missing-seat", "wrong-task-kind", "paused-with-seat"} {
		t.Run(scenario, func(t *testing.T) {
			v := &fakeVMM{}
			m, _, req := regionalHostFixture(t, v)
			switch scenario {
			case "no-owner":
				m.WithRuntimeAuthorizer(nil)
			case "no-journal":
				m.WithRuntimeHostJournal(nil)
			case "wrong-app":
				req.AppID = uuid.NewString()
			case "wrong-account":
				req.AccountID = uuid.NewString()
			case "wrong-deployment":
				req.DeploymentID = uuid.NewString()
			case "wrong-instance":
				req.Instance = uuid.NewString()
			case "builder":
				req.ExportDir = "/builder"
			case "execution":
				req.ExecutionOnly = true
			case "missing-seat":
				req.RegionalRuntime.Seat = nil
			case "wrong-task-kind":
				req.AppTaskOnly = true
			case "paused-with-seat":
				req.KeepPaused = true
			}
			if _, err := m.Wake(t.Context(), req); err == nil {
				t.Fatal("unbound metadata admitted")
			}
			if v.boots() != 0 || m.LeasedCount() != 0 {
				t.Fatal("invalid metadata reached host allocation")
			}
		})
	}
}

func TestRegionalHostResidentResumeAndWarmCaptureRespectSeatFence(t *testing.T) {
	for _, operation := range []string{"resume", "warm-snapshot", "snapshot-error-recovery"} {
		t.Run(operation, func(t *testing.T) {
			v := &quotaSnapshotVMM{fakeVMM: &fakeVMM{}}
			m, j, req := regionalHostFixture(t, v)
			if _, err := m.Wake(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			// Mutating the caller's request after publication cannot replace
			// the Manager's immutable host identity.
			original := req.RegionalRuntime.Clone()
			req.RegionalRuntime.Seat.ID = uuid.NewString()
			if _, err := j.FenceSeat(*original.Seat, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			var err error
			switch operation {
			case "resume":
				err = m.ResumeVM(t.Context(), req.Instance)
			case "warm-snapshot":
				_, err = m.WarmSnapshot(t.Context(), req.Instance, SnapshotSpec{})
			default:
				v.snapErr = errors.New("capture failed")
				_, err = m.SnapshotKeepAlive(t.Context(), req.Instance, SnapshotSpec{})
			}
			if !errors.Is(err, runtimequotahost.ErrFenced) {
				t.Fatalf("resident fence: %v", err)
			}
			if len(v.resumed) != 0 || len(m.LiveInstances()) != 1 {
				t.Fatal("fenced resident resumed or disappeared before teardown")
			}
			if err := m.Destroy(t.Context(), req.Instance); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegionalHostPausedRestoreDoesNotHaveServingAuthority(t *testing.T) {
	m, j, req := regionalHostFixture(t, &fakeVMM{})
	req.KeepPaused, req.Snapshot = true, usableSnapshot()
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityWarm
	req.RegionalRuntime.Seat = nil
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeVM(t.Context(), req.Instance); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatalf("warm resident resumed without a fresh serving seat: %v", err)
	}
	if _, err := j.FencePermit(req.RegionalRuntime.Permit, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalHostMigrationCaptureCannotEmbedAnUnguardedResume(t *testing.T) {
	v := &quotaSnapshotVMM{fakeVMM: &fakeVMM{}}
	m, j, req := regionalHostFixture(t, v)
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := j.FenceSeat(*req.RegionalRuntime.Seat, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	_, err := m.SnapshotKeepAlive(t.Context(), req.Instance, SnapshotSpec{ResumeBeforePublish: true, BeforeResume: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if v.earlyChecks != 0 || len(v.resumed) != 0 {
		t.Fatal("migration snapshot embedded a resume outside the host barrier")
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}
