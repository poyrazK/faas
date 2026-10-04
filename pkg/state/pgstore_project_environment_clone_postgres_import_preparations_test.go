//go:build !no_pg

// adr: 583
package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneBoundImportFixture(t *testing.T) (databaseSQLPinsFixture, state.ProjectEnvironmentClonePostgresDatabaseSQLPins, state.ProjectEnvironmentClonePostgresImportRequest) {
	t.Helper()
	x := cloneDatabaseSQLPinsFixture(t, true)
	pins, _, err := x.record()
	if err != nil {
		t.Fatal(err)
	}
	f := x.f
	input := archiveTestReceipt(state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: x.archive.Scope, DatabaseOID: x.oid, InventoryFingerprint: x.archive.InventoryFingerprint})
	if _, _, err = f.s.ClaimProjectEnvironmentClonePostgresArchiveUpload(f.ctx, f.l, x.archive.Scope.SourceDatabaseID, x.oid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.RecordProjectEnvironmentClonePostgresArchive(f.ctx, f.l, x.archive.Scope.SourceDatabaseID, x.oid, input); err != nil {
		t.Fatal(err)
	}
	r, err := copydatabases.OpenPreparation([]*age.X25519Identity{f.key}, f.source, x.plan, x.oid, pins.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.TargetForWorker()
	if err != nil {
		t.Fatal(err)
	}
	return x, pins, state.ProjectEnvironmentClonePostgresImportRequest{Input: input, Target: target, DatabaseSQLPinsCiphertextSHA256: pins.Sealed.CiphertextSHA256, DatabasePlanCiphertextSHA256: pins.DatabasePlanCiphertextSHA256, ArchiveReservationSHA256: pins.ArchiveReservationSHA256}
}

func TestPgClonePostgresImportPreparationConcurrentOriginalOwnershipAndHandoff(t *testing.T) {
	x, pins, request := cloneBoundImportFixture(t)
	f := x.f
	id := request.Input.Scope.SourceDatabaseID
	type result struct {
		i     state.ProjectEnvironmentClonePostgresImport
		first bool
		err   error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			i, first, err := f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, request)
			results <- result{i, first, err}
		})
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresImport
	firsts := 0
	for r := range results {
		if r.err != nil || !r.i.MatchesDatabaseSQLPins(pins) {
			t.Fatal("bound reservation", r.err)
		}
		if original.ImportID == "" {
			original = r.i
		}
		if r.i != original {
			t.Fatal("concurrent original binding changed")
		}
		if r.first {
			firsts++
		}
	}
	if firsts != 1 {
		t.Fatal("multiple original owners")
	}
	claimed, dispatch, err := f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid)
	if err != nil || !dispatch || !claimed.MatchesDatabaseSQLPins(pins) {
		t.Fatal("bound dispatch", err)
	}
	old := f.l
	if err = f.s.ReleaseProjectEnvironmentCloneLease(f.ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	f.l, err = f.s.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recovered, dispatch, err := f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid)
	if err != nil || dispatch || recovered != claimed {
		t.Fatal("handoff adopted/repeated original import", err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresImportForLease(f.ctx, old, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale recovery", err)
	}
	// SQL itself prevents deleting the original child or replacing its identity.
	for _, query := range []string{"DELETE FROM project_environment_clone_postgres_database_sql_pins WHERE operation_id=$1", "UPDATE project_environment_clone_postgres_database_sql_pins SET ciphertext_sha256=repeat('f',64) WHERE operation_id=$1"} {
		_, err = f.pool.Exec(f.ctx, query, f.l.Operation.ID)
		var e *pgconn.PgError
		if !errors.As(err, &e) || e.Code != "23503" {
			t.Fatal("original child FK did not retain ownership", err)
		}
	}
}

func TestPgClonePostgresImportPreparationRejectsChangedPrerequisitesAtEveryState(t *testing.T) {
	x, pins, request := cloneBoundImportFixture(t)
	f := x.f
	id := request.Input.Scope.SourceDatabaseID
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, request); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"reserved", "importing", "executed"} {
		if phase == "importing" {
			if _, _, err := f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "executed" {
			if _, err := f.s.RecordProjectEnvironmentClonePostgresImportExecution(f.ctx, f.l, id, x.oid, copyarchive.RestoreExecution{Input: request.Input, Target: request.Target}); err != nil {
				t.Fatal(err)
			}
		}
		for _, table := range []string{"project_environment_clone_postgres_inventories", "project_environment_clone_postgres_target_sql_pins", "project_environment_clone_postgres_role_plans", "project_environment_clone_postgres_database_plans", "project_environment_clone_postgres_database_sql_pins"} {
			t.Run(phase+"/"+table, func(t *testing.T) {
				var cipher []byte
				var hash string
				if err := f.pool.QueryRow(f.ctx, "SELECT ciphertext,ciphertext_sha256 FROM "+table+" WHERE operation_id=$1", f.l.Operation.ID).Scan(&cipher, &hash); err != nil {
					t.Fatal(err)
				}
				// Parent re-encryption changes lineage; the child test corrupts retained
				// bytes without changing its FK identity to exercise digest validation.
				changed := append(bytes.Clone(cipher), 0)
				newHash := hash
				if table != "project_environment_clone_postgres_database_sql_pins" {
					h := sha256.Sum256(changed)
					newHash = hex.EncodeToString(h[:])
				}
				if _, err := f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", changed, newHash, f.l.Operation.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.ProjectEnvironmentClonePostgresImportForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
					t.Fatal("changed prerequisite replayed import", err)
				}
				if _, _, err := f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
					t.Fatal("changed prerequisite claimed import", err)
				}
				if _, err := f.s.RecordProjectEnvironmentClonePostgresImportExecution(f.ctx, f.l, id, x.oid, copyarchive.RestoreExecution{Input: request.Input, Target: request.Target}); !errors.Is(err, state.ErrConflict) {
					t.Fatal("changed prerequisite completed import", err)
				}
				if _, err := f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", cipher, hash, f.l.Operation.ID); err != nil {
					t.Fatal(err)
				}
			})
		}
		if i, err := f.s.ProjectEnvironmentClonePostgresImportForLease(f.ctx, f.l, id, x.oid); err != nil || i.State != phase || !i.MatchesDatabaseSQLPins(pins) {
			t.Fatal("failed check changed original ownership", err)
		}
	}
}

