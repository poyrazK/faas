package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCompanionPortWaitsForTheBuiltMainPort is the apid half of Hunt 14
// H14-53: without an explicit port the build decides the main workload's port,
// so a companion on 8080 beside a gunicorn app on 8000 must not be refused at
// submission. imaged compares it with the built port.
func TestCompanionPortWaitsForTheBuiltMainPort(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanHobby)
	if !ok {
		t.Fatal("no limits for the Hobby plan")
	}
	acct := state.Account{Plan: api.PlanHobby}
	const image = "ghcr.io/me/nginx@sha256:aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	newReq := func(overrides *api.CreateDeploymentOverrides, companions ...api.Sidecar) *api.CreateDeploymentRequest {
		return &api.CreateDeploymentRequest{Overrides: overrides, Sidecars: companions}
	}
	web := api.Sidecar{Name: "web", Image: image, Type: api.SidecarTypeSidecar, Port: 8080, RamMB: 64}

	if p := apidserver.ValidateAndPlanSidecars(newReq(nil, web), acct, limits); p != nil {
		t.Fatalf("companion on 8080 without a declared main port refused at submission: %s", p.Detail)
	}
	p := apidserver.ValidateAndPlanSidecars(newReq(&api.CreateDeploymentOverrides{Port: 8080}, web), acct, limits)
	if p == nil || !strings.Contains(p.Detail, `"main" and "web" both claim port 8080`) {
		t.Fatalf("explicit main port 8080 with a companion on 8080: problem = %v, want a port conflict", p)
	}
	twin := web
	twin.Name = "proxy"
	p = apidserver.ValidateAndPlanSidecars(newReq(nil, web, twin), acct, limits)
	if p == nil || !strings.Contains(p.Detail, "both claim port 8080") {
		t.Fatalf("two companions on 8080: problem = %v, want a port conflict", p)
	}
}
