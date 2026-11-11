// adr: 20261025094915 — the overage choice is keyed by the usage subject, an
// account without a row is off, and the cap pools by the ledger's subject.
package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStoreOverageAndUsageSubjects(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	if enabled, err := store.OverageEnabled(ctx, accountID); err != nil || enabled {
		t.Fatalf("default overage = %v, %v; want off", enabled, err)
	}
	if enabled, chosen, err := store.OverageSetting(ctx, accountID); err != nil || enabled || chosen {
		t.Fatalf("setting before any choice = %v, %v, %v; want never chosen", enabled, chosen, err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for _, want := range []bool{true, false, true} {
		if err := store.SetOverage(ctx, accountID, want, now); err != nil {
			t.Fatal(err)
		}
		if got, err := store.OverageEnabled(ctx, accountID); err != nil || got != want {
			t.Fatalf("overage = %v, %v; want %v", got, err, want)
		}
	}
	if err := store.SetOverage(ctx, uuid.NewString(), true, now); err == nil {
		t.Fatal("overage stored for a subject that is not a resource namespace")
	}
	if _, err := store.OverageEnabled(ctx, "not-a-uuid"); err == nil {
		t.Fatal("malformed subject accepted")
	}

	database := postgresReadyDatabase(t, store, accountID, "orders", now)
	subjects, err := store.UsageSubjects(ctx, []string{database.ID, uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	// A personal account's database pools under the account itself.
	if len(subjects) != 1 || subjects[database.ID] != accountID {
		t.Fatalf("usage subjects = %v; want %s for %s only", subjects, accountID, database.ID)
	}
}

// ADR-20261025094915 amendment 2026-11-11: a subject that never chose, on a
// plan whose overage is on by default, is listed for overage billing; an
// explicit off removes it.
func TestPostgresStoreOverageSubjectsIncludeTheScaleDefault(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	postgresReadyDatabase(t, store, accountID, "orders", month.Add(time.Hour))
	if subjects, err := store.OverageSubjects(ctx, month); err != nil || len(subjects) != 0 {
		t.Fatalf("Pro subjects = %+v, %v; want none", subjects, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE accounts SET plan='scale' WHERE id=$1", accountID); err != nil {
		t.Fatal(err)
	}
	subjects, err := store.OverageSubjects(ctx, month)
	if err != nil || len(subjects) != 1 || subjects[0].SubjectID != accountID || !subjects[0].Overage {
		t.Fatalf("Scale subjects = %+v, %v; want the account with overage on", subjects, err)
	}
	if err := store.SetOverage(ctx, accountID, false, month.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if enabled, chosen, err := store.OverageSetting(ctx, accountID); err != nil || enabled || !chosen {
		t.Fatalf("setting after turning off = %v, %v, %v", enabled, chosen, err)
	}
	if subjects, err := store.OverageSubjects(ctx, month); err != nil || len(subjects) != 0 {
		t.Fatalf("Scale subjects after turning off = %+v, %v; want none", subjects, err)
	}
}
