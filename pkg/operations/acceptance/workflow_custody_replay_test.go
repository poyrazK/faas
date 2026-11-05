// adr: 521
package acceptance_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgOperationWorkflowCustodySurvivesOriginalBackendMigrationReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx, acct, app, def, tenant, _ := operationFixture(t, state.NewPgStore(pool))
	q := sqlc.New()
	claim := custodyLedgerFixture(t, ctx, pool, acct.ID, app.ID, def.ID, tenant.ID)
	before, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261001012633179_operation_backend_execution_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := pool.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
			t.Fatalf("original backend migration replay %d with active custody: %v", i, err)
		}
		after, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
		if err != nil || after != before {
			t.Fatalf("backend replay changed active custody: %+v %v", after, err)
		}
	}
	for _, change := range []func(*sqlc.PutCustomerOperationWorkflowCustodyParams){
		func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.Generation++ },
		func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.OperationID = backendUUID(uuid.NewString()) },
	} {
		foreign := claim
		change(&foreign)
		var problem *pgconn.PgError
		if err := q.PutCustomerOperationWorkflowCustody(ctx, pool, foreign); !errors.As(err, &problem) || problem.Code != "23503" {
			t.Fatalf("replayed custody accepted foreign operation or generation: %+v %v", problem, err)
		}
		after, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
		if err != nil || after != before {
			t.Fatalf("replay fence refusal changed active custody: %+v %v", after, err)
		}
	}
}
