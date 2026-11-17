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
	startup := &api.DeploymentHealthcheck{Path: "/healthz"}
	for _, tc := range []struct {
		name    string
		startup bool
		probe   *api.DeploymentLivenessProbe
		plan    api.Plan
		want    *api.DeploymentLivenessProbe
	}{
		{name: "hobby keeps the startup probe and adds liveness", startup: true, probe: probe, plan: api.PlanHobby, want: probe},
		{name: "hobby without other overrides", probe: probe, plan: api.PlanHobby, want: probe},
		{name: "free has no liveness probe to replace", startup: true, probe: probe, plan: api.PlanFree},
		{name: "no probe sent", startup: true, plan: api.PlanPro},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case owns its overrides: the function sets the probe in place.
			var overrides *api.CreateDeploymentOverrides
			if tc.startup {
				overrides = &api.CreateDeploymentOverrides{Healthcheck: startup}
			}
			got := apidserver.SourceLivenessOverrides(overrides, tc.probe, api.MustLimitsFor(tc.plan))
			var liveness *api.DeploymentLivenessProbe
			if got != nil {
				liveness = got.LivenessProbe
			}
			if liveness != tc.want {
				t.Fatalf("liveness = %+v, want %+v", liveness, tc.want)
			}
			if tc.startup && (got == nil || got.Healthcheck != startup) {
				t.Fatalf("the startup probe was dropped: %+v", got)
			}
		})
	}
}
