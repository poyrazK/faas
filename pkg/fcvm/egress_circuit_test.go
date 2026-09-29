// adr: 375
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func circuitWakeRequest(id, appID string) WakeRequest {
	return WakeRequest{Instance: id, AppID: appID, BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, EgressMbit: 100, Snapshot: usableSnapshot()}
}

func circuitTarget(addr string, port int) netns.EgressCircuitTarget {
	return netns.EgressCircuitTarget{Addr: netip.MustParseAddr(addr), Port: port}
}

func TestEgressCircuitDisabledIsNotAcknowledged(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.UpdateEgressCircuit(t.Context(), "app", nil); !errors.Is(err, ErrEgressCircuitDisabled) {
		t.Fatalf("disabled node acknowledged enforcement: %v", err)
	}
}

func TestPreparedWakeSeedsEnabledCircuitBeforeRestore(t *testing.T) {
	m, pool := testPreparedPool(t, 1)
	m.WithEgressCircuitBreaker(true)
	fillTestPreparedPool(t, m, pool, 250)
	if len(pool.ready) != 1 || !pool.ready[0].config.EgressCircuitEnabled {
		t.Fatal("prepared network omitted circuit enforcement")
	}
	run := m.run.(*fakeRunner)
	before := run.setupCount
	if err := m.UpdateEgressCircuit(t.Context(), "app", []netns.EgressCircuitTarget{circuitTarget("2001:db8::1", 5432)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "circuit-prepared") })
	req := circuitWakeRequest("circuit-prepared", "app")
	req.Plan, req.EgressMbit = api.PlanScale, 250
	_, err := m.WakeWithNetworkReady(t.Context(), req, func(_ WakeNetworkReady) {
		if !run.ran("2001:db8::1 . 5432") {
			t.Fatal("prepared network reached guest execution before seed")
		}
	})
	if err != nil || run.setupCount != before {
		t.Fatalf("prepared wake error=%v rebuilt=%v", err, run.setupCount != before)
	}
}

func TestWakeSeedsEgressCircuitBeforeRestoreAndPatchesPendingNetwork(t *testing.T) {
	run, vmm := &fakeInputRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm).WithEgressCircuitBreaker(true)
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "circuit-wake") })
	initial := []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432)}
	if err := m.UpdateEgressCircuit(t.Context(), "app", initial); err != nil {
		t.Fatal(err)
	}
	_, err := m.WakeWithNetworkReady(t.Context(), circuitWakeRequest("circuit-wake", "app"), func(_ WakeNetworkReady) {
		if len(vmm.restored) != 0 || !strings.Contains(string(run.input), "203.0.113.9 . 5432") {
			t.Fatal("guest restore preceded circuit seed")
		}
		if m.LiveCount() != 0 {
			t.Fatal("test requires a pending network before live publication")
		}
		before := run.inputRuns
		updated := []netns.EgressCircuitTarget{initial[0], circuitTarget("2001:db8::1", 6379)}
		if err := m.UpdateEgressCircuit(t.Context(), "app", updated); err != nil {
			t.Fatal(err)
		}
		if run.inputRuns != before+1 || !strings.Contains(string(run.input), "2001:db8::1 . 6379") {
			t.Fatal("pending wake did not receive a single atomic dual-family transaction")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEgressCircuitSeedFailurePreventsGuestRestore(t *testing.T) {
	run, vmm := &fakeRunner{failOn: "203.0.113.9 . 5432"}, &fakeVMM{}
	m := newTestManager(run, vmm).WithEgressCircuitBreaker(true)
	if err := m.UpdateEgressCircuit(t.Context(), "app", []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), circuitWakeRequest("circuit-failure", "app")); err == nil {
		t.Fatal("wake succeeded without enforcing desired circuits")
	}
	if len(vmm.restored) != 0 || m.LeasedCount() != 0 || len(m.egressCircuitNetworks) != 0 {
		t.Fatalf("failed seed restored guest or leaked resources: restores=%d leases=%d networks=%d", len(vmm.restored), m.LeasedCount(), len(m.egressCircuitNetworks))
	}
}

func TestEgressCircuitCloseSurvivesParkAndDoesNotPatchTornDownNamespace(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{}).WithEgressCircuitBreaker(true)
	if _, err := m.Wake(t.Context(), circuitWakeRequest("circuit-live", "app")); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateEgressCircuit(t.Context(), "app", []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432)}); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(t.Context(), "circuit-live"); err != nil {
		t.Fatal(err)
	}
	before := run.inputRuns
	if err := m.UpdateEgressCircuit(t.Context(), "app", nil); err != nil {
		t.Fatal(err)
	}
	if run.inputRuns != before || len(m.egressCircuitNetworks) != 0 {
		t.Fatal("update attempted to patch a torn-down namespace")
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "circuit-next") })
	if _, err := m.Wake(t.Context(), circuitWakeRequest("circuit-next", "app")); err != nil {
		t.Fatal(err)
	}
	if _, exists := m.appEgressCircuits["app"]; exists || strings.Contains(string(run.input), "203.0.113.9 . 5432") {
		t.Fatal("next wake inherited a closed circuit")
	}
}

