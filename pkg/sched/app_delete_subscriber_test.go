package sched

// Whitebox tests for pkg/sched/app_delete_subscriber.go (ADR-098).
// Modeled line-for-line on pkg/sched/deletion_subscriber_test.go.
// Tests run against an Engine-with-wakeCoord and inject a fake
// pg_notify producer via the db.Notification channel.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeSubscription delivers notifications through an in-memory
// channel. Tests use this to drive the subscriber's Run loop
// without standing up Postgres.
type fakeSubscription struct {
	mu     sync.Mutex
	ch     chan db.Notification
	closed bool
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{ch: make(chan db.Notification, 16)}
}

func (f *fakeSubscription) push(t *testing.T, n db.Notification) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		t.Fatalf("fakeSubscription closed")
	}
	f.ch <- n
}

func (f *fakeSubscription) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	close(f.ch)
}

func (f *fakeSubscription) channel() <-chan db.Notification {
	return f.ch
}

// TestAppDeleteSubscriber_ForgetOnNotify pins the headline fix:
// when an app_delete notification lands, any in-flight wake for
// the deleted app unwinds with ErrAppDeleted instead of waiting
// for the wake-coord TTL. The leader is mid-boot (sleepFor=50ms),
// the follower is parked on call.done; Forget closes done with
// ErrAppDeleted for both.
func TestAppDeleteSubscriber_ForgetOnNotify(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 128, 8)
	_ = acct

	vmm := &fakeVMM{sleepFor: 50 * time.Millisecond}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.0")

	sub := NewAppDeleteSubscriber(engine, testLog())
	fake := newFakeSubscription()
	defer fake.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, fake.channel()) }()

	// Leader is mid-boot. Add a follower so we exercise the
	// "followers unwind too" branch.
	leaderCall, isLeader, err := engine.wakeCoord.Enter(app.ID)
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}
	followerCall, isLeader, err := engine.wakeCoord.Enter(app.ID)
	if err != nil || isLeader {
		t.Fatalf("follower Enter: %v / isLeader=%v", err, isLeader)
	}

	// Notify. The subscriber's handle() decodes the payload and
	// calls wakeCoord.Forget(app.ID) — done is closed with
	// ErrAppDeleted for both the follower and the leader.
	fake.push(t, db.Notification{
		Channel: db.NotifyAppDelete,
		Payload: `{"app_id":"` + app.ID + `"}`,
	})

	followerOut := followerCall.Await(context.Background())
	if !errorsIs(followerOut.Err, ErrAppDeleted) {
		t.Fatalf("follower Err = %v, want ErrAppDeleted", followerOut.Err)
	}
	leaderOut := leaderCall.Await(context.Background())
	if !errorsIs(leaderOut.Err, ErrAppDeleted) {
		t.Fatalf("leader Err = %v, want ErrAppDeleted", leaderOut.Err)
	}

	// Forget has removed the entry from the map; Release's on
	// already-evicted entries are no-ops.
	engine.wakeCoord.Release(app.ID, leaderCall)
	engine.wakeCoord.Release(app.ID, followerCall)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("subscriber did not exit on ctx cancel")
	}
}

func TestAppDeleteSubscriber_DestroysDeletedAppVM(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 8)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.0")
	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	deleted := state.AppDeleted
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp deleted: %v", err)
	}

	sub := NewAppDeleteSubscriber(engine, testLog())
	sub.evictApp(context.Background(), app.ID)

	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", row.State)
	}
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
}

// TestAppDeleteSubscriber_IgnoresOtherChannels pins the defensive
// guard at handle(): a misrouted notification on a different
// channel must NOT trigger Forget (no innocent app evicted).
func TestAppDeleteSubscriber_IgnoresOtherChannels(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 8)

	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.0")
	sub := NewAppDeleteSubscriber(engine, testLog())
	fake := newFakeSubscription()
	defer fake.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, fake.channel()) }()

	// Register a leader so the entry exists.
	leaderCall, isLeader, err := engine.wakeCoord.Enter(app.ID)
	if err != nil || !isLeader {
		t.Fatalf("Enter: %v / isLeader=%v", err, isLeader)
	}
	// Push a notification on a wrong channel.
	fake.push(t, db.Notification{Channel: "app_changed", Payload: `{"app_id":"` + app.ID + `"}`})

	// Give the subscriber a chance to react (it shouldn't).
	time.Sleep(20 * time.Millisecond)
	if _, ok := engine.wakeCoord.inflight[app.ID]; !ok {
		t.Errorf("entry evicted by misrouted notification")
	}
	// Clean up so the goroutine exits.
	engine.wakeCoord.Release(app.ID, leaderCall)
	cancel()
	<-done
}

// TestAppDeleteSubscriber_BadPayloadIsLoggedNotPanicked pins the
// "loop outlives a transient bad event" contract. A malformed
// payload is logged and skipped.
func TestAppDeleteSubscriber_BadPayloadIsLoggedNotPanicked(t *testing.T) {
	engine := newEngine(t, state.NewMemStore(), &fakeVMM{}, &fakeNotifier{}, "1.0")
	sub := NewAppDeleteSubscriber(engine, testLog())
	fake := newFakeSubscription()
	defer fake.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, fake.channel()) }()

	// Bad JSON.
	fake.push(t, db.Notification{Channel: db.NotifyAppDelete, Payload: "not json"})
	// Empty app_id.
	fake.push(t, db.Notification{Channel: db.NotifyAppDelete, Payload: `{"app_id":""}`})
	// Empty payload.
	fake.push(t, db.Notification{Channel: db.NotifyAppDelete, Payload: ""})

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("subscriber did not exit on ctx cancel")
	}
}

// errorsIs is a tiny helper that wraps errors.Is for readability
// in the test assertions. Matches the pkg/sched style (errors.Is
// imports might pull stdlib transitively in tests).
func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}
