package appmetrics

import "testing"

func TestHistogramQuantileEmptyBoundsAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		q       float64
		buckets map[string]float64
		want    float64
	}{
		{"empty lower buckets", .5, map[string]float64{"0.1": 0, "0.5": 0, "1": 100, "+Inf": 100}, .75},
		{"overflow only", .95, map[string]float64{"0.1": 0, "0.5": 0, "1": 0, "+Inf": 100}, 1},
		{"empty tail before overflow", .99, map[string]float64{"0.1": 10, "0.5": 10, "1": 10, "+Inf": 100}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HistogramQuantile(tc.q, tc.buckets); got != tc.want {
				t.Fatalf("quantile=%g want=%g", got, tc.want)
			}
		})
	}
}
