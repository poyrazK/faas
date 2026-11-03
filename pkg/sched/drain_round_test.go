package sched_test

// adr: 20261101091437

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedCappedBacklog seeds an app of its own Hobby account, holds the account
// at its async cap and queues n async invocations due a millisecond apart
// from base. The drain lists them every round and can claim none, so they
// stay pending with their due_at unchanged.
func seedCappedBacklog(t *testing.T, store state.Store, n int, base time.Time) state.App {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "capped-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "capped-backlog", RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	enqueue := func(due time.Time) state.Invocation {
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/x", DueAt: due,
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
		return inv
	}
	limit := api.MustLimitsFor(api.PlanHobby).MaxAsyncInvocationsPerAccount
	for range limit {
		held := enqueue(base.Add(-time.Hour))
		if _, err := store.ClaimInvocationWithCap(ctx, held.ID, "", 3600, limit); err != nil {
			t.Fatalf("ClaimInvocationWithCap: %v", err)
		}
	}
	for i := range n {
		enqueue(base.Add(time.Duration(i) * time.Millisecond))
	}
	return app
}

// One app's backlog the node cannot dispatch, here an account at its async
// cap, sat at the head of every walk. With more of it than one walk lists,
// every tick restarted at the head and spent its walk on the same rows, so
// another tenant's due row behind them was never listed. The walk now
// resumes where the previous tick stopped.
func TestDrain_CappedBacklogBeyondWalkDoesNotStarveOtherApps(t *testing.T) {
	t.Parallel()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanHobby, true)
	ctx := context.Background()
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	other := apps[0]
	walk := sched.MaxDuePagesPerTick * 64
	backlog := walk + walk/2
	base := time.Now().Add(-time.Hour)
	seedCappedBacklog(t, store, backlog, base)
	mine, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: other.ID, AccountID: other.AccountID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The head part lists the first DueWalkHeadPages pages every tick; the
	// rest of each walk covers the backlog behind it, so the row behind the
	// backlog is listed within one round.
	resumed := (sched.MaxDuePagesPerTick - sched.DueWalkHeadPages) * 64
	bound := (backlog+1-sched.DueWalkHeadPages*64+resumed-1)/resumed + 1
	for tick := 1; tick <= bound; tick++ {
		d.Tick(ctx)
		d.XWaitDispatches()
		got, err := store.InvocationByID(ctx, mine.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == state.InvocationCompleted {
			if calls := ds.Calls().Load(); calls != 1 {
				t.Fatalf("synth calls = %d, want 1: the capped backlog must stay pending", calls)
			}
			return
		}
	}
	t.Fatalf("the other app's row was not dispatched within %d ticks behind a capped backlog of %d rows", bound, backlog)
}

// laneOrderSynth records the order the drain delivers invocations in and
// holds the ones listed in hold for that long.
type laneOrderSynth struct {
	mu    sync.Mutex
	order []string
	hold  map[string]time.Duration
}

func (s *laneOrderSynth) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}

func (s *laneOrderSynth) Invoke(_ context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	s.mu.Lock()
	s.order = append(s.order, inv.ID)
	hold := s.hold[inv.ID]
	s.mu.Unlock()
	time.Sleep(hold)
	inv.State = state.InvocationDispatching
	inv.InstanceID = "inst-" + inv.ID
	inv.Result = json.RawMessage(`{"ok":true}`)
	return inv, nil
}

func (s *laneOrderSynth) delivered() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// An app's FIFO queue messages spread through a backlog run in order even
// though the walk resumes mid-backlog. m1 is listed while the app's lane
// still runs m0, so it stays pending; the next tick resumes past m1 and
// lists m2 with the lane free. Starting the lane there would deliver m2
// before m1, so the round holds the app's lane until it starts over.
func TestDrain_ResumedWalkKeepsQueueLaneOrder(t *testing.T) {
	t.Parallel()
	h, store, _, _, _ := sched.NewDrainHarness(t, api.PlanHobby, true)
	ctx := context.Background()
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	fifo := apps[0]
	walk := sched.MaxDuePagesPerTick * 64
	backlog := walk + walk/2
	base := time.Now().Add(-time.Hour)
	seedCappedBacklog(t, store, backlog, base)
	message := func(due time.Time) state.Invocation {
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: fifo.ID, AccountID: fifo.AccountID, Source: state.InvocationQueue,
			Method: "POST", Path: "/x", DueAt: due,
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
		return inv
	}
	// m0 heads the backlog; m1 falls in the first tick's resumed part and m2
	// in the second's.
	m0 := message(base.Add(-time.Second))
	m1 := message(base.Add(time.Duration(walk/2)*time.Millisecond + time.Microsecond))
	m2 := message(base.Add(time.Duration(walk+walk/4)*time.Millisecond + time.Microsecond))
	synth := &laneOrderSynth{hold: map[string]time.Duration{m0.ID: time.Second}}
	d := sched.NewDrain(store, *h.Engine(),
		sched.WithDrainBatchSize(64),
		sched.WithDrainGatewaySynth(synth),
		sched.WithDrainNow(func() time.Time { return time.Now().UTC() }),
	)

	want := []string{m0.ID, m1.ID, m2.ID}
	for tick := 0; tick < 8 && len(synth.delivered()) < len(want); tick++ {
		d.Tick(ctx)
		d.XWaitDispatches()
	}
	got := synth.delivered()
	if len(got) != len(want) {
		t.Fatalf("delivered %d messages, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivery order = %v, want %v", got, want)
		}
	}
}
