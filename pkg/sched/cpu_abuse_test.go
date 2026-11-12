package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261112120603 — the switch accepts exactly off, shadow and enforce,
// and defaults to shadow.
func TestParseCPUAbuseMode(t *testing.T) {
	for _, s := range []string{"off", "shadow", "enforce"} {
		if m, err := sched.ParseCPUAbuseMode(s); err != nil || string(m) != s {
			t.Fatalf("ParseCPUAbuseMode(%q) = %q, %v", s, m, err)
		}
	}
	for _, s := range []string{"", "on", "Enforce"} {
		if _, err := sched.ParseCPUAbuseMode(s); err == nil {
			t.Fatalf("ParseCPUAbuseMode(%q) accepted", s)
		}
	}
	if sched.DefaultCPUAbuseMode != sched.CPUAbuseShadow {
		t.Fatalf("default mode = %q, want shadow", sched.DefaultCPUAbuseMode)
	}
}

func cpuAbuseEvents(t *testing.T, store *state.MemStore, appID string) []state.Event {
	t.Helper()
	rows, err := store.ListEvents(t.Context(), appID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var out []state.Event
	for _, ev := range rows {
		if ev.Kind == "instance.cpu_abuse" {
			out = append(out, ev)
		}
	}
	return out
}

// adr: 20261112120603 — shadow mode audits a pinned Free instance and leaves
// it running; a paid instance is only flagged, even in enforce mode.
func TestHandleSustainedCPUShadowAndPaidDoNotRecycle(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan api.Plan
		mode sched.CPUAbuseMode
		want string
	}{
		{"free shadow", api.PlanFree, sched.CPUAbuseShadow, sched.CPUAbuseActionShadow},
		{"pro enforce", api.PlanPro, sched.CPUAbuseEnforce, sched.CPUAbuseActionFlagged},
		{"hobby enforce", api.PlanHobby, sched.CPUAbuseEnforce, sched.CPUAbuseActionFlagged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			acct, app, _ := sched.SeedApp(t, store, tc.plan, 256, 1)
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			ctx := context.Background()
			woke, err := e.Wake(ctx, app.ID, "", "", "")
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			for range 3 {
				action, err := e.HandleSustainedCPU(ctx, woke.InstanceID, app.ID, tc.mode, 980, 1000, 16*time.Minute)
				if err != nil || action != tc.want {
					t.Fatalf("HandleSustainedCPU = %q, %v; want %q", action, err, tc.want)
				}
			}
			if ins, _ := store.InstanceByID(ctx, woke.InstanceID); ins.State != string(state.StateRunning) {
				t.Fatalf("instance state = %s, want still running", ins.State)
			}
			if got, _ := store.AccountByID(ctx, acct.ID); got.AbuseHeld() {
				t.Fatal("a non-recycling signal held the account")
			}
			if n := len(cpuAbuseEvents(t, store, app.ID)); n != 3 {
				t.Fatalf("instance.cpu_abuse events = %d, want 3", n)
			}
		})
	}
}

// adr: 20261112120603 — enforce mode recycles a pinned Free instance, and
// the second recycle in the window holds the account with reason
// cpu_sustained.
func TestHandleSustainedCPUEnforceRecyclesFreeAndHolds(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := sched.SeedApp(t, store, api.PlanFree, 256, 1)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	now := time.Unix(1_700_000_000, 0)
	(*e.Now()) = func() time.Time { return now }
	ctx := context.Background()

	first, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	action, err := e.HandleSustainedCPU(ctx, first.InstanceID, app.ID, sched.CPUAbuseEnforce, 250, 250, 15*time.Minute)
	if err != nil || action != sched.CPUAbuseActionRecycled {
		t.Fatalf("first signal = %q, %v; want recycled", action, err)
	}
	if ins, _ := store.InstanceByID(ctx, first.InstanceID); ins.State != string(state.StateStopped) {
		t.Fatalf("first instance state = %s, want stopped", ins.State)
	}
	if got, _ := store.AccountByID(ctx, acct.ID); got.AbuseHeld() {
		t.Fatal("one recycle must not hold the account")
	}
	// A repeat signal for an instance that is gone does nothing.
	if action, err := e.HandleSustainedCPU(ctx, first.InstanceID, app.ID, sched.CPUAbuseEnforce, 250, 250, 15*time.Minute); err != nil || action != "" {
		t.Fatalf("signal for a stopped instance = %q, %v; want no action", action, err)
	}

	now = now.Add(20 * time.Minute)
	second, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake: %v", err)
	}
	if _, err := e.HandleSustainedCPU(ctx, second.InstanceID, app.ID, sched.CPUAbuseEnforce, 250, 250, 15*time.Minute); err != nil {
		t.Fatalf("second signal: %v", err)
	}
	held, _ := store.AccountByID(ctx, acct.ID)
	if !held.AbuseHeld() || held.AbuseHoldReason != state.AccountAbuseHoldCPUSustained {
		t.Fatalf("account after repeat = held %v reason %q, want cpu_sustained hold", held.AbuseHeld(), held.AbuseHoldReason)
	}
	if n := len(cpuAbuseEvents(t, store, app.ID)); n != 2 {
		t.Fatalf("instance.cpu_abuse events = %d, want 2", n)
	}
}

// adr: 20261112120603 — off mode does nothing at all.
func TestHandleSustainedCPUOff(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanFree, 256, 1)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	ctx := context.Background()
	woke, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if action, err := e.HandleSustainedCPU(ctx, woke.InstanceID, app.ID, sched.CPUAbuseOff, 250, 250, time.Hour); err != nil || action != "" {
		t.Fatalf("off mode = %q, %v; want nothing", action, err)
	}
	if n := len(cpuAbuseEvents(t, store, app.ID)); n != 0 {
		t.Fatalf("off mode audited %d events", n)
	}
}
