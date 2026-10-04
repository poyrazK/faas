//go:build !no_pg

// adr:566
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
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneDatabasePlanFixture(t *testing.T, retainRoles bool) (rolePlanFixture, copydatabases.Plan, copydatabases.Sealed) {
	t.Helper()
	// The target SQL journal has a fixed private schema name, so this fixture
	// needs a whole private database instead of pgWithPool's schema fallback.
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	f := cloneRolePlanFixture(t, true)
	if retainRoles {
		if _, _, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, f.target.Scope.SourceDatabaseID, f.sealed); err != nil {
			t.Fatal(err)
		}
	}
	c, err := f.pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	// This metadata fixture maps every role to its existing SQL identity. It
	// creates a real private seed journal, with no role DDL or provider calls.
	seed, err := copyroles.Prepare(f.ctx, c.Conn(), f.plan, func(context.Context, copyarchive.RestoreTarget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	cfg := copyinventory.Config{PostgresMajor: 16, DatabaseName: f.target.DatabaseName, DatabaseOID: f.target.DatabaseOID, RoleName: f.target.RoleName, RoleOID: f.target.RoleOID}
	cfg.FingerprintKey[0] = 91
	i, err := copyinventory.Read(f.ctx, c.Conn(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	src, err := f.source.DatabaseCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	base, err := i.DatabaseCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	var dbs []copydatabases.Disposition
	for _, a := range src.Databases {
		var id uint32
		for _, b := range base.Databases {
			if a.Name == b.Name {
				id = b.OID
			}
		}
		dbs = append(dbs, copydatabases.Disposition{SourceOID: a.OID, ExistingTargetOID: id})
	}
	var spaces []copydatabases.TablespaceMapping
	for _, a := range src.Tablespaces {
		for _, b := range base.Tablespaces {
			if a.Name == b.Name {
				spaces = append(spaces, copydatabases.TablespaceMapping{SourceOID: a.OID, TargetOID: b.OID})
			}
		}
	}
	plan, err := copydatabases.NewPlan(f.source, seed, i, dbs, spaces)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := copydatabases.Seal(f.key.Recipient(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return f, plan, sealed
}

func TestPgClonePostgresDatabasePlanConcurrentOriginalOwnershipAndHandoff(t *testing.T) {
	f, _, sealed := cloneDatabasePlanFixture(t, true)
	id := f.target.Scope.SourceDatabaseID
	type result struct {
		r     state.ProjectEnvironmentClonePostgresDatabasePlan
		first bool
		err   error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			r, first, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed)
			results <- result{r, first, err}
		})
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresDatabasePlan
	firsts := 0
	for x := range results {
		if x.err != nil || !bytes.Equal(x.r.Sealed.Ciphertext, sealed.Ciphertext) || x.r.TargetDatabaseID != f.target.OwnerID || x.r.RolePlanCiphertextSHA256 != f.sealed.CiphertextSHA256 {
			t.Fatalf("concurrent database ownership: %v", x.err)
		}
		if original.CapturedAt.IsZero() {
			original = x.r
		}
		if !x.r.CapturedAt.Equal(original.CapturedAt) {
			t.Fatal("database plan replaced ownership time")
		}
		if x.first {
			firsts++
		}
	}
	if firsts != 1 {
		t.Fatal("database plan committed more than once")
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := copydatabases.Open([]*age.X25519Identity{other, f.key}, f.source, f.target, original.Sealed); err != nil {
		t.Fatalf("original encrypted complete database plan recovery: %v", err)
	}
	old := f.l
	if err := f.s.ReleaseProjectEnvironmentCloneLease(f.ctx, f.l, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	f.l, err = f.s.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, old, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale database read: %v", err)
	}
	if _, _, err = f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, old, id, f.sealed.CiphertextSHA256, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale database record: %v", err)
	}
	r, err := f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, f.l, id)
	if err != nil || !r.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("handoff rebased database plan: %v", err)
	}
	var private bool
	if err = f.pool.QueryRow(f.ctx, "SELECT state='provisioning' AND observed_generation=0 AND data_resource_id IS NULL FROM managed_postgres_databases WHERE id=$1", f.target.OwnerID).Scan(&private); err != nil || !private {
		t.Fatalf("database record published a dataset: %v", err)
	}
	if _, err = f.s.AdvanceProjectEnvironmentCloneOperation(f.ctx, f.l.Operation.AccountID, f.l.Operation.ProjectID, f.l.Operation.ID, f.l.Operation.Status, state.CloneOperationCopying, f.l.Operation.Revision, f.l.Operation.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("database plan completed capture: %v", err)
	}
}

func TestPgClonePostgresDatabasePlanRejectsReplacementAndEveryParentCipherChange(t *testing.T) {
	f, plan, sealed := cloneDatabasePlanFixture(t, true)
	id := f.target.Scope.SourceDatabaseID
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, strings.Repeat("f", 64), sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replaced parent accepted before first record: %v", err)
	}
	original, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "inventory", "target", "reencrypt", "key"} {
		t.Run(fault, func(t *testing.T) {
			changed := sealed
			var err error
			switch fault {
			case "scope":
				changed.Scope.SourceVersion = strings.Repeat("f", 64)
			case "inventory":
				changed.InventoryFingerprint = strings.Repeat("f", 64)
			case "target":
				changed.TargetFingerprint = strings.Repeat("f", 64)
			case "reencrypt":
				changed, err = copydatabases.Seal(f.key.Recipient(), plan)
			case "key":
				other, _ := age.GenerateX25519Identity()
				changed, err = copydatabases.Seal(other.Recipient(), plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("database ownership replaced: %v", err)
			}
		})
	}
	for _, table := range []string{"project_environment_clone_postgres_inventories", "project_environment_clone_postgres_target_sql_pins", "project_environment_clone_postgres_role_plans"} {
		t.Run(table, func(t *testing.T) {
			var cipher []byte
			var hash string
			if err := f.pool.QueryRow(f.ctx, "SELECT ciphertext,ciphertext_sha256 FROM "+table+" WHERE operation_id=$1", f.l.Operation.ID).Scan(&cipher, &hash); err != nil {
				t.Fatal(err)
			}
			changed := append(bytes.Clone(cipher), 0)
			sum := sha256.Sum256(changed)
			if _, err := f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", changed, hex.EncodeToString(sum[:]), f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, f.l, id); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("database parent replaced: %v", err)
			}
			if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("replaced database parent written: %v", err)
			}
			if _, err := f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", cipher, hash, f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE managed_postgres_databases SET storage_limit_bytes=123456,restore_window_seconds=0 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	r, err := f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, f.l, id)
	if err != nil || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("live source configuration rebased database plan: %v", err)
	}
	var raw string
	if err = f.pool.QueryRow(f.ctx, "SELECT row_to_json(p)::text FROM project_environment_clone_postgres_database_plans p WHERE operation_id=$1", f.l.Operation.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, f.target.DatabaseName) || strings.Contains(raw, f.target.RoleName) || strings.Contains(raw, "seeded_at") || strings.Contains(raw, "\"Databases\"") || strings.Contains(raw, "RoleProof") {
		t.Fatal("database ledger exposed private SQL database plan or seed identities")
	}
}

