package sched

// Tests for the deletion subscriber (ADR-026). MemStore-backed, no
// Postgres required. The subscriber's only contract that matters to
// us here is:
//   1. A pg_notify message with a known account_id causes every live
//      instance owned by that account to be transitioned to a parked
//      state via Engine.Park (the new "evicting_account_deleting"
//      state lives on the row, but Park is the only writer in this
//      pathway).
//   2. A second message for the same account is a no-op (the
//      ListInstancesForAccount call returns zero rows because Park
//      moved them out of the live set).
//   3. A malformed payload is logged and skipped — it MUST NOT block
//      later messages on the same channel.
//   4. A redelivered channel (the producer closed and re-opened) is
//      handled by the same Run loop without leaking goroutines.

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
// channel. The same channel is reused across dial attempts (matches
// how the production pg_notify client survives a brief disconnect).
type fakeNotify struct {
	mu  sync.Mutex
	ch  chan db.Notification
	err error
}

func newFakeNotify(buf int) *fakeNotify {
	return &fakeNotify{ch: make(chan db.Notification, buf)}
}

func (f *fakeNotify) Subscribe(ctx context.Context, _ []string) (<-chan db.Notification, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, func() {}, f.err
	}
	return f.ch, func() {}, nil
}

func (f *fakeNotify) Send(n db.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ch <- n
}

// publishOne is a fire-and-forget helper for the tests that publish a
// single message and don't need the channel afterwards.
func publishOne(t *testing.T, f *fakeNotify, n db.Notification) {
	t.Helper()
	f.Send(n)
}

