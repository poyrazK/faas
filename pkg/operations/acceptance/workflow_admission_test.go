// adr: 521
package acceptance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// These tests exercise private SQL seams. They do not enable native admission
// or claim that the separate public workflow runtime has acquired their guards.
func workflowAdmissionFixture(t *testing.T, ctx context.Context, db sqlc.DBTX, run, app, tenant string) {
	t.Helper()
	q := sqlc.New()
	now := pgtype.Timestamptz{Time: time.Now().UTC().Add(-time.Minute), Valid: true}
	if _, err := db.Exec(ctx, `INSERT INTO workflow_runs(
		id,app_id,platform_tenant_id,workflow_name,status,input,definition_snapshot,scheduled_for,created_at,updated_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,'export-flow','pending',$4::jsonb,$5::jsonb,$6::timestamptz,$6::timestamptz,$6::timestamptz)`,
		backendUUID(run), backendUUID(app), backendUUID(tenant), []byte(`{"customer":"owned"}`), []byte(retainedWorkflowSnapshot), now,
	); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCustomerOperationWorkflowStep(ctx, db, sqlc.InsertCustomerOperationWorkflowStepParams{
		RunID: backendUUID(run), StepName: "result", Input: []byte(`{"customer":"owned"}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPgOperationWorkflowAdmissionLedger(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, account, app, def, tenant, otherTenant := operationFixture(t, s)
	q := sqlc.New()
	operation, run := uuid.NewString(), uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := q.LockNativeWorkflowAdmission(ctx, tx, app.ID); err != nil {
		t.Fatal(err)
	}
	before, err := q.CountActiveNativeWorkflowRuns(ctx, tx, backendUUID(app.ID))
	if err != nil {
		t.Fatal(err)
	}
	workflowAdmissionFixture(t, ctx, tx, run, app.ID, tenant.ID)
	insertBackendOperation(t, ctx, tx, operation, run, "workflow", account.ID, app.ID, def.ID, tenant.ID)
	if n, err := markBackend(ctx, q, tx, "workflow", operation, run); err != nil || n != 1 {
		t.Fatalf("owned workflow identity: %d %v", n, err)
	}
	if n, err := bindBackend(ctx, q, tx, "workflow", operation, run); err != nil || n != 1 {
		t.Fatalf("owned binding: %d %v", n, err)
	}
	after, err := q.CountActiveNativeWorkflowRuns(ctx, tx, backendUUID(app.ID))
	if err != nil || after != before+1 {
		t.Fatalf("shared active count: %d -> %d: %v", before, after, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetNativeWorkflowRun(ctx, pool, backendUUID(run))
	if err != nil {
		t.Fatal(err)
	}
	if got.PlatformTenantID != backendUUID(tenant.ID) || got.OperationID != backendUUID(operation) || got.Status != "pending" {
		t.Fatalf("native identity: %+v", got)
	}
	requireSnapshotJSON(t, got.Input, []byte(`{"customer":"owned"}`))
	requireSnapshotJSON(t, got.DefinitionSnapshot, []byte(retainedWorkflowSnapshot))
	var steps, invocations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_steps WHERE run_id=$1 AND step_name='result' AND status='pending' AND attempt=0 AND input='{"customer":"owned"}'::jsonb`, run).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invocations WHERE operation_id=$1`, operation).Scan(&invocations); err != nil {
		t.Fatal(err)
	}
	if steps != 1 || invocations != 0 {
		t.Fatalf("native steps=%d HTTP invocations=%d", steps, invocations)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "admission-other", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []struct{ app, tenant string }{{app.ID, otherTenant.ID}, {otherApp.ID, tenant.ID}} {
		foreign := backendRunFixture(t, ctx, s, "workflow", account.ID, owner.app, owner.tenant)
		if n, err := markBackend(ctx, q, pool, "workflow", operation, foreign); err != nil || n != 0 {
			t.Fatalf("foreign marker: %d %v", n, err)
		}
		if n, err := bindBackend(ctx, q, pool, "workflow", operation, foreign); err != nil || n != 0 {
			t.Fatalf("foreign binding: %d %v", n, err)
		}
	}
}

func TestPgOperationWorkflowLegacyIsolationAndRetention(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, account, app, def, tenant, _ := operationFixture(t, s)
	q := sqlc.New()
	operation, owned := uuid.NewString(), uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	workflowAdmissionFixture(t, ctx, tx, owned, app.ID, tenant.ID)
	insertBackendOperation(t, ctx, tx, operation, owned, "workflow", account.ID, app.ID, def.ID, tenant.ID)
	if n, err := markBackend(ctx, q, tx, "workflow", operation, owned); err != nil || n != 1 {
		t.Fatalf("owned workflow identity: %d %v", n, err)
	}
	if n, err := bindBackend(ctx, q, tx, "workflow", operation, owned); err != nil || n != 1 {
		t.Fatalf("binding: %d %v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	legacy := backendRunFixture(t, ctx, s, "workflow", account.ID, app.ID, tenant.ID)
	if got, err := q.ClaimLegacyPendingWorkflowRun(ctx, pool); err != nil || got.ID != backendUUID(legacy) {
		t.Fatalf("legacy claim picked marked run: %+v %v", got, err)
	}
	if _, err := q.ClaimLegacyPendingWorkflowRun(ctx, pool); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("marked pending run claimed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET status='succeeded',finished_at=now()-interval '2 days' WHERE id=ANY($1::uuid[])`, []string{owned, legacy}); err != nil {
		t.Fatal(err)
	}
	if n, err := q.SweepUnboundNativeWorkflowRuns(ctx, pool, 3600000); err != nil || n != 1 {
		t.Fatalf("unbound history sweep: %d %v", n, err)
	}
	if _, err := q.GetNativeWorkflowRun(ctx, pool, backendUUID(owned)); err != nil {
		t.Fatalf("bound history removed: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM customer_operations WHERE id=$1`, operation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET status='running',finished_at=NULL,lease_until=now()-interval '1 minute' WHERE id=$1`, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_steps SET status='running',attempt=3 WHERE run_id=$1`, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NextDueLegacyWorkflowRun(ctx, pool, 1000); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("marked due run exposed after projection GC: %v", err)
	}
	if _, err := q.ClaimDueLegacyWorkflowRun(ctx, pool, sqlc.ClaimDueLegacyWorkflowRunParams{ID: backendUUID(owned), StaleMs: 1000}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("marked run claimed after projection GC: %v", err)
	}
	if err := q.RecoverLegacyWorkflowRun(ctx, pool, backendUUID(owned)); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []func(context.Context, sqlc.DBTX, pgtype.UUID) error{q.MarkLegacyWorkflowOutboundUnknown, q.CloseLegacyWorkflowOutboundUnknownAttempts, q.RecoverLegacyWorkflowSteps} {
		if err := phase(ctx, pool, backendUUID(owned)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := q.GetNativeWorkflowRun(ctx, pool, backendUUID(owned))
	if err != nil || got.Status != "running" || got.OperationID != backendUUID(operation) {
		t.Fatalf("permanent marker lost: %+v %v", got, err)
	}
	var status string
	var attempt int
	if err := pool.QueryRow(ctx, `SELECT status,attempt FROM workflow_steps WHERE run_id=$1 AND step_name='result'`, owned).Scan(&status, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "running" || attempt != 3 {
		t.Fatalf("legacy recovery changed marked step: %s/%d", status, attempt)
	}
}

func TestPgOperationWorkflowLegacyRecoveryPreservesCanonicalAttempts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, account, app, _, tenant, _ := operationFixture(t, s)
	q := sqlc.New()
	cases := []struct {
		name, snapshot, step string
		resumed              bool
		wantStatus           string
		wantAttempt          int
	}{
		{"ordinary", `{"steps":[{"name":"step","handler":"/run"}]}`, "step", false, "pending", 2},
		{"resumed", `{"steps":[{"name":"step","handler":"/run"}]}`, "step", true, "pending", 3},
		{"safe-outbound", `{"steps":[{"name":"step","outbound":{"method":"GET"}}]}`, "step", false, "pending", 3},
		{"idempotent-outbound", `{"steps":[{"name":"step","outbound":{"method":"POST","idempotency_supported":true}}]}`, "step", false, "pending", 3},
		{"foreach", `{"steps":[{"name":"batch","for_each":{"action":{"handler":"/item"}}}]}`, "step", false, "pending", 3},
		{"uncertain-outbound", `{"steps":[{"name":"step","outbound":{"method":"POST"}}]}`, "step", false, "dead", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := backendRunFixture(t, ctx, s, "workflow", account.ID, app.ID, tenant.ID)
			resume := 0
			if tc.resumed {
				resume = 1
			}
			if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET status='running',definition_snapshot=$2::jsonb,resume_count=$3,lease_until=now()-interval '1 minute' WHERE id=$1`, run, tc.snapshot, resume); err != nil {
				t.Fatal(err)
			}
			var parent any
			var index any
			if tc.name == "foreach" {
				parent, index = "batch", 0
				if _, err := pool.Exec(ctx, `INSERT INTO workflow_steps(run_id,step_name,status,attempt,input,foreach_count) VALUES($1,'batch','running',1,'{}',1)`, run); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `SELECT workflow_foreach_item_name('batch',0)`).Scan(&tc.step); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, `INSERT INTO workflow_steps(run_id,step_name,status,attempt,input,outbound_attempt_token,foreach_parent,foreach_index) VALUES($1,$2,'running',3,'{}',$3,$4,$5)`, run, tc.step, uuid.NewString(), parent, index); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO workflow_step_attempts(run_id,step_name,attempt,status) VALUES($1,$2,3,'running')`, run, tc.step); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if marker, err := q.LockNativeWorkflowOwnership(ctx, tx, backendUUID(run)); err != nil || marker != "" {
				t.Fatalf("legacy recovery lock: %q %v", marker, err)
			}
			for _, phase := range []func(context.Context, sqlc.DBTX, pgtype.UUID) error{q.MarkLegacyWorkflowOutboundUnknown, q.CloseLegacyWorkflowOutboundUnknownAttempts, q.RecoverLegacyWorkflowSteps} {
				if err := phase(ctx, tx, backendUUID(run)); err != nil {
					t.Fatal(err)
				}
			}
			if err := q.RecoverLegacyWorkflowRun(ctx, tx, backendUUID(run)); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var status string
			var attempt int
			var token pgtype.UUID
			if err := pool.QueryRow(ctx, `SELECT status,attempt,outbound_attempt_token FROM workflow_steps WHERE run_id=$1 AND step_name=$2`, run, tc.step).Scan(&status, &attempt, &token); err != nil {
				t.Fatal(err)
			}
			if status != tc.wantStatus || attempt != tc.wantAttempt || token.Valid {
				t.Fatalf("canonical recovery: %s/%d token=%v", status, attempt, token.Valid)
			}
			var attemptStatus string
			if err := pool.QueryRow(ctx, `SELECT status FROM workflow_step_attempts WHERE run_id=$1 AND step_name=$2 AND attempt=3`, run, tc.step).Scan(&attemptStatus); err != nil {
				t.Fatal(err)
			}
			if (tc.resumed || tc.name != "ordinary") && attemptStatus != "failed" {
				t.Fatalf("uncertain attempt remained open: %s", attemptStatus)
			}
			claimed, err := q.ClaimDueLegacyWorkflowRun(ctx, pool, sqlc.ClaimDueLegacyWorkflowRunParams{ID: backendUUID(run), StaleMs: 1000})
			if err != nil || claimed.Status != "running" {
				t.Fatalf("due legacy claim: %+v %v", claimed, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET status='succeeded',finished_at=now() WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := q.ClaimDueLegacyWorkflowRun(ctx, pool, sqlc.ClaimDueLegacyWorkflowRunParams{ID: backendUUID(run), StaleMs: 1000}); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("terminal legacy run revived: %v", err)
			}
		})
	}
}
