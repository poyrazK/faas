package fcvm

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

type primeCaptureVMM struct{ *quotaSnapshotVMM }

func (v *primeCaptureVMM) Snapshot(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.SnapshotKeepAlive(ctx, lease, spec)
}

func TestRegionalHostRejectsPlanAndPurposeBeforeAllocation(t *testing.T) {
	for _, kind := range []string{"larger-plan", "smaller-plan", "empty-plan", "prime-as-serving", "task-as-serving", "serving-as-prime", "prime-restore", "prime-paused", "prime-worker", "prime-job"} {
		t.Run(kind, func(t *testing.T) {
			v := &fakeVMM{}
			m, _, req := regionalHostFixture(t, v)
			switch kind {
			case "larger-plan":
				req.Plan = api.PlanScale
			case "smaller-plan":
				req.Plan = api.PlanHobby
			case "empty-plan":
				req.Plan = ""
			case "prime-as-serving":
				req.RegionalRuntime.Permit.Kind = runtimequota.CapacityPrime
				req.RegionalRuntime.Seat = nil
			case "task-as-serving":
				req.RegionalRuntime.Permit.Kind = runtimequota.CapacityAppTask
				req.RegionalRuntime.Seat = nil
			default:
				req.PrimeOnly = true
				if kind != "serving-as-prime" {
					req.RegionalRuntime.Permit.Kind = runtimequota.CapacityPrime
					req.RegionalRuntime.Seat = nil
				}
				switch kind {
				case "prime-restore":
					req.Snapshot = usableSnapshot()
				case "prime-paused":
					req.KeepPaused = true
				case "prime-worker":
					req.ExecutionMode = string(api.ExecutionModeWorker)
				case "prime-job":
					req.ExecutionMode = string(api.ExecutionModeJob)
				}
			}
			if _, err := m.Wake(t.Context(), req); !errors.Is(err, runtimequotahost.ErrBinding) {
				t.Fatalf("unbound lifecycle reached host: %v", err)
			}
			if v.boots() != 0 || v.restoredInstance(req.Instance) || m.LeasedCount() != 0 {
				t.Fatal("rejected lifecycle allocated resources")
			}
		})
	}
}

func TestRegionalPrimeRetainsPurposeAndAllowsOnlyCaptureAndTeardown(t *testing.T) {
	v := &primeCaptureVMM{quotaSnapshotVMM: &quotaSnapshotVMM{fakeVMM: &fakeVMM{}}}
	m, _, req := regionalHostFixture(t, v)
	req.PrimeOnly = true
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityPrime
	req.RegionalRuntime.Seat = nil
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.PrimeOnly = false
	if !inst.primeOnly {
		t.Fatal("caller changed retained prime purpose")
	}
	if ns, ok := m.NetnsFor(req.Instance); ok || ns != "" {
		t.Fatal("prime exposed ordinary forwarding")
	}
	if err := m.ResumeVM(t.Context(), req.Instance); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatalf("prime resumed through ordinary path: %v", err)
	}
	if _, err := m.WarmSnapshot(t.Context(), req.Instance, SnapshotSpec{}); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatalf("prime used warm capture: %v", err)
	}
	if _, err := m.SnapshotKeepAlive(t.Context(), req.Instance, SnapshotSpec{}); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatalf("prime entered migration: %v", err)
	}
	if len(v.resumed) != 0 || v.earlyChecks != 0 {
		t.Fatal("rejected prime operation resumed guest")
	}
	if _, err := m.Park(t.Context(), req.Instance, SnapshotSpec{ResumeBeforePublish: true, BeforeResume: func(context.Context) error {
		t.Fatal("destructive capture used caller's resume callback")
		return nil
	}}); err != nil {
		t.Fatal("prime could not capture and tear down", err)
	}
	if len(v.resumed) != 0 || v.earlyChecks != 0 {
		t.Fatal("destructive prime capture resumed guest")
	}
	if m.LeasedCount() != 0 || len(m.LiveInstances()) != 0 {
		t.Fatal("prime capture retained Manager resources")
	}
	// Portable cleanup is not native stop proof and never releases the cell's
	// issued physical hold or authorizes an operational stop signature.
}

func TestRegionalAppTaskRetainsCanonicalPlanAtCommandBoundary(t *testing.T) {
	m, _, req := regionalHostFixture(t, &fakeVMM{})
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityAppTask
	req.RegionalRuntime.Seat = nil
	req.CPUMillicores = api.DefaultAppCPUMillicores
	inst, err := m.WakeAppTask(t.Context(), AppTaskWakeRequest{WakeRequest: req})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Lease.Plan != req.Plan || validateRuntimePlacement(admissionForInstance(inst)) != nil {
		t.Fatal("task lost its actual plan or purpose")
	}
	if ns, ok := m.NetnsFor(req.Instance); ok || ns != "" {
		t.Fatal("task exposed ordinary forwarding")
	}
	inst.Lease.Plan = api.PlanScale
	if _, err := m.ExecuteAppTask(t.Context(), req.Instance, appTaskProtocolRequest("task-plan-test")); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatalf("changed task plan reached command transport: %v", err)
	}
	if m.LeasedCount() != 0 || len(m.LiveInstances()) != 0 {
		t.Fatal("denied task retained Manager resources")
	}
}

func TestForwardingNamespaceExcludesNonServingPurposes(t *testing.T) {
	for _, purpose := range []string{"serving", "prime", "app-task", "execution", "builder"} {
		t.Run(purpose, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			m.RegisterInstanceForTest("resident", "dep", "app", "account")
			m.mu.Lock()
			inst := m.live["resident"]
			inst.Lease.Netns = "fc-resident"
			inst.primeOnly = purpose == "prime"
			inst.AppTaskOnly = purpose == "app-task"
			inst.ExecutionOnly = purpose == "execution"
			inst.Lease.IsBuilder = purpose == "builder"
			m.mu.Unlock()
			ns, ok := m.NetnsFor("resident")
			if ok != (purpose == "serving") || (ok && ns != "fc-resident") || (!ok && ns != "") {
				t.Fatalf("forwarding purpose %s: %q %v", purpose, ns, ok)
			}
		})
	}
}
