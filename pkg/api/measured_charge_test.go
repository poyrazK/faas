// adr: 909 — the measured charge is use plus headroom, never below the
// floor's share and never above the reservation.
package api

import (
	"math"
	"testing"
)

func TestMeasuredChargeMB(t *testing.T) {
	for _, tc := range []struct {
		ram  int
		rss  float64
		want int
	}{
		{ram: 1024, rss: 200, want: 250},
		{ram: 1024, rss: 10, want: 80}, // floor 64 + 25%
		{ram: 256, rss: 400, want: 264},
		{ram: 512, rss: 0, want: 80},
		{ram: 512, rss: math.NaN(), want: 520},
		{ram: 512, rss: -1, want: 520},
		{ram: 1024, rss: 819.3, want: 1025},
	} {
		if got := MeasuredChargeMB(tc.ram, tc.rss); got != tc.want {
			t.Errorf("MeasuredChargeMB(%d, %v) = %d, want %d", tc.ram, tc.rss, got, tc.want)
		}
	}
}
