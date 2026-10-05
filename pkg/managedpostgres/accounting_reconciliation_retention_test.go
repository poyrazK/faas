// adr: 591 — repair remains atomic, append-only, and rooted in shared accounting.
package managedpostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func applyReconciliation(t *testing.T, service *Service, account string, request AccountingReconciliationRequest) (AccountingReconciliationRequest, AccountingReconciliationResult) {
	t.Helper()
	preview, err := service.ReconcileAccounting(context.Background(), account, "operator", request, false)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = preview.Revision
	result, err := service.ReconcileAccounting(context.Background(), account, "operator", request, true)
	if err != nil {
		t.Fatal(err)
	}
	return request, result
}

func TestAccountingReconciliationLifecycleFences(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := reconciliationFixture(t, kind)
			for _, state := range []State{StateProvisioning, StateUpdating, StateFailed, StateDeleting, StateDeleted} {
				// The deleted case exercises a lifecycle lease left by a legacy writer.
				leaseUntil := now.Add(-time.Minute)
				if state == StateDeleted {
					leaseUntil = now.Add(time.Minute)
				}
				switch s := store.(type) {
				case *MemoryStore:
					s.mu.Lock()
					d := s.databases[database.ID]
					d.State, d.LeaseToken, d.LeaseUntil = state, "legacy", leaseUntil
					d.DeletedAt = nil
					if state == StateDeleted {
						d.DeletedAt = database.DeletedAt
					}
					s.databases[database.ID] = d
					s.mu.Unlock()
				case *PostgresStore:
					if _, err := s.pool.Exec(context.Background(), "UPDATE managed_postgres_databases SET state=$1,lease_token='legacy',lease_until=$2,deleted_at=CASE WHEN $1='deleted' THEN $4::timestamptz ELSE NULL END WHERE id=$3", state, leaseUntil, database.ID, database.DeletedAt); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := service.ReconcileAccounting(context.Background(), database.AccountID, "operator", request, false); !errors.Is(err, ErrConflict) {
					t.Fatalf("state %s/lease fence accepted: %v", state, err)
				}
			}
			input := postgresTestDatabase(database.AccountID, "known", now.Add(-6*time.Hour))
			input.BackendID, input.BackendFingerprint = database.BackendID, database.BackendFingerprint
			known := retirementReadyDatabase(t, store, input)
			known = retirementDelete(t, store, known, now.Add(-2*time.Hour), true)
			request.DatabaseID, request.ProviderResourceID = known.ID, known.ProviderResourceID
			if _, err := service.ReconcileAccounting(context.Background(), known.AccountID, "operator", request, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("known identity accepted for legacy repair: %v", err)
			}
		})
	}
}

func TestAccountingReconciliationSharedDescendantsRequireRootRecovery(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 5, 12, 17, 0, 0, time.UTC)
			store, registry, provider, root := retirementFixture(t, kind, now.Add(-6*time.Hour))
			backend := registry.backends[root.BackendID]
			backend.Capabilities.RestoreUsageIncludedInSource, backend.Capabilities.RestoreUsageIsolated = true, false
			registry.backends[root.BackendID] = backend
			service, err := NewService(registry, store, ServiceOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			input := postgresTestDatabase(root.AccountID, "restored", now.Add(-4*time.Hour))
			input.BackendID, input.BackendFingerprint = root.BackendID, root.BackendFingerprint
			input.RestoreSourceDatabaseID, input.RestoreSourceResourceID = root.ID, root.ProviderResourceID
			input.RestorePointInTime = input.CreatedAt.Add(-time.Minute)
			child := retirementReadyDatabase(t, store, input)
			childProviderID := child.ProviderResourceID
			child = reconciliationLegacyDatabase(t, store, child, now.Add(-3*time.Hour))
			request := AccountingReconciliationRequest{ReconciliationID: uuid.NewString(), DatabaseID: child.ID, BackendID: child.BackendID,
				BackendFingerprint: child.BackendFingerprint, ProviderResourceID: childProviderID, ShutdownAt: now.Add(-2 * time.Hour), ObservedAt: now,
				EvidenceReference: "retained/branch-shutdown", EvidenceSHA256: strings.Repeat("a", 64), Reason: "Repair shared branch identity"}
			_, repaired := applyReconciliation(t, service, child.AccountID, request)
			if !repaired.SharedAccounting || !repaired.RequiresUsageRecovery {
				t.Fatalf("shared repair lost lineage: %+v", repaired)
			}
			from := child.CreatedAt.Truncate(time.Hour)
			importRequest := UsageImportRequest{ImportID: uuid.NewString(), DatabaseID: child.ID, EvidenceReference: "retained/export", EvidenceSHA256: strings.Repeat("b", 64), Reason: "Attempt independent branch charge",
				Windows: []UsageImportWindow{{From: from, To: from.Add(time.Hour), ObservedAt: now, Readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 60}}}}}
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", importRequest, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("independent shared-child import accepted: %v", err)
			}
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(ctx); err != nil {
				t.Fatal(err)
			}
			before, err := store.UsageSnapshot(ctx, root.AccountID, now)
			if err != nil || before.Stale(registry.UsagePolicy(), now) || before.ComputeUnitSeconds != 6*60 {
				t.Fatalf("branch recovery doubled or lost root usage: %+v %v", before, err)
			}
			shutdown, rootProviderID := now, root.ProviderResourceID
			root = reconciliationLegacyDatabase(t, store, root, now.Add(-time.Hour))
			now = now.Add(4 * time.Hour)
			request.ReconciliationID, request.DatabaseID, request.ProviderResourceID = uuid.NewString(), root.ID, rootProviderID
			request.ShutdownAt, request.ObservedAt = shutdown, now
			applyReconciliation(t, service, root.AccountID, request)
			after, err := store.UsageSnapshot(ctx, root.AccountID, now)
			if err != nil || !after.Stale(registry.UsagePolicy(), now) || after.ComputeUnitSeconds != before.ComputeUnitSeconds {
				t.Fatalf("root repair reused derived child coverage: %+v %v", after, err)
			}
			if _, err := collector.Collect(ctx); err != nil {
				t.Fatal(err)
			}
			after, err = store.UsageSnapshot(ctx, root.AccountID, now)
			if err != nil || after.Stale(registry.UsagePolicy(), now) || after.ComputeUnitSeconds != 7*60 {
				t.Fatalf("terminal root recovery: %+v %v", after, err)
			}
			for _, id := range provider.usageCalls {
				if id != request.ProviderResourceID {
					t.Fatalf("shared branch requested independent usage: %s", id)
				}
			}
		})
	}
}

