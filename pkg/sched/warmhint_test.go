// warmhint_test.go — broadcaster tests (ADR-025 axis 4).
//
// Covers the four documented properties of the warmHintBroadcaster
// (pkg/sched/warmhint.go):
//
//   1. Fan-out: every subscriber sees every emit.
//   2. Drop on full: a slow subscriber loses events but the
//      producer doesn't block; the dropped counter increments.
//   3. Unsubscribe: an unsubscribed subscriber stops receiving.
//   4. Concurrent emit: the broadcaster is race-free under
//      parallel emit/subscribe/unsubscribe (run with -race).
//
// Plus the property test: emit-on-change filtering at the engine
// level (RecordWakeIfChanged). The broadcaster itself is a
// unconditional fan-out; the change filter lives on the engine
// boundary.

package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"sync"
	"testing"
	"time"
)

func TestBroadcaster_FansOutToSubscribers(t *testing.T) {
	t.Parallel()
	b := sched.NewWarmHintBroadcaster()

	ch1, unsub1 := b.Subscribe(8)
	defer unsub1()
	ch2, unsub2 := b.Subscribe(8)
	defer unsub2()

	b.Emit(sched.WarmHintEvent{AppID: "app-1", NodeID: "node-a"})
	b.Emit(sched.WarmHintEvent{AppID: "app-2", NodeID: "node-b"})

	for _, ch := range []<-chan sched.WarmHintEvent{ch1, ch2} {
		got := drainN(t, ch, 2)
		if got[0].AppID != "app-1" || got[0].NodeID != "node-a" {
			t.Errorf("event[0] = %+v, want {app-1, node-a}", got[0])
		}
		if got[1].AppID != "app-2" || got[1].NodeID != "node-b" {
			t.Errorf("event[1] = %+v, want {app-2, node-b}", got[1])
		}
	}
}

func TestBroadcaster_DropsOnFullChannel(t *testing.T) {
	t.Parallel()
	b := sched.NewWarmHintBroadcaster()

	// bufCap=1 → second emit drops.
	ch, unsub := b.Subscribe(1)
	defer unsub()

	// Fill the buffer with the first emit.
	b.Emit(sched.WarmHintEvent{AppID: "app-1", NodeID: "node-a"})

	// Two more emits with the buffer still full → both drop.
	b.Emit(sched.WarmHintEvent{AppID: "app-2", NodeID: "node-b"})
	b.Emit(sched.WarmHintEvent{AppID: "app-3", NodeID: "node-c"})

	if got := b.DroppedCount(); got != 2 {
		t.Errorf("droppedCount = %d, want 2", got)
	}

	// The first emit is still in the buffer.
	got := <-ch
	if got.AppID != "app-1" {
		t.Errorf("first event = %+v, want {app-1, ...}", got)
	}
}

func TestBroadcaster_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	b := sched.NewWarmHintBroadcaster()

	ch, unsub := b.Subscribe(8)
	b.Emit(sched.WarmHintEvent{AppID: "app-1", NodeID: "node-a"})
	if got := drainN(t, ch, 1); got[0].AppID != "app-1" {
		t.Fatalf("pre-unsubscribe event = %+v, want {app-1, ...}", got[0])
	}

	// Unsubscribe.
	unsub()

	// Emit after unsubscribe — channel is closed; Recv returns zero+ok=false.
	b.Emit(sched.WarmHintEvent{AppID: "app-2", NodeID: "node-b"})

	// Channel is closed, so the next read returns the zero value
	// and ok=false. We don't read it explicitly — that would
	// block on a closed channel forever? No, a closed channel
	// always returns immediately with the zero value. Verify
	// subscriberCount is 0 and dropped is 0 (the post-unsubscribe
	// emit found an empty subs map).
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("subscriberCount after unsub = %d, want 0", got)
	}
	if got := b.DroppedCount(); got != 0 {
		t.Errorf("droppedCount after unsub = %d, want 0", got)
	}
}

func TestBroadcaster_NilSafety(t *testing.T) {
	t.Parallel()
	var b *sched.WarmHintBroadcaster
	// All public methods must tolerate a nil receiver.
	b.Emit(sched.WarmHintEvent{AppID: "x", NodeID: "y"})
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("nil subscriberCount = %d, want 0", got)
	}
	if got := b.DroppedCount(); got != 0 {
		t.Errorf("nil droppedCount = %d, want 0", got)
	}
}

