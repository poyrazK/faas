package state

// adr: 593. Native publication must retain original owner and config fences.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestMemOwnedApplicationStandardRuntimePublication(t *testing.T) {
	ownedStandardRuntimePublication(t, NewMemStore())
}

func TestMemOwnedApplicationStandardWarmPromotion(t *testing.T) {
	ownedStandardWarmPromotion(t, NewMemStore())
}

func ownedStandardPublicationFixture(t *testing.T, s standardConfigTestStore, paused bool) (RuntimeInstancePublication, runtimeadmission.Receipt) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	status := StateColdBooting
	if paused {
		status = StateWaking
	}
	ins, _, receipt := nativeBootTestAttempt(t, s, f, status)
	receipt.Paused = paused
	values, err := s.RuntimeAppValuesForDeployment(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	p := RuntimeInstancePublication{AccountID: f.app.AccountID, AppID: f.app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
		ExpectedState: ins.State, Netns: receipt.Netns, HostIP: receipt.HostIP, GuestUID: int(receipt.LeaseUID),
		Fence: fence.SecretFence, ConfigFence: fence, AdmissionReceipt: &receipt}
	if paused {
		p.TargetState = string(StateWarm)
	} else {
		p.Inputs = &RuntimeConfigInputs{Scope: normalizedDeploymentScope(f.dep.Scope), Boundary: time.Now().UTC().Truncate(time.Microsecond)}
	}
	if err := validateRuntimeInstancePublication(p); err != nil {
		t.Fatalf("fixture owner/receipt validation: %v", err)
	}
	return p, receipt
}

func ownedStandardRuntimePublication(t *testing.T, s standardConfigTestStore) {
	t.Helper()
	p, receipt := ownedStandardPublicationFixture(t, s, false)
	for _, change := range []func(*RuntimeInstancePublication){
		func(p *RuntimeInstancePublication) { p.WakeID = uuid.NewString() },
		func(p *RuntimeInstancePublication) { p.ConfigFence.Fingerprint = strings.Repeat("f", 64) },
		func(p *RuntimeInstancePublication) { p.AdmissionReceipt = nil },
	} {
		bad := p
		change(&bad)
		if _, err := s.PublishOwnedInstanceRuntime(t.Context(), bad); err == nil {
			t.Fatal("native publication bypassed owner/config/receipt fence")
		}
		actual, err := s.InstanceByID(t.Context(), p.InstanceID)
		if err != nil || actual.State != p.ExpectedState || actual.Netns != "" {
			t.Fatalf("refused publication changed runtime: %+v %v", actual, err)
		}
		if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), p.InstanceID); err == nil {
			t.Fatal("refused publication retained native receipt")
		}
		if _, err := s.InstanceRuntimeConfigFence(t.Context(), p.AccountID, p.AppID, p.InstanceID); err == nil {
			t.Fatal("refused publication retained owner proof")
		}
	}
	actual, err := s.PublishOwnedInstanceRuntime(t.Context(), p)
	if err != nil || actual.State != string(StateRunning) {
		t.Fatalf("owned native publication: %+v %v", actual, err)
	}
	retained, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), p.InstanceID)
	if err != nil || !retained.Equal(receipt) {
		t.Fatalf("native receipt missing after owner publication: %v", err)
	}
	proof, err := s.InstanceRuntimeConfigFence(t.Context(), p.AccountID, p.AppID, p.InstanceID)
	if err != nil || proof != p.ConfigFence {
		t.Fatalf("owned native publication lost config fence: %+v %v", proof, err)
	}
}

func ownedStandardWarmPromotion(t *testing.T, s standardConfigTestStore) {
	t.Helper()
	p, parent := ownedStandardPublicationFixture(t, s, true)
	if _, err := s.PublishOwnedInstanceRuntime(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	grant, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	receipt := parent.Clone()
	receipt.Binding, receipt.Paused, receipt.CompletedAtUnixNano = grant.Binding, false, time.Now().UnixNano()
	p.ExpectedState, p.TargetState, p.AdmissionReceipt, p.PromotionReceipt = string(StateWarm), string(StateRunning), nil, &receipt
	bad := p
	bad.ConfigFence.Fingerprint = strings.Repeat("f", 64)
	if _, err := s.PublishOwnedInstanceRuntime(t.Context(), bad); err == nil {
		t.Fatal("warm promotion bypassed retained config proof")
	}
	for range 2 {
		actual, err := s.PublishOwnedInstanceRuntime(t.Context(), p)
		if err != nil || actual.State != string(StateRunning) {
			t.Fatalf("owned warm promotion or exact retry: %+v %v", actual, err)
		}
	}
}
