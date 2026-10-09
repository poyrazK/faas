package sched

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestQueuePushLaneEligible(t *testing.T) {
	binding := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	queue := pgtype.Text{String: "queue", Valid: true}
	cases := []struct {
		name    string
		trigger sqlc.Trigger
		want    bool
	}{
		{"named bound queue push", sqlc.Trigger{Kind: "queue", Source: queue, Slug: "jobs", QueueBindingID: binding}, true},
		{"legacy unbound queue", sqlc.Trigger{Kind: "queue", Source: queue, Slug: "jobs"}, false},
		{"unnamed queue", sqlc.Trigger{Kind: "queue", Source: queue, QueueBindingID: binding}, false},
		{"delayed tasks", sqlc.Trigger{Kind: "queue", Source: pgtype.Text{String: "delayed_task", Valid: true}, Slug: "jobs", QueueBindingID: binding}, false},
		{"external broker", sqlc.Trigger{Kind: "kafka", Slug: "orders", QueueBindingID: binding}, false},
	}
	for _, tc := range cases {
		if got := queuePushLaneEligible(tc.trigger); got != tc.want {
			t.Errorf("%s: eligible=%t want %t", tc.name, got, tc.want)
		}
	}
}

func TestQueuePushLaneCount(t *testing.T) {
	slots := api.QueuePushDispatchSlotsPerNode
	cases := []struct {
		name                                      string
		maxConcurrency, allowed, depth, batchSize int
		want                                      int
	}{
		{"idle binding keeps one poller", 10, 10, 0, 1, 1},
		{"backlog at one record per batch", 10, 10, 6, 1, 6},
		{"batches cover several records", 10, 10, 7, 3, 3},
		{"binding concurrency caps lanes", 2, 10, 50, 1, 2},
		{"allowance caps lanes", 10, 3, 50, 1, 3},
		{"node slots cap lanes", api.QueueBindingMaxConcurrency, api.QueueBindingMaxConcurrency, 100000, 1, slots},
		{"unset batch size means one", 10, 10, 4, 0, 4},
	}
	for _, tc := range cases {
		if got := queuePushLaneCount(tc.maxConcurrency, tc.allowed, tc.depth, tc.batchSize); got != tc.want {
			t.Errorf("%s: lanes=%d want %d", tc.name, got, tc.want)
		}
	}
}

func TestQueuePushLaneAllowanceRampsAndBacksOff(t *testing.T) {
	var a queuePushLaneAllowance
	if got := a.current("t"); got != 1 {
		t.Fatalf("initial allowance=%d want 1", got)
	}
	for range 5 {
		a.observe("t", 4, false)
	}
	if got := a.current("t"); got != 4 {
		t.Fatalf("allowance after successes=%d want ceiling 4", got)
	}
	a.observe("t", 4, true)
	if got := a.current("t"); got != 2 {
		t.Fatalf("allowance after one failure=%d want 2", got)
	}
	a.observe("t", 4, true)
	a.observe("t", 4, true)
	if got := a.current("t"); got != 1 {
		t.Fatalf("allowance never drops below one, got %d", got)
	}
	if got := a.current("other"); got != 1 {
		t.Fatalf("allowance leaked across triggers: %d", got)
	}
}

// countingPoller records concurrent lane polls and returns no records, so a
// lane is observable without a gateway.
type countingPoller struct{ polls atomic.Int32 }

func (c *countingPoller) Kind() string { return "queue" }
func (c *countingPoller) Poll(context.Context, sqlc.Trigger) PollResult {
	c.polls.Add(1)
	return PollResult{}
}
func (c *countingPoller) Ack(context.Context, sqlc.Trigger, []string) error          { return nil }
func (c *countingPoller) Nack(context.Context, sqlc.Trigger, []string, string) error { return nil }
func (c *countingPoller) Close() error                                               { return nil }

func TestTriggerTickRunsQueuePushLanesForBacklog(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "push-lanes@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "push-lanes", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
		Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: acct.ID, AppID: app.ID, QueueName: "jobs",
			QueueBindingID: binding.ID, Source: state.InvocationQueue, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	trigger := sqlc.Trigger{
		ID: pgtypeUUIDFromString(t, "00000000-0000-0000-0000-0000000000a1"), AccountID: pgtypeUUIDFromString(t, acct.ID),
		AppID: pgtypeUUIDFromString(t, app.ID), Kind: string(api.TriggerKindQueue), Slug: "jobs", BatchSizeMax: 1,
		Source: pgtype.Text{String: string(state.InvocationQueue), Valid: true}, QueueBindingID: pgtypeUUIDFromString(t, binding.ID),
	}
	poller := &countingPoller{}
	l := &Loop{
		log:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		engine:            newEngine(t, undashedIDStore{store}, &fakeVMM{}, &fakeNotifier{}, "1.10.0"),
		gatewayHTTPClient: &http.Client{},
		triggerPollers:    map[string]triggerSource{trigger.ID.String(): poller},
	}

	// A fresh trigger starts at one lane however large the backlog is.
	l.scheduleQueuePushLanes(ctx, trigger, &fakeDeadLetterStore{})
	l.workPool().drain()
	if got := poller.polls.Load(); got != 1 {
		t.Fatalf("first tick ran %d lanes, want 1", got)
	}

	// Once the allowance has ramped, the binding's max_concurrency bounds a
	// ten-record backlog to four lanes.
	for range 10 {
		l.queuePushLanes.observe(trigger.ID.String(), 4, false)
	}
	poller.polls.Store(0)
	l.scheduleQueuePushLanes(ctx, trigger, &fakeDeadLetterStore{})
	l.workPool().drain()
	if got := poller.polls.Load(); got != 4 {
		t.Fatalf("ramped tick ran %d lanes, want binding max_concurrency 4", got)
	}
}

// undashedIDStore bridges the trigger row's UUID text form to MemStore, whose
// ids are undashed hex. PgStore ids are real UUIDs and need no bridging.
type undashedIDStore struct{ *state.MemStore }

func (s undashedIDStore) QueueBindingByID(ctx context.Context, accountID, appID, id string) (state.QueueBinding, error) {
	return s.MemStore.QueueBindingByID(ctx, undash(accountID), undash(appID), undash(id))
}

func (s undashedIDStore) QueueStateForBinding(ctx context.Context, appID, bindingID string) (state.QueueStats, error) {
	return s.MemStore.QueueStateForBinding(ctx, undash(appID), undash(bindingID))
}

func undash(id string) string { return strings.ReplaceAll(id, "-", "") }
