// adr: 20261101093015 — a node's broker trigger lanes are shared between
// accounts and taken in turn, and a broker trigger polls no more records than
// its app's plan rate allows.
package sched_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// laneAccount creates a Scale account with one app and n broker triggers.
func laneAccount(t *testing.T, store *state.MemStore, slug string, n int) []sqlc.Trigger {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, slug+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(api.PlanScale)
	triggers := make([]sqlc.Trigger, 0, n)
	for i := range n {
		trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "lane-broker", fmt.Sprintf("%s-events-%d", slug, i),
			true, []byte(`{}`), "lane-broker", 10, 10, 3, 1024, "dead_letter", limits)
		if err != nil {
			t.Fatal(err)
		}
		triggers = append(triggers, trigger)
	}
	return triggers
}

// laneSources is the lane-broker poller factory: one laneSource per trigger.
type laneSources struct {
	mu   sync.Mutex
	byID map[string]*laneSource
}

func installLaneSources(t *testing.T) *laneSources {
	t.Helper()
	sources := &laneSources{byID: map[string]*laneSource{}}
	sched.InstallPollerFactory(t, "lane-broker", func(trigger sqlc.Trigger) (sched.TriggerSource, error) {
		sources.mu.Lock()
		defer sources.mu.Unlock()
		return sources.byID[trigger.ID.String()], nil
	})
	return sources
}

func (s *laneSources) add(trigger sqlc.Trigger, source *laneSource) *laneSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source.started == nil {
		source.started = make(chan struct{})
	}
	s.byID[trigger.ID.String()] = source
	return source
}

func (s *laneSources) get(trigger sqlc.Trigger) *laneSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID[trigger.ID.String()]
}

// polled counts the triggers polled at least once.
func (s *laneSources) polled(triggers []sqlc.Trigger) int {
	n := 0
	for _, trigger := range triggers {
		if s.get(trigger).polls.Load() > 0 {
			n++
		}
	}
	return n
}

// laneHold holds every poll handed its channel until released.
type laneHold struct {
	ch   chan struct{}
	once sync.Once
}

func newLaneHold() *laneHold              { return &laneHold{ch: make(chan struct{})} }
func (h *laneHold) release()              { h.once.Do(func() { close(h.ch) }) }
func (h *laneHold) wait() <-chan struct{} { return h.ch }

func laneLoop(t *testing.T, store *state.MemStore) *sched.Loop {
	t.Helper()
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	return sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithGatewayHTTPClient(http.DefaultClient, "http://gateway.invalid")
}

