//go:build !no_pg

// adr: 590
package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deleting a published full-clone stage removes the database the clone
// restored for it. The copy is not customer-visible once its stage is gone,
// so cleanup reaches it through the clone; before, it stayed running, billed
// and bound with live credentials (ADR-20261024224230).
func TestPGDatabaseFullCloneStageDeleteRemovesRestoredDatabase(t *testing.T) {
	d := newDatabaseCloneFixture(t)
	ctx := t.Context()
	store := *d.f.Store()
	apps := *d.f.Apps()
	sourceDatabase := d.stageDatabase(apps[0].ID, "production")
	op := d.admitted("stage", "delete-stage")
	d.step(op.OperationID)
	d.step(op.OperationID)
	views := d.targets(op.OperationID)
	for _, view := range views {
		if err := store.MarkDeploymentLiveDark(ctx, view.TargetDeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	var current state.ProjectEnvironmentCloneOperation
	for range 4 {
		if current = d.step(op.OperationID); current.Status == state.CloneOperationReady {
			break
		}
	}
	if current.Status != state.CloneOperationReady {
		t.Fatalf("clone did not publish: %+v", current)
	}
	target := databaseResources(current)[0].TargetID

	// A stage serving live deployments cannot be deleted; retire them first.
	for _, view := range views {
		if err := store.UpdateDeploymentStatus(ctx, view.TargetDeploymentID, state.DeployFailed, "retired before stage delete"); err != nil {
			t.Fatal(err)
		}
	}
	req, rec := apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/"+d.project.Slug+"/environments/stage", d.project.Slug, nil)
	req.SetPathValue("environment", "stage")
	(*d.f.Srv()).DeleteProjectEnvironment(rec, req, d.acct)
	if rec.Code >= 300 {
		t.Fatalf("stage delete = %d %s", rec.Code, rec.Body.String())
	}

	var databaseState string
	var liveBindings int
	if err := (*d.f.Pool()).QueryRow(ctx, `SELECT state, (SELECT count(*) FROM managed_postgres_bindings WHERE database_id = $1 AND state <> 'deleted')
		FROM managed_postgres_databases WHERE id = $1`, target).Scan(&databaseState, &liveBindings); err != nil {
		t.Fatal(err)
	}
	if (databaseState != "deleted" && databaseState != "deleting") || liveBindings != 0 {
		t.Fatalf("restored copy is %s with %d live bindings after its stage was deleted", databaseState, liveBindings)
	}
	for _, app := range apps {
		if got := d.stageDatabase(app.ID, "production"); got != sourceDatabase {
			t.Fatalf("stage delete changed production workload %s's database to %s", app.Slug, got)
		}
	}
}
