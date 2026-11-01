// adr: 20261101093812
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// queueReplayHooks reach what the store API does not: a message's send time
// and a full queue.
type queueReplayHooks struct {
	// backdate moves a message's send time; MemStore takes it at enqueue.
	backdate func(t *testing.T, id string, at time.Time)
	// fill queues n pending messages for app.
	fill func(t *testing.T, app state.App, n int)
}

func memQueueReplayHooks(store state.Store) queueReplayHooks {
	return queueReplayHooks{
		backdate: func(*testing.T, string, time.Time) {},
		fill: func(t *testing.T, app state.App, n int) {
			t.Helper()
			for range n {
				if _, err := enqueueBacklogMessage(t, store, app, state.InvocationQueue, 0); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
}

func TestMemQueueReplayRestartsRetention(t *testing.T) {
	store := state.NewMemStore()
	testQueueReplayRestartsRetention(t, store, memQueueReplayHooks(store))
}

func TestMemQueueReplayMeetsQueueDepth(t *testing.T) {
	store := state.NewMemStore()
	testQueueReplayMeetsQueueDepth(t, store, memQueueReplayHooks(store))
}

// expiredQueueMessage sends a Hobby message two days ago and lets the expiry
// sweep move it to the dead letter.
func expiredQueueMessage(t *testing.T, store state.Store, app state.App, hooks queueReplayHooks) state.Invocation {
	t.Helper()
	ctx := t.Context()
	sent := time.Now().UTC().Add(-48 * time.Hour)
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationQueue,
		Payload: json.RawMessage(`{"message":"` + uuid.NewString() + `"}`), DueAt: sent, CreatedAt: sent})
	if err != nil {
		t.Fatal(err)
	}
	hooks.backdate(t, inv.ID, sent)
	if _, err := store.(state.QueueMessageExpiryStore).ExpirePendingQueueMessages(ctx, time.Now(), 100); err != nil {
		t.Fatal(err)
	}
	got := queueReplayState(t, store, inv.ID)
	if got.State != state.InvocationDeadLetter || got.OutcomeCode != state.QueueMessageExpiredCode {
		t.Fatalf("message sent 48 h ago is %s (%q); want dead_letter as %s", got.State, got.OutcomeCode, state.QueueMessageExpiredCode)
	}
	return got
}

func queueReplayState(t *testing.T, store state.Store, id string) state.Invocation {
	t.Helper()
	got, err := store.InvocationByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A message replayed from the dead letter after expiring was expired again
// by the next sweep: retention counted only from its send. It now counts
// from the replay, as the docs' "replay it once a worker can take it" needs.
func testQueueReplayRestartsRetention(t *testing.T, store state.Store, hooks queueReplayHooks) {
	ctx := t.Context()
	account := seedQueueBacklogAccount(t, store, api.PlanHobby)
	app := seedQueueBacklogApp(t, store, account)
	msg := expiredQueueMessage(t, store, app, hooks)
	if _, err := store.RetryQueueDeadLetter(ctx, account.ID, msg.ID); err != nil {
		t.Fatalf("replay: %v", err)
	}
	expiry := store.(state.QueueMessageExpiryStore)
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now(), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationPending {
		t.Fatalf("replayed message is %s (%q) after the next sweep; want pending: retention counts from the replay", got.State, got.OutcomeCode)
	}
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now().Add(25*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationDeadLetter {
		t.Fatalf("replayed message waiting 25 h is %s; want dead_letter", got.State)
	}
}

// Replay moved a dead-lettered queue message back to pending past the app's
// queue depth. It now meets the cap a send meets: a single replay is refused
// with the depth error, and replay-all skips the message and reports the
// refusal when it replays nothing else.
func testQueueReplayMeetsQueueDepth(t *testing.T, store state.Store, hooks queueReplayHooks) {
	ctx := t.Context()
	account := seedQueueBacklogAccount(t, store, api.PlanHobby)
	app := seedQueueBacklogApp(t, store, account)
	msg := expiredQueueMessage(t, store, app, hooks)
	depth := api.MustLimitsFor(api.PlanHobby).MaxQueueDepth
	hooks.fill(t, app, depth)

	_, err := store.RetryQueueDeadLetter(ctx, account.ID, msg.ID)
	var refused *state.InvocationDepthError
	if !errors.As(err, &refused) || refused.Limit != depth || refused.Observed != depth {
		t.Fatalf("replay into a full queue: %v; want the queue depth error at %d", err, depth)
	}
	if !state.IsQueueCapError(err) {
		t.Fatalf("IsQueueCapError(%v) = false", err)
	}
	replayed, err := store.ReplayDeadLetterEvents(ctx, account.ID, app.ID, 20)
	if replayed != 0 || !state.IsQueueCapError(err) {
		t.Fatalf("replay-all into a full queue = %d, %v; want 0 and the queue depth error", replayed, err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationDeadLetter {
		t.Fatalf("refused message is %s; want it left in the dead letter", got.State)
	}
}

func TestMemQueueLeaseLapsedOnLastAttemptDeadLetters(t *testing.T) {
	testQueueLeaseLapsedOnLastAttempt(t, state.NewMemStore())
}

// A pull lease that lapsed on its message's last attempt returned the message
// to pending, where only a later lease call dead-lettered it; with no
// consumer it expired after the plan's retention as queue_message_expired.
// It now dead-letters when the lease lapses, under its own code. A message
// with attempts left still returns to pending.
func testQueueLeaseLapsedOnLastAttempt(t *testing.T, store state.Store) {
	ctx := t.Context()
	account := seedQueueBacklogAccount(t, store, api.PlanHobby)
	slug := "lease-lapse-" + uuid.NewString()[:8]
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, WorkloadName: slug, Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive,
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID,
		AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(policy json.RawMessage) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
			QueueName: "jobs", QueueBindingID: binding.Binding.ID, DueAt: time.Now().Add(-time.Second),
			Payload: json.RawMessage(`{"n":1}`), RetryPolicyJSON: policy})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	last, retried := enqueue(json.RawMessage(`{"max_attempts":1}`)), enqueue(nil)
	leases, err := store.(state.QueueLeaseStore).LeaseQueueMessages(ctx, state.QueueLeaseRequest{AccountID: account.ID, AppID: app.ID,
		QueueName: "jobs", MaxMessages: 2, VisibilitySeconds: 30})
	if err != nil || len(leases) != 2 {
		t.Fatalf("lease: %d leases, %v; want both messages", len(leases), err)
	}
	reclaimer := store.(interface {
		RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
	})
	if _, err := reclaimer.RequeueExpiredInvocations(ctx, time.Now().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, last.ID); got.State != state.InvocationDeadLetter || got.OutcomeCode != state.QueueLeaseAttemptsExhaustedCode {
		t.Fatalf("lease lapsed on the last attempt: message is %s (%q); want dead_letter as %s", got.State, got.OutcomeCode, state.QueueLeaseAttemptsExhaustedCode)
	}
	if got := queueReplayState(t, store, retried.ID); got.State != state.InvocationPending {
		t.Fatalf("lease lapsed with attempts left: message is %s; want pending", got.State)
	}
}
