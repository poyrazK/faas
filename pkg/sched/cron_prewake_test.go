// adr: 20261107122928
package sched_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Amendment 3: the fire a cron pre-wake looks at is the earliest HTTP cron
// fire within the lead; an app with a cron due now, or one that fired within
// the lead, is marked busy, and runCronPrewake leaves it to that fire.
func TestCronPrewakeFires(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 59, 0, 0, time.UTC)
	lead := time.Minute
	cron := func(id, app, schedule string, cursor, lastFired time.Time, command []string) state.Cron {
		return state.Cron{ID: id, AppID: app, Schedule: schedule, Enabled: true, CreatedAt: cursor, LastFiredAt: lastFired, Command: command}
	}
	hourAgo := now.Add(-time.Hour + time.Minute) // 12:00, the last hourly fire
	for _, tc := range []struct {
		name     string
		crons    []state.Cron
		wantFire map[string]string // app -> cron id
		wantBusy []string
	}{
		{
			name:     "hourly cron fires within the lead",
			crons:    []state.Cron{cron("c1", "a", "0 * * * *", hourAgo, hourAgo, nil)},
			wantFire: map[string]string{"a": "c1"},
		},
		{
			name:  "fire beyond the lead",
			crons: []state.Cron{cron("c1", "a", "30 * * * *", now.Add(-29*time.Minute), now.Add(-29*time.Minute), nil)},
		},
		{
			name:  "command cron runs in its own VM",
			crons: []state.Cron{cron("c1", "a", "0 * * * *", hourAgo, hourAgo, []string{"rake", "tick"})},
		},
		{
			name: "disabled cron",
			crons: []state.Cron{func() state.Cron {
				c := cron("c1", "a", "0 * * * *", hourAgo, hourAgo, nil)
				c.Enabled = false
				return c
			}()},
		},
		{
			name: "a cron of the app due now",
			crons: []state.Cron{
				cron("c1", "a", "0 * * * *", hourAgo, hourAgo, nil),
				cron("c2", "a", "59 * * * *", hourAgo, hourAgo, nil),
			},
			wantFire: map[string]string{"a": "c1"},
			wantBusy: []string{"a"},
		},
		{
			name: "a cron of the app fired within the lead",
			crons: []state.Cron{
				cron("c1", "a", "0 * * * *", hourAgo, hourAgo, nil),
				cron("c2", "a", "* * * * *", now.Add(-time.Second), now.Add(-time.Second), nil),
			},
			wantFire: map[string]string{"a": "c1"},
			wantBusy: []string{"a"},
		},
		{
			name: "earliest of two crons",
			crons: []state.Cron{
				cron("c1", "a", "0 13 * * *", now.Add(-time.Hour), time.Time{}, nil),
				cron("c2", "a", "0 * * * *", hourAgo, hourAgo, nil),
				cron("c3", "b", "0 * * * *", hourAgo, hourAgo, nil),
			},
			wantFire: map[string]string{"a": "c1", "b": "c3"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, ids, busy := sched.XCronPrewakeFires(tc.crons, now, lead)
			if len(ids) != len(tc.wantFire) {
				t.Fatalf("fires = %v, want %v", ids, tc.wantFire)
			}
			for app, id := range tc.wantFire {
				if ids[app] != id {
					t.Errorf("app %s fire from %q, want %q", app, ids[app], id)
				}
				if want := now.Add(time.Minute); !at[app].Equal(want) {
					t.Errorf("app %s fire at %v, want %v", app, at[app], want)
				}
			}
			if len(busy) != len(tc.wantBusy) {
				t.Fatalf("busy = %v, want %v", busy, tc.wantBusy)
			}
			for _, app := range tc.wantBusy {
				if !busy[app] {
					t.Errorf("app %s not busy", app)
				}
			}
		})
	}
}

// prewakeFixture is a Scale app with an hourly HTTP cron that fires one
// minute after the returned clock. Its fire is in the real future: the
// prewarm store refuses a wake_at in the past.
func prewakeFixture(t *testing.T) (*state.MemStore, *sched.Engine, state.App, time.Time, time.Time) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "prewake@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, c := sched.NewAppAndCron(t, store, acct.ID, true)
	fire := time.Now().UTC().Truncate(time.Hour).Add(2 * time.Hour)
	now := fire.Add(-59 * time.Second)
	hourly, anchor := "0 * * * *", now.Add(-10*time.Second)
	if _, err := store.UpdateCron(ctx, c.ID, &hourly, nil, nil, &anchor); err != nil {
		t.Fatal(err)
	}
	eng, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	return store, eng, app, now, fire
}

func TestCronPrewakeFilesOneIntentForAParkedApp(t *testing.T) {
	ctx := context.Background()
	store, eng, app, now, fire := prewakeFixture(t)
	var subjects []platformflags.Subject
	reg := prometheus.NewRegistry()
	loop := sched.NewLoop(nil, eng, slog.Default()).WithClock(func() time.Time { return now }).
		WithCronPrewake(func(s platformflags.Subject) bool { subjects = append(subjects, s); return true }, reg)

	loop.RunCronTick(ctx)
	loop.RunCronTick(ctx) // the minute's second pass files nothing new

	intents, err := store.ListPrewarmIntentsForApp(ctx, app.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 {
		t.Fatalf("intents = %d, want 1: %+v", len(intents), intents)
	}
	got := intents[0]
	if got.Trigger != state.PrewarmTriggerCron || got.Count != 1 || !got.WakeAt.Equal(fire) ||
		!got.ExpiresAt.Equal(fire.Add(api.CronPrewakeHoldSeconds*time.Second)) || got.AccountID != app.AccountID {
		t.Fatalf("intent = %+v, want cron, 1, wake %v, expiry +%ds", got, fire, api.CronPrewakeHoldSeconds)
	}
	if len(subjects) == 0 || subjects[0].AccountID != app.AccountID || subjects[0].Plan != string(api.PlanScale) {
		t.Fatalf("flag subjects = %+v", subjects)
	}
	if n := prewakeOutcome(t, reg, "filed"); n != 1 {
		t.Fatalf("filed = %v, want 1", n)
	}
}

func TestCronPrewakeLeavesApps(t *testing.T) {
	for _, tc := range []struct {
		name     string
		released bool
		instance string // state of an instance the app already has
		off      bool
	}{
		{name: "account not released", released: false},
		{name: "running instance", released: true, instance: "running"},
		{name: "frozen instance", released: true, instance: "frozen"},
		{name: "switch off", released: true, off: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, eng, app, now, _ := prewakeFixture(t)
			if tc.instance != "" {
				if _, err := store.CreateInstance(ctx, app.ID, "dep-1", tc.instance, app.RAMMB, "node-a", "wake-1"); err != nil {
					t.Fatal(err)
				}
			}
			loop := sched.NewLoop(nil, eng, slog.Default()).WithClock(func() time.Time { return now })
			if !tc.off {
				loop.WithCronPrewake(func(platformflags.Subject) bool { return tc.released }, nil)
			}
			loop.RunCronTick(ctx)
			intents, err := store.ListPrewarmIntentsForApp(ctx, app.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(intents) != 0 {
				t.Fatalf("intents = %+v, want none", intents)
			}
		})
	}
}

// prewakeOutcome reads schedd_cron_prewake_total{outcome} from reg.
func prewakeOutcome(t *testing.T, reg *prometheus.Registry, outcome string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "schedd_cron_prewake_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "outcome" && label.GetValue() == outcome {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}
