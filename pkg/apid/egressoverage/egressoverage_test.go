package egressoverage

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261103131742
func TestResponse(t *testing.T) {
	tests := []struct {
		name       string
		plan       api.Plan
		enabled    bool
		billedLive bool
		want       api.EgressOverageResponse
	}{
		{"Pro, on, billed", api.PlanPro, true, true, api.EgressOverageResponse{Enabled: true, Billed: true, IncludedGiB: 500, MillicentsPerGiB: 3_000}},
		{"Hobby, off, not billed yet", api.PlanHobby, false, false, api.EgressOverageResponse{IncludedGiB: 100, MillicentsPerGiB: 3_000}},
		{"Free is never billed", api.PlanFree, false, true, api.EgressOverageResponse{IncludedGiB: 10, MillicentsPerGiB: 3_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Response(tt.plan, tt.enabled, tt.billedLive); got != tt.want {
				t.Fatalf("Response = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// adr: 20261103131742
func TestRefusal(t *testing.T) {
	tests := []struct {
		name    string
		plan    api.Plan
		enabled bool
		refused bool
	}{
		{"Free turning on", api.PlanFree, true, true},
		{"Free turning off", api.PlanFree, false, false},
		{"Hobby turning on", api.PlanHobby, true, false},
		{"Scale turning off", api.PlanScale, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Refusal(tt.plan, tt.enabled)
			if (p != nil) != tt.refused {
				t.Fatalf("Refusal = %v, want refused=%v", p, tt.refused)
			}
			if p != nil && (p.Code != api.CodePlanEgressOverageNotAllowed || p.Status != 403) {
				t.Fatalf("Refusal = %s %d, want %s 403", p.Code, p.Status, api.CodePlanEgressOverageNotAllowed)
			}
		})
	}
}
