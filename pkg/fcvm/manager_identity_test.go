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

// A token names the deployment scope its instance runs (ADR-1141 amendment
// 2026-10-25), for a live instance and for one still booting.
func TestManagerWorkloadTokenDeployment(t *testing.T) {
	m := &Manager{
		live:   map[string]*Instance{"live-1": {AppID: "app-1", AccountID: "acct-1", DeploymentID: "dep-live"}},
		waking: map[string]wakingInstance{"boot-1": {deploymentID: "dep-boot", appID: "app-2", accountID: "acct-2"}},
	}
	for instance, want := range map[string]string{"live-1": "dep-live", "boot-1": "dep-boot", "missing": ""} {
		if got := m.WorkloadTokenDeployment(instance); got != want {
			t.Errorf("WorkloadTokenDeployment(%q) = %q, want %q", instance, got, want)
		}
	}
}

// A booting guest's workload asks for queue tokens before readiness; the
// wake already fixed its identity (ADR-1141 amendment 2026-10-17).
func TestManagerWorkloadTokenIdentity(t *testing.T) {
	m := &Manager{
		live: map[string]*Instance{"live-1": {AppID: "app-1", AccountID: "acct-1"}},
		waking: map[string]wakingInstance{
			"boot-1":          {appID: "app-2", accountID: "acct-2"},
			"qualification-1": {},
			"contained-1":     {appID: "app-3", accountID: "acct-3", contained: true},
		},
	}
	tests := []struct {
		instance        string
		app, account    string
		resolves        bool
		instanceResolve bool // InstanceIdentity keeps the live-only rule
	}{
		{instance: "live-1", app: "app-1", account: "acct-1", resolves: true, instanceResolve: true},
		{instance: "boot-1", app: "app-2", account: "acct-2", resolves: true},
		{instance: "qualification-1"},
		{instance: "contained-1"}, // a rehearsal-contained wake gets no token
		{instance: "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.instance, func(t *testing.T) {
			appID, accountID, err := m.WorkloadTokenIdentity(t.Context(), tt.instance)
			if tt.resolves != (err == nil) || appID != tt.app || accountID != tt.account {
				t.Fatalf("WorkloadTokenIdentity = %q/%q, %v; want %q/%q resolves=%v", appID, accountID, err, tt.app, tt.account, tt.resolves)
			}
			if _, _, err := m.InstanceIdentity(tt.instance); tt.instanceResolve != (err == nil) {
				t.Fatalf("InstanceIdentity err = %v, want resolves=%v", err, tt.instanceResolve)
			}
		})
	}
}
