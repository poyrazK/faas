//go:build !no_pg

// adr: 590
package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

func newDatabaseCloneFixture(t *testing.T) fullCloneFixture {
	d := newFullCloneFixture(t, true)
	d.open(runtimeconfig.KeyFullEnvironmentClones, fullCloneAdmissionKey)
	return d
}

// databaseResources returns the operation's managed database resources.
func databaseResources(op state.ProjectEnvironmentCloneOperation) []state.ProjectEnvironmentCloneResource {
	var out []state.ProjectEnvironmentCloneResource
	for _, resource := range op.Resources {
		if resource.Kind == "managed_postgres" {
			out = append(out, resource)
		}
	}
	return out
}

// stageDatabase reads the database a stage workload's DATABASE_URL binds.
func (d fullCloneFixture) stageDatabase(appID, scope string) string {
	d.t.Helper()
	var databaseID string
	if err := (*d.f.Pool()).QueryRow(d.t.Context(), `SELECT b.database_id::text FROM app_secrets s
		JOIN managed_postgres_bindings b ON b.id = s.managed_postgres_binding_id
		WHERE s.app_id = $1 AND s.scope = $2 AND s.key = 'DATABASE_URL' AND b.state <> 'deleted'`, appID, scope).Scan(&databaseID); err != nil {
		d.t.Fatalf("read %s DATABASE_URL binding: %v", scope, err)
	}
	return databaseID
}

