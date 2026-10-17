package fcvm

// adr:1543 — managed probes require the original live caller and reviewed release.

import (
	"net/netip"
	"testing"
)

func TestServiceCallerIdentityTracksLiveNetworkOwnership(t *testing.T) {
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
	ip := netip.MustParseAddr("10.100.0.7")
	inst := &Instance{Lease: Lease{Instance: "task", HostIP: ip}, AppTaskOnly: true,
		AccountID: "account", AppID: "caller", DeploymentID: "deployment"}
	m.live["task"] = inst
	identity, matched := m.ResolveServiceCaller(ip)
	if !matched || identity.InstanceID != "task" || !identity.AppTask || identity.AppID != "caller" || identity.DeploymentID != "deployment" {
		t.Fatalf("live task identity = %+v, %v", identity, matched)
	}
	delete(m.live, "task")
	if _, matched := m.ResolveServiceCaller(ip); matched {
		t.Fatal("retired task address still authenticated")
	}
	m.live["replacement"] = &Instance{Lease: Lease{HostIP: ip}, AppID: "other", DeploymentID: "other-deployment"}
	identity, matched = m.ResolveServiceCaller(ip)
	if !matched || identity.AppTask || identity.AppID != "other" {
		t.Fatalf("reused address retained task identity: %+v, %v", identity, matched)
	}
	m.live["collision"] = inst
	if _, matched := m.ResolveServiceCaller(ip); matched {
		t.Fatal("ambiguous address authenticated")
	}
}

func TestServiceCallerIdentityRejectsIneligibleSlots(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Instance)
	}{
		{"paused", func(i *Instance) { i.Paused = true }},
		{"execution", func(i *Instance) { i.ExecutionOnly = true }},
		{"qualification", func(i *Instance) { i.QualificationOnly = true }},
		{"builder", func(i *Instance) { i.Lease.IsBuilder = true }},
		{"networkless", func(i *Instance) { i.Lease.Networkless = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{}, "1.0.0", nil, nil)
			ip := netip.MustParseAddr("10.100.0.7")
			inst := &Instance{Lease: Lease{HostIP: ip}, AppTaskOnly: true, AppID: "caller", DeploymentID: "deployment"}
			test.edit(inst)
			m.live["task"] = inst
			identity, _ := m.ResolveServiceCaller(ip)
			if identity.AppTask || identity.AppID != "" {
				t.Fatalf("ineligible slot authenticated: %+v", identity)
			}
		})
	}
}
