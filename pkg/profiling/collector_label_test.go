package profiling

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// collector_id takes one of a fixed set of values however many lifetimes
// upload (R4-5), and a lifetime keeps its value.
func TestCollectorLabelIsBounded(t *testing.T) {
	seen := map[string]bool{}
	for i := range 10 * api.ProfileCollectorLabelSlots {
		collector := fmt.Sprintf("instance-%d:generation-%d", i, i)
		label := collectorLabel(collector)
		if label != collectorLabel(collector) {
			t.Fatalf("label for %s is not stable", collector)
		}
		seen[label] = true
	}
	if len(seen) > api.ProfileCollectorLabelSlots {
		t.Fatalf("%d collector labels, want at most %d", len(seen), api.ProfileCollectorLabelSlots)
	}
}
