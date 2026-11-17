package fcvm

import (
	"reflect"
	"testing"
	"time"
)

func TestCaptureTimerPhasesSumToTotal(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return at }
	timer := newCaptureTimer(clock)
	for _, step := range []struct {
		phase   string
		elapsed time.Duration
	}{
		{"pause", 20 * time.Millisecond},
		{"create", 1500 * time.Millisecond},
		{"publish_mem", 4 * time.Second},
	} {
		at = at.Add(step.elapsed)
		timer.mark(step.phase)
	}
	at = at.Add(5 * time.Millisecond)
	want := []any{"pause_ms", int64(20), "create_ms", int64(1500), "publish_mem_ms", int64(4000), "total_ms", int64(5525)}
	if got := timer.attrs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("attrs = %v, want %v", got, want)
	}
	// attrs must not alias the recorded phases.
	got := timer.attrs()
	got[1] = int64(-1)
	if timer.phases[1] != int64(20) {
		t.Fatal("attrs aliased the recorded phases")
	}
}
