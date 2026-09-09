package fcvm

// adr: 101

import "testing"

func TestManagerInstanceIdentity(t *testing.T) {
	m := &Manager{live: map[string]*Instance{
		"inst-1": {AppID: "app-1", AccountID: "acct-1"},
	}}
	appID, accountID, err := m.InstanceIdentity("inst-1")
	if err != nil {
		t.Fatal(err)
	}
	if appID != "app-1" || accountID != "acct-1" {
		t.Fatalf("identity = %q/%q", appID, accountID)
	}
	if _, _, err := m.InstanceIdentity("missing"); err == nil {
		t.Fatal("missing instance unexpectedly resolved")
	}
	m.cidToID = map[uint32]string{42: "inst-1"}
	instance, appID, accountID, err := m.InstanceIdentityByCID(42)
	if err != nil || instance != "inst-1" || appID != "app-1" || accountID != "acct-1" {
		t.Fatalf("cid identity = %q/%q/%q, %v", instance, appID, accountID, err)
	}
}
