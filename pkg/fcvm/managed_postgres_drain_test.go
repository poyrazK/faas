//go:build linux || darwin

package fcvm

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresDrainFrame() state.ManagedPostgresDrainFrame {
	return state.ManagedPostgresDrainFrame{ID: uuid.NewString(), CutoverID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(),
		InstanceID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), OwnerNodeID: uuid.NewString(), OwnerRevision: 7, FencedAt: time.Now().UTC()}
}

// These tests exercise the production journal and Manager against fixture
// process/network observations. They do not supply native KVM acceptance.
func TestManagedPostgresDrainLostReplyAndManagerRestart(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	f := postgresDrainFrame()
	m.WithManagedPostgresDrainNodeID(f.NodeID)
	prepareLocallyOwnedDrain(t, v, f.InstanceID)
	p, err := m.RetireManagedPostgresDrain(t.Context(), f)
	if err != nil || !p.ValidFor(f) || m.alloc.InUse() != 0 {
		t.Fatalf("retirement: %+v %v", p, err)
	}
	// Lose the RPC reply and restart Manager on the same durable host journal.
	closeDrainDaemonOwner(t, v)
	restarted, secondVMM, _ := nativeManagerFixture(t)
	secondVMM.nativeRecovery.journal = v.nativeRecovery.journal
	restarted.WithManagedPostgresDrainNodeID(f.NodeID)
	again, err := restarted.RetireManagedPostgresDrain(t.Context(), f)
	if err != nil || again != p || restarted.alloc.InUse() != 0 {
		t.Fatalf("restart changed receipt: %+v %v", again, err)
	}
	if err := v.nativeRecovery.journal.prepare(t.Context(), leaseForSlot(f.InstanceID, 0)); err == nil {
		t.Fatal("late producer replaced retired generation")
	}
}

func closeDrainDaemonOwner(t *testing.T, v *JailerVMM) {
	t.Helper()
	r := v.nativeRecovery
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.daemonLock != nil {
		if err := r.daemonLock.Close(); err != nil {
			t.Fatal(err)
		}
		r.daemonLock = nil
	}
}

func TestManagedPostgresDrainCrashBeforeCleanupCannotInferHelperAbsence(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	f := postgresDrainFrame()
	prepareLocallyOwnedDrain(t, v, f.InstanceID)
	closeDrainDaemonOwner(t, v)
	restarted, secondVMM, _ := nativeManagerFixture(t)
	secondVMM.nativeRecovery.journal = v.nativeRecovery.journal
	restarted.WithManagedPostgresDrainNodeID(f.NodeID)
	p, err := restarted.RetireManagedPostgresDrain(t.Context(), f)
	if err == nil || p.ValidFor(f) || restarted.alloc.InUse() != 1 {
		t.Fatal("unknown helper ownership was acknowledged", p, err)
	}
}

func TestManagedPostgresDrainFailedCleanupRetainsNativeOwnership(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	f := postgresDrainFrame()
	m.WithManagedPostgresDrainNodeID(f.NodeID)
	prepareLocallyOwnedDrain(t, v, f.InstanceID)
	cause := errors.New("namespace remains")
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return cause }
	p, err := m.RetireManagedPostgresDrain(t.Context(), f)
	if !errors.Is(err, cause) || p.ValidFor(f) || m.alloc.InUse() != 1 {
		t.Fatalf("uncertain cleanup acknowledged: %+v %v", p, err)
	}
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return nil }
	p, err = m.RetireManagedPostgresDrain(t.Context(), f)
	if err != nil || !p.ValidFor(f) || m.alloc.InUse() != 0 {
		t.Fatalf("cleanup retry: %+v %v", p, err)
	}
}

func TestManagedPostgresDrainJoinsDelayedProducer(t *testing.T) {
	ctx := t.Context()
	m, v, _ := nativeManagerFixture(t)
	f := postgresDrainFrame()
	m.WithManagedPostgresDrainNodeID(f.NodeID)
	bootCtx, flight, err := m.beginInstanceBoot(ctx, f.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	producer := make(chan error, 1)
	go func() {
		<-bootCtx.Done()
		// The stop must join even a producer that publishes after cancellation.
		lease := leaseForSlot(f.InstanceID, 0)
		lease.Plan = api.PlanHobby
		producer <- v.prepareNativeLease(ctx, lease)
		m.finishInstanceFlight(f.InstanceID, flight)
	}()
	p, err := m.RetireManagedPostgresDrain(ctx, f)
	if err != nil || !p.ValidFor(f) {
		t.Fatalf("delayed producer escaped: %+v %v", p, err)
	}
	if err := <-producer; err != nil {
		t.Fatal(err)
	}
}

func prepareLocallyOwnedDrain(t *testing.T, v *JailerVMM, instance string) {
	t.Helper()
	l := leaseForSlot(instance, 0)
	l.Plan = api.PlanHobby
	if err := v.prepareNativeLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
}

func TestManagedPostgresDrainRejectsMissingOrStaleHostEvidence(t *testing.T) {
	for _, failure := range []string{"missing_journal", "kernel_changed", "wrong_node", "live_identity", "legacy_vmm"} {
		t.Run(failure, func(t *testing.T) {
			m, v, _ := nativeManagerFixture(t)
			f := postgresDrainFrame()
			m.WithManagedPostgresDrainNodeID(f.NodeID)
			if failure != "missing_journal" {
				prepareRecoveredNative(t, v, 0, f.InstanceID)
			}
			switch failure {
			case "kernel_changed":
				v.nativeRecovery.journal.bootID = func() (string, error) { return uuid.NewString(), nil }
			case "wrong_node":
				m.WithManagedPostgresDrainNodeID(uuid.NewString())
			case "live_identity":
				m.live[f.InstanceID] = &Instance{AppID: uuid.NewString(), Lease: leaseForSlot(f.InstanceID, 0)}
			case "legacy_vmm":
				m.vmm = &fakeVMM{}
			}
			p, err := m.RetireManagedPostgresDrain(t.Context(), f)
			if err == nil || p.ValidFor(f) {
				t.Fatalf("unconfirmed evidence passed: %+v %v", p, err)
			}
		})
	}
}
