//go:build !no_pg

// adr: 585
package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresImportFixture(t *testing.T, ready bool) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresImportRequest) {
	t.Helper()
	s, ctx, pool, lease, archive := clonePostgresArchiveFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, lease, archive, archiveLimits()); err != nil {
		t.Fatal(err)
	}
	target, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, archive.Scope.SourceDatabaseID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, archive.Scope.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Truncate(time.Microsecond)
	request := state.ProjectEnvironmentClonePostgresImportRequest{Input: archiveTestReceipt(archive), Target: copyarchive.RestoreTarget{Scope: archive.Scope, OwnerID: target.TargetDatabaseID,
		ProviderResourceID: "import-independent-project", DataResourceID: "import-independent-project/br-target", EndpointID: "ep-import-target", ProviderCreatedAt: created, EndpointCreatedAt: created,
		DatabaseName: "private_import_database", RoleName: "private_import_role", DatabaseOID: 17001, RoleOID: 17002}}
	if ready {
		if _, _, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, lease, archive.Scope.SourceDatabaseID, archive.DatabaseOID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, lease, archive.Scope.SourceDatabaseID, archive.DatabaseOID, request.Input); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, archive.Scope.SourceDatabaseID, state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: request.Target.ProviderResourceID, CreatedAt: created, Prepared: true}); err != nil {
			t.Fatal(err)
		}
	}
	// State protocol fixtures use synthetic archive/SQL observations. Actual
	// import contents and errors are qualified in the copyarchive package.
	return s, ctx, pool, lease, request
}

func TestPgClonePostgresImportSingleOwnerDispatchAndWorkerHandoff(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresImportFixture(t, true)
	type result struct {
		i     state.ProjectEnvironmentClonePostgresImport
		first bool
		err   error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			i, created, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request)
			results <- result{i, created, err}
		})
	}
	wg.Wait()
	close(results)
	var owner state.ProjectEnvironmentClonePostgresImport
	creations := 0
	for got := range results {
		if got.err != nil || got.i.State != "reserved" || !got.i.MatchesTarget(request.Target) || !got.i.ImportStartedAt.IsZero() {
			t.Fatalf("reservation: %v", got.err)
		}
		if owner.ImportID == "" {
			owner = got.i
		}
		if got.i != owner {
			t.Fatal("concurrent reservation replaced import ownership")
		}
		if got.first {
			creations++
		}
	}
	if creations != 1 {
		t.Fatal("import reserved more than once")
	}
	execution := copyarchive.RestoreExecution{Input: request.Input, Target: request.Target}
	id, oid := request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID
	if _, err := s.RecordProjectEnvironmentClonePostgresImportExecution(ctx, l, id, oid, execution); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("undispatched execution recorded: %v", err)
	}
	claims := make(chan result, 6)
	for range 6 {
		wg.Go(func() {
			i, dispatch, err := s.ClaimProjectEnvironmentClonePostgresImport(ctx, l, id, oid)
			claims <- result{i, dispatch, err}
		})
	}
	wg.Wait()
	close(claims)
	dispatches := 0
	for got := range claims {
		if got.err != nil || got.i.State != "importing" || got.i.ImportID != owner.ImportID || got.i.ImportStartedAt.IsZero() {
			t.Fatalf("claim: %v", got.err)
		}
		owner = got.i
		if got.first {
			dispatches++
		}
	}
	if dispatches != 1 {
		t.Fatal("concurrent claim repeated SQL dispatch")
	}
	old := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, dispatch, err := s.ClaimProjectEnvironmentClonePostgresImport(ctx, l, id, oid); err != nil || dispatch || recovered != owner {
		t.Fatalf("handoff redispatched uncertain SQL: %v %v", dispatch, err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresImportForLease(ctx, old, id, oid); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker read import: %v", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresImportExecution(ctx, old, id, oid, execution); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker wrote execution: %v", err)
	}
	finished, err := s.RecordProjectEnvironmentClonePostgresImportExecution(ctx, l, id, oid, execution)
	if err != nil || finished.State != "executed" || finished.ExecutedAt.IsZero() || finished.ImportID != owner.ImportID {
		t.Fatalf("execution receipt: %v", err)
	}
	if replay, err := s.RecordProjectEnvironmentClonePostgresImportExecution(ctx, l, id, oid, execution); err != nil || replay != finished {
		t.Fatalf("receipt reply-loss recovery: %v", err)
	}
	if recovered, dispatch, err := s.ClaimProjectEnvironmentClonePostgresImport(ctx, l, id, oid); err != nil || dispatch || recovered != finished {
		t.Fatalf("executed SQL dispatched again: %v", err)
	}
	var private bool
	if err := pool.QueryRow(ctx, "SELECT state='provisioning' AND observed_generation=0 AND data_resource_id IS NULL FROM managed_postgres_databases WHERE id=$1", request.Target.OwnerID).Scan(&private); err != nil || !private {
		t.Fatalf("command receipt published target readiness: %v", err)
	}
	op := l.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("import command receipt forged complete capture: %v", err)
	}
}

