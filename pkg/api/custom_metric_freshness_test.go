package api

import (
	"testing"
	"time"
)

func TestCustomMetricFreshRejectsFutureAndExpiredObservations(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		at   time.Time
		want bool
	}{{now, true}, {now.Add(-time.Minute), true}, {now.Add(time.Second), false}, {now.Add(-time.Hour), false}} {
		if got := CustomMetricFresh(tc.at, now); got != tc.want {
			t.Fatalf("freshness for %s=%v", tc.at, got)
		}
	}
}
