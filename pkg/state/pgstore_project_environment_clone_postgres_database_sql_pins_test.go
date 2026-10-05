//go:build !no_pg

// adr: 585
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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/state"
)

type databaseSQLPinsFixture struct {
	f       rolePlanFixture
	plan    copydatabases.Plan
	parent  state.ProjectEnvironmentClonePostgresDatabasePlan
	archive state.ProjectEnvironmentClonePostgresArchive
	sealed  copydatabases.SealedPreparation
	oid     uint32
}

func cloneDatabaseSQLPinsFixture(t *testing.T, retainArchive bool) databaseSQLPinsFixture {
	t.Helper()
	f, plan, sealed := cloneDatabasePlanFixture(t, true)
	parent, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, f.target.Scope.SourceDatabaseID, f.sealed.CiphertextSHA256, sealed)
	if err != nil {
		t.Fatal(err)
	}
	var oid uint32
	if err = f.pool.QueryRow(f.ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	c, err := f.pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := copydatabases.Prepare(f.ctx, c.Conn(), f.source, plan, oid, func(context.Context, copyarchive.RestoreTarget) error { return nil })
	c.Release()
	if err != nil {
		t.Fatal(err)
	}
	pins, err := copydatabases.SealPreparation(f.key.Recipient(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	var archive state.ProjectEnvironmentClonePostgresArchive
	if retainArchive {
		archive, _, err = f.s.ReserveProjectEnvironmentClonePostgresArchive(f.ctx, f.l, state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: f.target.Scope, DatabaseOID: oid, InventoryFingerprint: parent.Sealed.InventoryFingerprint, KeyID: f.key.Recipient().String(), StorageID: "private-artifacts", StorageFingerprint: strings.Repeat("d", 64), ReservedBytes: 8192}, archiveLimits())
		if err != nil {
			t.Fatal(err)
		}
	}
	return databaseSQLPinsFixture{f, plan, parent, archive, pins, oid}
}

func (x databaseSQLPinsFixture) record() (state.ProjectEnvironmentClonePostgresDatabaseSQLPins, bool, error) {
	return x.f.s.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(x.f.ctx, x.f.l, x.f.target.Scope.SourceDatabaseID, x.oid, x.parent.Sealed.CiphertextSHA256, x.archive.ReservationFingerprint(), x.sealed)
}

func TestPgClonePostgresDatabaseSQLPinsConcurrentOriginalOwnershipAndHandoff(t *testing.T) {
	x := cloneDatabaseSQLPinsFixture(t, true)
	type result struct {
		r     state.ProjectEnvironmentClonePostgresDatabaseSQLPins
		first bool
		err   error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() { r, first, err := x.record(); results <- result{r, first, err} })
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresDatabaseSQLPins
	firsts := 0
	for r := range results {
		if r.err != nil || !bytes.Equal(r.r.Sealed.Ciphertext, x.sealed.Ciphertext) || r.r.ArchiveOwnerID != x.archive.OwnerID {
			t.Fatal("concurrent pin ownership", r.err)
		}
		if original.CapturedAt.IsZero() {
			original = r.r
		}
		if !r.r.CapturedAt.Equal(original.CapturedAt) {
			t.Fatal("replaced first ownership time")
		}
		if r.first {
			firsts++
		}
	}
	if firsts != 1 {
		t.Fatal("multiple first pin owners")
	}
	other, _ := age.GenerateX25519Identity()
	r, err := copydatabases.OpenPreparation([]*age.X25519Identity{other, x.f.key}, x.f.source, x.plan, x.oid, original.Sealed)
	if err != nil || r.CreatedAt().IsZero() {
		t.Fatal("original metadata recovery", err)
	}
	old := x.f.l
	if err = x.f.s.ReleaseProjectEnvironmentCloneLease(x.f.ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	x.f.l, err = x.f.s.ClaimNextProjectEnvironmentClone(x.f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(x.f.ctx, old, x.f.target.Scope.SourceDatabaseID, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale pin recovery", err)
	}
	if _, _, err = x.f.s.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(x.f.ctx, old, x.f.target.Scope.SourceDatabaseID, x.oid, x.parent.Sealed.CiphertextSHA256, x.archive.ReservationFingerprint(), x.sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale pin record", err)
	}
	recovered, err := x.f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(x.f.ctx, x.f.l, x.f.target.Scope.SourceDatabaseID, x.oid)
	if err != nil || !bytes.Equal(recovered.Sealed.Ciphertext, original.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(original.CapturedAt) {
		t.Fatal("handoff rebased pins", err)
	}
	if _, err = x.f.s.AdvanceProjectEnvironmentCloneOperation(x.f.ctx, x.f.l.Operation.AccountID, x.f.l.Operation.ProjectID, x.f.l.Operation.ID, x.f.l.Operation.Status, state.CloneOperationCopying, x.f.l.Operation.Revision, x.f.l.Operation.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("pin ledger authorized readiness", err)
	}
}

func TestPgClonePostgresDatabaseSQLPinsRejectReplacementAndAllOriginalPrerequisiteChanges(t *testing.T) {
	x := cloneDatabaseSQLPinsFixture(t, true)
	f := x.f
	id := f.target.Scope.SourceDatabaseID
	for _, hashes := range [][2]string{{strings.Repeat("f", 64), x.archive.ReservationFingerprint()}, {x.parent.Sealed.CiphertextSHA256, strings.Repeat("f", 64)}} {
		if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(f.ctx, f.l, id, x.oid, hashes[0], hashes[1], x.sealed); !errors.Is(err, state.ErrConflict) {
			t.Fatal("substituted first-capture parent", err)
		}
	}
	if _, _, err := x.record(); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "owner", "provider", "fingerprint", "key", "reencrypt"} {
		t.Run(fault, func(t *testing.T) {
			s := x.sealed
			switch fault {
			case "scope":
				s.Scope.SourceVersion = strings.Repeat("f", 64)
			case "owner":
				s.OwnerID = uuid.NewString()
			case "provider":
				s.ProviderResourceID = "other-independent-provider"
			case "fingerprint":
				s.Fingerprint = strings.Repeat("f", 64)
			case "key":
				other, _ := age.GenerateX25519Identity()
				s.KeyID = other.Recipient().String()
			case "reencrypt":
				r, err := copydatabases.OpenPreparation([]*age.X25519Identity{f.key}, f.source, x.plan, x.oid, s)
				if err != nil {
					t.Fatal(err)
				}
				s, err = copydatabases.SealPreparation(f.key.Recipient(), r)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(f.ctx, f.l, id, x.oid, x.parent.Sealed.CiphertextSHA256, x.archive.ReservationFingerprint(), s); !errors.Is(err, state.ErrConflict) {
				t.Fatal("occupied pins replaced", err)
			}
		})
	}
	for _, table := range []string{"project_environment_clone_postgres_inventories", "project_environment_clone_postgres_target_sql_pins", "project_environment_clone_postgres_role_plans", "project_environment_clone_postgres_database_plans"} {
		t.Run(table, func(t *testing.T) {
			var cipher []byte
			var hash string
			if err := f.pool.QueryRow(f.ctx, "SELECT ciphertext,ciphertext_sha256 FROM "+table+" WHERE operation_id=$1", f.l.Operation.ID).Scan(&cipher, &hash); err != nil {
				t.Fatal(err)
			}
			changed := append(bytes.Clone(cipher), 0)
			h := sha256.Sum256(changed)
			if _, err := f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", changed, hex.EncodeToString(h[:]), f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
				t.Fatal("pin prerequisite changed", err)
			}
			if _, err := f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", cipher, hash, f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, field := range []string{"key_id", "storage_id", "storage_fingerprint", "reserved_bytes"} {
		t.Run(field, func(t *testing.T) {
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.WithoutCancel(f.ctx))
			value := any("changed-artifact")
			if field == "key_id" {
				other, _ := age.GenerateX25519Identity()
				value = other.Recipient().String()
			}
			if field == "storage_fingerprint" {
				value = strings.Repeat("f", 64)
			}
			if field == "reserved_bytes" {
				value = int64(8193)
			}
			if _, err = tx.Exec(f.ctx, "UPDATE project_environment_clone_postgres_archives SET "+field+"=$1 WHERE operation_id=$2", value, f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
				t.Fatal("archive reservation changed", err)
			}
			original := map[string]any{"key_id": x.archive.KeyID, "storage_id": x.archive.StorageID, "storage_fingerprint": x.archive.StorageFingerprint, "reserved_bytes": x.archive.ReservedBytes}[field]
			if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_archives SET "+field+"=$1 WHERE operation_id=$2", original, f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, _, err := f.s.ClaimProjectEnvironmentClonePostgresArchiveUpload(f.ctx, f.l, id, x.oid); err != nil {
		t.Fatal(err)
	}
	input := copyarchive.Receipt{Scope: x.archive.Scope, InventoryFingerprint: x.archive.InventoryFingerprint, SourceDatabaseOID: x.oid, PlainBytes: 1024, CiphertextBytes: 4096, CiphertextSHA256: strings.Repeat("e", 64)}
	if _, err := f.s.RecordProjectEnvironmentClonePostgresArchive(f.ctx, f.l, id, x.oid, input); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(f.ctx, f.l, id, x.oid); err != nil {
		t.Fatal("legitimate upload progress replaced reservation", err)
	}
	var raw string
	if err := f.pool.QueryRow(f.ctx, "SELECT row_to_json(p)::text FROM project_environment_clone_postgres_database_sql_pins p WHERE operation_id=$1", f.l.Operation.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, f.target.DatabaseName) || strings.Contains(raw, f.target.RoleName) || strings.Contains(raw, "\"created_at\"") || strings.Contains(raw, "plan_fingerprint") {
		t.Fatal("pin ledger exposed private preparation")
	}
}

func TestPgClonePostgresDatabaseSQLPinsRequireChargedArchiveAndRejectDamagedOrStaleMetadata(t *testing.T) {
	x := cloneDatabaseSQLPinsFixture(t, false)
	f := x.f
	id := f.target.Scope.SourceDatabaseID
	if _, _, err := x.record(); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("uncharged pin owner accepted", err)
	}
	var err error
	x.archive, _, err = f.s.ReserveProjectEnvironmentClonePostgresArchive(f.ctx, f.l, state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: f.target.Scope, DatabaseOID: x.oid, InventoryFingerprint: x.parent.Sealed.InventoryFingerprint, KeyID: f.key.Recipient().String(), StorageID: "private-artifacts", StorageFingerprint: strings.Repeat("d", 64), ReservedBytes: 8192}, archiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	oversized := x.sealed
	oversized.Ciphertext = bytes.Repeat([]byte{1}, api.PostgresCopyCiphertextMaxBytes+1)
	h := sha256.Sum256(oversized.Ciphertext)
	oversized.CiphertextSHA256 = hex.EncodeToString(h[:])
	if _, _, err = f.s.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(f.ctx, f.l, id, x.oid, x.parent.Sealed.CiphertextSHA256, x.archive.ReservationFingerprint(), oversized); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatal("unbounded pin owner", err)
	}
	if _, _, err = x.record(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_database_sql_pins SET ciphertext=decode('00','hex') WHERE operation_id=$1", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("damaged pin recovered", err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_database_sql_pins SET ciphertext=$1 WHERE operation_id=$2", x.sealed.Ciphertext, f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed phase recovered capture intent", err)
	}
	if _, _, err = x.record(); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed phase wrote pins", err)
	}
}

func cloneDatabaseSQLPinsMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261004095153284_environment_clone_postgres_database_sql_pins.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("pin migration lacks rollback")
	}
	return strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
}

func TestPgClonePostgresDatabaseSQLPinsMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	up, down := cloneDatabaseSQLPinsMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	shape := `SELECT (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_database_sql_pins'::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_database_sql_pins'::regclass)`
	var a, b, c, d string
	if err = tx.QueryRow(ctx, shape).Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	verificationUp, verificationDown := cloneVerificationMigrationParts(t)
	if _, err := tx.Exec(ctx, verificationDown); err != nil {
		t.Fatal(err)
	}
	importUp, importDown := cloneImportPreparationMigrationParts(t)
	if _, err := tx.Exec(ctx, importDown); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, importUp); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, verificationUp); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, shape).Scan(&c, &d); err != nil {
		t.Fatal(err)
	}
	if a != c || b != d {
		t.Fatal("pin migration changed shape")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	x := cloneDatabaseSQLPinsFixture(t, true)
	original, _, err := x.record()
	if err != nil {
		t.Fatal(err)
	}
	tx, err = x.f.pool.Begin(x.f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(x.f.ctx, down)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "23514" {
		t.Fatal("rollback erased occupied pins", err)
	}
	if err = tx.Rollback(x.f.ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := x.f.s.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(x.f.ctx, x.f.l, x.f.target.Scope.SourceDatabaseID, x.oid)
	if err != nil || !recovered.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(recovered.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatal("refused rollback changed original pins", err)
	}
}
