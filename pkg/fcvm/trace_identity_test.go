package fcvm

// adr: 957 — trace identities come only from serving workloads.

import "testing"

func TestTraceIdentityRejectsNonServingInstances(t *testing.T) {
	inst := &Instance{AccountID: "owner", AppID: "app", DeploymentID: "deployment"}
	m := &Manager{live: map[string]*Instance{"vm": inst}}
	got, err := m.InstanceTraceIdentity("vm")
	if err != nil || got != (TraceInstanceIdentity{AccountID: "owner", AppID: "app", DeploymentID: "deployment"}) {
		t.Fatalf("identity: %+v, %v", got, err)
	}
	if !inst.Lease.profileStartedAt.IsZero() {
		t.Fatal("trace identity mutated profiling lifetime state")
	}
	for _, kind := range []string{"paused", "execution", "task", "missing", "incomplete"} {
		inst.Paused, inst.ExecutionOnly, inst.AppTaskOnly = kind == "paused", kind == "execution", kind == "task"
		inst.DeploymentID = "deployment"
		if kind == "incomplete" {
			inst.DeploymentID = ""
		}
		id := "vm"
		if kind == "missing" {
			id = "gone"
		}
		if _, err := m.InstanceTraceIdentity(id); err == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
}
