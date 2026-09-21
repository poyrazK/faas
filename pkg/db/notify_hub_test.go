package db

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestListenDiff(t *testing.T) {
	t.Parallel()
	set := func(s ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, x := range s {
			m[x] = struct{}{}
		}
		return m
	}
	cases := []struct {
		name        string
		have, want  map[string]struct{}
		add, remove []string
	}{
		{name: "empty to some", have: set(), want: set("b", "a"), add: []string{"a", "b"}},
		{name: "no change", have: set("a"), want: set("a")},
		{name: "swap", have: set("a", "z"), want: set("z", "m"), add: []string{"m"}, remove: []string{"a"}},
		{name: "all gone", have: set("q", "p"), want: set(), remove: []string{"p", "q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			add, remove := listenDiff(tc.have, tc.want)
			if !reflect.DeepEqual(add, tc.add) || !reflect.DeepEqual(remove, tc.remove) {
				t.Fatalf("add=%v remove=%v want add=%v remove=%v", add, remove, tc.add, tc.remove)
			}
		})
	}
}

type recordingHubObserver struct {
	reconnects atomic.Int64
	dropped    atomic.Int64
	delivered  atomic.Int64
}

func (r *recordingHubObserver) HubReconnect()       { r.reconnects.Add(1) }
func (r *recordingHubObserver) HubDropped(string)   { r.dropped.Add(1) }
func (r *recordingHubObserver) HubDelivered(string) { r.delivered.Add(1) }

