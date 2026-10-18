// spec: §4.3

package sched

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// gatedSynth holds every invocation until the test releases it, and
// records the order and the dispatch deadline it was given.
type gatedSynth struct {
	release chan struct{}

	mu        sync.Mutex
	order     []string
	deadlines []time.Time
}

func (g *gatedSynth) SynthesizeRequest(context.Context, string, string, string) error { return nil }

func (g *gatedSynth) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	deadline, _ := ctx.Deadline()
	g.mu.Lock()
	g.order = append(g.order, inv.ID)
	g.deadlines = append(g.deadlines, deadline)
	g.mu.Unlock()
	select {
	case <-g.release:
	case <-ctx.Done():
		return inv, ctx.Err()
	}
	inv.State = state.InvocationDispatching
	inv.InstanceID = "inst-" + inv.ID
	inv.Result = json.RawMessage(`{"ok":true}`)
	return inv, nil
}

func (g *gatedSynth) calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.order...)
}

// newLongDrainHarness seeds a Pro function whose invocations may run for
// two minutes (request_timeout_s=120).
func newLongDrainHarness(t *testing.T, opts ...DrainOption) (*Drain, state.Store, state.App, *gatedSynth) {
	t.Helper()
	return newLongDrainHarnessWith(t, func(m *state.MemStore) state.Store { return m }, opts...)
}

