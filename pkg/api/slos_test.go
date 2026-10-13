package api_test

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidateCreateSLO(t *testing.T) {
	valid := api.CreateSLORequest{Name: "checkout", SLI: "availability", ObjectivePct: 99.9, WindowDays: 30}
	tests := []struct {
		name   string
		mutate func(*api.CreateSLORequest)
		wantBP int // 0 = invalid
	}{
		{"availability 99.9", func(*api.CreateSLORequest) {}, 9990},
		{"two decimals", func(r *api.CreateSLORequest) { r.ObjectivePct = 99.95 }, 9995},
		{"lower bound", func(r *api.CreateSLORequest) { r.ObjectivePct = 90 }, 9000},
		{"latency in bucket", func(r *api.CreateSLORequest) { r.SLI, r.LatencyThresholdMS = "latency", 250 }, 9990},
		{"latency between buckets", func(r *api.CreateSLORequest) { r.SLI, r.LatencyThresholdMS = "latency", 300 }, 0},
		{"latency missing threshold", func(r *api.CreateSLORequest) { r.SLI = "latency" }, 0},
		{"threshold on availability", func(r *api.CreateSLORequest) { r.LatencyThresholdMS = 250 }, 0},
		{"three decimals", func(r *api.CreateSLORequest) { r.ObjectivePct = 99.995 }, 0},
		{"100 percent", func(r *api.CreateSLORequest) { r.ObjectivePct = 100 }, 0},
		{"below 90", func(r *api.CreateSLORequest) { r.ObjectivePct = 89.99 }, 0},
		{"NaN", func(r *api.CreateSLORequest) { r.ObjectivePct = math.NaN() }, 0},
		{"bad window", func(r *api.CreateSLORequest) { r.WindowDays = 14 }, 0},
		{"bad name", func(r *api.CreateSLORequest) { r.Name = "Checkout" }, 0},
		{"unknown sli", func(r *api.CreateSLORequest) { r.SLI = "throughput" }, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			bp, p := api.ValidateCreateSLO(req)
			switch {
			case tt.wantBP == 0 && p == nil:
				t.Fatalf("got bp %d, want a validation problem", bp)
			case tt.wantBP != 0 && (p != nil || bp != tt.wantBP):
				t.Fatalf("got (%d, %+v), want %d", bp, p, tt.wantBP)
			}
		})
	}
}
