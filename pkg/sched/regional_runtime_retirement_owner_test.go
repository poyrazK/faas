package sched_test

// adr: 632

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestConfiguredEngineRetiresOriginalRegionalPermit(t *testing.T) {
	f := newRetirementCell(t, true)
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
	owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
	if err != nil {
		t.Fatal(err)
	}
	e, err := sched.NewEngine(t.Context(), f.store, sched.NewNodeLedger(), router, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RetireOriginalRegionalRuntime(t.Context(), f.p.InstanceID, f.p, uuid.NewString()); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("unconfigured owner accepted", err)
	}
	e.WithRegionalRuntimeRetirement(owner)
	// Wiring keeps a copy; later accidental caller mutation cannot replace the signer.
	owner.Signer = runtimequota.Signer{}
	if err := f.store.DeleteApp(t.Context(), f.p.Grant.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	r, err := e.RetireOriginalRegionalRuntime(t.Context(), f.p.InstanceID, f.p, uuid.NewString())
	if err != nil || r.StopEnvelope == nil || r.ReleasedAt == nil {
		t.Fatal("original retired permit was not released", err)
	}
	if head, err := f.store.RegionalRuntimeQuotaHead(t.Context(), f.p.Grant.Binding.RegionalAppID); err != nil || head.AdmissionEnabled {
		t.Fatal("retirement changed the retained frozen quota head", err)
	}
	if v.calls.Load() != 1 {
		t.Fatal("unexpected native calls")
	}
	e.WithRegionalRuntimeRetirement(nil)
	if _, err := e.RetireOriginalRegionalRuntime(t.Context(), f.p.InstanceID, f.p, uuid.NewString()); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("disabled owner accepted", err)
	}
}

func TestRegionalRuntimeRetirementOwnerRequiresTrustAndResolver(t *testing.T) {
	f := newRetirementCell(t, false)
	keys := runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}
	if _, err := sched.NewRegionalRuntimeRetirement(f.store, keys, sched.NewVMMRouter(nil, nil, nil)); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("missing resolver accepted", err)
	}
	router := sched.NewVMMRouter(nil, nil, nil).WithRegionalRuntimePeerResolver(retirementPeerResolver{})
	keys.Trust = runtimequota.Trust{}
	if _, err := sched.NewRegionalRuntimeRetirement(f.store, keys, router); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("untrusted signer accepted", err)
	}
}
