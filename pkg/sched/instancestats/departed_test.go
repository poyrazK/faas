// adr: 20261103121407
package instancestats

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestReaderKeepsDepartedNetworkCounters pins ADR-20261103121407: an instance
// that leaves the snapshot keeps its last network counters for
// api.MeterFinalNetworkReadingRetention, so meterd can checkpoint the bytes it
// sent after meterd's last once-a-minute read.
func TestReaderKeepsDepartedNetworkCounters(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r := NewReader()
	r.now = func() time.Time { return now }

	live := InstanceStat{InstanceID: "vm-a", AppID: "app", TXBytes: 9000, TX: Valid, RXBytes: 300, RX: Valid}
	noNet := InstanceStat{InstanceID: "vm-b", AppID: "app", TX: Unknown, RX: Unknown}
	r.Replace([]InstanceStat{live, noNet})
	if got := r.SnapshotDeparted(); len(got) != 0 {
		t.Fatalf("departed before any departure = %+v", got)
	}

	now = now.Add(time.Second)
	r.Replace(nil)
	got := r.SnapshotDeparted()
	if len(got) != 1 || got[0].InstanceID != "vm-a" || got[0].TXBytes != 9000 || got[0].RXBytes != 300 {
		t.Fatalf("departed = %+v, want vm-a with its last counters only", got)
	}

	// Later empty snapshots keep it until the retention ends.
	now = now.Add(api.MeterFinalNetworkReadingRetention - time.Second)
	r.Replace(nil)
	if got := r.SnapshotDeparted(); len(got) != 1 {
		t.Fatalf("departed inside retention = %+v, want vm-a", got)
	}
	now = now.Add(time.Second)
	r.Replace(nil)
	if got := r.SnapshotDeparted(); len(got) != 0 {
		t.Fatalf("departed after retention = %+v, want none", got)
	}
}

// TestReaderDropsDepartedRowWhenInstanceReturns: a row that reappears (a
// stalled node frame, not a teardown) is live again, not departed.
func TestReaderDropsDepartedRowWhenInstanceReturns(t *testing.T) {
	r := NewReader()
	row := InstanceStat{InstanceID: "vm-a", AppID: "app", TXBytes: 10, TX: Valid}
	r.Replace([]InstanceStat{row})
	r.Replace(nil)
	if len(r.SnapshotDeparted()) != 1 {
		t.Fatal("vm-a not departed")
	}
	row.TXBytes = 20
	r.Replace([]InstanceStat{row})
	if got := r.SnapshotDeparted(); len(got) != 0 {
		t.Fatalf("departed after return = %+v, want none", got)
	}
}
