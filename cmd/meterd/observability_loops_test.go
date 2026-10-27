package main

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 20261024222102 — meterd's customer observability loops export health
// series the faas_customer_observability alerts read.
func TestObservabilityLoopsRecordHealth(t *testing.T) {
	reg := prometheus.NewRegistry()
	loops := newObservabilityLoops(reg)
	if got := testutil.ToFloat64(loops.enabled.WithLabelValues(loopSLORollup)); got != 0 {
		t.Fatalf("slo_rollup enabled = %v before it starts, want 0", got)
	}
	if got := testutil.ToFloat64(loops.items.WithLabelValues(loopSyntheticChecks, "record_error")); got != 0 {
		t.Fatalf("record_error = %v, want a zero series before the first event", got)
	}
	loops.setEnabled(loopSyntheticChecks, true)
	at := time.Unix(1_800_000_000, 0)
	loops.pass(loopSyntheticChecks, nil, at)
	loops.pass(loopSyntheticChecks, errors.New("db down"), at.Add(time.Minute))
	loops.add(loopSyntheticChecks, "failed", 3)
	loops.add(loopSyntheticChecks, "ok", 0)
	for name, tc := range map[string]struct {
		got, want float64
	}{
		"enabled":              {testutil.ToFloat64(loops.enabled.WithLabelValues(loopSyntheticChecks)), 1},
		"last error-free pass": {testutil.ToFloat64(loops.lastPass.WithLabelValues(loopSyntheticChecks)), float64(at.Unix())},
		"ok passes":            {testutil.ToFloat64(loops.passes.WithLabelValues(loopSyntheticChecks, "ok")), 1},
		"error passes":         {testutil.ToFloat64(loops.passes.WithLabelValues(loopSyntheticChecks, "error")), 1},
		"failed items":         {testutil.ToFloat64(loops.items.WithLabelValues(loopSyntheticChecks, "failed")), 3},
		"ok items":             {testutil.ToFloat64(loops.items.WithLabelValues(loopSyntheticChecks, "ok")), 0},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", name, tc.got, tc.want)
		}
	}
	var none *observabilityLoops
	none.setEnabled(loopSLORollup, true)
	none.pass(loopSLORollup, nil, at)
	none.add(loopSLORollup, "recorded", 1)
}
