package sched_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// O20: a follow stream's discovery poll backs off while it finds nothing
// new and returns to the minimum only when a new instance attaches.
func TestNextLogFollowPollBacksOffUntilAnAttach(t *testing.T) {
	interval := sched.LogFollowPollMin
	var seen []time.Duration
	for range 6 {
		interval = sched.NextLogFollowPoll(interval, false)
		seen = append(seen, interval)
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("idle polls = %v, want %v", seen, want)
		}
	}
	if got := sched.NextLogFollowPoll(sched.LogFollowPollMax, true); got != sched.LogFollowPollMin {
		t.Fatalf("after an attach = %v, want %v", got, sched.LogFollowPollMin)
	}
}