func TestPgClonePostgresImportRejectsArchiveTargetAndSQLDescriptorSubstitution(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresImportFixture(t, true)
	owner, _, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*state.ProjectEnvironmentClonePostgresImportRequest)
	}{
		{"archive_hash", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Input.CiphertextSHA256 = strings.Repeat("f", 64)
		}},
		{"archive_length", func(r *state.ProjectEnvironmentClonePostgresImportRequest) { r.Input.PlainBytes++ }},
		{"inventory", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Input.InventoryFingerprint = strings.Repeat("f", 64)
		}},
		{"operation", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Input.Scope.OperationID = uuid.NewString()
		}},
		{"target_owner", func(r *state.ProjectEnvironmentClonePostgresImportRequest) { r.Target.OwnerID = uuid.NewString() }},
		{"provider", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Target.ProviderResourceID = "other-project"
		}},
		{"provider_time", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Target.ProviderCreatedAt = r.Target.ProviderCreatedAt.Add(time.Microsecond)
			r.Target.EndpointCreatedAt = r.Target.ProviderCreatedAt
		}},
		{"data_resource", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Target.DataResourceID = "other-project/br-other"
		}},
		{"endpoint", func(r *state.ProjectEnvironmentClonePostgresImportRequest) { r.Target.EndpointID = "ep-other" }},
		{"endpoint_time", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Target.EndpointCreatedAt = r.Target.EndpointCreatedAt.Add(time.Microsecond)
		}},
		{"sql_database", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Target.DatabaseName = "other-private-database"
		}},
		{"sql_database_oid", func(r *state.ProjectEnvironmentClonePostgresImportRequest) { r.Target.DatabaseOID++ }},
		{"sql_role", func(r *state.ProjectEnvironmentClonePostgresImportRequest) { r.Target.RoleName = "other-private-role" }},
		{"sql_role_oid", func(r *state.ProjectEnvironmentClonePostgresImportRequest) { r.Target.RoleOID++ }},
		{"target_scope", func(r *state.ProjectEnvironmentClonePostgresImportRequest) {
			r.Target.Scope.BackendFingerprint = strings.Repeat("f", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := request
			tc.edit(&changed)
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("substituted import reservation: %v", err)
			}
			if _, _, err := s.ClaimProjectEnvironmentClonePostgresImport(ctx, l, request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordProjectEnvironmentClonePostgresImportExecution(ctx, l, request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID, copyarchive.RestoreExecution{Input: changed.Input, Target: changed.Target}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("substituted execution: %v", err)
			}
		})
	}
	var raw string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(i)::text FROM project_environment_clone_postgres_imports i WHERE import_id=$1", owner.ImportID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, request.Target.DatabaseName) || strings.Contains(raw, request.Target.RoleName) {
		t.Fatal("ledger persisted private SQL names")
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_archives SET ciphertext_sha256=$1 WHERE owner_id=$2", strings.Repeat("f", 64), owner.ArchiveOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresImportForLease(ctx, l, request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retained archive changed under import: %v", err)
	}
}

func TestPgClonePostgresImportRequiresRetainedInputAndPreparedIndependentTarget(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresImportFixture(t, false)
	id, oid := request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unprepared input/target accepted: %v", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, l, id, state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: request.Target.ProviderResourceID, CreatedAt: request.Target.ProviderCreatedAt, Prepared: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unretained archive accepted: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM project_environment_clone_postgres_imports WHERE operation_id=$1", l.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected reservation retained ownership: %v", err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, id, oid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, id, oid, request.Input); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request); err != nil || !created {
		t.Fatalf("authenticated input/target rejected: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET restore_window_seconds=0,storage_limit_bytes=123456 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if replay, created, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request); err != nil || created || !replay.MatchesTarget(request.Target) {
		t.Fatalf("live source edit rebased import: %v", err)
	}
}

func TestPgClonePostgresImportMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	raw, err := migrations.FS.ReadFile("20261004095153265_environment_clone_postgres_imports.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration lacks explicit ownership-preserving rollback")
	}
	up, down := strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	shape := `SELECT
 (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_imports'::regclass AND attnum>0 AND NOT attisdropped),
 (SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_imports'::regclass)`
	var beforeColumns, beforeConstraints, afterColumns, afterConstraints string
	if err := tx.QueryRow(ctx, shape).Scan(&beforeColumns, &beforeConstraints); err != nil {
		t.Fatal(err)
	}
	verificationUp, verificationDown := cloneVerificationMigrationParts(t)
	if _, err := tx.Exec(ctx, verificationDown); err != nil {
		t.Fatal(err)
	}
	preparationUp, preparationDown := cloneImportPreparationMigrationParts(t)
	if _, err := tx.Exec(ctx, preparationDown); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, preparationUp); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, verificationUp); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&afterColumns, &afterConstraints); err != nil {
		t.Fatal(err)
	}
	if beforeColumns != afterColumns || beforeConstraints != afterConstraints {
		t.Fatal("empty import migration round trip changed table/constraints")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s, ctx, pool, l, request := clonePostgresImportFixture(t, true)
	owner, _, err := s.ReserveProjectEnvironmentClonePostgresImport(ctx, l, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, down)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("down forgot a reserved import owner: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.ProjectEnvironmentClonePostgresImportForLease(ctx, l, request.Input.Scope.SourceDatabaseID, request.Input.SourceDatabaseOID); err != nil || recovered != owner {
		t.Fatalf("refused down changed owned import: %v", err)
	}
}
