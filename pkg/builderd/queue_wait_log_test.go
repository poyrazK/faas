package builderd

import (
	"testing"
	"time"
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
