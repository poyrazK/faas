// adr: 20261021215353
package sched_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReapEarlyFreezeSelectsQuietInstancesInsideTheIdleTimeout(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	quiet := 10 * time.Second
	base := func(id string, idle time.Duration) sched.InstanceInfo {
		return sched.InstanceInfo{
			Instance: id, AppID: "app-" + id, Plan: api.PlanPro, State: state.StateRunning,
			AppType: string(state.AppTypeApp), LastRequest: now.Add(-idle), Started: now.Add(-time.Hour),
		}
	}
	timeout := time.Duration(sched.EffectiveIdleTimeoutS(api.PlanPro, string(state.AppTypeApp), 0)) * time.Second
	cooldown := now.Add(-5 * time.Second)
	cases := []sched.InstanceInfo{
		base("quiet", 30*time.Second),
		base("busy-recently", 5*time.Second),
		base("past-timeout", timeout+time.Second),
		func() sched.InstanceInfo { in := base("inflight", time.Minute); in.InflightRequests = 1; return in }(),
		func() sched.InstanceInfo { in := base("websocket", time.Minute); in.OpenConns = 1; return in }(),
		func() sched.InstanceInfo { in := base("tail", time.Minute); in.TailCount = 1; return in }(),
		func() sched.InstanceInfo { in := base("floor", time.Minute); in.MinInstances = 1; return in }(),
		func() sched.InstanceInfo {
			in := base("worker", time.Minute)
			in.Mode = string(state.InstanceModeWorker)
			return in
		}(),
		func() sched.InstanceInfo {
			in := base("mirror", time.Minute)
			in.Mode = string(state.InstanceModeMirror)
			return in
		}(),
		func() sched.InstanceInfo { in := base("frozen", time.Minute); in.State = state.StateFrozen; return in }(),
		func() sched.InstanceInfo {
			in := base("cooling", time.Minute)
			in.LastScaleInAt, in.ScaleInCooldownS = &cooldown, 60
			return in
		}(),
		func() sched.InstanceInfo {
			in := base("unknown", time.Minute)
			in.LastRequest, in.Started = time.Time{}, time.Time{}
			return in
		}(),
		func() sched.InstanceInfo {
			in := base("never-served", 0)
			in.LastRequest = time.Time{}
			in.Started = now.Add(-20 * time.Second)
			return in
		}(),
	}
	got := sched.ReapEarlyFreeze(now, cases, quiet)
	if want := []string{"never-served", "quiet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ReapEarlyFreeze = %v, want %v", got, want)
	}
	if sched.ReapEarlyFreeze(now, cases, 0) != nil {
		t.Fatal("a zero delay must select nothing")
	}
}