func TestEgressCircuitInvalidUpdatePreservesDesiredState(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithEgressCircuitBreaker(true)
	good := []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432)}
	if err := m.UpdateEgressCircuit(t.Context(), "app", good); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateEgressCircuit(t.Context(), "app", []netns.EgressCircuitTarget{{Port: 5432}}); err == nil {
		t.Fatal("malformed complete-set update accepted")
	}
	if !reflect.DeepEqual(m.appEgressCircuits["app"], good) {
		t.Fatal("malformed update replaced valid desired state")
	}
}

func TestCanonicalEgressCircuitTargets(t *testing.T) {
	v4, v6 := circuitTarget("203.0.113.9", 5432), circuitTarget("2001:db8::1", 6379)
	got, err := canonicalEgressCircuitTargets([]netns.EgressCircuitTarget{v6, v4, circuitTarget("::ffff:203.0.113.9", 5432)})
	if err != nil || !reflect.DeepEqual(got, []netns.EgressCircuitTarget{v4, v6}) {
		t.Fatalf("normalized union=%v, error=%v", got, err)
	}
}

func TestEgressCircuitDurableSeedOnReplacementNode(t *testing.T) {
	targets := []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432), circuitTarget("2001:db8::1", 5432)}
	for _, id := range []string{"circuit-node-one", "circuit-replacement"} {
		t.Run(id, func(t *testing.T) {
			run := &fakeInputRunner{}
			m := newTestManager(run, &fakeVMM{}).WithEgressCircuitBreaker(true).
				WithEgressCircuitSource(func(context.Context, string) (netns.EgressCircuitSnapshot, error) {
					return netns.EgressCircuitSnapshot{Revision: 7, Targets: targets}, nil
				})
			t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
			_, err := m.WakeWithNetworkReady(t.Context(), circuitWakeRequest(id, "app"), func(_ WakeNetworkReady) {
				if m.appEgressCircuitRevisions["app"] != 7 || !strings.Contains(string(run.input), "2001:db8::1 . 5432") {
					t.Fatal("new daemon boot did not seed durable dual-family policy")
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEgressCircuitDurableReadFailureRefusesBoot(t *testing.T) {
	vmm := &fakeVMM{}
	m := newTestManager(&fakeInputRunner{}, vmm).WithEgressCircuitBreaker(true).
		WithEgressCircuitSource(func(context.Context, string) (netns.EgressCircuitSnapshot, error) {
			return netns.EgressCircuitSnapshot{}, errors.New("store unavailable")
		})
	if _, err := m.Wake(t.Context(), circuitWakeRequest("circuit-no-store", "app")); err == nil || len(vmm.restored) != 0 || m.LeasedCount() != 0 {
		t.Fatalf("durable policy failure did not safely refuse boot: error=%v", err)
	}
}

func TestEgressCircuitDelayedRevisionCannotReopenClosedCircuit(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{}).WithEgressCircuitBreaker(true)
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "circuit-revision") })
	if _, err := m.Wake(t.Context(), circuitWakeRequest("circuit-revision", "app")); err != nil {
		t.Fatal(err)
	}
	open := netns.EgressCircuitSnapshot{Revision: 1, Targets: []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432)}}
	if _, err := m.UpdateEgressCircuitRevision(t.Context(), "app", open); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateEgressCircuitRevision(t.Context(), "app", netns.EgressCircuitSnapshot{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	revision, err := m.UpdateEgressCircuitRevision(t.Context(), "app", open)
	if err != nil || revision != 2 || strings.Contains(string(run.input), "add element") {
		t.Fatalf("delayed update regressed policy: revision=%d error=%v transaction=%s", revision, err, run.input)
	}
	_, err = m.UpdateEgressCircuitRevision(t.Context(), "app", netns.EgressCircuitSnapshot{Revision: 2, Targets: open.Targets})
	if !errors.Is(err, ErrEgressCircuitRevision) {
		t.Fatalf("inconsistent revision accepted: %v", err)
	}
}

type circuitFailingInputRunner struct {
	fakeInputRunner
	err error
}

func (r *circuitFailingInputRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	_ = r.fakeInputRunner.RunInput(ctx, argv, input)
	return r.err
}

func TestEgressCircuitStatusReportsPendingUntilNftSucceeds(t *testing.T) {
	run := &circuitFailingInputRunner{}
	m := newTestManager(run, &fakeVMM{}).WithEgressCircuitBreaker(true)
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "circuit-status") })
	if _, err := m.Wake(t.Context(), circuitWakeRequest("circuit-status", "app")); err != nil {
		t.Fatal(err)
	}
	snapshot := netns.EgressCircuitSnapshot{Revision: 3, Targets: []netns.EgressCircuitTarget{circuitTarget("203.0.113.9", 5432)}}
	run.err = errors.New("nft failed")
	if _, err := m.UpdateEgressCircuitRevision(t.Context(), "app", snapshot); err == nil {
		t.Fatal("failed nft acknowledged")
	}
	state, exists := m.EgressCircuitStatus("circuit-status")
	if !exists || !state.Enabled || state.Applied || state.DesiredRevision != 3 || state.AppliedRevision != 0 {
		t.Fatalf("failure reported as enforcement: %+v", state)
	}
	run.err = nil
	if _, err := m.UpdateEgressCircuitRevision(t.Context(), "app", snapshot); err != nil {
		t.Fatal(err)
	}
	state, _ = m.EgressCircuitStatus("circuit-status")
	if !state.Applied || state.AppliedRevision != 3 || state.TargetCount != 1 {
		t.Fatalf("successful retry not reported: %+v", state)
	}
}
