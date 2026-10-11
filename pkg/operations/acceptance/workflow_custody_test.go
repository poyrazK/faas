// adr: 521
package acceptance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func custodyLedgerFixture(t *testing.T, ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, account, app, definition, tenant string) sqlc.PutCustomerOperationWorkflowCustodyParams {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, run := uuid.NewString(), uuid.NewString()
	workflowAdmissionFixture(t, ctx, tx, run, app, tenant)
	insertBackendOperation(t, ctx, tx, operation, run, "workflow", account, app, definition, tenant)
	q := sqlc.New()
	if n, err := markBackend(ctx, q, tx, "workflow", operation, run); err != nil || n != 1 {
		t.Fatalf("custody workflow identity: %d %v", n, err)
	}
	if n, err := bindBackend(ctx, q, tx, "workflow", operation, run); err != nil || n != 1 {
		t.Fatalf("custody backend binding: %d %v", n, err)
	}
	claim := sqlc.PutCustomerOperationWorkflowCustodyParams{RunID: backendUUID(run), OperationID: backendUUID(operation), Generation: 1, Attempt: 1, CapabilityDigest: strings.Repeat("a", 64), LeaseUntil: pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Microsecond).Add(api.OperationExecutionLeaseMax), Valid: true}}
	if err := q.PutCustomerOperationWorkflowCustody(ctx, tx, claim); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestPgOperationWorkflowCustodyLedgerConstraints(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, app, def, tenant, _ := operationFixture(t, s)
	q := sqlc.New()
	claim := custodyLedgerFixture(t, ctx, pool, acct.ID, app.ID, def.ID, tenant.ID)
	before, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, code string
		change     func(*sqlc.PutCustomerOperationWorkflowCustodyParams)
	}{
		{"zero-attempt", "23514", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.Attempt = 0 }},
		{"negative-attempt", "23514", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.Attempt = -1 }},
		{"zero-generation", "23514", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.Generation = 0 }},
		{"wrong-generation", "23503", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.Generation = 2 }},
		{"foreign-operation", "23503", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) { p.OperationID = backendUUID(uuid.NewString()) }},
		{"malformed-digest", "23514", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) {
			p.CapabilityDigest = "presented-raw-capability"
		}},
		{"infinite-lease", "23514", func(p *sqlc.PutCustomerOperationWorkflowCustodyParams) {
			p.LeaseUntil = pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := claim
			tc.change(&changed)
			var problem *pgconn.PgError
			if err := q.PutCustomerOperationWorkflowCustody(ctx, pool, changed); !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("custody fence: %+v %v", problem, err)
			}
			after, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
			if err != nil || after != before {
				t.Fatalf("rejected custody changed stored capability: %+v %v", after, err)
			}
		})
	}
}

