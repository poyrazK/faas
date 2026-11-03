package sched_test

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// adr: 1322 (amendment of 2026-11-03) — an idle realtime trigger claims every
// 2 s, then every 5 s, instead of on every 1 s tick; a claim with messages
// polls at once again.
func TestRealtimeTriggerIdleBackoff(t *testing.T) {
	got := sched.RealtimeIdleDelays([]int{0, 0, 0, 3, 0})
	want := []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second, 0, 2 * time.Second}
	if !slices.Equal(got, want) {
		t.Fatalf("idle delays = %v, want %v", got, want)
	}
}
