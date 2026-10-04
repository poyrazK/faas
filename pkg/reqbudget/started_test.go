// adr: 531
package reqbudget

import (
	"context"
	"testing"
	"time"
)

func TestWithStartedAccountsElapsedAndNeverExtends(t *testing.T) {
	now := time.Now()
	parent, parentCancel, _ := WithStarted(t.Context(), now.Add(-200*time.Millisecond), time.Second, time.Second, "forward", "test")
	defer parentCancel()
	deadline, _ := parent.Deadline()
	if !deadline.Equal(now.Add(800 * time.Millisecond)) {
		t.Fatalf("elapsed time refunded: %s", deadline.Sub(now))
	}
	child, cancel, b := WithStarted(parent, now, 2*time.Second, 2*time.Second, "service", "test")
	defer cancel()
	if childDeadline, _ := child.Deadline(); !childDeadline.Equal(deadline) || b.Remaining(time.Time{}) > 800*time.Millisecond {
		t.Fatal("child extended the parent's total deadline")
	}
}

func TestWithStartedExpiredAndStreamDetach(t *testing.T) {
	base, baseCancel := context.WithCancel(t.Context())
	defer baseCancel()
	ctx, cancel, _ := WithStarted(base, time.Now().Add(-time.Second), time.Millisecond, time.Second, "forward", "test")
	defer cancel()
	stream, detach, stop := WithStream(ctx)
	defer stop()
	detach()
	if stream.Err() != context.DeadlineExceeded {
		t.Fatal("expired total deadline could be detached")
	}
}
