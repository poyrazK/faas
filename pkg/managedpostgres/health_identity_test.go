// adr: 463 — cached health and observation results are fenced to the dataset.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHealthRejectsMissingOrDifferentDatasetEvidence(t *testing.T) {
	const pin = "private-upstream/br-original"
	for _, observedPin := range []string{pin, "private-upstream/br-copy", ""} {
		t.Run("observed_"+observedPin, func(t *testing.T) {
			provider := &healthTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, observe: func(_ context.Context, id string) (ObservedDatabase, error) {
				if id != pin {
					t.Errorf("observed lifecycle alias instead of dataset: %q", id)
				}
				return ObservedDatabase{ProviderResourceID: id, DataResourceID: observedPin, Status: ProviderStatusReady, ComputeState: ComputeStateActive, Spec: testSpec()}, nil
			}}
			registry := testRegistry(t, provider, nil)
			store := NewMemoryStore()
			database := readyHealthDatabase(t, store, registry, time.Now().UTC())
			database.DataResourceID = pin
			collector, err := NewHealthCollector(registry, store, HealthCollectorOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result := collector.observe(t.Context(), database)
			valid := observedPin == pin
			if result.Succeeded != valid || (valid && result.LastErrorCode != "") || (!valid && result.LastErrorCode != "observation_invalid") {
				t.Fatalf("dataset evidence accepted incorrectly: %+v", result)
			}
		})
	}
}

func TestMemoryHealthDatasetBackfillInvalidatesSamplesAndLeases(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	store := NewMemoryStore()
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
	database := readyHealthDatabase(t, store, registry, now)
	old, err := store.ClaimHealthCheck(ctx, "legacy", now, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	result := HealthResult{HealthSnapshot: HealthSnapshot{ProviderStatus: "ready", ComputeState: ComputeStateActive, CheckedAt: now}, Succeeded: true, NextCheckAt: now.Add(time.Hour)}
	if err := store.FinishHealthCheck(ctx, old, result); err != nil {
		t.Fatal(err)
	}
	pin := database.ProviderResourceID + "/br-original"
	store.mu.Lock()
	current := store.databases[database.ID]
	current.DataResourceID = pin
	store.databases[database.ID] = current
	store.mu.Unlock()
	if rows, err := store.ReadHealthSnapshots(ctx, database.AccountID, []string{database.ID}); err != nil || len(rows) != 0 {
		t.Fatal("legacy sample followed a dataset backfill", rows, err)
	}
	now = now.Add(time.Second)
	claim, err := store.ClaimHealthCheck(ctx, "pinned", now, now.Add(30*time.Second))
	if err != nil || claim.Database.DataResourceID != pin {
		t.Fatalf("pin absent/not immediately due: %+v %v", claim, err)
	}
	store.mu.Lock()
	current = store.databases[database.ID]
	current.DataResourceID = database.ProviderResourceID + "/br-copy"
	store.databases[database.ID] = current
	store.mu.Unlock()
	result.CheckedAt = now
	if err := store.FinishHealthCheck(ctx, claim, result); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale dataset observation completed: %v", err)
	}
	now = now.Add(31 * time.Second)
	recovered, err := store.ClaimHealthCheck(ctx, "replacement", now, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Database.DataResourceID == pin {
		t.Fatal("replacement claim retained the old pin")
	}
	rows, err := store.ReadHealthSnapshots(ctx, database.AccountID, []string{database.ID})
	if err != nil || !rows[database.ID].CheckedAt.IsZero() {
		t.Fatal("old health relabeled with replacement dataset", rows, err)
	}
}

func TestPostgresHealthDatasetBackfillInvalidatesSamplesAndLeases(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	database := postgresReadyDatabase(t, store, accountID, "health-identity", now.Add(-time.Hour))
	old, err := store.ClaimHealthCheck(ctx, "legacy", now, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	result := HealthResult{HealthSnapshot: HealthSnapshot{ProviderStatus: "ready", ComputeState: ComputeStateActive, CheckedAt: now}, Succeeded: true, NextCheckAt: now.Add(time.Hour)}
	if err := store.FinishHealthCheck(ctx, old, result); err != nil {
		t.Fatal(err)
	}
	pin := database.ProviderResourceID + "/br-original"
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id=$2 WHERE id=$1`, database.ID, pin); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.ReadHealthSnapshots(ctx, accountID, []string{database.ID}); err != nil || len(rows) != 0 {
		t.Fatal("legacy sample followed the new dataset", rows, err)
	}
	if counts, err := store.CountDatabaseHealth(ctx, now.Add(-5*time.Minute), now); err != nil || counts.Healthy != 0 || counts.Stale != 1 {
		t.Fatalf("legacy health counted after pinning: %+v %v", counts, err)
	}
	now = now.Add(time.Second)
	claim, err := store.ClaimHealthCheck(ctx, "pinned", now, now.Add(30*time.Second))
	if err != nil || claim.Database.DataResourceID != pin || claim.Database.ProviderResourceID != database.ProviderResourceID {
		t.Fatalf("claim lost the distinct dataset/lifecycle identities: %+v %v", claim, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET data_resource_id=$2 WHERE id=$1`, database.ID, database.ProviderResourceID+"/br-copy"); err != nil {
		t.Fatal(err)
	}
	result.CheckedAt = now
	if err := store.FinishHealthCheck(ctx, claim, result); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale dataset observation completed: %v", err)
	}
	now = now.Add(31 * time.Second)
	current, err := store.ClaimHealthCheck(ctx, "replacement", now, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadHealthSnapshots(ctx, accountID, []string{database.ID})
	if err != nil || !rows[database.ID].CheckedAt.IsZero() {
		t.Fatal("old health relabeled with replacement dataset", rows, err)
	}
	result.CheckedAt = now
	if err := store.FinishHealthCheck(ctx, current, result); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ReadHealthSnapshots(ctx, accountID, []string{database.ID})
	if err != nil || rows[database.ID].ProviderStatus != "ready" {
		t.Fatal("pinned sample not readable", rows, err)
	}
	// An N-1 collector may still publish an alias-based sample. New readers
	// discard it, and the concrete target is due again without a schema change.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_health SET provider_resource_id=$2 WHERE database_id=$1`, database.ID, database.ProviderResourceID); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.ReadHealthSnapshots(ctx, accountID, []string{database.ID}); err != nil || len(rows) != 0 {
		t.Fatal("N-1 alias sample was trusted for a pinned database", rows, err)
	}
	if _, err := store.ClaimHealthCheck(ctx, "after-legacy", now, now.Add(30*time.Second)); err != nil {
		t.Fatal("legacy sample did not become immediately due", err)
	}
}
