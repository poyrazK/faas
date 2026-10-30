package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261029093621 — a scanned readiness check is the deployment's startup
// probe and its liveness probe, or neither where the plan has no liveness
// overrides.
func TestProjectReadinessOverrides(t *testing.T) {
	httpCheck := &api.DeploymentHealthcheck{Path: "/ready", IntervalS: 5, TimeoutS: 1, Retries: 3}
	grpcCheck := &api.DeploymentHealthcheck{GRPC: &api.DeploymentGRPCHealthcheck{Service: "api.v1"}}
	for _, tc := range []struct {
		name                     string
		check                    *api.DeploymentHealthcheck
		plan                     api.Plan
		wantReadiness, wantAlive string
	}{
		{"no check", nil, api.PlanPro, "", ""},
		{"http on pro", httpCheck, api.PlanPro,
			`{"path":"/ready","interval_s":5,"timeout_s":1,"retries":3}`, `{"path":"/ready"}`},
		{"grpc on pro", grpcCheck, api.PlanPro,
			`{"path":"","grpc":{"service":"api.v1"}}`, `{"grpc":{"service":"api.v1"}}`},
		{"free has no liveness overrides", httpCheck, api.PlanFree, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readiness, liveness := apidserver.ProjectReadinessOverrides(state.AppManifest{ProjectReadinessCheck: tc.check}, tc.plan)
			if string(readiness) != tc.wantReadiness || string(liveness) != tc.wantAlive {
				t.Errorf("overrides = %s, %s; want %s, %s", readiness, liveness, tc.wantReadiness, tc.wantAlive)
			}
		})
	}
}
