package profiling

import (
	"bytes"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Every scale-to-zero wake is a new collector: series stay bounded per
// deployment while each collector keeps a capture time of its own.
func TestCollectorSeriesAreBounded(t *testing.T) {
	slots := map[string]bool{}
	offsets := map[int64]bool{}
	for range 10_000 {
		slot, offset := collectorSeries(uuid.NewString())
		n, err := strconv.Atoi(slot)
		if err != nil || n < 0 || n >= api.ProfileCollectorSlots {
			t.Fatalf("slot %q outside [0,%d)", slot, api.ProfileCollectorSlots)
		}
		if offset < 0 || offset >= int64(time.Millisecond) {
			t.Fatalf("offset %d outside a millisecond", offset)
		}
		slots[slot], offsets[offset] = true, true
	}
	if len(slots) != api.ProfileCollectorSlots || len(offsets) < 9_900 {
		t.Fatalf("10,000 collectors used %d slots and %d offsets", len(slots), len(offsets))
	}
	p := cpuFixture(time.Unix(1_800_000_000, 0), int64(time.Millisecond))
	identity := sampleIdentity{ID: uuid.NewString(), Collector: uuid.NewString()}
	series, err := encodeSeries(Principal{AppID: "app"}, identity, "process_cpu", p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(series, []byte("collector_slot")) || bytes.Contains(series, []byte(identity.Collector)) {
		t.Fatal("series labels carry the per-lifetime collector instead of its slot")
	}
	if p.TimeNanos != time.Unix(1_800_000_000, 0).UnixNano() {
		t.Fatal("encoding shifted the caller's profile")
	}
}