func waitForLanes(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// One account's slow triggers filled every lane before another account had
// a trigger here. The next lane to finish goes to the waiting account, and
// the busy account never gets it back while the other one still has a
// trigger to poll: it cannot hold every lane while another account waits.
func TestOneAccountCannotHoldEveryBrokerLaneWhileAnotherWaits(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	slots := sched.XBrokerTriggerLaneSlots
	sources := installLaneSources(t)
	busy := laneAccount(t, store, "busy-lanes", slots+4)
	holds := make(map[string]*laneHold, len(busy))
	for _, trigger := range busy {
		hold := newLaneHold()
		holds[trigger.ID.String()] = hold
		sources.add(trigger, &laneSource{hold: hold.wait()})
	}
	loop := laneLoop(t, store)
	t.Cleanup(func() {
		for _, hold := range holds {
			hold.release()
		}
		loop.WorkPool().Drain()
	})
	busyAccount := busy[0].AccountID.String()

	loop.XRunTriggerTick(ctx)
	waitForLanes(t, "the busy account to fill every lane", func() bool { return sources.polled(busy) == slots })
	if got := loop.XBrokerLanesHeld(busyAccount); got != slots {
		t.Fatalf("busy account holds %d lanes, want all %d", got, slots)
	}

	waiting := laneAccount(t, store, "waiting-lanes", 1)
	waitingSource := sources.add(waiting[0], &laneSource{})
	waitingAccount := waiting[0].AccountID.String()
	loop.XRunTriggerTick(ctx)
	if got := loop.XBrokerLanesHeld(waitingAccount); got != 0 {
		t.Fatalf("waiting account holds %d lanes while every lane is busy", got)
	}

	// One busy lane finishes.
	for _, trigger := range busy {
		if sources.get(trigger).polls.Load() > 0 {
			holds[trigger.ID.String()].release()
			break
		}
	}
	// Each tick until the waiting trigger is polled, and a few after: its
	// lane comes back to it every time, never to the busy account.
	for round := int32(1); round <= 5; round++ {
		waitForLanes(t, fmt.Sprintf("the waiting trigger's poll %d", round), func() bool {
			if waitingSource.polls.Load() >= round {
				return true
			}
			loop.XRunTriggerTick(ctx)
			return false
		})
		if got := sources.polled(busy); got != slots {
			t.Fatalf("round %d: the busy account took a lane the waiting account was owed: %d of its triggers polled, want %d",
				round, got, slots)
		}
		waitForLanes(t, "the waiting trigger's lane to finish", func() bool { return loop.XBrokerLanesHeld(waitingAccount) == 0 })
	}
	if got := loop.XBrokerLanesHeld(busyAccount); got != slots-1 {
		t.Fatalf("busy account holds %d lanes, want %d", got, slots-1)
	}
}

// An account with more triggers than lanes has them take turns: the
// triggers left out of one round are the first served in the next.
func TestBrokerLanesRotateSoEveryTriggerGetsATurn(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	slots := sched.XBrokerTriggerLaneSlots
	sources := installLaneSources(t)
	triggers := laneAccount(t, store, "rotating-lanes", slots+4)
	first := newLaneHold()
	for _, trigger := range triggers {
		sources.add(trigger, &laneSource{hold: first.wait()})
	}
	loop := laneLoop(t, store)
	second := newLaneHold()
	t.Cleanup(func() {
		first.release()
		second.release()
		loop.WorkPool().Drain()
	})

	loop.XRunTriggerTick(ctx)
	waitForLanes(t, "the first round to fill every lane", func() bool { return sources.polled(triggers) == slots })
	var leftOut []sqlc.Trigger
	for _, trigger := range triggers {
		if sources.get(trigger).polls.Load() == 0 {
			leftOut = append(leftOut, trigger)
		}
	}
	if len(leftOut) != len(triggers)-slots {
		t.Fatalf("%d triggers left out of the first round, want %d", len(leftOut), len(triggers)-slots)
	}
	first.release()
	loop.WorkPool().Drain()

	for _, trigger := range triggers {
		sources.get(trigger).hold = second.wait()
	}
	loop.XRunTriggerTick(ctx)
	total := func() int32 {
		var n int32
		for _, trigger := range triggers {
			n += sources.get(trigger).polls.Load()
		}
		return n
	}
	waitForLanes(t, "the second round to fill every lane", func() bool { return total() == int32(2*slots) })
	for _, trigger := range leftOut {
		if got := sources.get(trigger).polls.Load(); got != 1 {
			t.Fatalf("trigger %s, left out of the first round, polled %d times in the second, want 1", trigger.ID, got)
		}
	}
}

// A broker trigger polls no more records than its app's plan rate allows,
// and an app out of budget is not polled at all: its records wait on the
// broker for a later poll rather than being dead-lettered.
func TestBrokerTriggerPollsWithinThePlanRecordRate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app, trigger := laneTrigger(t, store, "rated-broker@example.test", "rated-broker")
	sources := installLaneSources(t)
	source := sources.add(trigger, &laneSource{})
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
	rate := sched.XNewTriggerRecordRateLimiter(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	loop := laneLoop(t, store).WithTriggerRecordRateLimiter(rate)
	t.Cleanup(func() { loop.WorkPool().Drain() })
	tick := func() {
		loop.XRunTriggerTick(ctx)
		loop.WorkPool().Drain()
	}

	perSecond := api.PlanScale.TriggerRecordsPerSecondPerApp()
	if got := rate.Take(app.ID, api.PlanScale, perSecond); got != perSecond {
		t.Fatalf("draining the app's budget took %d, want %d", got, perSecond)
	}
	tick()
	if got := source.polls.Load(); got != 0 {
		t.Fatalf("an app out of record budget was polled %d times", got)
	}

	// Five records of budget: the poll asks the broker for five, not the
	// trigger's batch of ten.
	advance(5 * time.Second / time.Duration(perSecond))
	tick()
	if got, asked := source.polls.Load(), source.asked.Load(); got != 1 || asked != 5 {
		t.Fatalf("polls = %d asking for %d, want 1 poll asking for 5", got, asked)
	}

	// The poll found nothing, so its budget came back, and a second later
	// the bucket is full: the poll asks for the whole batch.
	advance(time.Second)
	tick()
	if got, asked := source.polls.Load(), source.asked.Load(); got != 2 || asked != trigger.BatchSizeMax {
		t.Fatalf("polls = %d asking for %d, want 2 polls, the last asking for %d", got, asked, trigger.BatchSizeMax)
	}
}

func TestTriggerRecordRateLimiterBudget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rate := sched.XNewTriggerRecordRateLimiter(func() time.Time { return now })
	hobby := api.PlanHobby.TriggerRecordsPerSecondPerApp()
	if hobby <= 0 {
		t.Fatalf("Hobby allows %d trigger records a second", hobby)
	}
	tests := []struct {
		name    string
		app     string
		plan    api.Plan
		want    int
		advance time.Duration
		refund  int
		took    int
	}{
		{name: "a full bucket holds one second", app: "a", plan: api.PlanHobby, want: hobby + 1, took: hobby},
		{name: "an empty bucket takes nothing", app: "a", plan: api.PlanHobby, want: 1, took: 0},
		{name: "a refill is proportional", app: "a", plan: api.PlanHobby, want: hobby, advance: time.Second / 2, took: hobby / 2},
		{name: "a refund comes back", app: "a", plan: api.PlanHobby, want: hobby, refund: hobby / 4, took: hobby / 4},
		{name: "apps have buckets of their own", app: "b", plan: api.PlanHobby, want: 3, took: 3},
		{name: "a plan without trigger records takes nothing", app: "c", plan: api.PlanFree, want: 3, took: 0},
		{name: "an unknown plan takes nothing", app: "d", plan: api.Plan("bogus"), want: 3, took: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now = now.Add(tc.advance)
			rate.Refund(tc.app, tc.refund)
			if got := rate.Take(tc.app, tc.plan, tc.want); got != tc.took {
				t.Fatalf("Take(%d) = %d, want %d", tc.want, got, tc.took)
			}
		})
	}
	var unlimited *sched.TriggerRecordRateLimiter
	if got := unlimited.Take("a", api.PlanFree, 7); got != 7 {
		t.Fatalf("a nil limiter took %d of 7", got)
	}
}
