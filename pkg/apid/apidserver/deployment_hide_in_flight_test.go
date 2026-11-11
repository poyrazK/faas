package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hiding only stamps deleted_at, so an in-flight deployment kept building
// on a row no list showed (deploy review 2026-11-10, D1). The handler
// refuses it with a fix that names cancel; after cancel, hide works.
func TestClearDeployment_InFlightRefusedUntilCancelled(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "hide-in-flight") // building

	rec := e.Do(t, http.MethodDelete, "/v1/deployments/"+dep.ID, nil, nil)
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeDeploymentHideInFlight {
		t.Fatalf("hide building deployment = %d %s; want 409 %s", rec.Code, rec.Body, api.CodeDeploymentHideInFlight)
	}
	if got, err := (*e.Store()).DeploymentByID(context.Background(), dep.ID); err != nil || got.DeletedAt != nil {
		t.Fatalf("refused hide still stamped the row: %+v %v", got.DeletedAt, err)
	}

	if _, _, err := (*e.Store()).CancelDeploymentTx(context.Background(), dep.ID, "test", state.CancelReasonUser); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/deployments/"+dep.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("hide cancelled deployment = %d %s; want 200", rec.Code, rec.Body)
	}
}
