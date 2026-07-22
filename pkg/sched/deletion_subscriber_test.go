package sched

// Tests for the deletion subscriber (ADR-026). MemStore-backed, no
// Postgres required. The subscriber's API is purely "drain this
// channel": tests build the channel themselves (via fakeNotify), and
// the live / parked assertions are framed around state.IsLive so
// the contract lives in pkg/state, not here.
//   1. A pg_notify message with a known account_id causes every live
//      instance owned by that account to be transitioned to the new
//      "evicting_account_deleting" terminal state.
//   2. A second message for the same account is a no-op (the rows
//      moved out of the live set).
//   3. A malformed payload is logged and skipped — it MUST NOT block
//      later messages on the same channel.
//   4. A channel that closes mid-flight causes Run to return nil
//      (the caller owns the reconnect, so "channel closed" is the
//      happy path for a fresh producer).

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeNotify is a producer that hands the subscriber a hand-fed
// channel. Tests build the channel themselves, drive Run directly,
// and close the channel when finished — the close is what Run waits
// on to return (PR #83 review #6 removed the SubFn reconnect path
// and the caller now owns the producer).
type fakeNotify struct {
	mu sync.Mutex
	ch chan db.Notification
}

func newFakeNotify(buf int) *fakeNotify {
	return &fakeNotify{ch: make(chan db.Notification, buf)}
}

// Channel returns the underlying channel so tests can hand it to
// DeletionSubscriber.Run directly.
func (f *fakeNotify) Channel() <-chan db.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ch
}

// Send publishes one notification. Blocks if the channel buffer is
// full — tests size the buffer generously so this never trips.
func (f *fakeNotify) Send(n db.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ch <- n
}

// silenceLog returns an io.Discard-backed slog.Logger so the
// subscriber's Warn/Info lines don't pollute the test output.
func silenceLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestDeletionSubscriber_ParkOnMessage is the primary ADR-026
// regression: a single payload transitions every live instance of
// the customer to the parked set, with no write to other accounts.
func TestDeletionSubscriber_ParkOnMessage(t *testing.T) {
	store := state.NewMemStore()
	acctA, appA, depA := seedOneAccount(t, store, "owner-a@example.com")
	if _, err := store.CreateInstance(context.Background(), appA.ID, depA.ID, "running", 128, state.DefaultLocalNodeName); err != nil {
		t.Fatalf("instance A1: %v", err)
	}
	if _, err := store.CreateInstance(context.Background(), appA.ID, depA.ID, "waking", 128, state.DefaultLocalNodeName); err != nil {
		t.Fatalf("instance A2: %v", err)
	}
	// Distant account — MUST NOT be touched.
	acctB, appB, depB := seedOneAccount(t, store, "owner-b@example.com")
	insB, err := store.CreateInstance(context.Background(), appB.ID, depB.ID, "running", 128, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("instance B: %v", err)
	}

	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(4)
	sub := NewDeletionSubscriber(engine, silenceLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{
		Channel: db.NotifyAccountDeletionPending,
		Payload: `{"account_id":"` + acctA.ID + `"}`,
	})

	if err := waitFor(func() bool {
		return countLive(store, acctA.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("account A still has %d live instances after publish", countLive(store, acctA.ID))
	}

	if liveB := countLive(store, acctB.ID); liveB != 1 {
		t.Errorf("account B live instances = %d, want 1 (insB=%s)", liveB, insB.ID)
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

// TestDeletionSubscriber_DuplicateMessageIsNoOp asserts the
// subscriber can be re-published against the same account without
// touching parked instances. Mirrors pg_notify's at-least-once
// delivery.
func TestDeletionSubscriber_DuplicateMessageIsNoOp(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedOneAccount(t, store, "dup@example.com")
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, "running", 128, state.DefaultLocalNodeName); err != nil {
		t.Fatalf("instance: %v", err)
	}

	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(4)
	sub := NewDeletionSubscriber(engine, silenceLog())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":"` + acct.ID + `"}`})
	if err := waitFor(func() bool {
		return countLive(store, acct.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("first publish didn't evict: %v", err)
	}
	if got := countLive(store, acct.ID); got != 0 {
		t.Errorf("after first evict, countLive = %d, want 0", got)
	}
	// Second message — the row is already in the terminal state so
	// the live filter excludes it. Assert no panic and no row
	// corruption.
	feed.Send(db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":"` + acct.ID + `"}`})
	if err := waitFor(func() bool {
		return countLive(store, acct.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("second publish left a live instance: %v", err)
	}
	if got, _ := store.AccountByID(context.Background(), acct.ID); got.ID != acct.ID {
		t.Fatalf("account vanished: got %+v", got)
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

// TestDeletionSubscriber_BadPayloadSkipped asserts a malformed
// payload logs + skips, the channel keeps flowing, and the next
// good message still produces action.
func TestDeletionSubscriber_BadPayloadSkipped(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedOneAccount(t, store, "bad@example.com")
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, "running", 128, state.DefaultLocalNodeName); err != nil {
		t.Fatalf("instance: %v", err)
	}
	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(4)
	sub := NewDeletionSubscriber(engine, silenceLog())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: "not-json"})
	feed.Send(db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":""}`})
	feed.Send(db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":"` + acct.ID + `"}`})

	if err := waitFor(func() bool {
		return countLive(store, acct.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("good payload didn't evict: %v", err)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

// TestDeletionSubscriber_ChannelCloseReturns asserts that closing the
// feed channel causes Run to return nil. This is the load-bearing
// signal that lets cmd/schedd's reconnect logic (the production
// seam that owns Subscribe) tear down Run cleanly on dial failure.
func TestDeletionSubscriber_ChannelCloseReturns(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(1)
	sub := NewDeletionSubscriber(engine, silenceLog())

	feed.mu.Lock()
	close(feed.ch)
	feed.mu.Unlock()

	// Bounded: a stuck Run would block until ctx fires. 200ms is
	// well past the channel-close wake-up but well below any human
	// noticing on CI.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := sub.Run(ctx, feed.Channel())
	if err != nil {
		t.Errorf("Run after close returned %v, want nil", err)
	}
}

// seedOneAccount creates a single Hobby account with one app + one
// live deployment, returning all three. Differs from engine_test.go's
// seedApp: it accepts a unique email so two calls inside the same
// test (for the "owner + bystander" pattern) don't collide on the
// hard-coded "u@example.com".
func seedOneAccount(t *testing.T, store *state.MemStore, email string) (state.Account, state.App, state.Deployment) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID:      acct.ID,
		Slug:           email + "-app",
		RAMMB:          128,
		MaxConcurrency: 2,
		IdleTimeoutS:   60,
		Type:           state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:abc",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return acct, app, dep
}

// countLive returns the number of instances for the account whose
// state counts as "live" by the official predicate (state.IsLive).
// MemStore's ListInstancesForAccount doesn't filter on state, so
// the helper does the filtering the test wants — and pins the
// production semantics: ADR-026 transitions instances OUT of the
// live set when schedd evicts them. Future state additions land
// through pkg/state/machine.go in one place.
func countLive(store *state.MemStore, accountID string) int {
	all, err := store.ListInstancesForAccount(context.Background(), accountID)
	if err != nil {
		return -1
	}
	n := 0
	for _, ins := range all {
		if state.IsLive(ins.State) {
			n++
		}
	}
	return n
}

func waitFor(pred func() bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
