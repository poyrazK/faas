// adr: 20261018000200
package rehearsal

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestPostgresFaultAdmissionRemainsSeparatelyGated(t *testing.T) {
	req := api.RegisterScenarioTestRequest{ManagedWork: &rehearsalwork.Plan{PostgresFault: &rehearsalfault.Postgres{Workload: "worker"}}}
	if ValidatePostgresFault(req, false) == nil {
		t.Fatal("unqualified database faults admitted")
	}
	if p := ValidatePostgresFault(req, true); p != nil {
		t.Fatal(p)
	}
	req.ManagedWork.PostgresFault = nil
	if p := ValidatePostgresFault(req, false); p != nil {
		t.Fatal("gate changed ordinary managed work")
	}
}
