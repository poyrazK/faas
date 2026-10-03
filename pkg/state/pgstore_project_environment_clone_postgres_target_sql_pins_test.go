//go:build !no_pg

// adr:375
package state_test

import (
	"bytes"
	"context"
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
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneTargetSQLPinsFixture(t *testing.T, ready bool) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, copyarchive.RestoreTarget, copyarchive.SealedTarget, string, *age.X25519Identity) {
	t.Helper()
	s, ctx, pool, l, r := clonePostgresImportFixture(t, ready)
	id, _ := age.GenerateX25519Identity()
	sealed, err := copyarchive.SealTarget(id.Recipient(), r.Target)
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, l, r.Target, sealed, r.Input.InventoryFingerprint, id
}

func TestPgClonePostgresTargetSQLPinsWriteOnceConcurrentCaptureAndWorkerHandoff(t *testing.T) {
	s, ctx, pool, l, target, sealed, fingerprint, key := cloneTargetSQLPinsFixture(t, true)
	id := target.Scope.SourceDatabaseID
	type result struct {
		receipt state.ProjectEnvironmentClonePostgresTargetSQLPins
		first   bool
		err     error
	}
	results := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			r, created, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fingerprint, sealed)
			results <- result{r, created, err}
		})
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresTargetSQLPins
	created := 0
	for got := range results {
		if got.err != nil || got.receipt.Sealed.Fingerprint != sealed.Fingerprint || !bytes.Equal(got.receipt.Sealed.Ciphertext, sealed.Ciphertext) {
			t.Fatalf("target SQL capture: %v", got.err)
		}
		if original.CapturedAt.IsZero() {
			original = got.receipt
		}
		if !got.receipt.CapturedAt.Equal(original.CapturedAt) {
			t.Fatal("concurrent capture replaced target SQL receipt")
		}
		if got.first {
			created++
		}
	}
	if created != 1 {
		t.Fatal("target SQL pins captured more than once")
	}
	opened, err := copyarchive.OpenTarget([]*age.X25519Identity{key}, target.Scope, original.Sealed)
	if err != nil || opened != target {
		t.Fatalf("durable encrypted pins: %v", err)
	}
	old := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, old, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker read pins: %v", err)
	}
	if _, _, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, old, id, fingerprint, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker replaced pins: %v", err)
	}
	recovered, err := s.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, id)
	if err != nil || !bytes.Equal(recovered.Sealed.Ciphertext, original.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(original.CapturedAt) {
		t.Fatalf("handoff changed target SQL pins: %v", err)
	}
	var private bool
	if err := pool.QueryRow(ctx, "SELECT state='provisioning' AND observed_generation=0 AND data_resource_id IS NULL FROM managed_postgres_databases WHERE id=$1", target.OwnerID).Scan(&private); err != nil || !private {
		t.Fatalf("pins published target readiness: %v", err)
	}
	op := l.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pins completed clone capture: %v", err)
	}
}

func TestPgClonePostgresTargetSQLPinsRejectScopeIdentityAndCiphertextReplacement(t *testing.T) {
	s, ctx, pool, l, target, sealed, fingerprint, key := cloneTargetSQLPinsFixture(t, true)
	id := target.Scope.SourceDatabaseID
	original, _, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fingerprint, sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "inventory", "owner", "project", "project_time", "database", "database_oid", "role", "role_oid", "endpoint", "reencrypt", "key"} {
		t.Run(fault, func(t *testing.T) {
			changed := target
			recipient := key.Recipient()
			fp := fingerprint
			switch fault {
			case "scope":
				changed.Scope.SourceVersion = strings.Repeat("f", 64)
			case "inventory":
				fp = strings.Repeat("f", 64)
			case "owner":
				changed.OwnerID = uuid.NewString()
			case "project":
				changed.ProviderResourceID = "other-project"
			case "project_time":
				changed.ProviderCreatedAt = changed.ProviderCreatedAt.Add(time.Microsecond)
				changed.EndpointCreatedAt = changed.ProviderCreatedAt
			case "database":
				changed.DatabaseName = "other_private_database"
			case "database_oid":
				changed.DatabaseOID++
			case "role":
				changed.RoleName = "other_private_role"
			case "role_oid":
				changed.RoleOID++
			case "endpoint":
				changed.EndpointID = "ep-other"
			case "key":
				other, _ := age.GenerateX25519Identity()
				recipient = other.Recipient()
			}
			input, err := copyarchive.SealTarget(recipient, changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fp, input); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("target SQL pins replaced: %v", err)
			}
		})
	}
	var raw string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(p)::text FROM project_environment_clone_postgres_target_sql_pins p WHERE operation_id=$1", l.Operation.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, target.DatabaseName) || strings.Contains(raw, target.RoleName) {
		t.Fatal("ledger persisted private SQL names")
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET restore_window_seconds=0,storage_limit_bytes=123456 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, id); err != nil || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("live source edit rebased pins: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_inventories SET fingerprint=$1 WHERE operation_id=$2", strings.Repeat("f", 64), l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("substituted source inventory accepted: %v", err)
	}
}

