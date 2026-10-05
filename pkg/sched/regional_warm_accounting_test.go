package sched

// adr: 627
import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalWarmAccountingIdentityAndConservation(t *testing.T) {
	f := newRegionalWarmFixture(t)
	l := f.engine.ledger
	ram := l.ResidentRAM()
	for _, pin := range []string{"app", "deployment", "node", "instance", "purpose"} {
		p := f.physical
		switch pin {
		case "app":
			p.Grant.Binding.RegionalAppID = uuid.NewString()
		case "deployment":
			p.DeploymentID = uuid.NewString()
		case "node":
			p.NodeID = uuid.NewString()
		case "instance":
			p.InstanceID = uuid.NewString()
		case "purpose":
			p.Kind = runtimequota.CapacityServing
		}
		if err := l.reserveRegionalWarmResume(f.warm.ID, p, 1, false); !errors.Is(err, runtimequota.ErrBinding) || l.Concurrency(f.app.ID) != 0 {
			t.Fatal("local reservation crossed identity", pin, err)
		}
	}
	if err := l.reserveRegionalWarmResume(f.warm.ID, f.physical, 1, false); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if err := l.reserveRegionalWarmResume(f.warm.ID, f.physical, 1, false); err != nil {
			t.Fatal(err)
		}
		l.BeginSnapshot(f.warm.ID)
		if l.Concurrency(f.app.ID) != 1 || l.ConcurrencyForDeployment(f.app.ID, f.dep.ID) != 1 || l.ResidentRAM() != ram {
			t.Fatal("retry or snapshot bookkeeping released/doubled a serving hold")
		}
	}
	other := f.physical
	other.ID = uuid.NewString()
	if err := l.reserveRegionalWarmResume(f.warm.ID, other, 1, true); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("recovery rebound a physical attempt", err)
	}
	// Recovery counts existing durable holds even when new admission is full.
	fresh := NewNodeLedger()
	request := Request{Instance: f.warm.ID, AppID: f.app.ID, DeploymentID: f.dep.ID, Plan: f.acct.Plan, NodeID: f.warm.NodeID, RAMMB: f.warm.RAMMB, MaxConcurrency: 1, Kind: KindWarmPool}
	if err := fresh.Admit(request); err != nil {
		t.Fatal(err)
	}
	request.Instance, request.Kind = uuid.NewString(), KindWake
	if err := fresh.Admit(request); err != nil {
		t.Fatal(err)
	}
	if err := fresh.reserveRegionalWarmResume(f.warm.ID, f.physical, 1, false); !errors.Is(err, state.ErrRegionalRuntimeCapacity) {
		t.Fatal(err)
	}
	if err := fresh.reserveRegionalWarmResume(f.warm.ID, f.physical, 0, true); err != nil || fresh.Concurrency(f.app.ID) != 2 {
		t.Fatal("recovery lost an above-cap hold", err)
	}
}
