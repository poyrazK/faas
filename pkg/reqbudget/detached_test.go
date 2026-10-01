package reqbudget

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithoutBudgetKeepsDetachedWorkBounded(t *testing.T) {
	type traceKey struct{}
	root, cancelRoot := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "trace"))
	defer cancelRoot()
	budget, cancelBudget, _ := WithRemaining(root, time.Minute, time.Minute, "forward", "GET:/mirror")
	defer cancelBudget()
	bounded, cancel := context.WithTimeout(WithoutBudget(context.WithoutCancel(budget)), 25*time.Millisecond)
	defer cancel()
	stream, detach, cancelStream := WithStream(bounded)
	defer cancelStream()
	detach()
	cancelRoot()
	cancelBudget()
	if _, ok := FromContext(stream); ok {
		t.Fatal("detached work retained the source request budget")
	}
	if stream.Value(traceKey{}) != "trace" {
		t.Fatal("detached work lost trace correlation")
	}
	if stream.Err() != nil {
		t.Fatalf("source cancellation reached detached work: %v", stream.Err())
	}
	select {
	case <-stream.Done():
		if !errors.Is(stream.Err(), context.DeadlineExceeded) {
			t.Fatalf("detached work ended with %v", stream.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("detaching the stream removed the background timeout")
	}
}
