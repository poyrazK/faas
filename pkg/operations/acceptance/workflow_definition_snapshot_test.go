// adr: 521
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const retainedWorkflowSnapshot = `{"name":"export-flow","steps":[{"name":"generating","path":"/exports"}]}`

func workflowDefinitionFixture(t *testing.T, def state.OperationDefinition, name string) sqlc.InsertCustomerOperationDefinitionParams {
	t.Helper()
	return sqlc.InsertCustomerOperationDefinitionParams{
		ID: backendUUID(uuid.NewString()), AccountID: backendUUID(def.AccountID), AppID: backendUUID(def.AppID),
		Scope: def.Scope, Name: name, Revision: strings.Repeat("a", 64), DeploymentID: backendUUID(def.DeploymentID),
		Spec:             []byte(fmt.Sprintf(`{"name":%q,"workflow":"export-flow","method":"POST","path":"/workflow-exports","owner":"platform_tenant"}`, name)),
		WorkflowSnapshot: []byte(retainedWorkflowSnapshot),
	}
}

func requireSnapshotJSON(t *testing.T, actual, expected []byte) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatalf("invalid retained snapshot: %v", err)
	}
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("retained snapshot changed: %s != %s", gotJSON, wantJSON)
	}
}

func workflowSnapshotMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261001015241186_operation_workflow_definition_snapshots.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("snapshot migration has no unique Up/Down boundary")
	}
	return parts[0], parts[1]
}

