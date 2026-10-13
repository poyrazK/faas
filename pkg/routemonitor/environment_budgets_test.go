// adr: 1252
package routemonitor

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestEnvironmentBudgetsRejectInvalidIntent(t *testing.T) {
	valid := api.SetEnvironmentRouteBudgetsRequest{AppID: uuid.NewString(), RequestID: uuid.NewString(), ExpectedRevision: new(int64), ExpectedAppRevision: new(int64), Inherit: true, Routes: []api.RouteMonitorRoute{}}
	if err := ValidateEnvironmentBudgets(valid); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*api.SetEnvironmentRouteBudgetsRequest){
		func(r *api.SetEnvironmentRouteBudgetsRequest) { r.Enabled = true },
		func(r *api.SetEnvironmentRouteBudgetsRequest) { r.ExpectedAppRevision = nil },
		func(r *api.SetEnvironmentRouteBudgetsRequest) { r.ExpectedRevision = nil },
		func(r *api.SetEnvironmentRouteBudgetsRequest) { r.Routes = nil },
		func(r *api.SetEnvironmentRouteBudgetsRequest) { r.ProjectID = uuid.NewString() },
		func(r *api.SetEnvironmentRouteBudgetsRequest) {
			r.Inherit = false
			r.Enabled = true
			r.Routes = []api.RouteMonitorRoute{{Method: "GET", Path: "https://external.example", MaxP95MS: 100}}
		},
	} {
		req := valid
		modify(&req)
		if err := ValidateEnvironmentBudgets(req); err == nil {
			t.Fatal("invalid scoped intent accepted")
		}
	}
}