func TestPgClonePostgresDatabasePlanRequiresOriginalRoleOwnerAndBoundsDamagedOrStaleMetadata(t *testing.T) {
	f, _, sealed := cloneDatabasePlanFixture(t, false)
	id := f.target.Scope.SourceDatabaseID
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing role prerequisite accepted: %v", err)
	}
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed); err != nil {
		t.Fatal(err)
	}
	oversized := sealed
	oversized.Ciphertext = bytes.Repeat([]byte{1}, api.PostgresCopyCiphertextMaxBytes+1)
	sum := sha256.Sum256(oversized.Ciphertext)
	oversized.CiphertextSHA256 = hex.EncodeToString(sum[:])
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, oversized); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("unbounded database record: %v", err)
	}
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_database_plans SET ciphertext=decode('00','hex') WHERE operation_id=$1", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, f.l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("damaged database recovery: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_database_plans SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", sealed.Ciphertext, sealed.CiphertextSHA256, f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, f.l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed phase recovered capture intent: %v", err)
	}
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed phase wrote capture intent: %v", err)
	}
}

func cloneDatabasePlanMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261004095153280_environment_clone_postgres_database_plans.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("database plan migration lacks rollback")
	}
	return strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
}

func TestPgClonePostgresDatabasePlanMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	up, down := cloneDatabasePlanMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	shape := `SELECT (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_database_plans'::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_database_plans'::regclass)`
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
	pinsUp, pinsDown := cloneDatabaseSQLPinsMigrationParts(t)
	if _, err = tx.Exec(ctx, pinsDown); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, pinsUp); err != nil {
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
		t.Fatal("database migration round trip changed shape")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	f, _, sealed := cloneDatabasePlanFixture(t, true)
	id := f.target.Scope.SourceDatabaseID
	original, _, err := f.s.RecordProjectEnvironmentClonePostgresDatabasePlan(f.ctx, f.l, id, f.sealed.CiphertextSHA256, sealed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, down)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "23514" {
		t.Fatalf("down erased database ownership: %v", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	r, err := f.s.ProjectEnvironmentClonePostgresDatabasePlanForLease(f.ctx, f.l, id)
	if err != nil || !r.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("refused rollback changed database plan ownership: %v", err)
	}
}
