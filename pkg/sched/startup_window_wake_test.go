package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261114120000 (deploy-path review P1): an app whose startup_deadline_s
// exceeds the 30 s spec window gets that window on every cold boot, not only
// its deploy prime, or it deploys and then never cold boots again (H12-10).
// The watchdog spares a wake's cold boot, and a restore's fallback cold boot,
// for the deadline plus boot time, and still kills one past it. Apps on the
// default deadline keep the spec budget.
func TestWatchdogGivesEveryColdBootTheStartupWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     api.Plan
		deadline int
		st       state.State
		age      time.Duration
		survive  bool
	}{
		{"default deadline wake at 31s", api.PlanScale, 0, state.StateColdBooting, 31 * time.Second, false},
		{"scale 120s wake at 100s", api.PlanScale, 120, state.StateColdBooting, 100 * time.Second, true},
		{"scale 120s wake at 140s", api.PlanScale, 120, state.StateColdBooting, 140 * time.Second, false},
		{"pro 60s wake at 70s", api.PlanPro, 60, state.StateColdBooting, 70 * time.Second, true},
		{"pro 60s wake at 80s", api.PlanPro, 60, state.StateColdBooting, 80 * time.Second, false},
		{"scale 120s restore fallback at 100s", api.PlanScale, 120, state.StateWaking, 100 * time.Second, true},
		{"default deadline restore fallback at 40s", api.PlanScale, 0, state.StateWaking, 40 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "window@example.com", tc.plan)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "slow-start", RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
				Manifest: state.AppManifest{StartupDeadlineS: tc.deadline}})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(tc.st), 256, state.DefaultLocalNodeName, "")
			if err != nil {
				t.Fatal(err)
			}
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			now := time.Now()
			sched.NewWatchdog(store, engine, nil).WithClock(func() time.Time { return now.Add(tc.age) }).SweepRuns(ctx)
			survived := sched.RowState(t, store, ins.ID) == string(tc.st)
			if survived != tc.survive {
				t.Fatalf("instance survived=%v at %s, want %v", survived, tc.age, tc.survive)
			}
		})
	}
}
