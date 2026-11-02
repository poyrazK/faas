package sched_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
)

// TestCoalesceInvocationDueDropsQueuedNotifications pins the burst collapse:
// every enqueued invocation sends invocation_due, and one walk lists them all,
// so the drain drops what is already queued instead of walking once per row.
func TestCoalesceInvocationDueDropsQueuedNotifications(t *testing.T) {
	notif := make(chan db.Notification, 8)
	for range 5 {
		notif <- db.Notification{Channel: db.NotifyInvocationDue}
	}
	sched.CoalesceInvocationDue(notif)
	if len(notif) != 0 {
		t.Fatalf("%d notifications left queued, want none", len(notif))
	}

	closed := make(chan db.Notification, 1)
	closed <- db.Notification{Channel: db.NotifyInvocationDue}
	close(closed)
	sched.CoalesceInvocationDue(closed) // returns on a closed channel
}

// TestDrainLeaseRecoveryCadence pins the spacing of the drain's lease
// recovery: the first safety tick recovers, later ticks wait out the interval.
func TestDrainLeaseRecoveryCadence(t *testing.T) {
	start := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	got := sched.DrainLeaseRecoveryDue(5*time.Second,
		start,
		start.Add(time.Second),
		start.Add(4*time.Second),
		start.Add(5*time.Second),
		start.Add(6*time.Second),
		start.Add(10*time.Second),
	)
	want := []bool{true, false, false, true, false, true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery due = %v, want %v", got, want)
	}
	if sched.DefaultDrainLeaseRecoveryInterval > 30*time.Second {
		t.Fatalf("DefaultDrainLeaseRecoveryInterval = %s; dispatch leases carry a 30 s margin", sched.DefaultDrainLeaseRecoveryInterval)
	}
}
