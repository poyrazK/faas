package builderd

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuilderSlotLogLineNamesTheQueueWait(t *testing.T) {
	for _, tc := range []struct {
		wait time.Duration
		want string
	}{
		{0, "allocated builder slot (guaranteed)\n"},
		{4 * time.Second, "allocated builder slot (guaranteed)\n"},
		{5 * time.Second, "allocated builder slot (guaranteed) after 5s in the build queue\n"},
		{3*time.Minute + 412*time.Millisecond, "allocated builder slot (guaranteed) after 3m0s in the build queue\n"},
	} {
		if got := builderSlotLogLine("guaranteed", tc.wait); got != tc.want {
			t.Errorf("builderSlotLogLine(%s) = %q, want %q", tc.wait, got, tc.want)
		}
	}
}

// TestBuilderQueueWaitReasonNamesTheSlotWait: the deploy stages booked a
// builder-slot wait to "Source downloaded" (Hunt 12 H12-5).
func TestBuilderQueueWaitReasonNamesTheSlotWait(t *testing.T) {
	enqueued := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		started time.Time
		want    string
	}{
		{time.Time{}, ""},
		{enqueued.Add(2 * time.Second), ""},
		{enqueued.Add(192*time.Second + 300*time.Millisecond), "waited 3m12s for a builder slot"},
	} {
		if got := builderQueueWaitReason(state.Build{EnqueuedAt: enqueued, StartedAt: tc.started}); got != tc.want {
			t.Errorf("started %s: %q, want %q", tc.started, got, tc.want)
		}
	}
}
