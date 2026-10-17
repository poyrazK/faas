// adr: 1132
package gateway

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type snapshotClock struct{ t time.Time }

func (c *snapshotClock) now() time.Time { return c.t }

// trainedLearner observes enough cold caller wakes, each followed by a call
// that had to restore the target, for Predict to return the target.
func trainedLearner(t *testing.T, clock *snapshotClock, caller string, targets ...string) *WakeAheadLearner {
	t.Helper()
	l := NewWakeAheadLearner(clock.now, nil)
	for i := 0; i < api.ServiceWakeAheadMinWakes+5; i++ {
		l.ObserveWake(caller)
		for _, target := range targets {
			l.ObserveCall(caller, target, true)
		}
		clock.t = clock.t.Add(api.ServiceWakeAheadFollowWindow + time.Second)
	}
	l.ObserveWake(caller) // folds the last window
	if got := l.Predict(caller); len(got) == 0 {
		t.Fatalf("trained learner predicts nothing for %s", caller)
	}
	return l
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The core ADR-1132 contract: evidence saved by one gateway process predicts
// the same targets in the next one, without relearning.
func TestWakeAheadSnapshotRoundTripPreservesPredictions(t *testing.T) {
	clock := &snapshotClock{t: time.Unix(1_000_000, 0)}
	l := trainedLearner(t, clock, "api", "auth", "billing")
	want := l.Predict("api")

	snap, changed := l.Snapshot()
	if !changed {
		t.Fatal("Snapshot reported no change after training")
	}
	if _, again := l.Snapshot(); again {
		t.Fatal("a second Snapshot with no new evidence reported a change")
	}
	path := filepath.Join(t.TempDir(), WakeAheadSnapshotFile)
	if err := SaveWakeAheadSnapshot(path, snap); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", perm)
	}
	loaded, ok, err := LoadWakeAheadSnapshot(path)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	fresh := NewWakeAheadLearner(clock.now, nil)
	if kept := fresh.Restore(loaded); kept != 1 {
		t.Fatalf("restored callers = %d, want 1", kept)
	}
	if got := fresh.Predict("api"); !slices.Equal(got, want) {
		t.Fatalf("restored prediction = %v, want %v", got, want)
	}
}

// A snapshot is input from disk: invalid counts, self-edges and anything
// past the ADR-977 bounds are dropped, and live evidence is never replaced.
func TestWakeAheadRestoreValidatesAndBounds(t *testing.T) {
	clock := &snapshotClock{t: time.Unix(1_000_000, 0)}
	edges := map[string]WakeAheadEdgeSnapshot{
		"nan":      {Calls: math.NaN(), Cold: 1},
		"negative": {Calls: -1, Cold: 0},
		"cold>all": {Calls: 2, Cold: 3},
		"api":      {Calls: 20, Cold: 20}, // self-edge
	}
	for i := 0; i < api.ServiceWakeAheadMaxEdgesPerApp+4; i++ {
		edges[string(rune('a'+i))+"-target"] = WakeAheadEdgeSnapshot{Calls: float64(30 + i), Cold: float64(30 + i)}
	}
	snap := WakeAheadSnapshot{Version: WakeAheadSnapshotVersion, SavedAt: clock.t, Callers: map[string]WakeAheadCallerSnapshot{
		"api":  {LastWake: clock.t, Wakes: 40, Edges: edges},
		"bad":  {LastWake: clock.t, Wakes: math.Inf(1), Edges: map[string]WakeAheadEdgeSnapshot{"x": {Calls: 1}}},
		"live": {LastWake: clock.t, Wakes: 40, Edges: map[string]WakeAheadEdgeSnapshot{"stale": {Calls: 40, Cold: 40}}},
	}}
	l := NewWakeAheadLearner(clock.now, nil)
	l.ObserveWake("live") // live evidence for "live" must win
	if kept := l.Restore(snap); kept != 1 {
		t.Fatalf("restored callers = %d, want only api", kept)
	}
	l.mu.Lock()
	api1 := l.callers["api"]
	if api1 == nil || len(api1.edges) != api.ServiceWakeAheadMaxEdgesPerApp {
		l.mu.Unlock()
		t.Fatalf("api edges = %v, want %d", api1, api.ServiceWakeAheadMaxEdgesPerApp)
	}
	for target := range api1.edges {
		if target == "api" || target == "nan" || target == "negative" || target == "cold>all" {
			l.mu.Unlock()
			t.Fatalf("invalid edge %q restored", target)
		}
	}
	// The weakest valid edges are the ones dropped by the cap.
	if _, ok := api1.edges["a-target"]; ok {
		l.mu.Unlock()
		t.Fatal("weakest edge survived the per-caller cap")
	}
	if _, ok := l.callers["live"].edges["stale"]; ok {
		l.mu.Unlock()
		t.Fatal("snapshot overwrote live evidence")
	}
	l.mu.Unlock()
	if kept := NewWakeAheadLearner(clock.now, nil).Restore(WakeAheadSnapshot{Version: 99, Callers: snap.Callers}); kept != 0 {
		t.Fatalf("foreign version restored %d callers", kept)
	}
}

func TestLoadWakeAheadSnapshotRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := LoadWakeAheadSnapshot(filepath.Join(dir, "missing.json")); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	for name, body := range map[string]string{
		"garbage.json": "{not json",
		"version.json": `{"version":99,"callers":{}}`,
		"huge.json":    `{"version":1,"pad":"` + strings.Repeat("x", api.ServiceWakeAheadSnapshotMaxBytes) + `"}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := LoadWakeAheadSnapshot(path); ok || err == nil {
			t.Fatalf("%s: ok=%v err=%v, want an error", name, ok, err)
		}
	}
}

// PersistWakeAhead restores at start, skips unchanged intervals, and writes
// the final evidence when the gateway shuts down.
func TestPersistWakeAheadRestoresAndWritesOnShutdown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wake-ahead")
	path := filepath.Join(dir, WakeAheadSnapshotFile)
	clock := &snapshotClock{t: time.Unix(1_000_000, 0)}

	// First process: learn, then shut down.
	first := trainedLearner(t, clock, "api", "auth")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); PersistWakeAhead(ctx, first, dir, time.Hour, clock.now, quietLog()) }()
	cancel()
	<-done
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no snapshot written on shutdown: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("state dir holds %d entries, want only the snapshot (no temp files)", len(entries))
	}

	// Second process: starts with the evidence already in place.
	second := NewWakeAheadLearner(clock.now, nil)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { defer close(done2); PersistWakeAhead(ctx2, second, dir, time.Hour, clock.now, quietLog()) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(second.Predict("api")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("restarted learner never restored the saved evidence")
		}
		time.Sleep(5 * time.Millisecond)
	}
	before, _ := os.Stat(path)
	cancel2()
	<-done2
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("unchanged evidence was rewritten on shutdown")
	}
}

func TestPersistWakeAheadIgnoresStaleSnapshot(t *testing.T) {
	dir := t.TempDir()
	clock := &snapshotClock{t: time.Unix(1_000_000, 0)}
	trained := trainedLearner(t, clock, "api", "auth")
	snap, _ := trained.Snapshot()
	if err := SaveWakeAheadSnapshot(filepath.Join(dir, WakeAheadSnapshotFile), snap); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(api.ServiceWakeAheadSnapshotMaxAge + time.Hour)
	l := NewWakeAheadLearner(clock.now, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // restore runs before the loop observes cancellation
	PersistWakeAhead(ctx, l, dir, time.Hour, clock.now, quietLog())
	if got := l.Predict("api"); len(got) != 0 {
		t.Fatalf("stale snapshot restored predictions %v", got)
	}
}
