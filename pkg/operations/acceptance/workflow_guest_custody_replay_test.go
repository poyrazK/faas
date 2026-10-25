// adr: 521
package acceptance_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgOperationWorkflowGuestCustodySurvivesOriginalBackendMigrationReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx, acct, app, def, tenant, _ := operationFixture(t, state.NewPgStore(pool))
	q := sqlc.New()
	claim := custodyLedgerFixture(t, ctx, pool, acct.ID, app.ID, def.ID, tenant.ID)
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_step_attempts(run_id,step_name,attempt,status) VALUES($1,'result',1,'running')`, claim.RunID); err != nil {
		t.Fatal(err)
	}
	binding := sqlc.InsertCustomerOperationWorkflowGuestParams{
		RunID: claim.RunID, StepName: "result", StepAttempt: 1,
		OperationID: claim.OperationID, Generation: claim.Generation, CoordinatorAttempt: claim.Attempt,
		CapabilityDigest: strings.Repeat("b", 64), DeadlineAt: claim.LeaseUntil,
	}
	if err := q.InsertCustomerOperationWorkflowGuest(ctx, pool, binding); err != nil {
		t.Fatal(err)
	}
	key := sqlc.GetCustomerOperationWorkflowGuestParams{RunID: claim.RunID, StepName: "result", StepAttempt: 1}
	before, err := q.GetCustomerOperationWorkflowGuest(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261001012633179_operation_backend_execution_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := pool.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
			t.Fatalf("original backend migration replay %d with active guest custody: %v", i, err)
		}
		after, err := q.GetCustomerOperationWorkflowGuest(ctx, pool, key)
		if err != nil || after != before {
			t.Fatalf("backend replay changed active guest custody: %+v %v", after, err)
		}
	}
	foreign := custodyLedgerFixture(t, ctx, pool, acct.ID, app.ID, def.ID, tenant.ID)
	cases := []struct {
		name, query, code string
		arg               any
	}{
		{"foreign-operation", `UPDATE customer_operation_workflow_guest_claims SET operation_id=$2 WHERE workflow_run_id=$1`, "23503", foreign.OperationID},
		{"wrong-generation", `UPDATE customer_operation_workflow_guest_claims SET generation=$2 WHERE workflow_run_id=$1`, "23503", claim.Generation + 1},
		{"wrong-kind", `UPDATE customer_operation_workflow_guest_claims SET execution_kind=$2 WHERE workflow_run_id=$1`, "23514", "job"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.query, claim.RunID, tc.arg)
			var problem *pgconn.PgError
			if !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("replayed guest custody accepted foreign identity or kind: %+v %v", problem, err)
			}
			after, err := q.GetCustomerOperationWorkflowGuest(ctx, pool, key)
			if err != nil || after != before {
				t.Fatalf("refused mutation changed active guest custody: %+v %v", after, err)
			}
		})
	}
}
