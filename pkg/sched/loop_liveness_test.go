package sched

// adr: 190

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// ADR-190: the main loop beats its Liveness entry; a nil registry is
// a no-op so every existing Loop test keeps working unchanged.
func TestLoopBeatMainIsNilSafeAndBeats(t *testing.T) {
	t.Parallel()
	var l Loop
	l.beatMain() // nil liveness

	lv := wire.NewLiveness()
	lv.Register(MainLoopName, MainLoopBudget)
	l.WithLiveness(lv)
	before := lv.Ages()[0].Age
	time.Sleep(2 * time.Millisecond)
	l.beatMain()
	after := lv.Ages()[0].Age
	if after > before+time.Millisecond {
		t.Fatalf("beat did not refresh main loop age: before=%s after=%s", before, after)
	}
}
