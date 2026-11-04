package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A fixed 100 ms poll cost about 20 CP queries a second per open execution
// stream, idle or not (bug hunt 2026-10-28 O16/O21). Idle polls now back
// off to 2 s and snap back when events arrive.
func TestExecutionEventsPollBacksOffWhileIdle(t *testing.T) {
	d := 100 * time.Millisecond
	var seen []time.Duration
	for range 6 {
		d = apidserver.NextExecutionEventsPoll(d, false)
		seen = append(seen, d)
	}
	if seen[0] != 200*time.Millisecond || seen[len(seen)-1] != 2*time.Second {
		t.Fatalf("idle backoff = %v, want 200ms doubling to a 2s cap", seen)
	}
	if got := apidserver.NextExecutionEventsPoll(2*time.Second, true); got != 100*time.Millisecond {
		t.Fatalf("after events the poll is %v, want 100ms", got)
	}
}
