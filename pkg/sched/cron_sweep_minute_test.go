// adr: 20261104091530
package sched_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A resumed pass that replaced the next minute's tick in the coalesced
// cron_sweep slot still starts that minute: the cron the previous minute
// already tried fires again instead of being skipped as tried.
func TestCronSweepResumeStartsANewMinute(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "sweep-minute@example.com", api.PlanHobby)
	sched.NewAppAndCron(t, store, acct.ID, true)
	vmm := &sched.FakeWakeVMM{}
	eng, _ := sched.MakeEngine(t, store, vmm)
	synth := &sched.RecordingSynth{}
	now := time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC)
	loop := sched.NewLoop(nil, eng, slog.Default()).WithGatewaySynth(synth).WithClock(func() time.Time { return now })

	loop.RunCronTick(ctx)
	sched.DeliverCronInvocations(t, store, eng, synth)
	if got := (*synth.Calls()).Load(); got != 1 {
		t.Fatalf("fires after the first minute = %d, want 1", got)
	}
	// The next minute arrives as a resume only (its tick was coalesced away).
	now = now.Add(time.Minute)
	loop.RunCronSweep(ctx)
	sched.DeliverCronInvocations(t, store, eng, synth)
	if got := (*synth.Calls()).Load(); got != 2 {
		t.Fatalf("fires after a resume-only minute = %d, want 2", got)
	}
}