// A clone of a source with a managed database restores that database once,
// at one point read after the configuration capture, and publishes a stage
// whose workloads bind the copy (ADR-20261024224230).
func TestPGDatabaseFullCloneRestoresAtOnePointAndPublishes(t *testing.T) {
	d := newDatabaseCloneFixture(t)
	ctx := t.Context()
	store := *d.f.Store()
	apps := *d.f.Apps()
	sourceDatabase := d.stageDatabase(apps[0].ID, "production")
	op := d.admitted("stage", "database")

	captured := d.step(op.OperationID)
	resources := databaseResources(captured)
	if captured.Status != state.CloneOperationCopying || len(resources) != 1 || resources[0].SourceID != sourceDatabase || resources[0].TargetID != "" {
		t.Fatalf("capture must record one database to copy: %+v", captured)
	}
	point, err := time.Parse(time.RFC3339Nano, resources[0].CapturePoint)
	if err != nil || !point.After(captured.CreatedAt) {
		t.Fatalf("the data point %q must follow the configuration capture at %s: %v", resources[0].CapturePoint, captured.CreatedAt, err)
	}
	if restores := (*d.f.Database()).Restores(); len(restores) != 0 {
		t.Fatalf("capture must not restore before a later pass: %+v", restores)
	}

	copying := d.step(op.OperationID)
	resources = databaseResources(copying)
	if copying.Status != state.CloneOperationCopying || len(resources) != 1 || resources[0].Status != "ready" || resources[0].TargetID == "" || resources[0].TargetID == sourceDatabase {
		t.Fatalf("copy must restore the database: %+v", copying)
	}
	restores := (*d.f.Database()).Restores()
	if len(restores) != 1 || !restores[0].PointInTime.Equal(point) {
		t.Fatalf("the provider must restore once at the data point %s: %+v", point, restores)
	}
	for _, view := range d.targets(op.OperationID) {
		if err := store.MarkDeploymentLiveDark(ctx, view.TargetDeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	current := copying
	for range 3 {
		if current = d.step(op.OperationID); current.Status == state.CloneOperationReady {
			break
		}
	}
	if current.Status != state.CloneOperationReady || current.TargetReleaseSetID == "" {
		t.Fatalf("a restored database must publish: %+v", current)
	}
	target := databaseResources(current)[0].TargetID
	for _, app := range apps {
		if got := d.stageDatabase(app.ID, "stage"); got != target {
			t.Fatalf("stage workload %s binds %s, want the restored copy %s", app.Slug, got, target)
		}
		if got := d.stageDatabase(app.ID, "production"); got != sourceDatabase {
			t.Fatalf("production workload %s now binds %s", app.Slug, got)
		}
	}
	if restores := (*d.f.Database()).Restores(); len(restores) != 1 {
		t.Fatalf("publication restored again: %+v", restores)
	}
}

// A bound database without point-in-time restore history cannot be copied
// at the clone's point, so admission names it and creates nothing.
func TestPGDatabaseFullCloneRefusesDatabaseWithoutRestoreWindow(t *testing.T) {
	d := newDatabaseCloneFixture(t)
	if _, err := (*d.f.Pool()).Exec(d.t.Context(), `UPDATE managed_postgres_databases SET restore_window_seconds = 0 WHERE account_id = $1`, d.acct.ID); err != nil {
		t.Fatal(err)
	}
	rec := d.admit("stage", "no-history")
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusConflict || len(problem.Errors) != 1 ||
		problem.Errors[0].Field != "resource_coverage.managed_postgres_bindings" || problem.Errors[0].Got != state.CloneBlockerPointInTimeRestoreUnavailable {
		t.Fatalf("refusal = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	if _, err := (*d.f.Store()).ProjectEnvironmentBySlug(d.t.Context(), d.acct.ID, d.project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a refused clone created its target: %v", err)
	}
}

// A source edit between the configuration capture and the data point would
// pair the captured workloads with data from another revision. The clone
// fails and compensates instead.
func TestPGDatabaseFullCloneFailsWhenSourceChangesBeforeItsDataPoint(t *testing.T) {
	d := newDatabaseCloneFixture(t)
	op := d.admitted("stage", "moving")
	if err := (*d.f.Store()).UpsertAppEnvInScope(d.t.Context(), d.acct.ID, (*d.f.Apps())[0].ID, "production", "CAPTURED", "edited"); err != nil {
		t.Fatal(err)
	}
	current := d.step(op.OperationID)
	if current.Status != state.CloneOperationCompensating || current.ErrorCode != "source_changed_during_capture" || len(databaseResources(current)) != 0 {
		t.Fatalf("a moving source must fail the clone before a data point: %+v", current)
	}
	if current = d.step(op.OperationID); current.Status != state.CloneOperationCompensated {
		t.Fatalf("compensation did not finish: %+v", current)
	}
	if restores := (*d.f.Database()).Restores(); len(restores) != 0 {
		t.Fatalf("a failed capture restored data: %+v", restores)
	}
}

// When a clone fails after restoring its database, compensation withdraws the
// stage's bindings, deletes the copy and removes the stage; the source
// database and its bindings are untouched.
func TestPGDatabaseFullCloneCompensationDeletesRestoredDatabase(t *testing.T) {
	d := newDatabaseCloneFixture(t)
	ctx := t.Context()
	store := *d.f.Store()
	apps := *d.f.Apps()
	sourceDatabase := d.stageDatabase(apps[0].ID, "production")
	op := d.admitted("stage", "failing")
	d.step(op.OperationID)
	copying := d.step(op.OperationID)
	resources := databaseResources(copying)
	if len(resources) != 1 || resources[0].Status != "ready" {
		t.Fatalf("copy must restore the database first: %+v", copying)
	}
	target := resources[0].TargetID
	views := d.targets(op.OperationID)
	if err := store.UpdateDeploymentStatus(ctx, views[0].TargetDeploymentID, state.DeployFailed, "prime failed"); err != nil {
		t.Fatal(err)
	}
	current := d.step(op.OperationID)
	if current.Status != state.CloneOperationCompensating || current.ErrorCode != "target_deployment_failed" {
		t.Fatalf("a failed target deployment must end the clone: %+v", current)
	}
	for range 3 {
		if current = d.step(op.OperationID); current.Status == state.CloneOperationCompensated {
			break
		}
	}
	if current.Status != state.CloneOperationCompensated {
		t.Fatalf("compensation did not finish: %+v", current)
	}
	var state_, liveBindings string
	if err := (*d.f.Pool()).QueryRow(ctx, `SELECT state, (SELECT count(*) FROM managed_postgres_bindings WHERE database_id = $1 AND state <> 'deleted')::text
		FROM managed_postgres_databases WHERE id = $1`, target).Scan(&state_, &liveBindings); err != nil {
		t.Fatal(err)
	}
	if state_ != "deleted" || liveBindings != "0" {
		t.Fatalf("restored copy is %s with %s live bindings, want deleted with none", state_, liveBindings)
	}
	var stageSecrets int
	if err := (*d.f.Pool()).QueryRow(ctx, `SELECT count(*) FROM app_secrets WHERE scope = 'stage' AND account_id = $1`, d.acct.ID).Scan(&stageSecrets); err != nil {
		t.Fatal(err)
	}
	if stageSecrets != 0 {
		t.Fatalf("%d stage secrets remain after compensation", stageSecrets)
	}
	for _, app := range apps {
		if got := d.stageDatabase(app.ID, "production"); got != sourceDatabase {
			t.Fatalf("compensation changed production workload %s's database to %s", app.Slug, got)
		}
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, d.acct.ID, d.project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the failed stage remains: %v", err)
	}
}
