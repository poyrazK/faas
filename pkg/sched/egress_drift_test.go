// tier-2 PR-B unit tests for the egress_drift subscriber (ADR-031 +
// ADR-033). The subscriber is a drain over an already-opened
// <-chan db.Notification; tests use the same fakeNotify pattern as
// deletion_subscriber_test.go and a recording RoutedVMM fake that
// captures every (nodeID, appID, allowlist) UpdateEgressAllowlist
// call. PG-backed integration is out of scope here — that lands in
// the sched pg suite.

package sched

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingRouterVMM records every UpdateEgressAllowlist call. All
// other RoutedVMM methods no-op so the engine construction in
// newEngine doesn't complain. Errors can be injected per-node so
// the "log and continue" path is exercised.
type recordingRouterVMM struct {
	mu sync.Mutex
	// calls is a chronologically-ordered list of patches pushed
	// to the (deduped-by-node) per-node vmmd.
	calls []recordedAllowlistCall
	// staticCalls (ADR-119) is the parallel per-node log for
	// UpdateStaticEgressIP patches.
	staticCalls []recordedStaticIPCall
	// nodeErrors is an optional per-nodeID error injection. nil
	// entries succeed.
	nodeErrors map[string]error
}

type recordedAllowlistCall struct {
	NodeID    string
	AppID     string
	Allowlist []netip.Prefix
}

// recordedStaticIPCall (ADR-119) is one UpdateStaticEgressIP
// patch logged by the recordingRouterVMM. IP is the dotted-quad
// string the patch pushed (or "" for a clear).
type recordedStaticIPCall struct {
	NodeID string
	AppID  string
	IP     string
}

func (r *recordingRouterVMM) UpdateEgressAllowlist(_ context.Context, nodeID, appID string, allowlist []netip.Prefix) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy the slice so a later mutation (or the caller's
	// reuse of the underlying array) doesn't reach into the
	// recorded value.
	cp := make([]netip.Prefix, len(allowlist))
	copy(cp, allowlist)
	r.calls = append(r.calls, recordedAllowlistCall{
		NodeID:    nodeID,
		AppID:     appID,
		Allowlist: cp,
	})
	if r.nodeErrors != nil {
		if err, ok := r.nodeErrors[nodeID]; ok {
			return err
		}
	}
	return nil
}

// UpdateStaticEgressIP (ADR-119) records the per-node
// static-IP patch. Mirrors UpdateEgressAllowlist above.
func (r *recordingRouterVMM) UpdateStaticEgressIP(_ context.Context, nodeID, accountID, appID string, ip string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.staticCalls = append(r.staticCalls, recordedStaticIPCall{
		NodeID: nodeID,
		AppID:  appID,
		IP:     ip,
	})
	if r.nodeErrors != nil {
		if err, ok := r.nodeErrors[nodeID]; ok {
			return err
		}
	}
	return nil
}

func (r *recordingRouterVMM) callsByNode() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := make(map[string]int, len(r.calls))
	for _, c := range r.calls {
		m[c.NodeID]++
	}
	return m
}

