package meter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ledgerStore serves the instance-billing ledger from a per-minute table
// and records every AppendUsage the sampler issues. failMinute makes the
// ledger read for that minute fail, like a tick cancelled mid-walk.
type ledgerStore struct {
	*state.MemStore
	mu         sync.Mutex
	resident   map[time.Time]map[string]int64
	failMinute time.Time
	appends    map[time.Time]int64 // minute -> mb_seconds appended
}

func (s *ledgerStore) InstanceBillingSeconds(_ context.Context, start, _ time.Time) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start.Equal(s.failMinute) {
		return nil, context.Canceled
	}
	out := map[string]int64{}
	for id, sec := range s.resident[start] {
		out[id] = sec
	}
	return out, nil
}

func (s *ledgerStore) ListJobInstancesInBillingWindow(context.Context, time.Time, time.Time) ([]state.JobBillingInstance, error) {
	return nil, nil
}

func (s *ledgerStore) AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error {
	s.mu.Lock()
	s.appends[minute] += mbSeconds
	s.mu.Unlock()
	return s.MemStore.AppendUsage(ctx, accountID, appID, instanceID, minute, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes, coldBootCount, tailSeconds)
}

func (s *ledgerStore) appended(minute time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appends[minute]
}

// countingCPU reports a growing CPU counter and counts reads, so a test can
// prove a caught-up minute never drains a live counter.
type countingCPU struct {
	mu    sync.Mutex
	reads int
}

func (c *countingCPU) CPUUsageUsec(string) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return uint64(c.reads) * 1000, true
}

// production-us hunt #6 (H5-55): while meterd crash-looped through the
// rc.246 rollback, each start's tick was cancelled mid-walk and the next
// tick rolled only the newest closed minute, so 02:52-03:00 stayed
// under-billed for good. A tick now catches up every closed minute after
// the newest one it rolled in full.
func TestSampler_CatchesUpMinutesAFailedTickLeftUnrolled(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	store := &ledgerStore{MemStore: mem, resident: map[time.Time]map[string]int64{}, appends: map[time.Time]int64{}}
	acct, err := mem.CreateAccount(ctx, "catchup@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "catchup", RAMMB: 256, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := mem.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 8, 2, 50, 0, 0, time.UTC)
	for m := 0; m < 12; m++ {
		store.resident[base.Add(time.Duration(m)*time.Minute)] = map[string]int64{ins.ID: 60}
	}
	perMinute := int64(api.BillableRAMMB(256)) * 60
	cpu := &countingCPU{}
	now := base.Add(time.Minute) // newest closed minute 02:50
	sampler := NewSampler(store, cpu, func() time.Time { return now })

	if _, err := sampler.SampleAndRoll(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	// 02:52's tick is cancelled while it walks the ledger.
	store.failMinute = base.Add(2 * time.Minute)
	now = base.Add(3 * time.Minute)
	if _, err := sampler.SampleAndRoll(ctx); err == nil {
		t.Fatal("tick with a failing ledger read succeeded")
	}
	store.failMinute = time.Time{}
	// meterd misses the next ticks entirely, then recovers at 02:58.
	now = base.Add(9 * time.Minute)
	readsBefore := cpu.reads
	rows, err := sampler.SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	for m := 0; m <= 8; m++ {
		minute := base.Add(time.Duration(m) * time.Minute)
		if got := store.appended(minute); got != perMinute {
			t.Fatalf("minute %s: appended %d MB-s, want %d", minute.Format("15:04"), got, perMinute)
		}
	}
	newest := base.Add(8 * time.Minute)
	for _, row := range rows {
		if row.CatchUp == row.Minute.Equal(newest) {
			t.Fatalf("row for %s has CatchUp=%v", row.Minute.Format("15:04"), row.CatchUp)
		}
	}
	if got := cpu.reads - readsBefore; got != 1 {
		t.Fatalf("recovery tick read the live CPU counter %d times, want once (newest minute only)", got)
	}
	caught := sampler.CaughtUpMinutes()
	if len(caught) != 0 {
		// No job sampler ran in this tick, so no minute is complete for
		// both samplers yet.
		t.Fatalf("CaughtUpMinutes = %v before the job sampler ran", caught)
	}
	if _, err := sampler.SampleJobsAndRoll(ctx); err != nil {
		t.Fatalf("job sampler: %v", err)
	}
	if got := len(sampler.CaughtUpMinutes()); got != 6 {
		t.Fatalf("CaughtUpMinutes = %d minutes, want 02:52..02:57", got)
	}
}

// A fresh meterd process catches up the whole window once. Minutes the
// previous process already rolled are not billed twice: usage_minutes keeps
// the first positive mb_seconds per (instance, minute).
func TestSampler_RestartCatchUpDoesNotDoubleBill(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	store := &ledgerStore{MemStore: mem, resident: map[time.Time]map[string]int64{}, appends: map[time.Time]int64{}}
	acct, err := mem.CreateAccount(ctx, "restart@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "restart", RAMMB: 256, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := mem.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	newest := time.Date(2026, 10, 8, 3, 30, 0, 0, time.UTC)
	for m := time.Duration(0); m <= 2*api.MeterCatchUpWindow; m += time.Minute {
		store.resident[newest.Add(-m)] = map[string]int64{ins.ID: 60}
	}
	clock := func() time.Time { return newest.Add(time.Minute) }
	if _, err := NewSampler(store, nil, clock).SampleAndRoll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSampler(store, nil, clock).SampleAndRoll(ctx); err != nil {
		t.Fatal(err)
	}
	usage, err := mem.UsageByHour(ctx, acct.ID, newest.Add(-2*api.MeterCatchUpWindow), newest.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var billed int64
	for _, u := range usage {
		billed += u.MBSeconds
	}
	minutes := int64(api.MeterCatchUpWindow / time.Minute)
	if want := minutes * int64(api.BillableRAMMB(256)) * 60; billed != want {
		t.Fatalf("billed %d MB-s after a restart, want %d (%d minutes once each)", billed, want, minutes)
	}
	if got := store.appended(newest.Add(-api.MeterCatchUpWindow)); got != 0 {
		t.Fatalf("rolled a minute %s outside the catch-up window", newest.Add(-api.MeterCatchUpWindow))
	}
}

func TestCatchUpMinutes(t *testing.T) {
	newest := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		through time.Time
		want    int
	}{
		{"steady state", newest.Add(-time.Minute), 0},
		{"same minute again", newest, 0},
		{"three missed", newest.Add(-4 * time.Minute), 3},
		{"fresh process", time.Time{}, int(api.MeterCatchUpWindow/time.Minute) - 1},
		{"long outage is bounded", newest.Add(-24 * time.Hour), int(api.MeterCatchUpWindow/time.Minute) - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := catchUpMinutes(tc.through, newest)
			if len(got) != tc.want {
				t.Fatalf("catchUpMinutes = %d minutes, want %d", len(got), tc.want)
			}
			for i, m := range got {
				if !m.Before(newest) || (i > 0 && !m.Equal(got[i-1].Add(time.Minute))) {
					t.Fatalf("minutes not ascending before newest: %v", got)
				}
			}
		})
	}
}