// TestNotifyHubDispatchDropsOnFullBufferWithoutBlocking pins the one
// contract change: an overflowing subscriber never stalls dispatch.
func TestNotifyHubDispatchDropsOnFullBufferWithoutBlocking(t *testing.T) {
	obs := &recordingHubObserver{}
	SetNotifyHubObserver(obs)
	defer SetNotifyHubObserver(nil)

	h := newNotifyHub(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	slow := &hubSubscriber{channels: map[string]struct{}{"c": {}}, out: make(chan Notification, 1)}
	fast := &hubSubscriber{channels: map[string]struct{}{"c": {}}, out: make(chan Notification, 8)}
	other := &hubSubscriber{channels: map[string]struct{}{"x": {}}, out: make(chan Notification, 8)}
	h.subs[slow] = struct{}{}
	h.subs[fast] = struct{}{}
	h.subs[other] = struct{}{}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 3; i++ {
			h.dispatch(Notification{Channel: "c", Payload: "p"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch blocked on a full subscriber buffer")
	}
	if len(fast.out) != 3 || len(slow.out) != 1 || len(other.out) != 0 {
		t.Fatalf("fast=%d slow=%d other=%d", len(fast.out), len(slow.out), len(other.out))
	}
	if slow.dropped.Load() != 2 || obs.dropped.Load() != 2 {
		t.Fatalf("dropped sub=%d observer=%d want 2/2", slow.dropped.Load(), obs.dropped.Load())
	}
}

func TestNotifyHubEnabledEnv(t *testing.T) {
	t.Setenv(NotifyHubEnv, "")
	if !notifyHubEnabled() {
		t.Fatal("unset must enable the hub")
	}
	t.Setenv(NotifyHubEnv, "0")
	if notifyHubEnabled() {
		t.Fatal("\"0\" must disable the hub")
	}
	t.Setenv(NotifyHubEnv, "1")
	if !notifyHubEnabled() {
		t.Fatal("\"1\" must enable the hub")
	}
}

// --- Postgres-backed ---------------------------------------------------

func hubTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.Open(t) // Open registers its own schema drop + pool close via t.Cleanup
	return pool, context.Background()
}

func listenBackends(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_stat_activity
		 WHERE datname = current_database()
		   AND pid <> pg_backend_pid()
		   AND query LIKE 'LISTEN %'`).Scan(&n); err != nil {
		t.Fatalf("pg_stat_activity: %v", err)
	}
	return n
}

// waitListenBackends polls until the LISTEN backend count reaches want.
//
// SubscribeWithReconnect returns once the subscription is accepted; the
// backend's own `LISTEN` lands a moment later, and pg_stat_activity only shows
// it once it has. Reading the count immediately therefore races the last
// subscriber — the legacy-path test saw backends=2 want 3 intermittently on
// CI, reddening PRs that had nothing to do with pkg/db.
//
// Polling keeps the assertion's meaning (the legacy path opens one connection
// per subscriber) and drops the timing assumption.
func waitListenBackends(ctx context.Context, t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		if n = listenBackends(ctx, t, pool); n == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("LISTEN backends=%d want %d after 10s", n, want)
}

func recv(t *testing.T, ch <-chan Notification, want string) {
	t.Helper()
	select {
	case n := <-ch:
		if n.Payload != want {
			t.Fatalf("payload=%q want %q", n.Payload, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no notification with payload %q within 5s", want)
	}
}

func TestNotifyHub_OneConnectionForManySubscribers(t *testing.T) {
	t.Setenv(NotifyHubEnv, "")
	pool, ctx := hubTestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var chans []<-chan Notification
	for i := 0; i < 10; i++ {
		ch, err := SubscribeWithReconnect(ctx, pool, []string{"hub_shared", "hub_only_" + string(rune('a'+i))}, log)
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		chans = append(chans, ch)
	}
	st := NotifyHubStatsFor(pool)
	if st.Subscribers != 10 || st.Channels != 11 || !st.Running {
		t.Fatalf("stats=%+v", st)
	}
	// One backend in LISTEN for all ten subscribers; the legacy path
	// would show ten. The query itself runs on a separate pooled conn.
	if n := listenBackends(ctx, t, pool); n != 1 {
		t.Fatalf("LISTEN backends=%d want 1", n)
	}
	if err := Notify(ctx, pool, "hub_shared", `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	for i, ch := range chans {
		recv(t, ch, `{"n":1}`)
		_ = i
	}
	// Per-subscriber channel isolation.
	if err := Notify(ctx, pool, "hub_only_c", `{"c":1}`); err != nil {
		t.Fatal(err)
	}
	recv(t, chans[2], `{"c":1}`)
	select {
	case n := <-chans[0]:
		t.Fatalf("subscriber 0 received %+v for a channel it did not subscribe", n)
	case <-time.After(200 * time.Millisecond):
	}

	// Last subscriber leaving releases the connection.
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := NotifyHubStatsFor(pool); st.Subscribers == 0 && !st.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := NotifyHubStatsFor(pool); st.Subscribers != 0 || st.Running {
		t.Fatalf("hub still running after all subscribers left: %+v", st)
	}
	for _, ch := range chans {
		if _, ok := <-ch; ok {
			t.Fatal("subscriber channel must close on ctx cancel")
		}
	}
}

func TestNotifyHub_ChannelAddedToRunningHubIsLiveOnReturn(t *testing.T) {
	t.Setenv(NotifyHubEnv, "")
	pool, ctx := hubTestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	first, err := SubscribeWithReconnect(ctx, pool, []string{"hub_first"}, log)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SubscribeWithReconnect(ctx, pool, []string{"hub_second"}, log)
	if err != nil {
		t.Fatal(err)
	}
	// No sleep: the LISTEN must already be applied when subscribe returns.
	if err := Notify(ctx, pool, "hub_second", `{"s":1}`); err != nil {
		t.Fatal(err)
	}
	recv(t, second, `{"s":1}`)
	if err := Notify(ctx, pool, "hub_first", `{"f":1}`); err != nil {
		t.Fatal(err)
	}
	recv(t, first, `{"f":1}`)
	if n := listenBackends(ctx, t, pool); n != 1 {
		t.Fatalf("LISTEN backends=%d want 1", n)
	}
}

func TestNotifyHub_ReconnectsAfterBackendTerminated(t *testing.T) {
	t.Setenv(NotifyHubEnv, "")
	pool, ctx := hubTestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	obs := &recordingHubObserver{}
	SetNotifyHubObserver(obs)
	defer SetNotifyHubObserver(nil)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch, err := SubscribeWithReconnect(ctx, pool, []string{"hub_reconnect"}, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := Notify(ctx, pool, "hub_reconnect", `{"before":1}`); err != nil {
		t.Fatal(err)
	}
	recv(t, ch, `{"before":1}`)

	// Kill the hub's backend from the server side.
	if _, err := pool.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = current_database() AND pid <> pg_backend_pid() AND query LIKE 'LISTEN %'`); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if NotifyHubStatsFor(pool).Reconnects >= 1 && listenBackends(ctx, t, pool) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if NotifyHubStatsFor(pool).Reconnects < 1 || obs.reconnects.Load() < 1 {
		t.Fatalf("hub did not record a reconnect: %+v", NotifyHubStatsFor(pool))
	}
	// Delivery resumes on the new connection; retry the notify because
	// the re-LISTEN may land a few ms after the backend count recovers.
	got := false
	for i := 0; i < 20 && !got; i++ {
		if err := Notify(ctx, pool, "hub_reconnect", `{"after":1}`); err != nil {
			t.Fatal(err)
		}
		select {
		case n := <-ch:
			if n.Payload == `{"after":1}` {
				got = true
			}
		case <-time.After(250 * time.Millisecond):
		}
	}
	if !got {
		t.Fatal("no delivery after reconnect")
	}
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("subscriber channel closed across a reconnect")
		}
	default:
	}
}

func TestNotifyHub_KillSwitchRestoresConnectionPerSubscriber(t *testing.T) {
	t.Setenv(NotifyHubEnv, "0")
	pool, ctx := hubTestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i := 0; i < 3; i++ {
		if _, err := SubscribeWithReconnect(ctx, pool, []string{"hub_legacy"}, log); err != nil {
			t.Fatal(err)
		}
	}
	waitListenBackends(ctx, t, pool, 3)
	if st := NotifyHubStatsFor(pool); st.Subscribers != 0 {
		t.Fatalf("hub must be unused under the kill switch: %+v", st)
	}
}

// TestNotifyHubDeliveredCountsEveryFanOut pins the transport half of the
// broadcast-amplification measurement.
//
// pg_notify has no routing: one emit reaches every subscriber that wants the
// channel. The delivered counter has to count the FAN-OUT, not the receive,
// or the amplification factor it feeds is off by the very multiplier it is
// meant to expose. Three subscribers on one channel means one notification
// produces three deliveries.
func TestNotifyHubDeliveredCountsEveryFanOut(t *testing.T) {
	obs := &recordingHubObserver{}
	SetNotifyHubObserver(obs)
	defer SetNotifyHubObserver(nil)

	h := newNotifyHub(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 3; i++ {
		h.subs[&hubSubscriber{
			channels: map[string]struct{}{"amp": {}},
			out:      make(chan Notification, 4),
		}] = struct{}{}
	}
	// A subscriber on another channel must not be counted: the factor is
	// per-channel, and counting uninterested subscribers would inflate it.
	h.subs[&hubSubscriber{
		channels: map[string]struct{}{"other": {}},
		out:      make(chan Notification, 4),
	}] = struct{}{}

	h.dispatch(Notification{Channel: "amp", Payload: "x"})

	if got := obs.delivered.Load(); got != 3 {
		t.Errorf("delivered = %d, want 3 — one emit fanned out to three interested subscribers", got)
	}
	if got := obs.dropped.Load(); got != 0 {
		t.Errorf("dropped = %d, want 0", got)
	}
}

// TestNotifyHubDeliveredAndDroppedAccountForEveryFanOut pins that the two
// counters partition the fan-out. An overflowing subscriber must be counted
// exactly once, as a drop — never as both, and never as neither, or the
// amplification arithmetic silently loses events.
func TestNotifyHubDeliveredAndDroppedAccountForEveryFanOut(t *testing.T) {
	obs := &recordingHubObserver{}
	SetNotifyHubObserver(obs)
	defer SetNotifyHubObserver(nil)

	h := newNotifyHub(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	full := &hubSubscriber{channels: map[string]struct{}{"amp": {}}, out: make(chan Notification, 1)}
	roomy := &hubSubscriber{channels: map[string]struct{}{"amp": {}}, out: make(chan Notification, 8)}
	h.subs[full] = struct{}{}
	h.subs[roomy] = struct{}{}

	const emits = 3
	for i := 0; i < emits; i++ {
		h.dispatch(Notification{Channel: "amp", Payload: "x"})
	}

	// 2 subscribers × 3 emits = 6 fan-outs. The 1-deep subscriber takes one
	// and drops two; the roomy one takes all three.
	const wantFanOut = 2 * emits
	if got := obs.delivered.Load() + obs.dropped.Load(); got != wantFanOut {
		t.Errorf("delivered+dropped = %d, want %d — the counters must partition every fan-out",
			got, wantFanOut)
	}
	if got := obs.delivered.Load(); got != 4 {
		t.Errorf("delivered = %d, want 4", got)
	}
	if got := obs.dropped.Load(); got != 2 {
		t.Errorf("dropped = %d, want 2", got)
	}
}
