package reqbudget

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ADR-093: before response headers, every tighter hop deadline and explicit
// cancellation must still apply, even when its Budget value is inherited.
func TestWithStreamHonorsImmediateParent(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "explicit cancellation"
		if deadline {
			name = "earlier hop deadline"
		}
		t.Run(name, func(t *testing.T) {
			budget, cancelBudget, _ := WithRemaining(t.Context(), time.Hour, time.Hour, "forward", "")
			defer cancelBudget()
			var parent context.Context
			var cancelParent context.CancelFunc
			wantErr := context.Canceled
			if deadline {
				parent, cancelParent = context.WithDeadline(budget, time.Now().Add(-time.Second))
				wantErr = context.DeadlineExceeded
			} else {
				parent, cancelParent = context.WithCancel(budget)
				cancelParent()
			}
			defer cancelParent()
			stream, _, cancel := WithStream(parent)
			defer cancel()
			if deadline {
				want, _ := parent.Deadline()
				if got, ok := stream.Deadline(); !ok || got.After(want) {
					t.Errorf("stream deadline %v exceeds parent deadline %v", got, want)
				}
			}
			select {
			case <-stream.Done():
				if !errors.Is(stream.Err(), wantErr) {
					t.Fatalf("stream error = %v, want %v", stream.Err(), wantErr)
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("stream ignored its already-canceled immediate parent")
			}
		})
	}
}

func TestWithStreamExpiredValueCancelsWithoutParentTimer(t *testing.T) {
	parent := NewContext(t.Context(), Budget{Started: time.Now().Add(-time.Minute), Total: time.Second})
	stream, detach, cancel := WithStream(parent)
	defer cancel()
	detach() // An expired admission budget cannot be rescued by starting a stream.
	select {
	case <-stream.Done():
		if !errors.Is(stream.Err(), context.DeadlineExceeded) {
			t.Fatalf("stream error = %v", stream.Err())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expired budget value produced an uncanceled stream")
	}
}

type streamCancellationProbe struct {
	context.Context
	observed chan struct{}
}

func (c streamCancellationProbe) Done() <-chan struct{} {
	c.observed <- struct{}{}
	return c.Context.Done()
}

// Reproduce the valid interleaving where the timer wins the select just as
// detach sets its flag, before detach closes its wake channel. The watcher
// must stay alive to forward later client cancellation.
func TestStreamTimerDetachRaceKeepsClientCancellation(t *testing.T) {
	base, cancelBase := context.WithCancel(t.Context())
	defer cancelBase()
	probe := streamCancellationProbe{Context: base, observed: make(chan struct{}, 4)}
	s := &streamContext{
		current: context.Background(), base: probe, done: make(chan struct{}),
		detached: make(chan struct{}), timer: time.NewTimer(0), isDetached: true,
	}
	defer s.cancel()
	exited := make(chan struct{})
	go func() { s.watch(); close(exited) }()
	for i := 0; i < 2; i++ {
		select {
		case <-probe.observed:
		case <-exited:
			t.Fatal("watcher exited after the detached timer fired, abandoning client cancellation")
		case <-time.After(time.Second):
			t.Fatal("watcher did not resume observing client cancellation")
		}
	}
	cancelBase()
	select {
	case <-s.Done():
		if !errors.Is(s.Err(), context.Canceled) {
			t.Fatalf("stream error = %v", s.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not close the detached stream")
	}
}

func TestWithStreamDetachRestoresBaseDeadline(t *testing.T) {
	base, cancelBase := context.WithTimeout(t.Context(), time.Hour)
	defer cancelBase()
	budget, cancelBudget, _ := WithRemaining(base, time.Minute, time.Minute, "forward", "")
	defer cancelBudget()
	hop, cancelHop := context.WithTimeout(budget, time.Second)
	defer cancelHop()
	stream, detach, cancel := WithStream(hop)
	defer cancel()
	wantHop, _ := hop.Deadline()
	if got, ok := stream.Deadline(); !ok || !got.Equal(wantHop) {
		t.Errorf("pre-detach deadline = %v, want hop deadline %v", got, wantHop)
	}
	detach()
	wantBase, _ := base.Deadline()
	if got, ok := stream.Deadline(); !ok || !got.Equal(wantBase) {
		t.Fatalf("post-detach deadline = %v, want base deadline %v", got, wantBase)
	}
	cancelHop()
	select {
	case <-stream.Done():
		t.Fatal("a detached stream still observes the admission-hop cancellation")
	case <-time.After(20 * time.Millisecond):
	}
	cancelBase()
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("detached stream ignored the base cancellation")
	}
}

func TestWithStreamCannotDetachCanceledAdmission(t *testing.T) {
	budget, cancelBudget, _ := WithRemaining(t.Context(), time.Hour, time.Hour, "forward", "")
	defer cancelBudget()
	parent, cancelParent := context.WithCancel(budget)
	defer cancelParent()
	stream, detach, cancel := WithStream(parent)
	defer cancel()
	cancelParent()
	detach()
	select {
	case <-stream.Done():
		if !errors.Is(stream.Err(), context.Canceled) {
			t.Fatalf("stream error = %v", stream.Err())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("detach revived an already-canceled admission context")
	}
}
