package dashboard

import "testing"

// adr: 20261020105001 — sparklines scale to the peak inside the box; an empty or
// all-zero series is a flat line along the bottom.
func TestSparklinePoints(t *testing.T) {
	for _, tc := range []struct {
		values []int64
		want   string
	}{
		{nil, "0,19 84,19"},
		{[]int64{0, 0, 0}, "0.0,19.0 42.0,19.0 84.0,19.0"},
		{[]int64{0, 5, 10}, "0.0,19.0 42.0,10.0 84.0,1.0"},
		{[]int64{7}, "0.0,1.0"},
	} {
		if got := SparklinePoints(tc.values); got != tc.want {
			t.Errorf("SparklinePoints(%v) = %q, want %q", tc.values, got, tc.want)
		}
	}
}
