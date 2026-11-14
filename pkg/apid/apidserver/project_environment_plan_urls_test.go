package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Hunt 33 H33-33: `deploy --environment staging` listed the production
// hostnames under "Public on the internet". A non-production plan names each
// existing app's environment URL instead.
func TestProjectEnvironmentPlanNamesEnvironmentURLs(t *testing.T) {
	envID, ids, plan := apidserver.ProjectEnvironmentPlanURLs(t)
	if len(plan.Workloads) == 0 {
		t.Fatal("staging plan has no workloads")
	}
	for _, w := range plan.Workloads {
		want := apidserver.ProjectEnvironmentWorkloadURL(envID, ids[w.Name])
		if w.PublicURL != want || want == "" {
			t.Errorf("%s: public_url=%q, want %q", w.Name, w.PublicURL, want)
		}
	}
}