func TestPgOperationWorkflowDefinitionSnapshotLedger(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, app, httpDef, _, _ := operationFixture(t, s)
	q := sqlc.New()
	httpRow, err := q.GetCustomerOperationDefinition(ctx, pool, sqlc.GetCustomerOperationDefinitionParams{
		ID: backendUUID(httpDef.ID), AccountID: backendUUID(acct.ID),
	})
	if err != nil || httpRow.WorkflowSnapshot != nil {
		t.Fatalf("HTTP definition gained a private workflow target: %+v %v", httpRow, err)
	}
	params := workflowDefinitionFixture(t, httpDef, "workflow-export")
	native, err := q.InsertCustomerOperationDefinition(ctx, pool, params)
	if err != nil {
		t.Fatal(err)
	}
	requireSnapshotJSON(t, native.WorkflowSnapshot, params.WorkflowSnapshot)
	owned := sqlc.CustomerOperationDeploymentDefinitionParams{
		DeploymentID: backendUUID(httpDef.DeploymentID), AppID: backendUUID(app.ID), AccountID: backendUUID(acct.ID),
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET workflows=$2::jsonb WHERE id=$1`, httpDef.DeploymentID, `[`+retainedWorkflowSnapshot+`]`); err != nil {
		t.Fatal(err)
	}
	selected, err := q.CustomerOperationDeploymentDefinition(ctx, pool, owned)
	if err != nil || selected.Scope != httpDef.Scope {
		t.Fatalf("owner could not read selected deployment workflows: %+v %v", selected, err)
	}
	requireSnapshotJSON(t, selected.Workflows, []byte(`[`+retainedWorkflowSnapshot+`]`))
	otherAcct, err := s.CreateAccount(ctx, "snapshot-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-other", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	foreignAccount, foreignApp := owned, owned
	foreignAccount.AccountID = backendUUID(otherAcct.ID)
	foreignApp.AppID = backendUUID(otherApp.ID)
	for _, selector := range []sqlc.CustomerOperationDeploymentDefinitionParams{foreignAccount, foreignApp} {
		if _, err := q.CustomerOperationDeploymentDefinition(ctx, pool, selector); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("foreign selector read the deployment DAG: %v", err)
		}
	}
	// Deployment metadata can change during administrative repair. Existing
	// definitions must keep the DAG retained when the definition was inserted.
	if _, err := pool.Exec(ctx, `UPDATE deployments SET workflows=$2::jsonb WHERE id=$1`, httpDef.DeploymentID, `[{"name":"replacement-flow","steps":[]}]`); err != nil {
		t.Fatal(err)
	}
	assertWorkflowDefinitionSnapshotReads(t, ctx, pool, q, httpDef, native.ID, params.WorkflowSnapshot)
	up, _ := workflowSnapshotMigrationParts(t)
	for attempt := range 2 {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("populated snapshot migration replay %d: %v", attempt, err)
		}
		assertWorkflowDefinitionSnapshotReads(t, ctx, pool, q, httpDef, native.ID, params.WorkflowSnapshot)
	}
}

func assertWorkflowDefinitionSnapshotReads(t *testing.T, ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, httpDef state.OperationDefinition, id string, snapshot []byte) {
	t.Helper()
	row, err := q.GetCustomerOperationDefinition(ctx, db, sqlc.GetCustomerOperationDefinitionParams{ID: backendUUID(id), AccountID: backendUUID(httpDef.AccountID)})
	if err != nil {
		t.Fatal(err)
	}
	requireSnapshotJSON(t, row.WorkflowSnapshot, snapshot)
	byDeployment, err := q.GetCustomerOperationDefinitionForDeployment(ctx, db, sqlc.GetCustomerOperationDefinitionForDeploymentParams{
		AppID: backendUUID(httpDef.AppID), AccountID: backendUUID(httpDef.AccountID), DeploymentID: backendUUID(httpDef.DeploymentID), Name: "workflow-export",
	})
	if err != nil || byDeployment.ID != id {
		t.Fatalf("deployment query lost retained definition: %+v %v", byDeployment, err)
	}
	requireSnapshotJSON(t, byDeployment.WorkflowSnapshot, snapshot)
	rows, err := q.ListCustomerOperationDefinitionsForDeployment(ctx, db, sqlc.ListCustomerOperationDefinitionsForDeploymentParams{
		AppID: backendUUID(httpDef.AppID), AccountID: backendUUID(httpDef.AccountID), DeploymentID: backendUUID(httpDef.DeploymentID),
	})
	if err != nil || len(rows) != 2 {
		t.Fatalf("definition inventory changed: %+v %v", rows, err)
	}
	found := false
	for _, item := range rows {
		if item.ID == id {
			found = true
			requireSnapshotJSON(t, item.WorkflowSnapshot, snapshot)
		} else if item.ID != httpDef.ID || item.WorkflowSnapshot != nil {
			t.Fatalf("HTTP definition inventory changed: %+v", item)
		}
	}
	if !found {
		t.Fatal("list omitted the retained workflow definition")
	}
	byRoute, err := q.GetCustomerOperationDefinitionForRoute(ctx, db, sqlc.GetCustomerOperationDefinitionForRouteParams{
		AccountID: backendUUID(httpDef.AccountID), AppID: backendUUID(httpDef.AppID), DeploymentID: backendUUID(httpDef.DeploymentID),
		Method: httpDef.Spec.Method, Path: httpDef.Spec.Path,
	})
	if err != nil || byRoute.ID != httpDef.ID || byRoute.WorkflowSnapshot != nil {
		t.Fatalf("workflow target replaced the existing HTTP route: %+v %v", byRoute, err)
	}
}

func TestPgOperationWorkflowDefinitionTargetConstraints(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, _, _, def, _, _ := operationFixture(t, s)
	q := sqlc.New()
	cases := []struct {
		name   string
		mutate func(map[string]any, *sqlc.InsertCustomerOperationDefinitionParams)
	}{
		{"missing-snapshot", func(_ map[string]any, p *sqlc.InsertCustomerOperationDefinitionParams) { p.WorkflowSnapshot = nil }},
		{"non-object-snapshot", func(_ map[string]any, p *sqlc.InsertCustomerOperationDefinitionParams) {
			p.WorkflowSnapshot = []byte(`[]`)
		}},
		{"different-snapshot-name", func(_ map[string]any, p *sqlc.InsertCustomerOperationDefinitionParams) {
			p.WorkflowSnapshot = []byte(`{"name":"other","steps":[]}`)
		}},
		{"missing-snapshot-steps", func(_ map[string]any, p *sqlc.InsertCustomerOperationDefinitionParams) {
			p.WorkflowSnapshot = []byte(`{"name":"export-flow"}`)
		}},
		{"non-array-snapshot-steps", func(_ map[string]any, p *sqlc.InsertCustomerOperationDefinitionParams) {
			p.WorkflowSnapshot = []byte(`{"name":"export-flow","steps":{}}`)
		}},
		{"missing-workflow-name", func(spec map[string]any, _ *sqlc.InsertCustomerOperationDefinitionParams) {
			delete(spec, "workflow")
		}},
		{"empty-workflow-name", func(spec map[string]any, _ *sqlc.InsertCustomerOperationDefinitionParams) {
			spec["workflow"] = ""
		}},
		{"non-string-workflow-name", func(spec map[string]any, _ *sqlc.InsertCustomerOperationDefinitionParams) {
			spec["workflow"] = map[string]any{"name": "export-flow"}
		}},
		{"http-with-private-snapshot", func(spec map[string]any, _ *sqlc.InsertCustomerOperationDefinitionParams) {
			delete(spec, "workflow")
		}},
		{"null-workflow", func(spec map[string]any, _ *sqlc.InsertCustomerOperationDefinitionParams) { spec["workflow"] = nil }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := workflowDefinitionFixture(t, def, fmt.Sprintf("invalid-workflow-%d", i))
			var spec map[string]any
			if err := json.Unmarshal(params.Spec, &spec); err != nil {
				t.Fatal(err)
			}
			tc.mutate(spec, &params)
			var err error
			params.Spec, err = json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			_, err = q.InsertCustomerOperationDefinition(ctx, pool, params)
			var constraint *pgconn.PgError
			if !errors.As(err, &constraint) || constraint.Code != "23514" || constraint.ConstraintName != "customer_operation_definition_target" {
				t.Fatalf("invalid target did not hit its shape constraint: %v", err)
			}
		})
	}
}

func TestPgOperationWorkflowSnapshotMigrationPreservesHTTPIdentity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, _, def, tenant, _ := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID,
		IdempotencyKey: "snapshot-http-upgrade", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.OperationEvents(ctx, acct.ID, tenant.ID, op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	up, down := workflowSnapshotMigrationParts(t)
	// Restore only this table's previous schema with real HTTP rows in place.
	// Other incoming main tables and the test database's migration history stay
	// intact; this is a schema compatibility test, not an old-binary rollout.
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var hasSnapshot bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='customer_operation_definitions' AND column_name='workflow_snapshot')`).Scan(&hasSnapshot); err != nil || hasSnapshot {
		t.Fatalf("fixture did not restore the previous definition schema: %t %v", hasSnapshot, err)
	}
	for attempt := range 3 {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("HTTP schema upgrade/replay %d: %v", attempt, err)
		}
		duplicate, created, err := s.AdmitOperation(ctx, admission)
		if err != nil || created || duplicate.ID != op.ID || duplicate.Generation != op.Generation || duplicate.CurrentInvocationID != op.CurrentInvocationID {
			t.Fatalf("snapshot migration changed existing HTTP work: %+v %t %v", duplicate, created, err)
		}
		currentDef, err := s.OperationDefinitionByID(ctx, acct.ID, def.ID)
		if err != nil || currentDef.Revision != def.Revision || currentDef.DeploymentID != def.DeploymentID {
			t.Fatalf("snapshot migration changed the pinned HTTP definition: %+v %v", currentDef, err)
		}
		after, err := s.OperationEvents(ctx, acct.ID, tenant.ID, op.ID, 0, 100)
		beforeJSON, beforeErr := json.Marshal(before)
		afterJSON, afterErr := json.Marshal(after)
		if err != nil || beforeErr != nil || afterErr != nil || string(beforeJSON) != string(afterJSON) {
			t.Fatalf("snapshot migration changed HTTP events: %s -> %s: %v %v %v", beforeJSON, afterJSON, err, beforeErr, afterErr)
		}
	}
}
