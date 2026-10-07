// adr: 521
// Backend identities and their receipts remain owned by one operation.
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/pressly/goose/v3"
)

func backendUUID(id string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true}
}

func TestPgOperationBackendIdentityLedger(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, account, app, def, tenant, otherTenant := operationFixture(t, s)
	http, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID,
		DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "http-fallback", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	row, err := q.LockCustomerOperationBackendExecution(ctx, pool, backendUUID(http.CurrentInvocationID))
	if err != nil || row.ExecutionKind != "http" || row.Generation != 1 {
		t.Fatalf("released HTTP writer lost its generated identity: %+v %v", row, err)
	}
	otherAccount, err := s.CreateAccount(ctx, "backend-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: otherAccount.ID, Slug: "backend-other", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"workflow", "job"} {
		t.Run(kind, func(t *testing.T) {
			run := backendRunFixture(t, ctx, s, kind, account.ID, app.ID, tenant.ID)
			foreignRun := backendRunFixture(t, ctx, s, kind, otherAccount.ID, otherApp.ID, "")
			var otherTenantRun string
			if kind == "workflow" {
				otherTenantRun = backendRunFixture(t, ctx, s, kind, account.ID, app.ID, otherTenant.ID)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			id := uuid.NewString()
			insertBackendOperation(t, ctx, tx, id, run, kind, account.ID, app.ID, def.ID, tenant.ID)
			if n, err := markBackend(ctx, q, tx, kind, id, foreignRun); err != nil || n != 0 {
				t.Fatalf("foreign backend acquired operation identity: %d %v", n, err)
			}
			if otherTenantRun != "" {
				if n, err := markBackend(ctx, q, tx, kind, id, otherTenantRun); err != nil || n != 0 {
					t.Fatalf("another tenant's workflow acquired operation identity: %d %v", n, err)
				}
				if n, err := bindBackend(ctx, q, tx, kind, id, otherTenantRun); err != nil || n != 0 {
					t.Fatalf("another tenant's workflow bound to operation: %d %v", n, err)
				}
			}
			if n, err := markBackend(ctx, q, tx, kind, id, run); err != nil || n != 1 {
				t.Fatalf("owned backend identity: %d %v", n, err)
			}
			if n, err := bindBackend(ctx, q, tx, kind, id, foreignRun); err != nil || n != 0 {
				t.Fatalf("foreign backend bound to operation: %d %v", n, err)
			}
			if n, err := bindBackend(ctx, q, tx, kind, id, run); err != nil || n != 1 {
				t.Fatalf("owned backend binding: %d %v", n, err)
			}
			row, err := q.LockCustomerOperationBackendExecution(ctx, tx, backendUUID(run))
			if err != nil || row.ExecutionKind != kind || row.Generation != 1 {
				t.Fatalf("backend identity was fabricated: %+v %v", row, err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("native identity did not satisfy its deferred current pointer: %v", err)
			}
			assertBackendEventOwnership(t, ctx, pool, http.ID, run)
			assertBackendGenerationFence(t, ctx, pool, id, run, kind, account.ID, app.ID, def.ID, tenant.ID)
			assertBackendMarkerSurvivesGC(t, ctx, pool, id, run, kind, account.ID, app.ID, def.ID, tenant.ID)
		})
	}
}

func TestPgOperationBackendMigrationReplayPreservesHTTPIdentity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, account, _, def, tenant, _ := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "populated-migration-replay", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.OperationEvents(ctx, account.ID, tenant.ID, op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.FS.ReadFile("20261001012633179_operation_backend_execution_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		if _, err := pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
			t.Fatalf("populated migration replay %d: %v", attempt, err)
		}
	}
	duplicate, created, err := s.AdmitOperation(ctx, admission)
	if err != nil || created || duplicate.ID != op.ID || duplicate.CurrentInvocationID != op.CurrentInvocationID {
		t.Fatalf("migration changed submission or execution identity: %+v %t %v", duplicate, created, err)
	}
	after, err := s.OperationEvents(ctx, account.ID, tenant.ID, op.ID, 0, 100)
	beforeJSON, beforeErr := json.Marshal(before)
	afterJSON, afterErr := json.Marshal(after)
	if err != nil || beforeErr != nil || afterErr != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatalf("migration changed HTTP events: %s -> %s: %v %v %v", beforeJSON, afterJSON, err, beforeErr, afterErr)
	}
}

