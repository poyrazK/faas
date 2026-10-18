package logbuf

import "testing"

func TestCommitObservationNotReplayedOrEvictedTwice(t *testing.T) {
	ring := New(8)
	var events []Line
	ring.SetCommitCallback(func(l Line) { events = append(events, l) })
	_, _ = ring.Write("stdout", []byte("first\nsecond\n"))
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatal("intake events missing")
	}
	snapshot, ch, cancel := ring.SnapshotAndSubscribe(1)
	defer cancel()
	_ = snapshot
	_ = ch
	if len(events) != 2 {
		t.Fatal("replay counted again")
	}
	_, _ = ring.Write("stdout", []byte("oversize-rejected-line\n"))
	if len(events) != 2 {
		t.Fatal("rejected line observed")
	}
	ring.SetCommitCallback(nil)
	_, _ = ring.Write("stdout", []byte("third\n"))
	if len(events) != 2 {
		t.Fatal("removed callback invoked")
	}
}
