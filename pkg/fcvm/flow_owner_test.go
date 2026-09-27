package fcvm

import (
	"net/netip"
	"testing"
	"time"
)

func TestLookupFlowOwnerLeaseReuseAndAmbiguity(t *testing.T) {
	m := NewManager(nil, nil, Paths{}, "", nil, nil)
	ip := netip.MustParseAddr("10.100.0.5")
	firstAt := time.Now().UTC().Add(-time.Minute)
	m.live["first"] = &Instance{Lease: Lease{HostIP: ip}, flowActivatedAt: firstAt, AccountID: "account-1", AppID: "app-1", DeploymentID: "deployment-1"}
	first, ok := m.LookupFlowOwner(ip.String(), firstAt.Add(time.Second))
	if !ok || first.InstanceID != "first" || first.AccountID != "account-1" || first.DeploymentID != "deployment-1" {
		t.Fatalf("first owner = %#v, ok=%t", first, ok)
	}
	delete(m.live, "first")
	secondAt := firstAt.Add(30 * time.Second)
	m.live["second"] = &Instance{Lease: Lease{HostIP: ip}, flowActivatedAt: secondAt, AccountID: "account-2", AppID: "app-2", DeploymentID: "deployment-2"}
	if _, ok := m.LookupFlowOwner(ip.String(), firstAt.Add(time.Second)); ok {
		t.Fatal("delayed event from previous lease must not be attributed to the new owner")
	}
	second, ok := m.LookupFlowOwner(ip.String(), secondAt.Add(time.Second))
	if !ok || second.InstanceID != "second" || second.AccountID != "account-2" {
		t.Fatalf("reused IP owner = %#v, ok=%t", second, ok)
	}
	m.live["ambiguous"] = &Instance{Lease: Lease{HostIP: ip}, flowActivatedAt: secondAt, AccountID: "account-3"}
	if _, ok := m.LookupFlowOwner(ip.String(), secondAt.Add(time.Second)); ok {
		t.Fatal("ambiguous lease must not be attributed")
	}
}
