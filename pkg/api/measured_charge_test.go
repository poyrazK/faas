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

// adr: 20261031143521 — a frozen instance's charge has the 8 MB frozen floor:
// a paused VM cannot grow, and a thaw re-admits its full reservation.
func TestFrozenChargeMB(t *testing.T) {
	for _, tc := range []struct {
		ram  int
		rss  float64
		want int
	}{
		{ram: 1024, rss: 4, want: 10}, // compressed: floor 8 + 25%
		{ram: 1024, rss: 50, want: 63},
		{ram: 1024, rss: 200, want: 250},
		{ram: 128, rss: 200, want: 136},
		{ram: 512, rss: 0, want: 10},
		{ram: 512, rss: math.NaN(), want: 520},
	} {
		if got := FrozenChargeMB(tc.ram, tc.rss); got != tc.want {
			t.Errorf("FrozenChargeMB(%d, %v) = %d, want %d", tc.ram, tc.rss, got, tc.want)
		}
	}
}

// adr: 20261027090015 — agent run VMs book from their version's peak and are
// charged at least that peak, never more than the plan reservation.
func TestAgentReservationAndCharge(t *testing.T) {
	nan := math.NaN()
	for _, tc := range []struct {
		name             string
		ram, peak        int
		rss              float64
		reserve, charged int
	}{
		{name: "no history books and charges the plan", ram: 2048, peak: 0, rss: 300, reserve: 2048, charged: 2056},
		{name: "the peak with headroom", ram: 2048, peak: 300, rss: nan, reserve: 450, charged: 308},
		{name: "a larger sample raises the charge", ram: 2048, peak: 300, rss: 900.2, reserve: 450, charged: 909},
		{name: "a smaller sample keeps the peak", ram: 2048, peak: 300, rss: 120, reserve: 450, charged: 308},
		{name: "the floors", ram: 2048, peak: 10, rss: 5, reserve: 128, charged: 72},
		{name: "capped at the plan", ram: 1024, peak: 900, rss: 5000, reserve: 1024, charged: 1032},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AgentReservationMB(tc.ram, tc.peak); got != tc.reserve {
				t.Errorf("reservation %d, want %d", got, tc.reserve)
			}
			if got := AgentChargeMB(tc.ram, tc.peak, tc.rss); got != tc.charged {
				t.Errorf("charge %d, want %d", got, tc.charged)
			}
		})
	}
}
