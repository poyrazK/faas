package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A multipart deploy's liveness_probe replaces the plan-default /healthz
// probe where the plan has one, and is ignored on Free, which has none.
func TestSourceLivenessOverridesFollowThePlan(t *testing.T) {
	probe := &api.DeploymentLivenessProbe{Path: api.DataAPILivenessPath}
	startup := &api.CreateDeploymentOverrides{Healthcheck: &api.DeploymentHealthcheck{Path: "/healthz"}}
	for _, tc := range []struct {
		name      string
		overrides *api.CreateDeploymentOverrides
		probe     *api.DeploymentLivenessProbe
		plan      api.Plan
		want      *api.DeploymentLivenessProbe
	}{
		{name: "hobby keeps the startup probe and adds liveness", overrides: startup, probe: probe, plan: api.PlanHobby, want: probe},
		{name: "hobby without other overrides", probe: probe, plan: api.PlanHobby, want: probe},
		{name: "free has no liveness probe to replace", overrides: startup, probe: probe, plan: api.PlanFree},
		{name: "no probe sent", overrides: startup, plan: api.PlanPro},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apidserver.SourceLivenessOverrides(tc.overrides, tc.probe, api.MustLimitsFor(tc.plan))
			var liveness *api.DeploymentLivenessProbe
			if got != nil {
				liveness = got.LivenessProbe
			}
			if liveness != tc.want {
				t.Fatalf("liveness = %+v, want %+v", liveness, tc.want)
			}
			if tc.overrides != nil && (got == nil || got.Healthcheck != tc.overrides.Healthcheck) {
				t.Fatalf("the startup probe was dropped: %+v", got)
			}
		})
	}
}
