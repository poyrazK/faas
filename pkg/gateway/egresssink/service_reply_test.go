package egresssink

import (
	"testing"
	"time"
)

// Billing review R8-2 slice 3: service reply bytes drain in their own field,
// never add to the response bytes, and survive a failed send.
func TestRecordServiceReplyBytesDrainsSeparately(t *testing.T) {
	start := time.Date(2026, 11, 15, 10, 0, 5, 0, time.UTC)
	clock, _ := newClock(start)
	sink := NewEgressSinkWithClock(clock)
	sink.RecordServiceReplyBytes("i-1", 700)
	sink.RecordServiceReplyBytes("i-1", 300)
	sink.RecordServiceReplyBytes("i-1", 0)
	sink.RecordServiceReplyBytes("", 50)

	drained := sink.DrainRecords()
	if len(drained) != 1 || drained[0].ServiceReplyBytes != 1000 || drained[0].Bytes != 0 || drained[0].Requests != 0 {
		t.Fatalf("drained = %+v, want one record of 1000 service reply bytes", drained)
	}
	sink.RestoreRecords(drained)
	again := sink.DrainRecords()
	if len(again) != 1 || again[0].ServiceReplyBytes != 1000 {
		t.Fatalf("after restore = %+v, want the 1000 bytes requeued", again)
	}
	if left := sink.DrainRecords(); len(left) != 0 {
		t.Fatalf("second drain = %+v, want empty", left)
	}
}
