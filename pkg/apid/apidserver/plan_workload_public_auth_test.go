package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPlanWorkloadPublicAuthMode pins Hunt 32 H32-30: the deploy plan listed
// a new Pro app as "Public on the internet" while its URL answered 401.
func TestPlanWorkloadPublicAuthMode(t *testing.T) {
	if got, want := apidserver.PlanWorkloadPublicAuthMode(nil, api.PlanPro), api.PlanPro.PublicAuthModeDefault(); want != api.AppPublicAuthModeOpen && got != want {
		t.Fatalf("new Pro app = %q, want the plan default %q", got, want)
	}
	if got := apidserver.PlanWorkloadPublicAuthMode(&state.App{PublicAuthMode: api.AppPublicAuthModeOpen}, api.PlanPro); got != "" {
		t.Fatalf("existing open app = %q, want omitted", got)
	}
	if got := apidserver.PlanWorkloadPublicAuthMode(&state.App{PublicAuthMode: api.AppPublicAuthModeBearer}, api.PlanHobby); got != api.AppPublicAuthModeBearer {
		t.Fatalf("existing bearer app = %q, want bearer", got)
	}
	if got := apidserver.PlanWorkloadPublicAuthMode(&state.App{}, api.PlanPro); got != "" {
		t.Fatalf("existing app with no stored mode = %q, want omitted (open)", got)
	}
}
