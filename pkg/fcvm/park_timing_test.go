package fcvm

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Prod hunt r72: captures ran into schedd's 45 s budget while vmmd logged
// nothing, so the slow step could not be named. A failed or slow park logs
// each phase; a fast successful one stays quiet.
func TestParkPhasesReportSlowOrFailedCaptures(t *testing.T) {
	start := time.Unix(0, 0)
	phases := newParkPhases(start)
	phases.mark("flight", start.Add(10*time.Millisecond))
	phases.mark("snapshot", start.Add(44*time.Second))

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	phases.report(log, "ins-1", errors.New("context deadline exceeded"), start.Add(45*time.Second))
	out := buf.String()
	for _, want := range []string{"park: capture timing", "instance=ins-1", "flight_ms=10", "snapshot_ms=43990", "total_ms=45000", "last_phase=snapshot", "context deadline exceeded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}

	buf.Reset()
	fast := newParkPhases(start)
	fast.mark("snapshot", start.Add(2*time.Second))
	fast.report(log, "ins-2", nil, start.Add(2*time.Second))
	if buf.Len() != 0 {
		t.Fatalf("fast successful park logged: %s", buf.String())
	}

	buf.Reset()
	slow := newParkPhases(start)
	slow.report(log, "ins-3", nil, start.Add(parkSlowLogThreshold))
	if !strings.Contains(buf.String(), "last_phase=begin") {
		t.Fatalf("slow park without phases: %s", buf.String())
	}
}
