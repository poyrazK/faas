package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// A host without deployment-attached app tasks rejects `bindings verify` as
// not implemented. The inventory says so, so `bindings check` can report a
// coverage gap instead of asking for a verify that cannot run.
func TestBindingInventoryReportsWhetherProbesCanRun(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		e := apidserver.Setup(t, api.PlanPro)
		if enabled {
			apidserver.EnableAppTaskAPIForTest(&e)
		}
		app, deployment := apidserver.SeedAppTaskDeployment(t, e, "probe-gate")
		inventory := apidserver.DecodeBindingInventory(t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?deployment_id="+deployment.ID, nil, nil))
		if inventory.VerificationProbesAvailable == nil || *inventory.VerificationProbesAvailable != enabled {
			t.Fatalf("app tasks enabled=%v: verification_probes_available = %v", enabled, inventory.VerificationProbesAvailable)
		}
	}
}
