package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPostgresCutoverRejectsTemporaryTargetsAndLegacyWriters(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: "cutover-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	source := postgresReadyDatabase(t, store, account, "source", now.Add(-time.Hour))
	targetInput := postgresTestDatabase(account, "restore", now.Add(-time.Hour))
	targetInput.RestoreSourceDatabaseID = source.ID
	targetInput.RestoreSourceResourceID = source.ProviderResourceID
	targetInput.RestorePointInTime = now.Add(-time.Minute)
	target, _, err := store.Reserve(ctx, targetInput, 1)
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.Claim(ctx, account, target.ID, "restore", StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordProviderResource(ctx, target.ID, "restore", "restored-resource", now); err != nil {
		t.Fatal(err)
	}
	target, err = store.FinishProvision(ctx, target.ID, "restore", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []CredentialAccess{CredentialReadWrite, CredentialMigration} {
		b := testBinding(account, source.ID, app.ID, uuid.NewString(), now)
		b.Access = access
		b.Scope = "default"
		if access == CredentialMigration {
			b.EnvironmentKey = "MIGRATION_DATABASE_URL"
		}
		if _, _, err = store.ReserveBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimBinding(ctx, account, b.ID, "binding", BindingStateProvisioning, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		sink := &postgresBindingCredentialSink{store: state.NewPgStore(pool)}
		ref, err := sink.Put(ctx, claimed, bindingTestMaterial())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.FinishBindingProvision(ctx, b.ID, "binding", "provider-role", ref, now); err != nil {
			t.Fatal(err)
		}
	}
	r := PrepareCutoverRequest{ID: uuid.NewString(), AccountID: account, AppID: app.ID, Scope: "default", SourceDatabaseID: source.ID, TargetDatabaseID: target.ID}

	if target.RestoreSlotUntil.IsZero() {
		t.Fatal("target is not temporary")
	}
	if _, _, err := store.Reserve(reserveWithinLimit(ctx), targetInput, 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("racing reservation adopted temporary target", err)
	}
	if _, _, err := store.ReserveCutover(ctx, r, now); !errors.Is(err, ErrConflict) {
		t.Fatal("cutover admitted temporary target", err)
	}
	// Exercise the SQL an older worker uses, bypassing the new Go guard.
	err = sqlc.New().InsertManagedPostgresCutover(ctx, pool, sqlc.InsertManagedPostgresCutoverParams{
		ID: r.ID, AccountID: account, AppID: app.ID, Scope: "default", SourceID: source.ID, TargetID: target.ID,
		SourceBackend: source.BackendID, SourceFingerprint: source.BackendFingerprint, SourceResource: source.ProviderResourceID, SourceGeneration: source.DesiredGeneration,
		TargetBackend: target.BackendID, TargetFingerprint: target.BackendFingerprint, TargetResource: target.ProviderResourceID, TargetGeneration: target.DesiredGeneration, Now: healthTimestamp(now)})
	var rejected *pgconn.PgError
	if !errors.As(err, &rejected) || rejected.ConstraintName != "managed_postgres_cutover_conflict" || !strings.Contains(rejected.Message, "temporary restore") {
		t.Fatalf("N-1 SQL bypassed catalog protection: %v", err)
	}
	if _, err = store.ClearRestoreSlot(ctx, target.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.ReserveCutover(ctx, r, now); err != nil {
		t.Fatal("permanent target refused", err)
	}
}
