package api

import (
	"strings"
	"testing"
	"time"
)

func TestMCPReadinessMaintenanceValidation(t *testing.T) {
	now := time.Now().UTC()
	valid := CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: now.Add(time.Hour), Reason: "upgrade"}
	if err := ValidateMCPReadinessMaintenance(valid, now); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*CreateMCPReadinessMaintenanceRequest){func(r *CreateMCPReadinessMaintenanceRequest) { r.Scope = "__all__" }, func(r *CreateMCPReadinessMaintenanceRequest) { r.Reason = "  " }, func(r *CreateMCPReadinessMaintenanceRequest) { r.Reason = strings.Repeat("a", 501) }, func(r *CreateMCPReadinessMaintenanceRequest) { r.StartsAt = now.Add(-2 * time.Minute) }, func(r *CreateMCPReadinessMaintenanceRequest) { r.EndsAt = now }, func(r *CreateMCPReadinessMaintenanceRequest) { r.EndsAt = now.Add(721 * time.Hour) }} {
		r := valid
		alter(&r)
		if ValidateMCPReadinessMaintenance(r, now) == nil {
			t.Fatal("invalid window", r)
		}
	}
}