func TestPgOperationBackendUpgradePreservesHTTPIdentity(t *testing.T) {
	pool := pgtest.Open(t)
	baseline := fstest.MapFS{}
	files, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	const identityMigration = "20261001012633179_operation_backend_execution_identity.sql"
	for _, file := range files {
		// Start from the true pre-identity schema. Later migrations may depend on
		// columns and keys introduced here and must be applied afterwards in order.
		if !strings.HasSuffix(file.Name(), ".sql") || file.Name() >= identityMigration {
			continue
		}
		data, err := migrations.FS.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		baseline[file.Name()] = &fstest.MapFile{Data: data, Mode: 0o644}
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Current CreateDeployment reads these later-added schema objects. Provision
	// only their shapes so the fixture can seed a deployment in the pre-identity
	// schema; leave the migrations unapplied for the ordered upgrade below.
	if _, err := pool.Exec(t.Context(), `CREATE TABLE workflow_automation_definitions (
		app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
		name text NOT NULL CHECK (length(name)>0),
		version bigint NOT NULL CHECK (version>0),
		draft jsonb NOT NULL CHECK (jsonb_typeof(draft)='object' AND draft->>'name'=name),
		published jsonb CHECK (published IS NULL OR (jsonb_typeof(published)='object' AND published->>'name'=name)),
		published_version bigint NOT NULL DEFAULT 0 CHECK (published_version>=0 AND published_version<=version),
		enabled boolean NOT NULL DEFAULT true,
		updated_at timestamptz NOT NULL DEFAULT now(),
		PRIMARY KEY (app_id, name),
		CHECK ((published IS NULL) = (published_version=0))
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE deployments ADD COLUMN IF NOT EXISTS environment_workload_runtime jsonb`); err != nil {
		t.Fatal(err)
	}
	// Current CreateDeployment also pins workload settings. Provision the exact
	// later-migration table shapes so the pre-identity fixture can seed its
	// deployment; leave the migrations unapplied for the ordered upgrade below.
	if _, err := pool.Exec(t.Context(), `CREATE TABLE IF NOT EXISTS project_environment_workload_specs (
		id uuid PRIMARY KEY,
		environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
		app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
		revision bigint NOT NULL CHECK (revision > 0),
		config_hash text NOT NULL CHECK (config_hash ~ '^[a-f0-9]{64}$'),
		settings json NOT NULL CHECK (json_typeof(settings) = 'object'),
		created_at timestamptz NOT NULL DEFAULT now(),
		UNIQUE (environment_id, app_id, revision),
		UNIQUE (environment_id, app_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE IF NOT EXISTS project_environment_workload_heads (
		environment_id uuid NOT NULL,
		app_id uuid NOT NULL,
		spec_id uuid NOT NULL,
		PRIMARY KEY (environment_id, app_id),
		FOREIGN KEY (environment_id, app_id, spec_id)
			REFERENCES project_environment_workload_specs(environment_id, app_id, id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE IF NOT EXISTS project_environment_workload_deployment_specs (
		deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
		spec_id uuid NOT NULL REFERENCES project_environment_workload_specs(id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatal(err)
	}
	// The current operation fixture writes these later additions while the
	// migration under test is specifically the execution-identity change.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE customer_operation_definitions ADD COLUMN IF NOT EXISTS workflow_snapshot jsonb`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE UNIQUE INDEX IF NOT EXISTS deployments_operation_code_pin_owner_idx ON deployments(id,app_id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE IF NOT EXISTS customer_operation_code_pins (
		deployment_id uuid PRIMARY KEY,
		app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
		expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
		CONSTRAINT customer_operation_code_pins_owner_fk FOREIGN KEY(deployment_id,app_id)
			REFERENCES deployments(id,app_id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	ctx := context.Background()
	account, err := s.CreateAccount(ctx, "operations@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "operations-test", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:operations", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	// This test covers the identity migration, not the later deployment-live
	// transition. Seed only the status needed by admission so that transition's
	// newer retention views do not become part of the pre-identity schema.
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status='live' WHERE id=$1`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	definition, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID,
		OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: deployment.Scope,
			DeploymentID: deployment.ID, Spec: operationSpec()}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	admission := state.OperationAdmission{AccountID: account.ID, DefinitionID: definition.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "populated-main-upgrade", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, admission)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("current main could not accept the older timestamp migration: %v", err)
	}
	duplicate, created, err := s.AdmitOperation(ctx, admission)
	if err != nil || created || duplicate.ID != op.ID || duplicate.CurrentInvocationID != op.CurrentInvocationID {
		t.Fatalf("upgrade changed HTTP identity: %+v %t %v", duplicate, created, err)
	}
	row, err := sqlc.New().LockCustomerOperationBackendExecution(ctx, pool, backendUUID(op.CurrentInvocationID))
	if err != nil || row.ExecutionKind != "http" || row.Generation != 1 {
		t.Fatalf("upgrade did not backfill the owned HTTP binding: %+v %v", row, err)
	}
}

func backendRunFixture(t *testing.T, ctx context.Context, s *state.PgStore, kind, account, app, tenant string) string {
	t.Helper()
	if kind == "workflow" {
		run := state.WorkflowRun{AppID: app, PlatformTenantID: tenant, WorkflowName: "export", DefinitionSnapshot: []byte(`{}`)}
		if err := s.CreateWorkflowRun(ctx, &run); err != nil {
			t.Fatal(err)
		}
		return run.ID
	}
	job, err := s.JobCreate(ctx, account, "backend-export", "batch", "test/image", []string{"true"}, 128, 30, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.JobRunCreate(ctx, job.ID, account, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	return run.ID
}

func insertBackendOperation(t *testing.T, ctx context.Context, db sqlc.DBTX, id, run, kind, account, app, definition, tenant string) {
	t.Helper()
	record, err := json.Marshal(map[string]any{"id": id, "account_id": account, "app_id": app,
		"definition_id": definition, "platform_tenant_id": tenant, "state": "accepted", "generation": 1,
		"current_execution_id": run, "execution_kind": kind})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = sqlc.New().InsertCustomerOperation(ctx, db, sqlc.InsertCustomerOperationParams{ID: backendUUID(id),
		AccountID: backendUUID(account), AppID: backendUUID(app), TenantID: backendUUID(tenant), DefinitionID: backendUUID(definition),
		State: "accepted", Record: record, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
}

func markBackend(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, kind, operation, run string) (int64, error) {
	if kind == "workflow" {
		return q.SetCustomerOperationWorkflowIdentity(ctx, db, sqlc.SetCustomerOperationWorkflowIdentityParams{RunID: backendUUID(run), OperationID: backendUUID(operation)})
	}
	return q.SetCustomerOperationJobIdentity(ctx, db, sqlc.SetCustomerOperationJobIdentityParams{RunID: backendUUID(run), OperationID: backendUUID(operation)})
}

func bindBackend(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, kind, operation, run string) (int64, error) {
	if kind == "workflow" {
		return q.InsertCustomerOperationWorkflowExecution(ctx, db, sqlc.InsertCustomerOperationWorkflowExecutionParams{RunID: backendUUID(run), OperationID: backendUUID(operation), Generation: 1})
	}
	return q.InsertCustomerOperationJobExecution(ctx, db, sqlc.InsertCustomerOperationJobExecutionParams{RunID: backendUUID(run), OperationID: backendUUID(operation), Generation: 1})
}

func assertBackendEventOwnership(t *testing.T, ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, operation, execution string) {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = sqlc.New().InsertCustomerOperationEvent(ctx, tx, sqlc.InsertCustomerOperationEventParams{
		OperationID: backendUUID(operation), ExecutionID: backendUUID(execution), Sequence: 99, EventType: "progress",
		Data: []byte(`{}`), CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	assertBackendForeignKey(t, tx.Commit(ctx), "customer_operation_events_execution_owner_fkey")
}

func assertBackendGenerationFence(t *testing.T, ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, operation, run, kind, account, app, definition, tenant string) {
	t.Helper()
	wrongGeneration, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wrongGeneration.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockCustomerOperationBackendExecution(ctx, wrongGeneration, backendUUID(run))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(row.Record, &record); err != nil {
		t.Fatal(err)
	}
	record["generation"] = json.RawMessage(`2`)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	err = q.UpdateCustomerOperation(ctx, wrongGeneration, sqlc.UpdateCustomerOperationParams{ID: backendUUID(operation), State: "accepted",
		Record: raw, ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	assertBackendForeignKey(t, wrongGeneration.Commit(ctx), "customer_operations_current_execution_fkey")
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	other := uuid.NewString()
	insertBackendOperation(t, ctx, tx, other, run, kind, account, app, definition, tenant)
	if n, err := markBackend(ctx, sqlc.New(), tx, kind, other, run); err != nil || n != 0 {
		t.Fatalf("backend marker was reassigned to a second operation: %d %v", n, err)
	}
	assertBackendForeignKey(t, tx.Commit(ctx), "customer_operations_current_execution_fkey")
}

func assertBackendForeignKey(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != constraint {
		t.Fatalf("expected owned backend fence %s, got %v", constraint, err)
	}
}

func assertBackendMarkerSurvivesGC(t *testing.T, ctx context.Context, db interface {
	sqlc.DBTX
	Begin(context.Context) (pgx.Tx, error)
}, operation, run, kind, account, app, definition, tenant string) {
	t.Helper()
	q := sqlc.New()
	row, err := q.LockCustomerOperationBackendExecution(ctx, db, backendUUID(run))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(row.Record, &record); err != nil {
		t.Fatal(err)
	}
	record["state"] = json.RawMessage(`"succeeded"`)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = q.UpdateCustomerOperation(ctx, db, sqlc.UpdateCustomerOperationParams{ID: backendUUID(operation), State: "succeeded",
		Record: raw, ExpiresAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := q.PruneCustomerOperations(ctx, db, sqlc.PruneCustomerOperationsParams{
		Now: pgtype.Timestamptz{Time: now, Valid: true}, PageLimit: 100})
	if err != nil || n != 1 {
		t.Fatalf("settled native projection GC: %d %v", n, err)
	}
	if _, err := q.LockCustomerOperationBackendExecution(ctx, db, backendUUID(run)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired projection retained its execution binding: %v", err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	other := uuid.NewString()
	insertBackendOperation(t, ctx, tx, other, run, kind, account, app, definition, tenant)
	if n, err := markBackend(ctx, q, tx, kind, other, run); err != nil || n != 0 {
		t.Fatalf("projection GC removed the backend replay marker: %d %v", n, err)
	}
	if n, err := bindBackend(ctx, q, tx, kind, other, run); err != nil || n != 0 {
		t.Fatalf("expired backend acquired another operation binding: %d %v", n, err)
	}
	assertBackendForeignKey(t, tx.Commit(ctx), "customer_operations_current_execution_fkey")
}