func TestPgOperationWorkflowCustodyCandidateAndWakeLedger(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, app, def, tenant, otherTenant := operationFixture(t, s)
	q := sqlc.New()
	claim := custodyLedgerFixture(t, ctx, pool, acct.ID, app.ID, def.ID, tenant.ID)
	if _, err := q.NextCustomerOperationWorkflowRun(ctx, pool); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("live custody exposed early wake: %v", err)
	}
	expired := pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute), Valid: true}
	if err := q.RenewCustomerOperationWorkflowCustody(ctx, pool, sqlc.RenewCustomerOperationWorkflowCustodyParams{RunID: claim.RunID, LeaseUntil: expired}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := q.LockCustomerOperationWorkflowRun(ctx, tx, claim.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NextCustomerOperationWorkflowRun(ctx, pool); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("candidate stole locked native run: %v", err)
	}
	if err := q.RenewCustomerOperationWorkflowCustody(ctx, tx, sqlc.RenewCustomerOperationWorkflowCustodyParams{RunID: claim.RunID, LeaseUntil: claim.LeaseUntil}); err != nil {
		t.Fatal(err)
	}
	fresh, err := q.GetCustomerOperationWorkflowCustody(ctx, tx, claim.RunID)
	if err != nil || !fresh.LeaseUntil.Time.Equal(claim.LeaseUntil.Time) {
		t.Fatalf("fresh read after run lock: %+v %v", fresh, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NextCustomerOperationWorkflowRun(ctx, pool); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("renewed custody stolen: %v", err)
	}
	if err := q.RevokeCustomerOperationWorkflowCustody(ctx, pool, claim.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET platform_tenant_id=$2 WHERE id=$1`, claim.RunID, otherTenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NextCustomerOperationWorkflowRun(ctx, pool); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("different tenant selected: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET platform_tenant_id=$2 WHERE id=$1`, claim.RunID, tenant.ID); err != nil {
		t.Fatal(err)
	}
	got, err := q.NextCustomerOperationWorkflowRun(ctx, pool)
	if err != nil || got.ID != claim.RunID {
		t.Fatalf("owned expired candidate: %+v %v", got, err)
	}
	if err := q.StartCustomerOperationWorkflowRun(ctx, pool, sqlc.StartCustomerOperationWorkflowRunParams{ID: claim.RunID, OperationID: claim.OperationID, LeaseUntil: claim.LeaseUntil}); err != nil {
		t.Fatal(err)
	}
	if err := q.RenewCustomerOperationWorkflowRun(ctx, pool, sqlc.RenewCustomerOperationWorkflowRunParams{ID: claim.RunID, LeaseUntil: claim.LeaseUntil}); err != nil {
		t.Fatal(err)
	}
	wake := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET status='pending',scheduled_for=$2 WHERE id=$1`, claim.RunID, wake); err != nil {
		t.Fatal(err)
	}
	if err := q.ParkCustomerOperationWorkflowRun(ctx, pool, sqlc.ParkCustomerOperationWorkflowRunParams{ID: claim.RunID, Status: "awaiting_event", DueAt: pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	parked, err := q.GetNativeWorkflowRun(ctx, pool, claim.RunID)
	if err != nil || parked.Status != "pending" || !parked.ScheduledFor.Time.Equal(wake) || parked.LeaseUntil.Valid {
		t.Fatalf("parking lost earlier callback wake: %+v %v", parked, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_steps SET status='running',attempt=2 WHERE run_id=$1`, claim.RunID); err != nil {
		t.Fatal(err)
	}
	running, err := q.CustomerOperationWorkflowHasRunningStep(ctx, pool, claim.RunID)
	if err != nil || !running {
		t.Fatalf("unresolved step hidden: %v %v", running, err)
	}
	if err := q.StopUncertainCustomerOperationWorkflow(ctx, pool, claim.RunID); err != nil {
		t.Fatal(err)
	}
	stopped, err := q.GetNativeWorkflowRun(ctx, pool, claim.RunID)
	if err != nil || stopped.Status != "failed" || !stopped.FinishedAt.Valid {
		t.Fatalf("uncertain native evidence: %+v %v", stopped, err)
	}
	running, err = q.CustomerOperationWorkflowHasRunningStep(ctx, pool, claim.RunID)
	if err != nil || !running {
		t.Fatalf("uncertain stop erased step evidence: %v %v", running, err)
	}
	status, err := q.CustomerOperationWorkflowTenantStatus(ctx, pool, sqlc.CustomerOperationWorkflowTenantStatusParams{TenantID: backendUUID(tenant.ID), AccountID: backendUUID(acct.ID)})
	if err != nil || status != "active" {
		t.Fatalf("tenant status: %q %v", status, err)
	}
	if _, err := q.CustomerOperationWorkflowTenantStatus(ctx, pool, sqlc.CustomerOperationWorkflowTenantStatusParams{TenantID: backendUUID(tenant.ID), AccountID: backendUUID(uuid.NewString())}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign account read tenant state: %v", err)
	}
}

func TestPgOperationWorkflowCustodyMigrationReplayPreservesIdentity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, app, def, tenant, _ := operationFixture(t, s)
	q := sqlc.New()
	claim := custodyLedgerFixture(t, ctx, pool, acct.ID, app.ID, def.ID, tenant.ID)
	before, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	http, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "custody-http-stable", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261001050027299_operation_workflow_coordinator_claims.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := pool.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
			t.Fatalf("populated replay %d: %v", i, err)
		}
	}
	after, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID)
	if err != nil || after != before {
		t.Fatalf("migration replay changed custody identity: %+v %v", after, err)
	}
	duplicate, created, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "custody-http-stable", Input: []byte(`{"count":1}`)})
	if err != nil || created || duplicate.ID != http.ID || duplicate.CurrentInvocationID != http.CurrentInvocationID {
		t.Fatalf("custody replay changed HTTP operation: %+v %v", duplicate, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM customer_operations WHERE id=$1`, claim.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetCustomerOperationWorkflowCustody(ctx, pool, claim.RunID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("custody survived execution binding retention: %v", err)
	}
	if native, err := q.GetNativeWorkflowRun(ctx, pool, claim.RunID); err != nil || native.OperationID != claim.OperationID {
		t.Fatalf("projection GC erased permanent native marker: %+v %v", native, err)
	}
}
