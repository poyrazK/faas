// adr: 570
package reqbudget

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCancellationFenceSurvivesNestedBudgetDetachment(t *testing.T) {
	client, disconnect := context.WithCancel(t.Context())
	defer disconnect()
	initial, stopInitial, _ := WithRemaining(client, 60*time.Millisecond, time.Second, "public", "test")
	defer stopInitial()
	fenced, revoke := WithCancellationFence(initial)
	defer revoke(nil)
	nested, stopNested, _ := WithRemaining(fenced, time.Second, time.Second, "forward", "test")
	defer stopNested()
	stream, detach, stopStream := WithStream(nested)
	defer stopStream()
	detach()
	<-initial.Done()
	// A parent's Done can close before cancellation reaches its children.
	// Observe the fence itself before asserting its deadline state.
	select {
	case <-fenced.Done():
	case <-time.After(time.Second):
		t.Fatal("ordinary request fence did not receive its budget deadline")
	}
	if !errors.Is(fenced.Err(), context.DeadlineExceeded) {
		t.Fatalf("ordinary request fence lost its budget deadline: %v", fenced.Err())
	}
	if stream.Err() != nil || fenced.Err() == nil {
		t.Fatalf("budget/fence detachment changed: stream=%v ordinary=%v", stream.Err(), fenced.Err())
	}
	cause := errors.New("security generation revoked")
	revoke(cause)
	select {
	case <-stream.Done():
		if !errors.Is(CancellationFenceCause(stream), cause) {
			t.Fatalf("stream lost lifetime cancellation cause: %v", CancellationFenceCause(stream))
		}
	case <-time.After(time.Second):
		t.Fatal("detached stream escaped its revocation fence")
	}
}

func TestCancellationFenceRetainsClientCancellationAndOrdinaryDeadline(t *testing.T) {
	for _, detach := range []bool{false, true} {
		client, disconnect := context.WithCancel(t.Context())
		fenced, revoke := WithCancellationFence(client)
		ctx, cancel, _ := WithRemaining(fenced, time.Second, time.Second, "forward", "test")
		stream, detachBudget, stop := WithStream(ctx)
		if detach {
			detachBudget()
		}
		disconnect()
		select {
		case <-stream.Done():
			if !errors.Is(stream.Err(), context.Canceled) {
				t.Fatal(stream.Err())
			}
		case <-time.After(time.Second):
			t.Fatal("fence hid client cancellation")
		}
		stop()
		cancel()
		revoke(nil)
	}
	ctx, cancel, _ := WithRemaining(t.Context(), 20*time.Millisecond, time.Second, "forward", "test")
	defer cancel()
	fenced, revoke := WithCancellationFence(ctx)
	defer revoke(nil)
	<-fenced.Done()
	if !errors.Is(fenced.Err(), context.DeadlineExceeded) {
		t.Fatal("ordinary request escaped its existing deadline")
	}
}
