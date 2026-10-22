package executionprofiles_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionprofiles"
)

func TestCapabilitiesFollowPlanAndControlPlane(t *testing.T) {
	for _, tc := range []struct {
		plan      api.Plan
		enabled   bool
		available bool
		reasons   []string
	}{
		{api.PlanPro, true, true, nil},
		{api.PlanPro, false, false, []string{"control_plane_disabled"}},
		{api.PlanFree, true, false, []string{"plan_not_entitled"}},
		{api.Plan("unknown"), false, false, []string{"plan_not_entitled", "control_plane_disabled"}},
	} {
		got := executionprofiles.Capabilities(tc.plan, tc.enabled)
		entitled := tc.plan == api.PlanPro
		if got.Plan != string(tc.plan) || got.AdmissionAvailable != tc.available || got.PlanEntitled != entitled || got.ControlPlaneEnabled != tc.enabled ||
			!slices.Equal(got.UnavailableReasons, tc.reasons) || (got.Limits != nil) != entitled || len(got.Runtimes) != 4 || len(got.Profiles) != 2 {
			t.Fatalf("%s enabled=%v: %+v", tc.plan, tc.enabled, got)
		}
		if entitled && (got.Limits.MaxBundleFiles != api.ExecutionBundleMaxFiles || got.Limits.MaxTimeoutMS == 0) {
			t.Fatalf("%s limits = %+v", tc.plan, *got.Limits)
		}
	}
}
