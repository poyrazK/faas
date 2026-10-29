package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The customer dashboard has no per-deployment path; a failed-deployment
// email opens the deployment on its app's Deployments tab, the link PR
// comments and GitHub deployments already use.
func TestDeploymentDashboardURLOpensTheDeploymentsTab(t *testing.T) {
	for base, want := range map[string]string{
		"":                     "https://gregale.dev/dashboard/apps/shop-api?deployment=dep-1&tab=Deployments",
		"https://gregale.dev/": "https://gregale.dev/dashboard/apps/shop-api?deployment=dep-1&tab=Deployments",
		"https://example.test": "https://example.test/dashboard/apps/shop-api?deployment=dep-1&tab=Deployments",
	} {
		if got := apidserver.DeploymentDashboardURL(base, "shop-api", "dep-1"); got != want {
			t.Errorf("DeploymentDashboardURL(%q) = %q, want %q", base, got, want)
		}
	}
}
