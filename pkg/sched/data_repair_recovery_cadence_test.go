package sched_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// TestDataRepairRecoveryCadence pins RecoverOnce's spacing: the first tick
// recovers, an idle coordinator then waits 10 s, and a pending claim keeps
// recovery on every tick.
func TestDataRepairRecoveryCadence(t *testing.T) {
	start := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	ticks := []time.Time{start, start.Add(time.Second), start.Add(9 * time.Second), start.Add(10 * time.Second), start.Add(11 * time.Second)}
	if got, want := sched.DataRepairRecoveryDue(false, ticks...), []bool{true, false, false, true, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("idle recovery due = %v, want %v", got, want)
	}
	if got, want := sched.DataRepairRecoveryDue(true, ticks...), []bool{true, true, true, true, true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending-claim recovery due = %v, want %v", got, want)
	}
}
