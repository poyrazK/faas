// adr: 20261027162741 — an external broker trigger runs on a lane of its
// own, so one tenant's slow trigger no longer holds the others, and only the
// schedd that owns the trigger's app polls it.
package sched_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// laneSource is a broker poller whose Poll can be held; it never returns
// records, so a poll ends the trigger's tick work.
type laneSource struct {
	sched.TriggerSource
	hold    <-chan struct{}
	polls   atomic.Int32
	started chan struct{}
	once    sync.Once
}

func (s *laneSource) Kind() string { return "lane-broker" }

func (s *laneSource) Poll(ctx context.Context, _ sqlc.Trigger) sched.PollResult {
	s.polls.Add(1)
	s.once.Do(func() { close(s.started) })
	if s.hold != nil {
		select {
		case <-s.hold:
		case <-ctx.Done():
		}
	}
	return sched.PollResult{}
}

func (s *laneSource) Close() error { return nil }

func laneTrigger(t *testing.T, store *state.MemStore, email, slug string) (state.App, sqlc.Trigger) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, email, api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(api.PlanScale)
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "lane-broker", slug+"-events", true, []byte(`{}`), "lane-broker", 10, 10, 3, 1024, "dead_letter", limits)
	if err != nil {
		t.Fatal(err)
	}
	return app, trigger
}

func TestSlowBrokerTriggerDoesNotHoldOthers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, slow := laneTrigger(t, store, "slow-broker@example.test", "slow-broker")
	_, quick := laneTrigger(t, store, "quick-broker@example.test", "quick-broker")
	release := make(chan struct{})
	sources := map[string]*laneSource{
		slow.ID.String():  {hold: release, started: make(chan struct{})},
		quick.ID.String(): {started: make(chan struct{})},
	}
	sched.InstallPollerFactory(t, "lane-broker", func(trigger sqlc.Trigger) (sched.TriggerSource, error) {
		return sources[trigger.ID.String()], nil
	})
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil))).WithGatewayHTTPClient(http.DefaultClient, "http://gateway.invalid")
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		loop.WorkPool().Drain()
	})

	tickDone := make(chan struct{})
	go func() {
		loop.XRunTriggerTick(ctx)
		close(tickDone)
	}()
	select {
	case <-sources[slow.ID.String()].started:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow trigger was never polled")
	}
	select {
	case <-sources[quick.ID.String()].started:
	case <-time.After(5 * time.Second):
		t.Fatal("the quick trigger waited behind the slow one")
	}
	select {
	case <-tickDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the trigger tick waited for the slow trigger's batch")
	}
	releaseOnce.Do(func() { close(release) })
}

func TestBrokerTriggerPolledOnlyByItsAppsNode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	mine, ownTrigger := laneTrigger(t, store, "own-broker@example.test", "own-broker")
	theirs, otherTrigger := laneTrigger(t, store, "other-broker@example.test", "other-broker")
	if err := store.SetAppNodeID(ctx, mine.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppNodeID(ctx, theirs.ID, "node-b"); err != nil {
		t.Fatal(err)
	}
	sources := map[string]*laneSource{
		ownTrigger.ID.String():   {started: make(chan struct{})},
		otherTrigger.ID.String(): {started: make(chan struct{})},
	}
	sched.InstallPollerFactory(t, "lane-broker", func(trigger sqlc.Trigger) (sched.TriggerSource, error) {
		return sources[trigger.ID.String()], nil
	})
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOwnerNodeID("node-a")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil))).WithGatewayHTTPClient(http.DefaultClient, "http://gateway.invalid")

	loop.XRunTriggerTick(ctx)
	loop.WorkPool().Drain()
	if got := sources[ownTrigger.ID.String()].polls.Load(); got != 1 {
		t.Fatalf("own app's trigger polls = %d, want 1", got)
	}
	if got := sources[otherTrigger.ID.String()].polls.Load(); got != 0 {
		t.Fatalf("another node's trigger polls = %d, want 0", got)
	}
	if _, kept := (*loop.TriggerPollers())[otherTrigger.ID.String()]; kept {
		t.Fatal("this node built a poller for another node's trigger")
	}
}
