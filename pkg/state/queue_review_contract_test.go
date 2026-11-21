// adr: 20261112200000 — queue review fixes (2026-11-12).
package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// queueReviewPull is a worker app with one production pull binding.
type queueReviewPull struct {
	account state.Account
	app     state.App
	binding state.QueueBinding
}

func seedQueueReviewPull(t *testing.T, store state.Store, plan api.Plan) queueReviewPull {
	t.Helper()
	ctx := t.Context()
	account := seedQueueBacklogAccount(t, store, plan)
	slug := "queue-review-" + uuid.NewString()[:8]
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, WorkloadName: slug, Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive,
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID,
		AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	return queueReviewPull{account: account, app: app, binding: created.Binding}
}

func (p queueReviewPull) send(t *testing.T, store state.Store, policy json.RawMessage) state.Invocation {
	t.Helper()
	inv, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: p.account.ID, AppID: p.app.ID, Source: state.InvocationQueue,
		QueueName: p.binding.QueueName, QueueBindingID: p.binding.ID, DueAt: time.Now().Add(-time.Second),
		Payload: json.RawMessage(`{"n":1}`), RetryPolicyJSON: policy})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func (p queueReviewPull) lease(t *testing.T, store state.Store, queue string, visibility int) []state.QueueLease {
	t.Helper()
	leases, err := store.(state.QueueLeaseStore).LeaseQueueMessages(t.Context(), state.QueueLeaseRequest{AccountID: p.account.ID,
		AppID: p.app.ID, QueueName: queue, MaxMessages: 10, VisibilitySeconds: visibility})
	if err != nil {
		t.Fatal(err)
	}
	return leases
}

func TestMemQueueRetryBackoffDoesNotExpire(t *testing.T) {
	testQueueRetryBackoffDoesNotExpire(t, state.NewMemStore())
}

