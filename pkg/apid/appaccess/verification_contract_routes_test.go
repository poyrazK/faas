package appaccess

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/authz"
)

// A verification contract gates promotion (ADR-20261122220000): reading one
// is reading the app, and changing or removing one changes a release gate.
// #7923 and #7931 both listed these routes, with DeployApp and
// ManageAppSecurity; merged together the duplicate keys broke main's build.
func TestVerificationContractRoutesNeedAppSecurityToChange(t *testing.T) {
	for pattern, want := range map[string]authz.OrgAction{
		"GET /v1/apps/{slug}/verification-contract":    authz.OrgActionReadApp,
		"PUT /v1/apps/{slug}/verification-contract":    authz.OrgActionManageAppSecurity,
		"DELETE /v1/apps/{slug}/verification-contract": authz.OrgActionManageAppSecurity,
	} {
		if got := routeActions[pattern]; got != want {
			t.Errorf("%s: got %q, want %q", pattern, got, want)
		}
	}
}
