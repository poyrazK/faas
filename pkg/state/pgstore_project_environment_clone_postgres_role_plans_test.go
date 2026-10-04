//go:build !no_pg

// adr:568
package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/state"
)

type rolePlanFixture struct {
	s      *state.PgStore
	ctx    context.Context
	pool   *pgxpool.Pool
	l      state.ProjectEnvironmentCloneLease
	target copyarchive.RestoreTarget
	source copyinventory.ExportPlan
	plan   copyroles.Plan
	sealed copyroles.Sealed
	key    *age.X25519Identity
}

func cloneRolePlanFixture(t *testing.T, retainPins bool) rolePlanFixture {
	t.Helper()
	s, ctx, pool, l, original, sourceKey := clonePostgresInventoryFixture(t)
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, l, original.Scope.SourceDatabaseID, original); err != nil {
		t.Fatal(err)
	}
	owner, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, l, original.Scope.SourceDatabaseID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, l, original.Scope.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	target := copyarchive.RestoreTarget{Scope: original.Scope, OwnerID: owner.TargetDatabaseID, ProviderResourceID: "role-independent-project", DataResourceID: "role-independent-project/root", EndpointID: "ep-role-independent", ProviderCreatedAt: at, EndpointCreatedAt: at}
	if err = pool.QueryRow(ctx, `SELECT current_database(),current_user,(SELECT oid FROM pg_database WHERE datname=current_database()),(SELECT oid FROM pg_roles WHERE rolname=current_user)`).Scan(&target.DatabaseName, &target.RoleName, &target.DatabaseOID, &target.RoleOID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, l, original.Scope.SourceDatabaseID, state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: target.ProviderResourceID, CreatedAt: at, Prepared: true}); err != nil {
		t.Fatal(err)
	}
	key, _ := age.GenerateX25519Identity()
	pins, err := copyarchive.SealTarget(key.Recipient(), target)
	if err != nil {
		t.Fatal(err)
	}
	if retainPins {
		if _, _, err = s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, original.Scope.SourceDatabaseID, original.Fingerprint, pins); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := copyinventory.OpenInventory([]*age.X25519Identity{sourceKey}, original.Scope, original)
	if err != nil {
		t.Fatal(err)
	}
	source, err := inventory.PlanExports(original.Scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := source.RoleCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	choices := make([]copyroles.Disposition, 0, len(roles.Roles))
	for _, r := range roles.Roles {
		choices = append(choices, copyroles.Disposition{SourceOID: r.OID, ExistingTargetOID: r.OID})
	}
	// Real codec/catalogue data, synthetic provider/capture placement. This state
	// fixture never connects to a provider target or executes target role DDL.
	plan, err := copyroles.NewPlan(source, inventory, target, choices)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := copyroles.Seal(key.Recipient(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return rolePlanFixture{s, ctx, pool, l, target, source, plan, sealed, key}
}

func TestPgClonePostgresRolePlanWriteOnceConcurrentOwnershipAndHandoff(t *testing.T) {
	f := cloneRolePlanFixture(t, true)
	id := f.target.Scope.SourceDatabaseID
	type result struct {
		r     state.ProjectEnvironmentClonePostgresRolePlan
		first bool
		err   error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			r, first, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed)
			results <- result{r, first, err}
		})
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresRolePlan
	firsts := 0
	for r := range results {
		if r.err != nil || !bytes.Equal(r.r.Sealed.Ciphertext, f.sealed.Ciphertext) || r.r.TargetDatabaseID != f.target.OwnerID {
			t.Fatalf("concurrent role plan ownership: %v", r.err)
		}
		if original.CapturedAt.IsZero() {
			original = r.r
		}
		if !r.r.CapturedAt.Equal(original.CapturedAt) {
			t.Fatal("concurrent plan replaced ownership time")
		}
		if r.first {
			firsts++
		}
	}
	if firsts != 1 {
		t.Fatal("role plan committed more than once")
	}
	previous, _ := age.GenerateX25519Identity()
	opened, err := copyroles.Open([]*age.X25519Identity{previous, f.key}, f.source, f.target, original.Sealed)
	if err != nil || opened.Summary() != f.plan.Summary() {
		t.Fatalf("durable original role plan recovery: %v", err)
	}
	old := f.l
	if err = f.s.ReleaseProjectEnvironmentCloneLease(f.ctx, f.l, 0); err != nil {
		t.Fatal(err)
	}
	f.l, err = f.s.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, old, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker recovered role plan: %v", err)
	}
	if _, _, err = f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, old, id, f.sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker wrote role plan: %v", err)
	}
	recovered, err := f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, f.l, id)
	if err != nil || !recovered.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(recovered.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("handoff rebased role plan: %v", err)
	}
	var private bool
	if err = f.pool.QueryRow(f.ctx, "SELECT state='provisioning' AND observed_generation=0 AND data_resource_id IS NULL FROM managed_postgres_databases WHERE id=$1", f.target.OwnerID).Scan(&private); err != nil || !private {
		t.Fatalf("role plan published a ready target: %v", err)
	}
	if _, err = f.s.AdvanceProjectEnvironmentCloneOperation(f.ctx, f.l.Operation.AccountID, f.l.Operation.ProjectID, f.l.Operation.ID, f.l.Operation.Status, state.CloneOperationCopying, f.l.Operation.Revision, f.l.Operation.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("role plan completed capture: %v", err)
	}
}