// Retention counted from the send, so a nack delayed past the plan's
// retention turned the retry into queue_message_expired before it was due.
// The wait now restarts when the message becomes due again.
func testQueueRetryBackoffDoesNotExpire(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanHobby)
	msg := p.send(t, store, nil)
	leases := p.lease(t, store, p.binding.QueueName, 30)
	if len(leases) != 1 {
		t.Fatalf("leased %d messages; want 1", len(leases))
	}
	if _, err := store.(state.QueueLeaseStore).NackQueueLeases(ctx, p.account.ID, p.app.ID, p.binding.QueueName,
		[]string{leases[0].Receipt}, 23*time.Hour, "later"); err != nil {
		t.Fatal(err)
	}
	expiry := store.(state.QueueMessageExpiryStore)
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now().Add(25*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationPending {
		t.Fatalf("message nacked for 23 h is %s (%q) 25 h after its send; want pending until its retry waited a retention", got.State, got.OutcomeCode)
	}
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now().Add(48*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationDeadLetter || got.OutcomeCode != state.QueueMessageExpiredCode {
		t.Fatalf("retry left waiting a whole retention is %s (%q); want it expired", got.State, got.OutcomeCode)
	}
}

func TestMemQueueSuspendedOwnerMessagesDoNotExpire(t *testing.T) {
	testQueueSuspendedOwnerMessagesDoNotExpire(t, state.NewMemStore())
}

// A suspended account cannot lease, yet its queued messages kept expiring.
// They now wait for the reactivation.
func testQueueSuspendedOwnerMessagesDoNotExpire(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanHobby)
	msg := p.send(t, store, nil)
	if err := store.UpdateAccountStatus(ctx, p.account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	expiry := store.(state.QueueMessageExpiryStore)
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now().Add(25*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationPending {
		t.Fatalf("suspended owner's message is %s (%q); want pending", got.State, got.OutcomeCode)
	}
	if err := store.UpdateAccountStatus(ctx, p.account.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	// adr: 20261113180000 (R5-3) — the hold restarted the wait, so the
	// reactivated owner's message is not expired by the next sweep.
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now().Add(26*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationPending {
		t.Fatalf("reactivated owner's message is %s at the first sweep; want pending with retention left", got.State)
	}
	if _, err := expiry.ExpirePendingQueueMessages(ctx, time.Now().Add(50*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationDeadLetter {
		t.Fatalf("reactivated owner's message left a whole retention is %s; want it expired", got.State)
	}
}

func TestMemQueueReplayClearsDeadLetterHorizon(t *testing.T) {
	testQueueReplayClearsDeadLetterHorizon(t, state.NewMemStore())
}

// adr: 20261113180000 (R6-1, R8-2) — a replay kept the dead letter's
// retention horizon and outcome code: the reaper deleted the replayed
// message as soon as it settled again, and a later success reported the old
// expiry.
func testQueueReplayClearsDeadLetterHorizon(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanHobby)
	msg := p.send(t, store, nil)
	if _, err := store.(state.QueueMessageExpiryStore).ExpirePendingQueueMessages(ctx, time.Now().Add(25*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.InvocationDefaultRetentionStore).StampDefaultInvocationRetention(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if got := queueReplayState(t, store, msg.ID); got.State != state.InvocationDeadLetter || got.ResultRetentionUntil == nil {
		t.Fatalf("expired message = %s, horizon %v; want a stamped dead letter", got.State, got.ResultRetentionUntil)
	}
	if _, err := store.RetryQueueDeadLetter(ctx, p.account.ID, msg.ID); err != nil {
		t.Fatal(err)
	}
	got := queueReplayState(t, store, msg.ID)
	if got.State != state.InvocationPending || got.ResultRetentionUntil != nil || got.OutcomeCode != "" {
		t.Fatalf("replayed message = %s, horizon %v, code %q; want pending with neither", got.State, got.ResultRetentionUntil, got.OutcomeCode)
	}
}

func TestMemQueueHeldPushMessagesKeepRetention(t *testing.T) {
	testQueueHeldPushMessagesKeepRetention(t, state.NewMemStore())
}

// adr: 20261113180000 (R5-5) — a push queue skipped for a spend cap or a
// compute budget hold spent no attempts, but its messages expired once the
// hold outlasted the retention. A refresh restarts the wait of messages that
// have waited the given age, and only theirs.
func testQueueHeldPushMessagesKeepRetention(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanHobby)
	old, fresh := p.send(t, store, nil), p.send(t, store, nil)
	refresher := store.(state.QueueHoldRefresher)
	n, err := refresher.RefreshHeldQueueMessages(ctx, p.app.ID, p.binding.ID, p.binding.QueueName, time.Hour, 100)
	if err != nil || n != 0 {
		t.Fatalf("refreshed %d fresh messages, %v; want none", n, err)
	}
	n, err = refresher.RefreshHeldQueueMessages(ctx, p.app.ID, p.binding.ID, p.binding.QueueName, 0, 100)
	if err != nil || n != 2 {
		t.Fatalf("refreshed %d messages, %v; want both", n, err)
	}
	for _, id := range []string{old.ID, fresh.ID} {
		if got := queueReplayState(t, store, id); got.State != state.InvocationPending || got.DueAt.After(time.Now()) {
			t.Fatalf("refreshed message = %s due %v; want pending and due", got.State, got.DueAt)
		}
	}
}

func TestMemQueueBindingRecreateUnderNewQueueName(t *testing.T) {
	testQueueBindingRecreateUnderNewQueueName(t, state.NewMemStore())
}

// adr: 20261113180000 (R5-2) — a retired binding revived only when the
// create repeated its old queue name too; re-created under a new queue name
// it was refused and its held messages stranded. The name alone revives it
// now, and the held messages drain under the new queue name.
func testQueueBindingRecreateUnderNewQueueName(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanPro)
	held := p.send(t, store, nil)
	consumers := store.(state.QueueBindingConsumerStore)
	if _, err := consumers.DeleteQueueBindingWithConsumer(ctx, p.account.ID, p.app.ID, p.binding.ID); err != nil {
		t.Fatal(err)
	}
	revived, err := consumers.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: p.account.ID, AppID: p.app.ID,
		Name: "jobs", QueueName: "work", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5})
	if err != nil {
		t.Fatalf("re-create under a new queue name: %v", err)
	}
	if revived.Binding.ID != p.binding.ID || revived.Binding.QueueName != "work" || revived.Binding.RetiredAt != nil {
		t.Fatalf("re-created binding = %+v; want %s revived as queue work", revived.Binding, p.binding.ID)
	}
	stats, err := store.QueueStateForQueue(ctx, p.app.ID, "work")
	if err != nil || stats.Depth != 1 {
		t.Fatalf("state for the new queue name = %+v, %v; want the held message counted (R7-3)", stats, err)
	}
	leases := p.lease(t, store, "work", 30)
	if len(leases) != 1 || leases[0].Invocation.ID != held.ID {
		t.Fatalf("revived binding leased %d messages; want the held message %s", len(leases), held.ID)
	}
}

func TestMemQueueBindingRecreateRevivesRetired(t *testing.T) {
	testQueueBindingRecreateRevivesRetired(t, state.NewMemStore())
}

// Deleting a binding retires it and holds its messages, and its name and
// queue name stayed reserved with no way back: the held messages could never
// drain. Re-creating it with the same name and queue name now revives it;
// any other reuse of the retained queue name is still refused.
func testQueueBindingRecreateRevivesRetired(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanPro)
	held := p.send(t, store, nil)
	consumers := store.(state.QueueBindingConsumerStore)
	if _, err := consumers.DeleteQueueBindingWithConsumer(ctx, p.account.ID, p.app.ID, p.binding.ID); err != nil {
		t.Fatal(err)
	}
	other := state.QueueBinding{AccountID: p.account.ID, AppID: p.app.ID, Name: "replacement", QueueName: "jobs", Mode: "pull",
		WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 5}
	if _, err := consumers.CreateQueueBindingWithConsumer(ctx, other); err == nil {
		t.Fatal("another binding took the retired queue name")
	}
	again := other
	again.Name, again.MaxConcurrency = "jobs", 3
	revived, err := consumers.CreateQueueBindingWithConsumer(ctx, again)
	if err != nil {
		t.Fatalf("re-create with the retired name and queue: %v", err)
	}
	if revived.Binding.ID != p.binding.ID || revived.Binding.RetiredAt != nil || !revived.Binding.Enabled || revived.Binding.MaxConcurrency != 3 {
		t.Fatalf("re-created binding = %+v; want the retired binding %s revived with the new settings", revived.Binding, p.binding.ID)
	}
	leases := p.lease(t, store, "jobs", 30)
	if len(leases) != 1 || leases[0].Invocation.ID != held.ID {
		t.Fatalf("revived binding leased %d messages; want the held message %s", len(leases), held.ID)
	}
}

func TestMemQueueLeaseSettlesAfterBindingRename(t *testing.T) {
	testQueueLeaseSettlesAfterBindingRename(t, state.NewMemStore())
}

// A lease selects a binding's messages by its identity, but settling them
// compared the message's queue name with the path's: after the binding's
// queue_name was renamed, its earlier messages leased under the new name and
// every ack, nack and extend under that name answered lease_expired, so each
// was redelivered until it dead-lettered. They now settle under the new name.
func testQueueLeaseSettlesAfterBindingRename(t *testing.T, store state.Store) {
	ctx := t.Context()
	p := seedQueueReviewPull(t, store, api.PlanPro)
	acked, nacked := p.send(t, store, nil), p.send(t, store, nil)
	renamed := "renamed"
	if _, err := store.(state.QueueBindingConsumerStore).UpdateQueueBindingWithConsumer(ctx, p.account.ID, p.app.ID, p.binding.ID,
		state.UpdateQueueBindingParams{QueueName: &renamed}); err != nil {
		t.Fatal(err)
	}
	leases := p.lease(t, store, renamed, 30)
	if len(leases) != 2 {
		t.Fatalf("leased %d messages under the new name; want both earlier messages", len(leases))
	}
	receipts := map[string]string{}
	for _, lease := range leases {
		receipts[lease.Invocation.ID] = lease.Receipt
	}
	leaser := store.(state.QueueLeaseStore)
	extended, err := leaser.ExtendQueueLeases(ctx, p.account.ID, p.app.ID, renamed, []string{receipts[acked.ID]}, 60)
	if err != nil || len(extended.Succeeded) != 1 {
		t.Fatalf("extend under the new name: %+v, %v; want it applied", extended, err)
	}
	out, err := leaser.AckQueueLeases(ctx, p.account.ID, p.app.ID, renamed, []string{receipts[acked.ID]})
	if err != nil || len(out.Succeeded) != 1 {
		t.Fatalf("ack under the new name: %+v, %v; want it applied", out, err)
	}
	if got := queueReplayState(t, store, acked.ID); got.State != state.InvocationCompleted {
		t.Fatalf("acked message is %s; want completed", got.State)
	}
	out, err = leaser.NackQueueLeases(ctx, p.account.ID, p.app.ID, renamed, []string{receipts[nacked.ID]}, 0, "retry")
	if err != nil || len(out.Succeeded) != 1 {
		t.Fatalf("nack under the new name: %+v, %v; want it applied", out, err)
	}
	// Another queue's name still cannot settle the binding's messages.
	other := p.send(t, store, nil)
	otherLeases := p.lease(t, store, renamed, 30)
	for _, lease := range otherLeases {
		if lease.Invocation.ID != other.ID {
			continue
		}
		out, err := leaser.AckQueueLeases(ctx, p.account.ID, p.app.ID, "jobs-unrelated", []string{lease.Receipt})
		if err != nil || len(out.Succeeded) != 0 {
			t.Fatalf("ack under an unrelated queue name: %+v, %v; want it refused", out, err)
		}
	}
}
