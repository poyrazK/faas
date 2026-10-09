package fcvm

// adr: 792 — profile identities distinguish serving workloads and collector lifetimes.

import (
	"testing"
	"time"
)

func TestProfileIdentityRejectsNonServingInstancesAndRotatesLifetime(t *testing.T) {
	inst := &Instance{AccountID: "owner", AppID: "app", DeploymentID: "deployment", Runtime: "node24"}
	m := &Manager{live: map[string]*Instance{"vm": inst}}
	first, err := m.InstanceProfileIdentity("vm")
	if err != nil || first.AccountID != "owner" || first.StartedAt.IsZero() {
		t.Fatalf("identity: %+v, %v", first, err)
	}
	inst.Lease.profileStartedAt = first.StartedAt.Add(time.Second)
	second, err := m.InstanceProfileIdentity("vm")
	if err != nil || first.Generation == second.Generation {
		t.Fatal("replacement process reused profiling identity")
	}
	for _, kind := range []string{"paused", "execution", "task", "missing"} {
		inst.Paused, inst.ExecutionOnly, inst.AppTaskOnly = kind == "paused", kind == "execution", kind == "task"
		id := "vm"
		if kind == "missing" {
			id = "gone"
		}
		if _, err := m.InstanceProfileIdentity(id); err == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
}
