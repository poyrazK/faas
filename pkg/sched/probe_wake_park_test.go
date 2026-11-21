package sched

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261116120000 — an instance only a baseline health probe woke parks
// once the probe's grace has passed, despite the gateway's keep-alive
// connection; a customer request after the probe, work in flight, a grace
// not yet over or the app's floor keep it running.
func TestReapIdleParksProbeOnlyInstances(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	done := now.Add(-api.ProtectionProbeParkGrace - time.Second)
	probe := func(id string, edit func(*InstanceInfo)) InstanceInfo {
		in := InstanceInfo{
			Instance: id, AppID: "app-" + id, Plan: api.PlanPro, AppType: string(state.AppTypeApp), State: state.StateRunning,
			Started: done.Add(-3 * time.Second), LastRequest: done.Add(-time.Second), ProbeWakeDoneAt: done,
			IdleTimeoutS: 300, OpenConns: 1,
		}
		if edit != nil {
			edit(&in)
		}
		return in
	}
	instances := []InstanceInfo{
		probe("probe-only", nil),
		probe("customer-after", func(in *InstanceInfo) { in.LastRequest = done.Add(5 * time.Second) }),
		probe("in-flight", func(in *InstanceInfo) { in.InflightRequests = 1 }),
		probe("in-grace", func(in *InstanceInfo) {
			in.ProbeWakeDoneAt = now.Add(-time.Second)
			in.LastRequest = now.Add(-2 * time.Second)
		}),
		probe("floor", func(in *InstanceInfo) { in.MinInstances = 1 }),
		probe("no-probe", func(in *InstanceInfo) { in.ProbeWakeDoneAt = time.Time{}; in.OpenConns = 0 }),
	}
	got := ReapIdle(now, instances, nil, nil)
	if !slices.Equal(got, []string{"probe-only"}) {
		t.Fatalf("ReapIdle = %v; want only the probe-only instance", got)
	}
	if !instances[0].probeOnly() || instances[1].probeOnly() {
		t.Fatal("probeOnly: want the probe-only instance and not the one a customer used after the probe")
	}
}
