package fcvm

import (
	"net/netip"
	"testing"
	"time"
)

// adr: 018 — conntrack attribution uses the instance's host-side IP.
func TestLookupFlowOwnerLeaseReuseAndAmbiguity(t *testing.T) {
	m := NewManager(nil, nil, Paths{}, "", nil, nil)
	ip := netip.MustParseAddr("10.100.0.5")
	firstAt := time.Now().UTC().Add(-time.Minute)
	m.live["first"] = &Instance{Lease: Lease{HostIP: ip}, flowActivatedAt: firstAt, AccountID: "account-1", AppID: "app-1", DeploymentID: "deployment-1"}
	first, ok := m.LookupFlowOwner(ip.String(), firstAt.Add(time.Second))
	if !ok || first.InstanceID != "first" || first.AccountID != "account-1" || first.DeploymentID != "deployment-1" {
		t.Fatalf("first owner = %#v, ok=%t", first, ok)
	}
	m.retireFlowOwnerLocked("first", m.live["first"])
	firstInst := m.live["first"]
	delete(m.live, "first")
	duringTeardown, ok := m.LookupFlowOwner(ip.String(), time.Now().UTC())
	if !ok || duringTeardown.InstanceID != "first" {
		t.Fatalf("owner during teardown = %#v, ok=%t", duringTeardown, ok)
	}
	m.finishFlowOwnerRetirement("first", firstInst)
	retired, ok := m.LookupFlowOwner(ip.String(), firstAt.Add(time.Second))
	if !ok || retired.InstanceID != "first" {
		t.Fatalf("retired owner = %#v, ok=%t", retired, ok)
	}
	secondAt := time.Now().UTC().Add(time.Millisecond)
	m.live["second"] = &Instance{Lease: Lease{HostIP: ip}, flowActivatedAt: secondAt, AccountID: "account-2", AppID: "app-2", DeploymentID: "deployment-2"}
	prior, ok := m.LookupFlowOwner(ip.String(), firstAt.Add(time.Second))
	if !ok || prior.InstanceID != "first" {
		t.Fatalf("delayed event should resolve to the previous lease, got %#v, ok=%t", prior, ok)
	}
	second, ok := m.LookupFlowOwner(ip.String(), secondAt.Add(time.Second))
	if !ok || second.InstanceID != "second" || second.AccountID != "account-2" {
		t.Fatalf("reused IP owner = %#v, ok=%t", second, ok)
	}
	m.retiredFlowLeases[ip.String()] = append(m.retiredFlowLeases[ip.String()], retiredFlowLease{
		owner: FlowOwner{InstanceID: "overlap", AccountID: "account-4"},
		from:  secondAt, until: secondAt.Add(2 * time.Second),
	})
	if _, ok := m.LookupFlowOwner(ip.String(), secondAt.Add(time.Second)); ok {
		t.Fatal("overlapping live and retired leases must be ambiguous")
	}
	m.retiredFlowLeases[ip.String()] = m.retiredFlowLeases[ip.String()][:1]
	m.live["ambiguous"] = &Instance{Lease: Lease{HostIP: ip}, flowActivatedAt: secondAt, AccountID: "account-3"}
	if _, ok := m.LookupFlowOwner(ip.String(), secondAt.Add(time.Second)); ok {
		t.Fatal("ambiguous lease must not be attributed")
	}
}
