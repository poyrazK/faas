package profiling

import (
	"fmt"
	"testing"
	"time"

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

// Lifetimes sharing a slot keep distinct capture times, below a millisecond
// apart, and encoding leaves the caller's profile unshifted.
func TestCollectorTimeOffsetSeparatesSlotSharers(t *testing.T) {
	offsets := map[int64]bool{}
	for i := range 10_000 {
		offset := collectorTimeOffset(fmt.Sprintf("instance-%d:generation-%d", i, i))
		if offset < 0 || offset >= int64(time.Millisecond) {
			t.Fatalf("offset %d outside a millisecond", offset)
		}
		offsets[offset] = true
	}
	if len(offsets) < 9_900 {
		t.Fatalf("10,000 lifetimes got %d distinct offsets", len(offsets))
	}
	at := time.Unix(1_800_000_000, 0)
	p := cpuFixture(at, int64(time.Millisecond))
	if _, err := encodeSeries(Principal{AppID: "app"}, sampleIdentity{ID: "id", Collector: "lifetime"}, "process_cpu", p); err != nil {
		t.Fatal(err)
	}
	if p.TimeNanos != at.UnixNano() {
		t.Fatal("encoding shifted the caller's profile")
	}
}