func TestPgClonePostgresRolePlanRejectsReplacementAndRetainsOriginalDependencies(t *testing.T) {
	// A private database gives this privacy assertion a distinct SQL name. The
	// shared database name "postgres" is also the legitimate scope backend ID.
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	f := cloneRolePlanFixture(t, true)
	id := f.target.Scope.SourceDatabaseID
	original, _, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "inventory", "target", "reencrypt", "key"} {
		t.Run(fault, func(t *testing.T) {
			changed := f.sealed
			var sealErr error
			switch fault {
			case "scope":
				changed.Scope.SourceVersion = strings.Repeat("f", 64)
			case "inventory":
				changed.InventoryFingerprint = strings.Repeat("f", 64)
			case "target":
				changed.TargetFingerprint = strings.Repeat("f", 64)
			case "reencrypt":
				changed, sealErr = copyroles.Seal(f.key.Recipient(), f.plan)
			case "key":
				other, _ := age.GenerateX25519Identity()
				changed, sealErr = copyroles.Seal(other.Recipient(), f.plan)
			}
			if sealErr != nil {
				t.Fatal(sealErr)
			}
			if _, _, recordErr := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, changed); !errors.Is(recordErr, state.ErrConflict) {
				t.Fatalf("role plan substituted: %v", recordErr)
			}
		})
	}
	var raw string
	if err = f.pool.QueryRow(f.ctx, "SELECT row_to_json(p)::text FROM project_environment_clone_postgres_role_plans p WHERE operation_id=$1", f.l.Operation.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	databaseName, _ := json.Marshal(f.target.DatabaseName)
	roleName, _ := json.Marshal(f.target.RoleName)
	if strings.Contains(raw, string(databaseName)) || strings.Contains(raw, string(roleName)) {
		t.Fatal("role plan ledger exposed SQL names")
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE managed_postgres_databases SET storage_limit_bytes=123456,restore_window_seconds=0 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if r, err := f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, f.l, id); err != nil || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("live source config rebased role plan: %v", err)
	}
	for _, table := range []string{"project_environment_clone_postgres_inventories", "project_environment_clone_postgres_target_sql_pins"} {
		t.Run(table, func(t *testing.T) {
			var cipher []byte
			var hash string
			if err = f.pool.QueryRow(f.ctx, "SELECT ciphertext,ciphertext_sha256 FROM "+table+" WHERE operation_id=$1", f.l.Operation.ID).Scan(&cipher, &hash); err != nil {
				t.Fatal(err)
			}
			changed := append(bytes.Clone(cipher), 0)
			sum := sha256.Sum256(changed)
			if _, err = f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", changed, hex.EncodeToString(sum[:]), f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, f.l, id); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("committed dependency replaced: %v", err)
			}
			if _, err = f.pool.Exec(f.ctx, "UPDATE "+table+" SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", cipher, hash, f.l.Operation.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPgClonePostgresRolePlanRequiresOriginalPinsAndBoundsMetadata(t *testing.T) {
	f := cloneRolePlanFixture(t, false)
	id := f.target.Scope.SourceDatabaseID
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unpinned target retained role plan: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM project_environment_clone_postgres_role_plans WHERE operation_id=$1", f.l.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected role plan retained input: %v", err)
	}
	oversized := f.sealed
	oversized.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
	if _, _, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, oversized); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("unbounded role plan stored: %v", err)
	}
	pins, err := copyarchive.SealTarget(f.key.Recipient(), f.target)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.RecordProjectEnvironmentClonePostgresTargetSQLPins(f.ctx, f.l, id, f.sealed.InventoryFingerprint, pins); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_role_plans SET ciphertext=decode('00','hex') WHERE operation_id=$1", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, f.l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("damaged role plan recovered: %v", err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_role_plans SET ciphertext=$1,ciphertext_sha256=$2 WHERE operation_id=$3", f.sealed.Ciphertext, f.sealed.CiphertextSHA256, f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, f.l, id); err != nil {
		t.Fatalf("restored original plan prerequisites: %v", err)
	}
	// An actual phase change must reject old capture intent even if the test
	// deliberately leaves the token/revision unchanged in the stored operation.
	if _, err = f.pool.Exec(f.ctx, "UPDATE project_environment_clone_operations SET status='compensating' WHERE id=$1", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("compensating operation accepted capture input: %v", err)
	}
}

func TestPgClonePostgresRolePlanMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	raw, err := migrations.FS.ReadFile("20261004095153272_environment_clone_postgres_role_plans.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("role plan migration lacks explicit rollback")
	}
	up, down := strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	shape := `SELECT (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_role_plans'::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_role_plans'::regclass)`
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
	databaseUp, databaseDown := cloneDatabasePlanMigrationParts(t)
	if _, err = tx.Exec(ctx, databaseDown); err != nil {
		t.Fatal(err)
	}
	memberUp, memberDown := cloneMembershipPlanMigrationParts(t)
	if _, err = tx.Exec(ctx, memberDown); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, memberUp); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, databaseUp); err != nil {
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
		t.Fatal("empty role plan migration round trip changed shape")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	f := cloneRolePlanFixture(t, true)
	id := f.target.Scope.SourceDatabaseID
	original, _, err := f.s.RecordProjectEnvironmentClonePostgresRolePlan(f.ctx, f.l, id, f.sealed)
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
		t.Fatalf("down erased committed role ownership: %v", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := f.s.ProjectEnvironmentClonePostgresRolePlanForLease(f.ctx, f.l, id); err != nil || !r.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("refused rollback changed role ownership: %v", err)
	}
}
