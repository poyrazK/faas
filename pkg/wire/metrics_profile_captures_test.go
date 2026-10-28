// adr: 1155 — schedd's on-demand capture metrics keep their labels to closed
// sets and record nothing without a registry.
package wire

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestProfileCaptureMetrics(t *testing.T) {
	var none *ProfileCaptureMetrics
	none.CaptureFinished("manual", "ready", time.Second, time.Second)
	none.AutomaticSkipped("slow_request", "quota")

	reg := prometheus.NewRegistry()
	m := NewProfileCaptureMetrics(reg)
	m.CaptureFinished("memory_pressure", "timeout", 2*time.Second, 12*time.Second)
	m.CaptureFinished("made-up trigger", "made-up result", 0, 0)
	m.AutomaticSkipped("slow_request", "made-up reason")
	for _, tc := range []struct {
		name string
		got  float64
		want float64
	}{
		{"timeout", testutil.ToFloat64(m.Finished.WithLabelValues("memory_pressure", "timeout")), 1},
		{"unknown labels bounded", testutil.ToFloat64(m.Finished.WithLabelValues("manual", "unreachable")), 1},
		{"skip reason bounded", testutil.ToFloat64(m.Skipped.WithLabelValues("slow_request", "unavailable")), 1},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if n := testutil.CollectAndCount(m.Finished); n != len(ProfileCaptureTriggers)*len(ProfileCaptureResults) {
		t.Errorf("finished series = %d, want every trigger and result pre-created", n)
	}
	if again := NewProfileCaptureMetrics(reg); again.Finished != m.Finished {
		t.Error("a second registration on the same registry must reuse the families")
	}
}
