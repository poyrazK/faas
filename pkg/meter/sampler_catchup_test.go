package meter

import (
	"context"
	"errors"
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
	calls      map[time.Time]int   // minute -> AppendUsage calls
}

func newLedgerStore() *ledgerStore {
	return &ledgerStore{MemStore: state.NewMemStore(), resident: map[time.Time]map[string]int64{},
		appends: map[time.Time]int64{}, calls: map[time.Time]int{}}
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
	s.calls[minute]++
	s.mu.Unlock()
	return s.MemStore.AppendUsage(ctx, accountID, appID, instanceID, minute, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes, coldBootCount, tailSeconds)
}

func (s *ledgerStore) appended(minute time.Time) (mbSeconds int64, calls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appends[minute], s.calls[minute]
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

// seedRunningInstance creates an app with one running 256 MB instance that
// the ledger reports resident for every minute in [from, to).
func seedRunningInstance(t *testing.T, store *ledgerStore, from, to time.Time) (state.Account, state.Instance) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), "catchup@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "catchup", RAMMB: 256, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(context.Background(), app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	for m := from; m.Before(to); m = m.Add(time.Minute) {
		store.resident[m] = map[string]int64{ins.ID: 60}
	}
	return acct, ins
}

// recordComplete marks [from, to) compute-complete, as a healthy meterd did.
func recordComplete(t *testing.T, store state.FinancialStore, from, to time.Time) {
	t.Helper()
	for m := from; m.Before(to); m = m.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(context.Background(), m, true, false); err != nil {
			t.Fatal(err)
		}
	}
}

// tick runs one meterd sample tick the way Loop.Run does, including the
// financial sampling record, and returns the app rows and the tick error.
func tick(t *testing.T, store *ledgerStore, sampler *Sampler, at time.Time) ([]RolledRow, error) {
	t.Helper()
	ctx := context.Background()
	rows, err := sampler.SampleAndRoll(ctx)
	_, jerr := sampler.SampleJobsAndRoll(ctx)
	sampleErr := errors.Join(err, jerr)
	if recErr := (&Loop{store: store}).recordFinancialSample(ctx, sampler, at, sampleErr); recErr != nil {
		t.Fatalf("record financial sample: %v", recErr)
	}
	return rows, sampleErr
}

