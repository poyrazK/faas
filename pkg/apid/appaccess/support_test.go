package appaccess

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/authz"
)

// Every support route is a reviewed app route whose organization action is a
// read or a deploy (ADR-20261116163508): adding a route that configures,
// reads secrets or deletes to the support list must fail here first.
func TestSupportRoutesAreReviewedReadsAndDeploys(t *testing.T) {
	allowed := map[authz.OrgAction]bool{
		authz.OrgActionReadApp:     true,
		authz.OrgActionReadAppLogs: true,
		authz.OrgActionDeployApp:   true,
	}
	if len(SupportRoutes()) == 0 {
		t.Fatal("no support routes")
	}
	for _, pattern := range SupportRoutes() {
		action, ok := routeActions[pattern]
		if !ok {
			t.Errorf("support route %q has no reviewed organization action", pattern)
			continue
		}
		if !allowed[action] {
			t.Errorf("support route %q needs %s; support may only read and deploy", pattern, action)
		}
		if strings.HasPrefix(pattern, "GET ") != (action != authz.OrgActionDeployApp) {
			t.Errorf("support route %q: reads must be GET and deploys must not be", pattern)
		}
	}
	for _, forbidden := range []string{
		"POST /v1/apps/{slug}/deployments",
		"POST /v1/apps/{slug}/deployments/source-tarball",
		"POST /v1/apps/{slug}/deployments/dev-source",
		"DELETE /v1/apps/{slug}",
	} {
		if SupportRoute(forbidden) {
			t.Errorf("support route list includes %q", forbidden)
		}
	}
}