// newLongDrainHarnessWith is newLongDrainHarness over a wrapped MemStore.
func newLongDrainHarnessWith(t *testing.T, wrap func(*state.MemStore) state.Store, opts ...DrainOption) (*Drain, state.Store, state.App, *gatedSynth) {
	t.Helper()
	ctx := context.Background()
	store := wrap(state.NewMemStore())
	acct, err := store.CreateAccount(ctx, "long@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "long-fn", RAMMB: 256, MaxConcurrency: 5,
		IdleTimeoutS: 60, Manifest: state.AppManifest{RequestTimeoutS: 120}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	synth := &gatedSynth{release: make(chan struct{})}
	opts = append([]DrainOption{WithDrainGatewaySynth(synth),
		WithDrainLogger(slog.New(slog.NewTextHandler(testWriter{t}, nil)))}, opts...)
	d := NewDrain(store, eng, opts...)
	return d, store, app, synth
}

func enqueueLongInvocation(t *testing.T, store state.Store, app state.App, source state.InvocationSource, due time.Time) state.Invocation {
	t.Helper()
	inv, err := store.EnqueueInvocation(context.Background(), state.Invocation{AppID: app.ID, AccountID: app.AccountID,
		Source: source, Method: "POST", Path: "/work", DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func waitInvocationState(t *testing.T, store state.Store, id string, want state.InvocationState) state.Invocation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := store.InvocationByID(context.Background(), id)
		if err == nil && got.State == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("invocation %s state = %q (err %v), want %q", id, got.State, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ADR-1180: a long-timeout app's invocation is delivered in the background, so
// the tick does not wait for it, under the ordinary lease it keeps renewing.
func TestDrain_LongTimeoutInvocationDeliversInTheBackground(t *testing.T) {
	t.Parallel()
	d, store, app, synth := newLongDrainHarness(t)
	inv := enqueueLongInvocation(t, store, app, state.InvocationAsyncInvoke, time.Now().Add(-time.Second))

	ticked := make(chan struct{})
	started := time.Now()
	go func() { d.Tick(context.Background()); close(ticked) }()
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("the tick waited for a long invocation")
	}
	claimed := waitInvocationState(t, store, inv.ID, state.InvocationDispatching)
	if claimed.LeaseExpiresAt == nil || claimed.LeaseExpiresAt.After(started.Add(65*time.Second)) {
		t.Fatalf("lease expires at %v, want the ordinary 60 s lease, renewed while the run lasts", claimed.LeaseExpiresAt)
	}
	for len(synth.calls()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	synth.mu.Lock()
	deadline := synth.deadlines[0]
	synth.mu.Unlock()
	if want := started.Add(api.AsyncInvocationDispatchTimeout(2 * time.Minute)); deadline.Before(want.Add(-5*time.Second)) || deadline.After(want.Add(5*time.Second)) {
		t.Fatalf("dispatch deadline %v, want about %v", deadline, want)
	}

	close(synth.release)
	waitInvocationState(t, store, inv.ID, state.InvocationCompleted)
}

// ADR-1180: a long-timeout app's FIFO queue messages are delivered one at a
// time, in order, even though each runs in the background.
func TestDrain_LongTimeoutQueueMessagesStayInOrder(t *testing.T) {
	t.Parallel()
	d, store, app, synth := newLongDrainHarness(t)
	first := enqueueLongInvocation(t, store, app, state.InvocationQueue, time.Now().Add(-2*time.Second))
	second := enqueueLongInvocation(t, store, app, state.InvocationQueue, time.Now().Add(-time.Second))

	d.Tick(context.Background())
	waitInvocationState(t, store, first.ID, state.InvocationDispatching)
	for len(synth.calls()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	d.Tick(context.Background())
	if got := synth.calls(); len(got) != 1 || got[0] != first.ID {
		t.Fatalf("delivered %v while the first message ran, want only %s", got, first.ID)
	}
	if got, err := store.InvocationByID(context.Background(), second.ID); err != nil || got.State != state.InvocationPending {
		t.Fatalf("second message state = %q (err %v), want pending", got.State, err)
	}

	close(synth.release)
	waitInvocationState(t, store, first.ID, state.InvocationCompleted)
	d.Tick(context.Background())
	waitInvocationState(t, store, second.ID, state.InvocationCompleted)
	if got := synth.calls(); len(got) != 2 || got[1] != second.ID {
		t.Fatalf("delivery order %v, want %s then %s", got, first.ID, second.ID)
	}
}

// ADR-1180: an invocation of a long-timeout app is bounded by its own dispatch
// deadline, not the synth client's limit; an unmarked one keeps that limit.
func TestLongDispatchOutlivesTheSynthClientLimit(t *testing.T) {
	const handlerTime = 150 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(handlerTime):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"state":"dispatching","status_code":200,"result":{"ok":true}}`))
	}))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 50 * time.Millisecond // stands in for the 30 s synth limit
	h := &httpGatewaySynth{client: client, basePrefix: srv.URL}
	inv := state.Invocation{ID: "inv-1", AppID: "app-1", Source: state.InvocationAsyncInvoke, Method: http.MethodPost, Path: "/slow"}
	wake := WakeResult{InstanceID: "instance-1", NodeID: "node-1"}

	ctx, cancel := context.WithTimeout(withLongDispatch(t.Context()), time.Second)
	defer cancel()
	if out, err := h.InvokeWithWake(ctx, "app-1", inv, wake); err != nil || string(out.Result) != `{"ok":true}` {
		t.Fatalf("long dispatch with a 1 s deadline and a 150 ms handler: result=%s err=%v", out.Result, err)
	}
	if _, err := h.InvokeWithWake(t.Context(), "app-1", inv, wake); err == nil {
		t.Fatal("an unmarked invocation outlived the synth client's limit")
	}
}

// ADR-1180: schedd waits for a trigger batch as long as the gateway may
// spend on it; an app at the default keeps the 60 s it always had.
func TestTriggerBatchContextFollowsTheAppsInvocationTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "batch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	long, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "long", Manifest: state.AppManifest{RequestTimeoutS: 240}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), slog.Default())
	for _, tc := range []struct {
		appID string
		want  time.Duration
	}{
		{appID: plain.ID, want: triggerBatchDefaultWait},
		{appID: long.ID, want: api.TriggerBatchDispatchTimeout(4*time.Minute) + api.AsyncInvocationLeaseMargin},
	} {
		started := time.Now()
		batchCtx, cancel := loop.triggerBatchContext(ctx, tc.appID)
		deadline, ok := batchCtx.Deadline()
		cancel()
		if drift := deadline.Sub(started) - tc.want; !ok || drift > time.Second || drift < -time.Second {
			t.Errorf("app %s: batch deadline in %s, want %s", tc.appID, deadline.Sub(started), tc.want)
		}
	}
	// A caller's own deadline is kept.
	callerCtx, cancelCaller := context.WithTimeout(ctx, time.Second)
	defer cancelCaller()
	batchCtx, cancel := loop.triggerBatchContext(callerCtx, long.ID)
	defer cancel()
	want, _ := callerCtx.Deadline()
	if got, _ := batchCtx.Deadline(); !got.Equal(want) {
		t.Errorf("batch deadline %v replaced the caller's %v", got, want)
	}
}

// ADR-1180: the drain renews a long invocation's short lease while the run
// lasts, so the expired-lease sweep never requeues a live run.
func TestDrain_LongInvocationRenewsItsLease(t *testing.T) {
	t.Parallel()
	d, store, app, synth := newLongDrainHarness(t, WithDrainWakeLease(3))
	inv := enqueueLongInvocation(t, store, app, state.InvocationAsyncInvoke, time.Now().Add(-time.Second))
	d.Tick(context.Background())
	claimed := waitInvocationState(t, store, inv.ID, state.InvocationDispatching)
	time.Sleep(5 * time.Second) // past the 3 s lease it was claimed with
	current, err := store.InvocationByID(context.Background(), inv.ID)
	if err != nil || current.State != state.InvocationDispatching || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(*claimed.LeaseExpiresAt) {
		t.Fatalf("after 5 s the claim is %+v (err %v); want it dispatching under a renewed lease", current, err)
	}
	if n, err := store.(interface {
		RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
	}).RequeueExpiredInvocations(context.Background(), time.Now(), 10); err != nil || n != 0 {
		t.Fatalf("a live run was requeued: %d %v", n, err)
	}
	close(synth.release)
	waitInvocationState(t, store, inv.ID, state.InvocationCompleted)
}

// lostInvocationLease reports every renewal as refused, as when the claim
// was requeued under this dispatcher.
type lostInvocationLease struct {
	MemStore *state.MemStore
}

func (*lostInvocationLease) RenewInvocationLease(context.Context, string, int, int) (bool, error) {
	return false, nil
}

// ADR-1180: when the drain cannot keep a long invocation's claim, it cancels
// the delivery so a requeued attempt cannot overlap it.
func TestDrain_LongInvocationLeaseLossCancelsDelivery(t *testing.T) {
	t.Parallel()
	d, store, app, synth := newLongDrainHarnessWith(t, func(m *state.MemStore) state.Store {
		return statetest.NewMem(&lostInvocationLease{MemStore: m})
	}, WithDrainWakeLease(3))
	inv := enqueueLongInvocation(t, store, app, state.InvocationAsyncInvoke, time.Now().Add(-time.Second))
	d.Tick(context.Background())
	// The first renewal, a second in, is refused; the delivery is
	// cancelled and the invocation goes back for a retry.
	got := waitInvocationState(t, store, inv.ID, state.InvocationPending)
	if got.Attempts != 1 || len(synth.calls()) != 1 {
		t.Fatalf("invocation %+v after %d deliveries; want one cancelled delivery awaiting retry", got, len(synth.calls()))
	}
}

// ADR-1180: a long-timeout app's trigger records go one per batch; other
// apps keep their batch size.
func TestLongTimeoutTriggerBatchIsOneRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "batch-cap@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	long, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "long", Manifest: state.AppManifest{RequestTimeoutS: 240}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), slog.Default())
	trigger := func(appID string) sqlc.Trigger {
		t.Helper()
		parsed, err := uuid.Parse(appID)
		if err != nil {
			t.Fatal(err)
		}
		return sqlc.Trigger{AppID: pgtype.UUID{Bytes: parsed, Valid: true}, BatchSizeMax: 10}
	}
	if got := loop.longTimeoutTriggerBatch(ctx, trigger(long.ID)).BatchSizeMax; got != 1 {
		t.Errorf("long-timeout app batch size = %d, want 1", got)
	}
	if got := loop.longTimeoutTriggerBatch(ctx, trigger(plain.ID)).BatchSizeMax; got != 10 {
		t.Errorf("default app batch size = %d, want 10", got)
	}
	if got := (&Loop{}).longTimeoutTriggerBatch(ctx, trigger(long.ID)).BatchSizeMax; got != 10 {
		t.Errorf("loop without an engine changed the batch size to %d", got)
	}
}