func TestPgClonePostgresImportPreparationRejectsSubstitutionAndNeverAdoptsLegacyOwners(t *testing.T) {
	for _, phase := range []string{"reserved", "importing", "executed"} {
		t.Run(phase, func(t *testing.T) {
			x, _, request := cloneBoundImportFixture(t)
			f := x.f
			id := request.Input.Scope.SourceDatabaseID
			for _, field := range []string{"partial", "malformed", "child", "plan", "archive"} {
				bad := request
				switch field {
				case "partial":
					bad.ArchiveReservationSHA256 = ""
				case "malformed":
					bad.DatabaseSQLPinsCiphertextSHA256 = "invalid"
				case "child":
					bad.DatabaseSQLPinsCiphertextSHA256 = strings.Repeat("f", 64)
				case "plan":
					bad.DatabasePlanCiphertextSHA256 = strings.Repeat("f", 64)
				case "archive":
					bad.ArchiveReservationSHA256 = strings.Repeat("f", 64)
				}
				if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, bad); err == nil {
					t.Fatal("substituted first binding", field)
				}
			}
			legacy := request
			legacy.DatabaseSQLPinsCiphertextSHA256 = ""
			legacy.DatabasePlanCiphertextSHA256 = ""
			legacy.ArchiveReservationSHA256 = ""
			original, _, err := f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, legacy)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "reserved" {
				original, _, err = f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "executed" {
				original, err = f.s.RecordProjectEnvironmentClonePostgresImportExecution(f.ctx, f.l, id, x.oid, copyarchive.RestoreExecution{Input: request.Input, Target: request.Target})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err = f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, request); !errors.Is(err, state.ErrConflict) {
				t.Fatal("retroactively adopted legacy SQL attempt", err)
			}
			if recovered, err := f.s.ProjectEnvironmentClonePostgresImportForLease(f.ctx, f.l, id, x.oid); err != nil || recovered != original {
				t.Fatal("rejection erased legacy ownership", err)
			}
		})
	}
}

func cloneImportPreparationMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261004095153288_environment_clone_postgres_import_preparations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("import preparation migration lacks rollback")
	}
	return strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
}

func TestPgClonePostgresImportPreparationMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	up, down := cloneImportPreparationMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	shape := `SELECT (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_imports'::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_get_constraintdef(oid)) ORDER BY conrelid::regclass::text,conname)::text FROM pg_constraint WHERE conrelid IN ('project_environment_clone_postgres_imports'::regclass,'project_environment_clone_postgres_database_sql_pins'::regclass))`
	var a, b, c, d string
	if err = tx.QueryRow(ctx, shape).Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	verificationUp, verificationDown := cloneVerificationMigrationParts(t)
	if _, err := tx.Exec(ctx, verificationDown); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, verificationUp); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, shape).Scan(&c, &d); err != nil || a != c || b != d {
		t.Fatal("round trip changed preparation binding shape", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	x, _, request := cloneBoundImportFixture(t)
	f := x.f
	id := request.Input.Scope.SourceDatabaseID
	original, _, err := f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"reserved", "importing", "executed"} {
		if phase == "importing" {
			original, _, err = f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid)
			if err != nil {
				t.Fatal(err)
			}
		}
		if phase == "executed" {
			original, err = f.s.RecordProjectEnvironmentClonePostgresImportExecution(f.ctx, f.l, id, x.oid, copyarchive.RestoreExecution{Input: request.Input, Target: request.Target})
			if err != nil {
				t.Fatal(err)
			}
		}
		tx, err = f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(f.ctx, down)
		var e *pgconn.PgError
		if !errors.As(err, &e) || e.Code != "23514" {
			t.Fatal("rollback erased original import preparation", err)
		}
		if err = tx.Rollback(f.ctx); err != nil {
			t.Fatal(err)
		}
		if got, err := f.s.ProjectEnvironmentClonePostgresImportForLease(f.ctx, f.l, id, x.oid); err != nil || got != original {
			t.Fatal("refused down changed import ownership", err)
		}
	}
}