// snapshotLen returns the current call count under the lock. The
// bare `len(r.calls)` reads the test ran before caused the race
// detector to fire on the post-wait pass (the goroutine that
// handled the payload may still be writing the same struct after
// waitFor observed the trigger). Every test that races against a
// fan-out goroutine MUST go through this helper.
func (r *recordingRouterVMM) snapshotLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// snapshot returns a copy of the recorded calls under the lock.
// The returned slice is owned by the caller; mutations don't
// reach into the recording fake.
func (r *recordingRouterVMM) snapshot() []recordedAllowlistCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedAllowlistCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// Stub the rest of RoutedVMM so the engine can be constructed.
func (r *recordingRouterVMM) CreateColdBoot(context.Context, string, string, AppSpec) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (r *recordingRouterVMM) CreateFromSnapshot(context.Context, string, string, AppSpec, SnapshotRef) (*WakeOutcome, error) {
	return &WakeOutcome{}, nil
}
func (r *recordingRouterVMM) PauseAndSnapshot(context.Context, string, string, string, string, string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the egress-drift
// test's no-op seam — the egress drift path doesn't fire warm
// captures (the engine reaper is the only entry point).
func (r *recordingRouterVMM) WarmSnapshot(context.Context, string, string, string, string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}
func (r *recordingRouterVMM) Destroy(context.Context, string, string) error { return nil }

// StopInstance (M-2 / ADR-138 §Decision 1) is the
// graceful signal-then-grace-then-SIGKILL stop
// sequence. Test fakes default to no-op + nil —
// the engine's per-mode dispatch lives in
// pkg/sched/engine_stop_pgtest_test.go (commit 6).
func (r *recordingRouterVMM) StopInstance(_ context.Context, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}
func (r *recordingRouterVMM) StopInstanceOnNode(_ context.Context, _, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}

// FrameworkReady implements RoutedVMM for the egress-drift test
// fake (issue #470 / PR #470-FU-B). No-op — the egress-drift
// tests don't exercise the warm-capture path; the engine tests
// cover the framework-ready wiring.
func (r *recordingRouterVMM) FrameworkReady(context.Context, string, string, int64) error {
	return nil
}
func (r *recordingRouterVMM) Ping(_ context.Context, _ string) (*PingOutcome, error) {
	return &PingOutcome{FcVersion: "1.10.0"}, nil
}

// Stats implements RoutedVMM (issue #170 / PR-A, observability
// slice). Egress-drift tests do not assert on Stats contents; the
// instancestats package's own tests cover the wire. Returns the
// empty snapshot so the VMM / RoutedVMM contract stays satisfied.
func (r *recordingRouterVMM) Stats(_ context.Context, _ string) (*StatsSnapshot, error) {
	return &StatsSnapshot{}, nil
}

// Logs (issue #254 / Move 4, issue #517 / PR-B) — the egress_drift
// test rig doesn't drive log streams, so the recordingRouterVMM
// returns a no-op fake stream that closes immediately. Tests that
// exercise the Move 4 path inject a different fake. PR-B adds the
// sinceWrittenAt time lower-bound; the fake ignores it.
func (r *recordingRouterVMM) Logs(_ context.Context, _, _ string, _ int64, _ time.Time) (LogStream, error) {
	return &fakeLogStream{}, nil
}

// Tier A5 (ADR-066): no-op stubs to satisfy RoutedVMM. The
// egress drift test doesn't drive the migration path.
func (r *recordingRouterVMM) PrepareLiveMigration(context.Context, string, string, string) (LiveMigrationPrepare, error) {
	return LiveMigrationPrepare{}, nil
}
func (r *recordingRouterVMM) AdoptMigratedInstance(context.Context, string, string, AppSpec, string, string, string) (LiveMigrationAdopt, error) {
	return LiveMigrationAdopt{}, nil
}
func (r *recordingRouterVMM) AcknowledgeMigration(context.Context, string, string, string) error {
	return nil
}
func (r *recordingRouterVMM) CancelLiveMigration(context.Context, string, string, string) error {
	return nil
}

// seedEgressApp populates the store with one account + one app +
// one deployment + a caller-supplied list of live instances, each
// pinned to a nodeID. Returns the account, app, and the
// deployment IDs (the caller uses app.ID downstream).
func seedEgressApp(
	t *testing.T,
	store *state.MemStore,
	email string,
	nodes []string,
) (state.App, []state.Instance) {
	t.Helper()
	_, app, dep := seedOneAccount(t, store, email)
	var instances []state.Instance
	for _, nodeID := range nodes {
		ins, err := store.CreateInstance(context.Background(), app.ID, dep.ID, "running", 128, nodeID, "")
		if err != nil {
			t.Fatalf("CreateInstance(node=%s): %v", nodeID, err)
		}
		instances = append(instances, ins)
	}
	return app, instances
}

// setAppAllowlist writes the per-app egress allowlist on the
// store. Used by tests that need to stage an allowlist BEFORE
// sending the pg_notify payload.
func setAppAllowlist(t *testing.T, store *state.MemStore, appID string, allowlist []netip.Prefix) {
	t.Helper()
	params := state.UpdateAppParams{
		EgressAllowlist:    &allowlist,
		SetEgressAllowlist: true,
	}
	if _, err := store.UpdateApp(context.Background(), appID, params); err != nil {
		t.Fatalf("UpdateApp(%s): %v", appID, err)
	}
}

// TestEgressDrift_FiltersToKindUpdated is the primary regression:
// only kind="updated" payloads reach the vmmd. Other kinds are
// silently dropped (the engine's existing app_changed loop logs
// the others; the egress subscriber is read-only on that surface).
func TestEgressDrift_FiltersToKindUpdated(t *testing.T) {
	store := state.NewMemStore()
	app, _ := seedEgressApp(t, store, "egress-owner-filter@example.com", []string{"node-A"})
	setAppAllowlist(t, store, app.ID, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")})

	router := &recordingRouterVMM{}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	// Wrong channel — must be ignored.
	feed.Send(db.Notification{Channel: "deployment_changed", Payload: `{"app_id":"` + app.ID + `"}`})
	// Right channel, wrong kind — must be ignored.
	for _, k := range []string{"renamed", "deleted", "parked", "woken"} {
		feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"` + k + `","app_id":"` + app.ID + `"}`})
	}

	// One valid payload — should fan out to node-A.
	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":"` + app.ID + `","slug":"the-app"}`})

	if err := waitFor(func() bool { return router.snapshotLen() == 1 }, 2*time.Second); err != nil {
		t.Fatalf("expected exactly 1 valid call, got %d", router.snapshotLen())
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

// TestEgressDrift_FansOutAcrossNodes — 3 live instances pinned — 3 live instances pinned
// to 2 different nodes; one payload must result in one call per
// node. Per-instance dedup by nodeID.
func TestEgressDrift_FansOutAcrossNodes(t *testing.T) {
	store := state.NewMemStore()
	app, _ := seedEgressApp(t, store, "egress-owner-fanout@example.com",
		[]string{"node-A", "node-A", "node-B"})
	// Stage the allowlist BEFORE the notify.
	setAppAllowlist(t, store, app.ID, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")})

	router := &recordingRouterVMM{}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":"` + app.ID + `"}`})

	if err := waitFor(func() bool {
		c := router.callsByNode()
		return c["node-A"] == 1 && c["node-B"] == 1
	}, 2*time.Second); err != nil {
		t.Fatalf("expected 1 call to node-A and 1 to node-B; got %v", router.callsByNode())
	}

	// Every call carries the new allowlist. Snapshot under the
	// lock so the race detector doesn't fire on the post-wait
	// read.
	for _, c := range router.snapshot() {
		if len(c.Allowlist) != 1 || c.Allowlist[0].String() != "8.8.8.0/24" {
			t.Errorf("call %+v has wrong allowlist", c)
		}
		if c.AppID != app.ID {
			t.Errorf("call %+v has wrong app_id", c)
		}
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v", err)
	}
}

// TestEgressDrift_IdempotentOnRedelivery — the vmmd side
// (samePrefixSet short-circuit) handles redelivery, but the
// subscriber is also safe: two identical payloads just push
// twice. vmmd's idempotency is the load-bearing wall, not the
// subscriber's; this test pins the subscriber's "fan out every
// payload" contract.
func TestEgressDrift_IdempotentOnRedelivery(t *testing.T) {
	store := state.NewMemStore()
	app, _ := seedEgressApp(t, store, "egress-owner-idem@example.com", []string{"node-A"})
	setAppAllowlist(t, store, app.ID, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")})

	router := &recordingRouterVMM{}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	for i := 0; i < 3; i++ {
		feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":"` + app.ID + `"}`})
	}
	if err := waitFor(func() bool { return router.snapshotLen() == 3 }, 2*time.Second); err != nil {
		t.Fatalf("expected 3 calls (subscriber fans out every payload; vmmd short-circuits), got %d", router.snapshotLen())
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v", err)
	}
}

// TestEgressDrift_RejectsBadPayload — bad JSON / empty
// app_id / non-JSON payloads are dropped without
// propagating. Subscriber stays alive.
func TestEgressDrift_RejectsBadPayload(t *testing.T) {
	store := state.NewMemStore()
	app, _ := seedEgressApp(t, store, "egress-owner-bad@example.com", []string{"node-A"})
	setAppAllowlist(t, store, app.ID, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")})

	router := &recordingRouterVMM{}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: "not-json"})
	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"foo":"bar"}`})
	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":""}`})
	// A valid payload after the bad ones proves the loop
	// survived.
	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":"` + app.ID + `"}`})

	if err := waitFor(func() bool { return router.snapshotLen() == 1 }, 2*time.Second); err != nil {
		t.Fatalf("expected 1 call after 3 bad + 1 good, got %d", router.snapshotLen())
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v", err)
	}
}

