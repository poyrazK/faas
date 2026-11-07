// adr: 20261107122928 — with idle_ladder_idle_out on, the idle ladder owns
// an idle instance vmmd holds quiet-paused: the reaper leaves it RUNNING past
// its idle timeout, never freezes it, parks it at L5, and RAM pressure takes
// it before any instance still inside its idle timeout.
package sched_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type ladderActivity map[string]sched.InstanceActivity

func (a ladderActivity) SnapshotActivity(time.Time) map[string]sched.InstanceActivity { return a }

func TestReaperLeavesIdleOutToTheLadder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		released bool
		level    int
		// state after one tick 2 min and one 11 min after the last request
		at2m, at11m state.State
	}{
		{name: "held at L2", released: true, level: 2, at2m: state.StateRunning, at11m: state.StateParked},
		{name: "held at L1", released: true, level: 1, at2m: state.StateRunning, at11m: state.StateParked},
		{name: "running guest idles out as before", released: true, level: 0, at2m: state.StateFrozen},
		{name: "account not released", released: false, level: 2, at2m: state.StateFrozen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sched.NewFrozenFixture(t, true)
			e, store, ins := *f.E(), *f.Store(), *f.Ins()
			e.EnableFrozenTier()
			var subjects []platformflags.Subject
			e.WithIdleLadderIdleOut(func(s platformflags.Subject) bool {
				subjects = append(subjects, s)
				return tc.released
			})
			ctx := t.Context()
			last := time.Now()
			if _, err := store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: ins.ID, LastRequest: last}}); err != nil {
				t.Fatal(err)
			}
			activity := ladderActivity{ins.ID: {IdleLadderLevel: tc.level}}
			reap := func(at time.Time) state.State {
				t.Helper()
				sched.NewLoop(nil, e, sched.TestLog()).WithClock(func() time.Time { return at }).
					WithInstanceActivity(activity).RunReaper(ctx)
				row, err := store.InstanceByID(ctx, ins.ID)
				if err != nil {
					t.Fatal(err)
				}
				return state.State(row.State)
			}
			if got := reap(last.Add(2 * time.Minute)); got != tc.at2m {
				t.Fatalf("state 2 min after the last request = %s, want %s", got, tc.at2m)
			}
			if len(subjects) == 0 || subjects[0].AccountID != f.App().AccountID || subjects[0].Plan != string(api.PlanPro) {
				t.Fatalf("flag subjects = %+v, want the app's account on pro", subjects)
			}
			if tc.at11m == "" {
				return
			}
			if got := reap(last.Add(9 * time.Minute)); got != state.StateRunning {
				t.Fatalf("state 9 min after the last request = %s, want running", got)
			}
			if got := reap(last.Add(11 * time.Minute)); got != tc.at11m {
				t.Fatalf("state 11 min after the last request = %s, want %s", got, tc.at11m)
			}
		})
	}
}

func TestIdleLadderHoldKeepsALongerConfiguredTimeout(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	in := sched.InstanceInfo{
		Instance: "i", AppID: "a", Plan: api.PlanScale, State: state.StateRunning,
		AppType: string(state.AppTypeApp), IdleTimeoutS: 1200, Started: now.Add(-time.Hour),
		IdleLadderLevel: 2, IdleLadderHold: true,
	}
	in.LastRequest = now.Add(-15 * time.Minute)
	if got := sched.ReapIdle(now, []sched.InstanceInfo{in}, nil, nil); len(got) != 0 {
		t.Fatalf("ReapIdle 15 min idle with a 20 min timeout = %v, want none", got)
	}
	in.LastRequest = now.Add(-21 * time.Minute)
	if got := sched.ReapIdle(now, []sched.InstanceInfo{in}, nil, nil); !reflect.DeepEqual(got, []string{"i"}) {
		t.Fatalf("ReapIdle 21 min idle = %v, want [i]", got)
	}
}

func TestEarlyFreezeLeavesLadderHeldInstances(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	base := sched.InstanceInfo{
		Plan: api.PlanPro, State: state.StateRunning, AppType: string(state.AppTypeApp),
		LastRequest: now.Add(-30 * time.Second), Started: now.Add(-time.Hour), IdleLadderLevel: 1,
	}
	held, other := base, base
	held.Instance, held.AppID, held.IdleLadderHold = "held", "a", true
	other.Instance, other.AppID = "other", "b"
	got := sched.ReapEarlyFreeze(now, []sched.InstanceInfo{held, other}, 10*time.Second)
	if !reflect.DeepEqual(got, []string{"other"}) {
		t.Fatalf("ReapEarlyFreeze = %v, want [other]", got)
	}
}

// RAM pressure takes what the ladder holds past its idle timeout before an
// instance inside its idle timeout, even one idle longer.
func TestMemoryEvictionTakesLadderSurplusFirst(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	inside := sched.InstanceInfo{
		Instance: "inside", AppID: "a", Plan: api.PlanPro, State: state.StateRunning, NodeID: "n",
		AppType: string(state.AppTypeApp), RAMMB: 1024, Started: now.Add(-time.Hour),
		LastRequest: now.Add(-250 * time.Second), // Pro's app timeout is 300 s
	}
	surplus := sched.InstanceInfo{
		Instance: "surplus", AppID: "b", Plan: api.PlanHobby, State: state.StateRunning, NodeID: "n",
		AppType: string(state.AppTypeApp), RAMMB: 512, Started: now.Add(-time.Hour),
		LastRequest:     now.Add(-120 * time.Second), // Hobby's is 60 s
		IdleLadderLevel: 2, IdleLadderHold: true,
	}
	used, ceiling := map[string]int{"n": 10_100}, map[string]int{"n": 10_000}
	got := sched.SelectMemoryEvictions(now, []sched.InstanceInfo{inside, surplus}, used, ceiling, map[string]int{"surplus": 600})
	if len(got) == 0 || got[0] != "surplus" {
		t.Fatalf("SelectMemoryEvictions = %v, want surplus first", got)
	}
	surplus.IdleLadderHold = false
	got = sched.SelectMemoryEvictions(now, []sched.InstanceInfo{inside, surplus}, used, ceiling, map[string]int{"surplus": 600})
	if len(got) == 0 || got[0] != "inside" {
		t.Fatalf("SelectMemoryEvictions without the ladder = %v, want inside first (LRU)", got)
	}
}