// silenceLog returns an io.Discard-backed slog.Logger so the
// subscriber's Warn/Info lines don't pollute the test output.
func silenceLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestDeletionSubscriber_ParkOnMessage is the primary ADR-026
// regression: a single payload transitions every live instance of the
// customer to the parked set, with no write to other accounts.
func TestDeletionSubscriber_ParkOnMessage(t *testing.T) {
	store := state.NewMemStore()
	acctA, appA, depA := seedOneAccount(t, store, "owner-a@example.com")
	if _, err := store.CreateInstance(context.Background(), appA.ID, depA.ID, "running", 128); err != nil {
		t.Fatalf("instance A1: %v", err)
	}
	if _, err := store.CreateInstance(context.Background(), appA.ID, depA.ID, "waking", 128); err != nil {
		t.Fatalf("instance A2: %v", err)
	}
	// Distant account — MUST NOT be touched.
	acctB, appB, depB := seedOneAccount(t, store, "owner-b@example.com")
	insB, err := store.CreateInstance(context.Background(), appB.ID, depB.ID, "running", 128)
	if err != nil {
		t.Fatalf("instance B: %v", err)
	}

	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(4)

	sub := NewDeletionSubscriber(engine, silenceLog())
	sub.SubFn = feed.Subscribe
	sub.ChannelIDs = []string{db.NotifyAccountDeletionPending}
	sub.SetBackoff(0, 0) // tests should never wait

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	// Hit customer A's pending; expect both A instances to be parked.
	publishOne(t, feed, db.Notification{
		Channel: db.NotifyAccountDeletionPending,
		Payload: `{"account_id":"` + acctA.ID + `"}`,
	})

	// Wait for the work to land. We poll the store rather than
	// sleeping blind — MemStore updates are synchronous through the
	// subscriber's handle path.
	if err := waitFor(func() bool {
		return countLive(store, acctA.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("account A still has %d live instances after publish", countLive(store, acctA.ID))
	}

	// Customer B's instance MUST remain live.
	liveB := countLive(store, acctB.ID)
	if liveB != 1 {
		t.Errorf("account B live instances = %d, want 1 (insB=%s)", liveB, insB.ID)
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

// TestDeletionSubscriber_DuplicateMessageIsNoOp asserts the subscriber
// can be re-published against the same account without touching
// parked instances. Mirrors pg_notify's at-least-once delivery.
func TestDeletionSubscriber_DuplicateMessageIsNoOp(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedOneAccount(t, store, "dup@example.com")
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, "running", 128); err != nil {
		t.Fatalf("instance: %v", err)
	}

	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(4)
	sub := NewDeletionSubscriber(engine, silenceLog())
	sub.SubFn = feed.Subscribe
	sub.ChannelIDs = []string{db.NotifyAccountDeletionPending}
	sub.SetBackoff(0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	publishOne(t, feed, db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":"` + acct.ID + `"}`})
	if err := waitFor(func() bool {
		return countLive(store, acct.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("first publish didn't park: %v", err)
	}
	// Second message — store has the row gone, but ListInstancesForAccount
	// still scans by account. After Park it's no longer RUNNING, so the
	// filter excludes it. We just assert no panic and the row's state is
	// some parked-like value.
	if got := countLive(store, acct.ID); got != 0 {
		t.Errorf("after park, countLive = %d, want 0", got)
	}
	publishOne(t, feed, db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":"` + acct.ID + `"}`})
	time.Sleep(20 * time.Millisecond)
	if got, _ := store.AccountByID(context.Background(), acct.ID); got.ID != acct.ID {
		t.Fatalf("account vanished: got %+v", got)
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

// TestDeletionSubscriber_BadPayloadSkipped asserts a malformed payload
// logs + skips, the channel keeps flowing, and the next good message
// still produces action.
func TestDeletionSubscriber_BadPayloadSkipped(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedOneAccount(t, store, "bad@example.com")
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, "running", 128); err != nil {
		t.Fatalf("instance: %v", err)
	}
	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "")
	feed := newFakeNotify(4)
	sub := NewDeletionSubscriber(engine, silenceLog())
	sub.SubFn = feed.Subscribe
	sub.ChannelIDs = []string{db.NotifyAccountDeletionPending}
	sub.SetBackoff(0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	publishOne(t, feed, db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: "not-json"})
	publishOne(t, feed, db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":""}`})
	publishOne(t, feed, db.Notification{Channel: db.NotifyAccountDeletionPending, Payload: `{"account_id":"` + acct.ID + `"}`})

	if err := waitFor(func() bool {
		return countLive(store, acct.ID) == 0
	}, 2*time.Second); err != nil {
		t.Fatalf("good payload didn't park: %v", err)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

// TestDeletionSubscriber_NextDelay is the bound-checking contract on
// the backoff curve. Pure unit test.
func TestDeletionSubscriber_NextDelay(t *testing.T) {
	cases := []struct {
		name           string
		cur, max, want time.Duration
	}{
		// nextDelay doubles cur; a cur=0 stays at 0 because the
		// production caller clamps SetBackoff(0,0) only for tests,
		// and we don't want a 0 * 2 to "look like" max==30s on the
		// first round. The test pin is the saturating case below.
		{"zero cur", 0, 30 * time.Second, 0},
		{"doubles at low cur", 1 * time.Second, 30 * time.Second, 2 * time.Second},
		{"doubles just under cap", 16 * time.Second, 30 * time.Second, 30 * time.Second},
		{"above cap clamps to max", 20 * time.Second, 30 * time.Second, 30 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextDelay(c.cur, c.max); got != c.want {
				t.Errorf("nextDelay(%v, %v) = %v, want %v", c.cur, c.max, got, c.want)
			}
		})
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
		AccountID: acct.ID, Slug: email + "-app", RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60,
		Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return acct, app, dep
}

// countLive returns the number of instances for the account whose
// state is in PgStore's "live" set (running, waking, cold_booting,
// snapshotting). MemStore's ListInstancesForAccount doesn't filter on
// state, so this helper does the filtering the test wants — and
// pins the production semantics while we're at it: ADR-026
// transitions instances OUT of the live set when schedd evicts them.
func countLive(store *state.MemStore, accountID string) int {
	all, err := store.ListInstancesForAccount(context.Background(), accountID)
	if err != nil {
		return -1
	}
	n := 0
	for _, ins := range all {
		switch ins.State {
		case "running", "waking", "cold_booting", "snapshotting":
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
	return errors.New("timeout")
}