func completedMinutes(t *testing.T, store state.FinancialStore, from, to time.Time) map[time.Time]bool {
	t.Helper()
	minutes, err := store.FinancialCompletedComputeMinutes(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	out := map[time.Time]bool{}
	for _, m := range minutes {
		out[m] = true
	}
	return out
}

// production-us hunt #6 (H5-55): while meterd crash-looped through the
// rc.246 rollback, each start's tick was cancelled mid-walk and the next
// tick rolled only the newest closed minute, so 02:52-03:00 stayed
// under-billed for good. A tick now catches up every closed minute meterd
// never recorded compute-complete, and records it once caught up.
// adr: 790
func TestSampler_CatchesUpMinutesNeverRecordedComplete(t *testing.T) {
	store := newLedgerStore()
	base := time.Date(2026, 10, 8, 2, 50, 0, 0, time.UTC)
	seedRunningInstance(t, store, base, base.Add(12*time.Minute))
	recordComplete(t, store, base.Add(-api.MeterCatchUpWindow), base)
	perMinute := int64(api.BillableRAMMB(256)) * 60
	cpu := &countingCPU{}
	now := base.Add(time.Minute) // newest closed minute 02:50
	sampler := NewSampler(store, cpu, func() time.Time { return now })

	if _, err := tick(t, store, sampler, now); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	// 02:52's tick is cancelled while it walks the ledger.
	store.failMinute = base.Add(2 * time.Minute)
	now = base.Add(3 * time.Minute)
	if _, err := tick(t, store, sampler, now); err == nil {
		t.Fatal("tick with a failing ledger read succeeded")
	}
	store.failMinute = time.Time{}
	// meterd misses the next ticks entirely, then recovers at 02:59.
	now = base.Add(9 * time.Minute)
	readsBefore := cpu.reads
	rows, err := tick(t, store, sampler, now)
	if err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	for m := 0; m <= 8; m++ {
		minute := base.Add(time.Duration(m) * time.Minute)
		if got, calls := store.appended(minute); got != perMinute || calls != 1 {
			t.Fatalf("minute %s: appended %d MB-s in %d calls, want %d once", minute.Format("15:04"), got, calls, perMinute)
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
	complete := completedMinutes(t, store, base, base.Add(9*time.Minute))
	for m := 0; m <= 8; m++ {
		if minute := base.Add(time.Duration(m) * time.Minute); !complete[minute] {
			t.Fatalf("minute %s not recorded compute-complete after catch-up", minute.Format("15:04"))
		}
	}
}

// A fresh meterd process re-rolls only the minutes the record lacks, so a
// restart neither re-walks complete history nor bills a minute twice, and an
// outage longer than half an hour is recovered too.
// adr: 790
func TestSampler_RestartRerollsOnlyIncompleteMinutes(t *testing.T) {
	store := newLedgerStore()
	newest := time.Date(2026, 10, 8, 3, 30, 0, 0, time.UTC)
	from := newest.Add(-2 * time.Hour)
	acct, ins := seedRunningInstance(t, store, from, newest.Add(time.Minute))
	gapStart, gapEnd := newest.Add(-90*time.Minute), newest.Add(-35*time.Minute)
	recordComplete(t, store, newest.Add(-api.MeterCatchUpWindow), gapStart)
	recordComplete(t, store, gapEnd, newest)
	perMinute := int64(api.BillableRAMMB(256)) * 60
	// The previous process billed every minute outside the outage.
	for m := from; m.Before(newest); m = m.Add(time.Minute) {
		if !m.Before(gapStart) && m.Before(gapEnd) {
			continue
		}
		if err := store.MemStore.AppendUsage(context.Background(), acct.ID, ins.AppID, ins.ID, m, perMinute, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	// Two restarts in a row, each catching up for several ticks.
	for range 2 {
		now := newest.Add(time.Minute)
		sampler := NewSampler(store, nil, func() time.Time { return now })
		for range 5 {
			if _, err := tick(t, store, sampler, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	for m := from; !m.After(newest); m = m.Add(time.Minute) {
		_, calls := store.appended(m)
		inGap := !m.Before(gapStart) && m.Before(gapEnd)
		switch {
		case inGap && calls != 1:
			t.Fatalf("outage minute %s rolled %d times, want once", m.Format("15:04"), calls)
		case !inGap && !m.Equal(newest) && calls != 0:
			t.Fatalf("complete minute %s re-rolled %d times", m.Format("15:04"), calls)
		}
	}
	usage, err := store.UsageByHour(context.Background(), acct.ID, from.Add(-time.Hour), newest.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var billed int64
	for _, u := range usage {
		billed += u.MBSeconds
	}
	if want := int64(2*60+1) * perMinute; billed != want {
		t.Fatalf("billed %d MB-s, want %d (every resident minute once)", billed, want)
	}
}

// Without any record (a new environment) a tick catches up at most
// api.MeterCatchUpMinutesPerTick minutes, oldest first, never outside the
// window, and never re-rolls a minute this process already rolled.
// adr: 790
func TestSampler_CatchUpIsBoundedPerTick(t *testing.T) {
	store := newLedgerStore()
	newest := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	start := newest.Add(-api.MeterCatchUpWindow)
	seedRunningInstance(t, store, start.Add(-time.Hour), newest.Add(2*time.Minute))
	now := newest.Add(time.Minute)
	sampler := NewSampler(store, nil, func() time.Time { return now })
	if _, err := sampler.SampleAndRoll(context.Background()); err != nil {
		t.Fatal(err)
	}
	caught := append([]time.Time(nil), sampler.caughtUp...)
	if len(caught) != api.MeterCatchUpMinutesPerTick || !caught[0].Equal(start) {
		t.Fatalf("first tick caught up %d minutes from %v, want %d from %v", len(caught), caught, api.MeterCatchUpMinutesPerTick, start)
	}
	if _, calls := store.appended(start.Add(-time.Minute)); calls != 0 {
		t.Fatal("caught up a minute outside the window")
	}
	// No record is ever written here; the next tick still moves on.
	now = now.Add(time.Minute)
	if _, err := sampler.SampleAndRoll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sampler.caughtUp[0].Equal(start.Add(time.Duration(api.MeterCatchUpMinutesPerTick) * time.Minute)) {
		t.Fatalf("second tick started at %v, want the next unrolled minute", sampler.caughtUp[0])
	}
	for _, m := range []time.Time{start, newest} {
		if _, calls := store.appended(m); calls != 1 {
			t.Fatalf("minute %s rolled %d times by one process, want once", m.Format("15:04"), calls)
		}
	}
}
