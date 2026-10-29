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
