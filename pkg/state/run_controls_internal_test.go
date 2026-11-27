package state

import (
	"math"
	"testing"
	"time"
)

func TestRunControlUTCAndSettlementBounds(t *testing.T) {
	for _, text := range []string{"2026-11-16T00:00:00Z", "2026-11-16T03:00:00+03:00", "2026-11-15T19:00:00-05:00"} {
		at, err := time.Parse(time.RFC3339, text)
		if err != nil {
			t.Fatal(err)
		}
		if got := runControlDay(at).Format(time.RFC3339); got != "2026-11-16T00:00:00Z" {
			t.Fatal("budget day follows host timezone", got)
		}
	}
	for _, tc := range []struct{ reserved, timeout, wall, want int64 }{{136000, 1000, 100, 13600}, {136000, 1000, math.MaxInt64, 136000}, {136000, 1000, -1, 0}, {136000, 0, 100, 136000}, {0, 1000, 100, 0}} {
		if got := settledRunCompute(tc.reserved, tc.timeout, tc.wall); got != tc.want {
			t.Fatal("settlement overflow or missing evidence released allowance", tc, got)
		}
	}
}
