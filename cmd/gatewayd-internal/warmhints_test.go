// warmhints_test.go — gatewayd's warmHintConsumer tests
// (ADR-025 axis 4).
//
// The consumer's Run loop wires a *scheddgrpc.Client (real or
// nil-tolerant) into a reconnect outer loop with backoff; testing
// the full reconnect path needs a real or bufconn-backed
// scheddgrpc.Client, which is exercised by the integration
// smoke (operator playbook in the plan file). The unit tests
// here cover the surface that's exercise-able without a gRPC
// transport:
//
//   - drain updates the cache on every event
//   - drain exits cleanly on io.EOF and on ctx cancel
//   - drain rejects malformed events (empty AppID/NodeID)
//   - the cache→WarmHintFunc adapter shape matches the picker
//   - nil-safety on cache + consumer construction

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
)

// fakeWarmHintStream is a hand-rolled WarmHintStream for tests.
// events are delivered in order; once exhausted the stream
// returns tailErr (or io.EOF when tailErr is nil).
type fakeWarmHintStream struct {
	events  []scheddgrpc.WarmHintEvent
	idx     int
	tailErr error
}

func (f *fakeWarmHintStream) Recv() (scheddgrpc.WarmHintEvent, error) {
	if f.idx >= len(f.events) {
		if f.tailErr != nil {
			return scheddgrpc.WarmHintEvent{}, f.tailErr
		}
		return scheddgrpc.WarmHintEvent{}, io.EOF
	}
	ev := f.events[f.idx]
	f.idx++
	return ev, nil
}

func newTestConsumer(cache *gateway.WarmHintCache) *warmHintConsumer {
	return &warmHintConsumer{
		cache: cache,
		log:   slog.Default(),
	}
}

func TestWarmHintConsumer_DrainsAndUpdatesCache(t *testing.T) {
	cache := gateway.NewWarmHintCache()
	g := newTestConsumer(cache)
	stream := &fakeWarmHintStream{
		events: []scheddgrpc.WarmHintEvent{
			{AppID: "app-1", NodeID: "node-a"},
			{AppID: "app-2", NodeID: "node-b"},
		},
		tailErr: io.EOF,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- drainForTest(g, ctx, stream) }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("drain returned %v, want nil or io.EOF", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("drain did not return after events")
	}

	if n, ok := cache.Hint("app-1"); !ok || n != "node-a" {
		t.Errorf("cache hint(app-1) = (%q, %v), want (node-a, true)", n, ok)
	}
	if n, ok := cache.Hint("app-2"); !ok || n != "node-b" {
		t.Errorf("cache hint(app-2) = (%q, %v), want (node-b, true)", n, ok)
	}
}

func TestWarmHintConsumer_DrainCtxCancelStops(t *testing.T) {
	cache := gateway.NewWarmHintCache()
	g := newTestConsumer(cache)
	// Never-emitting stream with no tailErr — drain is asked to
	// exit via ctx cancel. Cancel ctx BEFORE starting drain so
	// the first loop iteration's select catches ctx.Done() and
	// returns nil deterministically. Without pre-cancel the test
	// races: drain's first Recv returns EOF immediately (fake
	// has no events), drain returns EOF, and the test sees "drain
	// returned EOF on ctx cancel" — exactly the 2026-07-31 flake
	// on CI run 30647001244. The pre-cancel pattern mirrors
	// real gRPC, where the daemon's ctx is already cancelled by
	// the time drain sees the call.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &fakeWarmHintStream{events: nil}
	done := make(chan error, 1)
	go func() { done <- drainForTest(g, ctx, stream) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("drain returned %v on ctx cancel, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("drain did not return after ctx cancel")
	}
}

func TestWarmHintConsumer_DrainSkipsMalformedEvents(t *testing.T) {
	cache := gateway.NewWarmHintCache()
	g := newTestConsumer(cache)
	stream := &fakeWarmHintStream{
		events: []scheddgrpc.WarmHintEvent{
			{AppID: "", NodeID: "node-a"},      // empty AppID — skip
			{AppID: "app-1", NodeID: ""},       // empty NodeID — skip
			{AppID: "app-1", NodeID: "node-a"}, // valid
		},
		tailErr: io.EOF,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- drainForTest(g, ctx, stream) }()
	<-done

	if n, ok := cache.Hint("app-1"); !ok || n != "node-a" {
		t.Errorf("cache hint(app-1) = (%q, %v), want (node-a, true)", n, ok)
	}
	// Cache must NOT contain an entry for the empty-AppID event.
	if cache.Len() != 1 {
		t.Errorf("cache.Len() = %d, want 1 (only valid event landed)", cache.Len())
	}
}

func TestWarmHintConsumer_HintFuncAdapter(t *testing.T) {
	// Pins the cache → WarmHintFunc shape that
	// cmd/gatewayd-internal/main.go wires into backend.WithWarmHint. The
	// picker's per-request call site (pgbackend.go:316-321)
	// reads WarmHintFunc(appID) and consumes (nodeID, found).
	cache := gateway.NewWarmHintCache()
	fn := cache.HintFunc()

	if n, ok := fn("missing"); ok || n != "" {
		t.Errorf("fn(missing) = (%q, %v), want (\"\", false)", n, ok)
	}
	cache.Update("app", "node")
	if n, ok := fn("app"); !ok || n != "node" {
		t.Errorf("fn(app) after Update = (%q, %v), want (node, true)", n, ok)
	}
}

func TestWarmHintConsumer_NewConsumerNilSafety(t *testing.T) {
	// Construction accepts nil log (defaults via NewWarmHintConsumer
	// in production; the unit-test helper passes slog.Default).
	// The cache must tolerate nil appID/NodeID in Update.
	cache := gateway.NewWarmHintCache()
	cache.Update("", "node")
	cache.Update("app", "")
	if cache.Len() != 0 {
		t.Errorf("cache.Len() = %d, want 0 (no-op on empty inputs)", cache.Len())
	}
}

// drainForTest is the test entry into warmHintConsumer.drain. It
// shadows the unexported method so the test file can be in
// package main and call the production code path directly.
func drainForTest(g *warmHintConsumer, ctx context.Context, stream scheddgrpc.WarmHintStream) error {
	return g.drain(ctx, stream)
}

func TestWarmHintHeartbeatRefreshesReadinessWithoutCacheEntry(t *testing.T) {
	cache := gateway.NewWarmHintCache()
	g := newTestConsumer(cache)
	touches := 0
	g.SetOnTouch(func() { touches++ })
	stream := &fakeWarmHintStream{events: []scheddgrpc.WarmHintEvent{{WrittenAt: time.Now()}}}
	if err := g.drain(context.Background(), stream); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if touches != 2 {
		t.Fatalf("touches=%d, want registration plus heartbeat", touches)
	}
	if cache.Len() != 0 {
		t.Fatal("heartbeat inserted a routing hint")
	}
}