// TestEgressDrift_NodeErrorDoesNotStopLoop — one node fails,
// the other succeeds. Loop continues, error is logged.
func TestEgressDrift_NodeErrorDoesNotStopLoop(t *testing.T) {
	store := state.NewMemStore()
	app, _ := seedEgressApp(t, store, "egress-owner-node-err@example.com",
		[]string{"node-A", "node-B"})
	setAppAllowlist(t, store, app.ID, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")})

	router := &recordingRouterVMM{
		nodeErrors: map[string]error{
			"node-A": errors.New("synthetic vmmd error"),
		},
	}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":"` + app.ID + `"}`})

	if err := waitFor(func() bool {
		// node-A errors; node-B succeeds. We expect
		// node-B's call recorded (node-A's was
		// attempted but errored).
		c := router.callsByNode()
		return c["node-B"] == 1
	}, 2*time.Second); err != nil {
		t.Fatalf("expected 1 call to node-B (after node-A errored); got %v", router.callsByNode())
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v", err)
	}
}

// TestEgressDrift_EmptyAllowlistClears is the "[]" path — the
// subscriber re-reads the column (now empty) and pushes the
// empty list. vmmd's side flips the chain policy back to
// accept.
func TestEgressDrift_EmptyAllowlistClears(t *testing.T) {
	store := state.NewMemStore()
	app, _ := seedEgressApp(t, store, "egress-owner-empty@example.com", []string{"node-A"})
	// Stage an empty allowlist on the app.
	empty := []netip.Prefix{}
	setAppAllowlist(t, store, app.ID, empty)

	router := &recordingRouterVMM{}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx, feed.Channel()) }()

	feed.Send(db.Notification{Channel: db.NotifyAppChanged, Payload: `{"kind":"updated","app_id":"` + app.ID + `"}`})

	if err := waitFor(func() bool { return router.snapshotLen() == 1 }, 2*time.Second); err != nil {
		t.Fatalf("expected 1 call (empty allowlist), got %d", router.snapshotLen())
	}
	// Snapshot under the lock so the race detector doesn't fire
	// on the timing of "wait returned vs the goroutine's last
	// write".
	calls := router.snapshot()
	if got := len(calls[0].Allowlist); got != 0 {
		t.Errorf("expected empty allowlist on the wire, got %d entries", got)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v", err)
	}
}

// TestEgressDrift_ChannelCloseReturnsNil — closing the
// channel without a context cancel must return nil (not
// ctx.Err()) — the producer signalling "I'm done" is a
// graceful path, not an error.
func TestEgressDrift_ChannelCloseReturnsNil(t *testing.T) {
	store := state.NewMemStore()
	router := &recordingRouterVMM{}
	engine := newEngine(t, store, router, &fakeNotifier{}, "")
	sub := NewEgressDriftSubscriber(engine, router, silenceLog())
	feed := newFakeNotify(1)

	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- sub.Run(ctx, feed.Channel()) }()
	close(feed.ch)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run on channel-close: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on channel close")
	}
}

// (no extra imports — silenceLog(), waitFor(), fakeNotifier,
// seedOneAccount(), newFakeNotify(), newEngine() all come from
// sibling test files in the same package.)