func TestBroadcaster_ConcurrentEmit(t *testing.T) {
	t.Parallel()
	b := sched.NewWarmHintBroadcaster()
	const nSubs = 4
	const nEmits = 100
	const bufCap = 256 // generous buffer so emit doesn't drop under -race

	subs := make([]<-chan sched.WarmHintEvent, nSubs)
	for i := 0; i < nSubs; i++ {
		ch, unsub := b.Subscribe(bufCap)
		subs[i] = ch
		defer unsub()
	}

	var wg sync.WaitGroup
	wg.Add(nSubs)
	for i := 0; i < nSubs; i++ {
		go func(sub <-chan sched.WarmHintEvent) {
			defer wg.Done()
			seen := 0
			for seen < nEmits {
				select {
				case <-sub:
					seen++
				case <-time.After(5 * time.Second):
					t.Errorf("timeout waiting for event %d", seen)
					return
				}
			}
		}(subs[i])
	}

	for i := 0; i < nEmits; i++ {
		b.Emit(sched.WarmHintEvent{AppID: "app", NodeID: "node"})
	}
	wg.Wait()
}

// TestBroadcaster_EmitPreservesZeroWrittenAt pins the pass-through
// contract: the broadcaster does NOT stamp WrittenAt — the caller
// (Engine.admitAndDispatch) owns that timestamp. A zero WrittenAt
// arriving at the broadcaster flows out to subscribers as zero,
// exactly as it came in. This matches the wider invariant: the
// broadcaster is a pure fan-out with no opinion on the payload.
func TestBroadcaster_EmitPreservesZeroWrittenAt(t *testing.T) {
	t.Parallel()
	b := sched.NewWarmHintBroadcaster()
	ch, unsub := b.Subscribe(2)
	defer unsub()

	b.Emit(sched.WarmHintEvent{AppID: "app", NodeID: "node"})

	got := drainN(t, ch, 1)[0]
	if !got.WrittenAt.IsZero() {
		t.Errorf("WrittenAt = %v, want zero (broadcaster must NOT stamp on pass-through)", got.WrittenAt)
	}
}

// TestBroadcaster_EmitPreservesExplicitWrittenAt pins the
// pass-through contract for non-zero timestamps. The broadcaster
// does not mutate the payload it fans out; whatever the caller
// passes is what subscribers see.
func TestBroadcaster_EmitPreservesExplicitWrittenAt(t *testing.T) {
	t.Parallel()
	b := sched.NewWarmHintBroadcaster()
	ch, unsub := b.Subscribe(2)
	defer unsub()

	want := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	b.Emit(sched.WarmHintEvent{AppID: "app", NodeID: "node", WrittenAt: want})

	got := drainN(t, ch, 1)[0]
	if !got.WrittenAt.Equal(want) {
		t.Errorf("WrittenAt = %v, want %v (explicit timestamp must be preserved)", got.WrittenAt, want)
	}
}

// drainN reads up to n events from ch, with a 1s safety timeout
// per event. Returns whatever was collected (caller asserts the
// length).
func drainN(t *testing.T, ch <-chan sched.WarmHintEvent, n int) []sched.WarmHintEvent {
	t.Helper()
	out := make([]sched.WarmHintEvent, 0, n)
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-time.After(1 * time.Second):
			t.Fatalf("drainN: timeout at %d/%d", len(out), n)
			return out
		}
	}
	return out
}

// TestRecordWakeIfChanged pins the change-detect contract used by
// the engine's emit gate (engine.go RecordWake site).
func TestRecordWakeIfChanged(t *testing.T) {
	t.Parallel()
	w := sched.NewWarmAffinity(0)

	// (1) Cold entry — changed=true, prev="".
	prev, changed := w.RecordWakeIfChanged("app", "node-a")
	if !changed {
		t.Errorf("cold entry: changed = false, want true")
	}
	if prev != "" {
		t.Errorf("cold entry: prev = %q, want \"\"", prev)
	}

	// (2) Same-node re-wake — changed=false, prev is set.
	prev, changed = w.RecordWakeIfChanged("app", "node-a")
	if changed {
		t.Errorf("same-node re-wake: changed = true, want false")
	}
	if prev != "node-a" {
		t.Errorf("same-node re-wake: prev = %q, want node-a", prev)
	}

	// (3) Different-node re-wake — changed=true, prev is the old node.
	prev, changed = w.RecordWakeIfChanged("app", "node-b")
	if !changed {
		t.Errorf("different-node: changed = false, want true")
	}
	if prev != "node-a" {
		t.Errorf("different-node: prev = %q, want node-a", prev)
	}

	// (4) Empty appID — silent no-op.
	_, changed = w.RecordWakeIfChanged("", "node-c")
	if changed {
		t.Errorf("empty appID: changed = true, want false (no-op)")
	}
	_, changed = w.RecordWakeIfChanged("app", "")
	if changed {
		t.Errorf("empty nodeID: changed = true, want false (no-op)")
	}
}
