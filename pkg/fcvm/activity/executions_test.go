package activity

import (
	"testing"
	"time"
)

func TestExecutionOccupancyOutlivesHTTPWithoutCountingRequestsTwice(t *testing.T) {
	now := time.Unix(1, 0)
	a := New(func() time.Time { return now })
	a.Begin("vm")
	a.BeginExecution("vm")
	if n, _ := a.Inflight("vm"); n != 1 {
		t.Fatal("foreground overlap counted twice")
	}
	if n, _ := a.Total("vm"); n != 1 {
		t.Fatal("execution inflated request throughput")
	}
	a.End("vm")
	if n, _, _ := a.Quiet("vm"); n != 1 {
		t.Fatal("HTTP EOF released execution")
	}
	now = now.Add(time.Second)
	a.EndExecution("vm")
	a.EndExecution("vm")
	if n, since, _ := a.Quiet("vm"); n != 0 || !since.Equal(now) {
		t.Fatal("idle clock did not start at execution completion")
	}
}