func TestAccountingReconciliationAuditFailureRollsBackCatalogAndCoverage(t *testing.T) {
	store, service, database, request, now := reconciliationFixture(t, "postgres")
	ctx := context.Background()
	seedReconciliationLedger(t, store, service, database, now.Add(-2*time.Hour))
	before, err := store.UsageSnapshot(ctx, database.AccountID, now)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, false)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = preview.Revision
	pg := store.(*PostgresStore)
	if _, err := pg.pool.Exec(ctx, `CREATE FUNCTION reject_reconciliation_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected audit failure'; END $$;
CREATE TRIGGER reject_reconciliation_test BEFORE INSERT ON managed_postgres_accounting_reconciliations FOR EACH ROW EXECUTE FUNCTION reject_reconciliation_test();`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true); err == nil {
		t.Fatal("audit failure accepted")
	}
	current, err := store.Get(ctx, database.AccountID, database.ID)
	if err != nil || !reflect.DeepEqual(database, current) {
		t.Fatalf("catalog partially committed: %+v %v", current, err)
	}
	after, err := store.UsageSnapshot(ctx, database.AccountID, now)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("coverage or money partially committed: %+v %v", after, err)
	}
	if _, err := pg.pool.Exec(ctx, "DROP TRIGGER reject_reconciliation_test ON managed_postgres_accounting_reconciliations; DROP FUNCTION reject_reconciliation_test();"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestAccountingReconciliationAuditReplayAndAccountErasure(t *testing.T) {
	store, service, database, request, _ := reconciliationFixture(t, "postgres")
	request, applied := applyReconciliation(t, service, database.AccountID, request)
	pg := store.(*PostgresStore)
	ctx := context.Background()
	if _, err := pg.pool.Exec(ctx, "DELETE FROM goose_db_version WHERE version_id=20261005085250802"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pg.pool); err != nil {
		t.Fatal(err)
	}
	replay, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true)
	if err != nil || !reflect.DeepEqual(applied, replay) {
		t.Fatalf("migration replay changed audit: %+v %v", replay, err)
	}
	for _, query := range []string{
		"UPDATE managed_postgres_accounting_reconciliations SET reason='changed' WHERE account_id=$1",
		"DELETE FROM managed_postgres_accounting_reconciliations WHERE account_id=$1",
		"DELETE FROM managed_postgres_databases WHERE account_id=$1",
	} {
		if _, err := pg.pool.Exec(ctx, query, database.AccountID); err == nil {
			t.Fatal("retained evidence could be removed independently")
		}
	}
	accounts := state.NewPgStore(pg.pool)
	if err := accounts.MarkAccountDeletionPending(ctx, database.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := accounts.DeleteAccount(ctx, database.AccountID); err != nil {
		t.Fatalf("audit prevented account erasure: %v", err)
	}
	var count int
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM managed_postgres_accounting_reconciliations WHERE account_id=$1", database.AccountID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("account erasure retained audit: %d %v", count, err)
	}
}
