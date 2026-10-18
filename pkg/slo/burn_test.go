package slo

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMultiWindowBurn(t *testing.T) {
	def := state.SLO{AppID: "a", SLI: state.SLIAvailability, ObjectiveBP: 9990} // budget 0.1%
	tests := []struct {
		name               string
		badPct1h, badPct6h float64 // percent of requests failing in each window
		want               float64
	}{
		{"healthy", 0, 0, 0},
		{"short spike only", 2, 0.1, 1 * 14.4 / 6}, // 1h burn 20, 6h burn 1 rescaled
		{"sustained", 2, 1, 20},                    // min(20, 10×2.4=24)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prom := &fakeProm{fn: func(q string) (float64, error) {
				bad := tt.badPct1h
				if strings.Contains(q, "[6h]") {
					bad = tt.badPct6h
				}
				if strings.Contains(q, "5..") {
					return 100000, nil
				}
				return 100000 * (1 - bad/100), nil
			}}
			got, err := MultiWindowBurn(context.Background(), prom, def)
			if err != nil || math.Abs(got-tt.want) > 1e-6 {
				t.Fatalf("got %v (%v), want %v", got, err, tt.want)
			}
		})
	}
}