func TestPgClonePostgresTargetSQLPinsRequirePreparedTargetAndBoundEncryptedMetadata(t *testing.T) {
	s, ctx, pool, l, target, sealed, fp, _ := cloneTargetSQLPinsFixture(t, false)
	id := target.Scope.SourceDatabaseID
	if _, _, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fp, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unprepared target pins captured: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM project_environment_clone_postgres_target_sql_pins WHERE operation_id=$1", l.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected capture retained pins: %v", err)
	}
	oversized := sealed
	oversized.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
	if _, _, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fp, oversized); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("unbounded encrypted pins accepted: %v", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, l, id, state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: target.ProviderResourceID, CreatedAt: target.ProviderCreatedAt, Prepared: true}); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, id, fp, sealed); err != nil || !created {
		t.Fatalf("prepared target pins rejected: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_target_sql_pins SET ciphertext=decode('00','hex') WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("damaged committed target pins accepted: %v", err)
	}
}

func TestPgClonePostgresTargetSQLPinsMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	raw, err := migrations.FS.ReadFile("20261003070000000_environment_clone_postgres_target_sql_pins.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration lacks explicit rollback")
	}
	up, down := strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
	// The later role-plan table references these pins. Follow migration order
	// while round-tripping the empty prerequisite, as goose rollback does.
	childRaw, err := migrations.FS.ReadFile("20261003080000000_environment_clone_postgres_role_plans.sql")
	if err != nil {
		t.Fatal(err)
	}
	childParts := strings.Split(string(childRaw), "-- +goose Down")
	if len(childParts) != 2 {
		t.Fatal("role plan migration lacks rollback")
	}
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	shape := `SELECT
 (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_target_sql_pins'::regclass AND attnum>0 AND NOT attisdropped),
 (SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid)) ORDER BY conname)::text FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_target_sql_pins'::regclass)`
	var beforeCols, beforeConstraints, afterCols, afterConstraints string
	if err := tx.QueryRow(ctx, shape).Scan(&beforeCols, &beforeConstraints); err != nil {
		t.Fatal(err)
	}
	databaseUp, databaseDown := cloneDatabasePlanMigrationParts(t)
	if _, err := tx.Exec(ctx, databaseDown); err != nil {
		t.Fatal(err)
	}
	memberUp, memberDown := cloneMembershipPlanMigrationParts(t)
	if _, err := tx.Exec(ctx, memberDown); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, childParts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, strings.TrimPrefix(childParts[0], "-- +goose Up")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, memberUp); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, databaseUp); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&afterCols, &afterConstraints); err != nil {
		t.Fatal(err)
	}
	if beforeCols != afterCols || beforeConstraints != afterConstraints {
		t.Fatal("empty pins migration round trip changed shape")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s, ctx, pool, l, target, sealed, fp, _ := cloneTargetSQLPinsFixture(t, true)
	original, _, err := s.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, target.Scope.SourceDatabaseID, fp, sealed)
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
		t.Fatalf("down erased committed target SQL pins: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, target.Scope.SourceDatabaseID); err != nil || !r.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(r.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("refused down changed target SQL pins: %v", err)
	}
}
