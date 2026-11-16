package logarchive

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A retired instance's lines ship before the next tick, and only its own
// files: `gregale logs` read a just-parked instance as an archive gap for up
// to FlushInterval (prod 2026-11-16).
func TestShipInstanceUploadsOnlyThatInstance(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	ts := time.Date(2026, 11, 16, 7, 46, 0, 0, time.UTC)
	for _, inst := range []string{"inst-parked", "inst-running"} {
		if _, err := sh.spool.Write(inst, 1, "stdout", ts, "line"); err != nil {
			t.Fatalf("Write %s: %v", inst, err)
		}
	}
	sh.ShipInstance("inst-parked")
	select {
	case <-sh.kick:
	default:
		t.Fatal("ShipInstance did not wake the shipper")
	}
	sh.shipRetired(context.Background())
	var parked, running int
	for key := range fake.objects {
		switch {
		case strings.Contains(key, "/inst-parked/"):
			parked++
		case strings.Contains(key, "/inst-running/"):
			running++
		}
	}
	if parked != 1 || running != 0 {
		t.Fatalf("objects=%v, want only inst-parked", keys(fake.objects))
	}
	// A second wake with nothing pending is a no-op.
	sh.shipRetired(context.Background())
}

// Many retirements coalesce into one wake and never block the caller.
func TestShipInstanceNeverBlocks(t *testing.T) {
	sh, _, _ := newTestShipper(t, 7)
	for i := range maxRetiredPending + 10 {
		sh.ShipInstance("inst-" + string(rune('a'+i%26)) + strings.Repeat("x", i%7))
	}
	if len(sh.kick) != 1 {
		t.Fatalf("pending wakes=%d, want 1", len(sh.kick))
	}
	var nilShipper *Shipper
	nilShipper.ShipInstance("inst")
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
