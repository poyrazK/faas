package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresCutoverActivationRejectsLegacyTemporaryTarget(t *testing.T) {
	store, _, cutover, _ := admissionCutoverFixture(t)
	ctx := t.Context()
	target, err := store.Get(ctx, cutover.AccountID, cutover.Target.ID)
	if err != nil {
		t.Fatal(err)
	}
	// An older worker reserved this cutover before catalog protection existed.
	if _, err := store.pool.Exec(ctx, `UPDATE managed_postgres_databases SET restore_slot_until=$2 WHERE id=$1`, target.ID, target.CreatedAt.Add(api.ManagedPostgresRestoreSlotWindow)); err != nil {
		t.Fatal(err)
	}
	fence, err := store.FenceCutoverAdmission(ctx, cutover.AccountID, cutover.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ActivateCutover(ctx, CutoverActivation{AccountID: cutover.AccountID, CutoverID: cutover.ID, FencedAt: fence.FencedAt, LiveDeploymentID: "irrelevant", Now: time.Now().UTC()})
	activationBlockedWith(t, err, ActivationDatabasesChanged)
	// N-1 has no Go guard, so its activation UPDATE must also be refused.
	_, err = store.pool.Exec(ctx, `UPDATE managed_postgres_cutovers SET state='activated',updated_at=clock_timestamp() WHERE id=$1`, cutover.ID)
	var rejected *pgconn.PgError
	if !errors.As(err, &rejected) || rejected.ConstraintName != "managed_postgres_cutover_conflict" || !strings.Contains(rejected.Message, "temporary restore") {
		t.Fatalf("legacy activation bypassed catalog protection: %v", err)
	}
}

func TestPostgresCutoverMigrationRetainsAlreadyActivatedTarget(t *testing.T) {
	store, accounts, cutover, live, node := activationFixture(t)
	ctx := t.Context()
	fence, err := store.FenceCutoverAdmission(ctx, cutover.AccountID, cutover.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := accounts.ReserveManagedPostgresDrainPlan(ctx, cutover.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range plan.Members {
		if member.BlockerCode == "parked_instance_unproven" {
			continue
		}
		if err := accounts.CompleteManagedPostgresDrain(ctx, member.Drain.Frame, cutoverDrainProof(member.Drain.Frame)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ActivateCutover(ctx, CutoverActivation{AccountID: cutover.AccountID, CutoverID: cutover.ID, FencedAt: fence.FencedAt, LiveDeploymentID: live.ID, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	target, err := store.Get(ctx, cutover.AccountID, cutover.Target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE managed_postgres_databases SET restore_slot_until=$2 WHERE id=$1`, target.ID, target.CreatedAt.Add(api.ManagedPostgresRestoreSlotWindow)); err != nil {
		t.Fatal(err)
	}
	// Replay the embedded, batched repair against legacy activated data.
	data, err := migrations.FS.ReadFile("20261105141918773_managed_postgres_permanent_cutover_targets.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "DO $$\nDECLARE changed")
	if start < 0 {
		t.Fatal("migration repair block missing")
	}
	end := strings.Index(string(data)[start:], "$$;") + len("$$;")
	if end < len("$$;") {
		t.Fatal("migration repair block unterminated")
	}
	if _, err := store.pool.Exec(ctx, string(data)[start:start+end]); err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get(ctx, cutover.AccountID, target.ID)
	if err != nil || !retained.RestoreSlotUntil.IsZero() || retained.State != StateReady || retained.Spec != target.Spec || retained.ProviderResourceID != target.ProviderResourceID {
		t.Fatalf("activated target kept an expiry or changed identity: database=%+v err=%v", retained, err)
	}
}
