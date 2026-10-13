package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type queueLeaseFixture struct {
	store   *MemStore
	account Account
	app     App
	binding QueueBinding
}

func newQueueLeaseFixture(t *testing.T, plan api.Plan, mode string, maxConcurrency int) queueLeaseFixture {
	t.Helper()
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "leases@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "lease-worker", Type: AppTypeApp, WorkloadClass: WorkloadClassWorker, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBinding(ctx, QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
		Mode: mode, WorkloadClass: WorkloadClassWorker, Enabled: true, MaxConcurrency: maxConcurrency})
	if err != nil {
		t.Fatal(err)
	}
	return queueLeaseFixture{store: store, account: account, app: app, binding: binding}
}

func (f queueLeaseFixture) enqueue(t *testing.T, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		inv, err := f.store.EnqueueInvocation(context.Background(), Invocation{AccountID: f.account.ID, AppID: f.app.ID,
			Source: InvocationQueue, QueueName: "jobs", QueueBindingID: f.binding.ID, DueAt: time.Now().Add(-time.Duration(n-i) * time.Millisecond),
			Payload: json.RawMessage(`{"n":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inv.ID)
	}
	return ids
}

func (f queueLeaseFixture) lease(t *testing.T, max, visibility int, instance string) []QueueLease {
	t.Helper()
	leases, err := f.store.LeaseQueueMessages(context.Background(), QueueLeaseRequest{AccountID: f.account.ID, AppID: f.app.ID,
		QueueName: "jobs", MaxMessages: max, VisibilitySeconds: visibility, InstanceID: instance})
	if err != nil {
		t.Fatal(err)
	}
	return leases
}

func (f queueLeaseFixture) state(t *testing.T, id string) Invocation {
	t.Helper()
	inv, err := f.store.InvocationByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestQueueLeaseAckCompletesInFIFOOrder(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanPro, "pull", 10)
	ids := f.enqueue(t, 3)
	leases := f.lease(t, 2, 30, "")
	if len(leases) != 2 || leases[0].Invocation.ID != ids[0] || leases[1].Invocation.ID != ids[1] {
		t.Fatalf("leased %+v, want the two oldest of %v", leases, ids)
	}
	if !strings.HasPrefix(leases[0].Receipt, ids[0]+".") || leases[0].Invocation.Attempts != 1 {
		t.Fatalf("receipt %q attempt %d", leases[0].Receipt, leases[0].Invocation.Attempts)
	}
	out, err := f.store.AckQueueLeases(context.Background(), f.account.ID, f.app.ID, "jobs", []string{leases[0].Receipt, "not-a-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Succeeded) != 1 || len(out.Failed) != 1 || out.Failed[0].Reason != QueueLeaseReasonInvalid {
		t.Fatalf("ack outcome %+v", out)
	}
	if got := f.state(t, ids[0]); got.State != InvocationCompleted {
		t.Fatalf("acked message is %s", got.State)
	}
	// A second ack of the same receipt no longer owns the message.
	out, err = f.store.AckQueueLeases(context.Background(), f.account.ID, f.app.ID, "jobs", []string{leases[0].Receipt})
	if err != nil || len(out.Failed) != 1 || out.Failed[0].Reason != QueueLeaseReasonExpired {
		t.Fatalf("re-ack outcome %+v err=%v", out, err)
	}
}

func TestQueueLeaseHonorsMaxConcurrency(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanPro, "pull", 2)
	f.enqueue(t, 5)
	if got := len(f.lease(t, 10, 30, "")); got != 2 {
		t.Fatalf("first lease took %d, want binding max_concurrency 2", got)
	}
	if got := len(f.lease(t, 10, 30, "")); got != 0 {
		t.Fatalf("second lease took %d while the cap is held", got)
	}
}

func TestQueueLeaseStopsAtResponseBudget(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanScale, "pull", 100)
	big := json.RawMessage(`"` + strings.Repeat("a", api.QueueLeaseMaxResponseBytes/3) + `"`)
	huge := json.RawMessage(`"` + strings.Repeat("b", api.QueueLeaseMaxResponseBytes+1) + `"`)
	for i, payload := range []json.RawMessage{big, big, big, huge} {
		if _, err := f.store.EnqueueInvocation(context.Background(), Invocation{AccountID: f.account.ID, AppID: f.app.ID,
			Source: InvocationQueue, QueueName: "jobs", QueueBindingID: f.binding.ID, DueAt: time.Now().Add(time.Duration(i-10) * time.Millisecond),
			Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(f.lease(t, 100, 30, "")); got != 2 {
		t.Fatalf("first lease took %d messages, want the 2 that fit %d bytes", got, api.QueueLeaseMaxResponseBytes)
	}
	if got := len(f.lease(t, 100, 30, "")); got != 1 {
		t.Fatalf("second lease took %d messages, want the third alone", got)
	}
	// A message larger than the budget still leases on its own.
	if got := len(f.lease(t, 100, 30, "")); got != 1 {
		t.Fatalf("oversized message: leased %d, want 1", got)
	}
}

func TestQueueLeaseNackRetriesThenDeadLetters(t *testing.T) {
	// Hobby allows three queue attempts.
	f := newQueueLeaseFixture(t, api.PlanHobby, "pull", 5)
	id := f.enqueue(t, 1)[0]
	for attempt := 1; attempt <= 3; attempt++ {
		leases := f.lease(t, 1, 30, "")
		if len(leases) != 1 {
			t.Fatalf("attempt %d: no lease (state %s)", attempt, f.state(t, id).State)
		}
		out, err := f.store.NackQueueLeases(context.Background(), f.account.ID, f.app.ID, "jobs", []string{leases[0].Receipt}, 0, "boom")
		if err != nil || len(out.Succeeded) != 1 {
			t.Fatalf("attempt %d nack %+v err=%v", attempt, out, err)
		}
		got := f.state(t, id)
		if attempt < 3 {
			if got.State != InvocationPending || got.LastError != "boom" {
				t.Fatalf("attempt %d: state %s error %q", attempt, got.State, got.LastError)
			}
			// Make the retry due now so the next lease sees it.
			got.DueAt = time.Now().Add(-time.Millisecond)
			f.store.mu.Lock()
			f.store.invocations[id] = got
			f.store.mu.Unlock()
		} else if got.State != InvocationDeadLetter {
			t.Fatalf("budget spent: state %s, want dead_letter", got.State)
		}
	}
}

func TestQueueLeaseNackDelayPostponesRetry(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanPro, "pull", 5)
	id := f.enqueue(t, 1)[0]
	leases := f.lease(t, 1, 30, "")
	if _, err := f.store.NackQueueLeases(context.Background(), f.account.ID, f.app.ID, "jobs", []string{leases[0].Receipt}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, id); got.State != InvocationPending || got.DueAt.Before(time.Now().Add(59*time.Minute)) {
		t.Fatalf("delayed nack: state %s due %s", got.State, got.DueAt)
	}
	if got := len(f.lease(t, 1, 30, "")); got != 0 {
		t.Fatalf("delayed message was leased early (%d)", got)
	}
}

func TestQueueLeaseExpiryRedeliversAndFencesStaleReceipt(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanPro, "pull", 5)
	id := f.enqueue(t, 1)[0]
	first := f.lease(t, 1, 30, "instance-a")[0]
	// The lease lapses; the reaper returns the message to pending.
	inv := f.state(t, id)
	past := time.Now().Add(-time.Second)
	inv.LeaseExpiresAt = &past
	f.store.mu.Lock()
	f.store.invocations[id] = inv
	f.store.mu.Unlock()
	if _, err := f.store.RequeueExpiredInvocations(context.Background(), time.Now(), 10); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, id); got.State != InvocationPending {
		t.Fatalf("expired lease left the message %s", got.State)
	}
	second := f.lease(t, 1, 30, "instance-b")[0]
	if second.Invocation.Attempts != 2 {
		t.Fatalf("redelivery attempt %d, want 2", second.Invocation.Attempts)
	}
	out, err := f.store.AckQueueLeases(context.Background(), f.account.ID, f.app.ID, "jobs", []string{first.Receipt, second.Receipt})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Succeeded) != 1 || out.Succeeded[0] != second.Receipt || len(out.Failed) != 1 || out.Failed[0].Reason != QueueLeaseReasonExpired {
		t.Fatalf("stale receipt was not fenced: %+v", out)
	}
}

func TestQueueLeaseExtendAndInstanceTracking(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanPro, "pull", 5)
	f.enqueue(t, 2)
	leases := f.lease(t, 2, 5, "instance-a")
	before := *leases[0].Invocation.LeaseExpiresAt
	out, err := f.store.ExtendQueueLeases(context.Background(), f.account.ID, f.app.ID, "jobs", []string{leases[0].Receipt}, 600)
	if err != nil || len(out.Succeeded) != 1 {
		t.Fatalf("extend %+v err=%v", out, err)
	}
	if after := *f.state(t, leases[0].Invocation.ID).LeaseExpiresAt; !after.After(before.Add(500 * time.Second)) {
		t.Fatalf("lease moved from %s to %s", before, after)
	}
	held, err := f.store.QueueLeasesByInstance(context.Background(), f.app.ID)
	if err != nil || held["instance-a"] != 2 {
		t.Fatalf("leases by instance %v err=%v", held, err)
	}
}

func TestQueueLeaseRequiresEnabledPullBinding(t *testing.T) {
	f := newQueueLeaseFixture(t, api.PlanPro, "push", 5)
	f.enqueue(t, 1)
	_, err := f.store.LeaseQueueMessages(context.Background(), QueueLeaseRequest{AccountID: f.account.ID, AppID: f.app.ID, QueueName: "jobs"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("push binding leased: err=%v", err)
	}
	_, err = f.store.LeaseQueueMessages(context.Background(), QueueLeaseRequest{AccountID: f.account.ID, AppID: f.app.ID, QueueName: "jobs", MaxMessages: api.QueueLeaseMaxMessages + 1})
	if !errors.Is(err, ErrInvalidArgument) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("oversized lease: err=%v", err)
	}
}
