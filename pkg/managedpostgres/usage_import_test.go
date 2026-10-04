// adr: 583 — audited imports preserve window, observation and ownership fences.
package managedpostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func usageImportFixture(t *testing.T, kind string) (retirementUsageStore, *Service, Database, UsageImportRequest, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 5, 12, 17, 0, 0, time.UTC)
	store, registry, _, database := retirementFixture(t, kind, now.Add(-10*time.Hour))
	service, err := NewService(registry, store, ServiceOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := UsageImportRequest{ImportID: uuid.NewString(), DatabaseID: database.ID,
		EvidenceReference: "retained/export-20261005", EvidenceSHA256: strings.Repeat("a", 64), Reason: "Recover unavailable provider history"}
	from := database.CreatedAt.Truncate(time.Hour)
	backend, err := registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		window := UsageImportWindow{From: from, To: from.Add(time.Hour), ObservedAt: now.Add(-4 * time.Hour)}
		for _, meter := range backend.Capabilities.UsageMeters {
			window.Readings = append(window.Readings, MeterReading{Meter: meter, Quantity: 60})
		}
		request.Windows = append(request.Windows, window)
		from = window.To
	}
	return store, service, database, request, now
}

func TestUsageImportPreviewApplyAndDurableReplay(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := usageImportFixture(t, kind)
			ctx := context.Background()
			before, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.ImportUsage(ctx, database.AccountID, "operator", request, false)
			if err != nil || preview.Applied || preview.PreviousCostMillicents != 0 || !validSHA256(preview.Revision) {
				t.Fatalf("preview: %+v %v", preview, err)
			}
			afterPreview, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil || !reflect.DeepEqual(before, afterPreview) {
				t.Fatal("preview changed ledger")
			}
			request.ExpectedRevision = preview.Revision
			applied, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true)
			if err != nil || !applied.Applied || applied.Revision != preview.Revision || applied.WindowCount != 2 {
				t.Fatalf("apply: %+v %v", applied, err)
			}
			after, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil || after.ComputeUnitSeconds != 120 || !after.Stale(service.registry.UsagePolicy(), now) || !applied.ObservedAt.Equal(request.Windows[0].ObservedAt) {
				t.Fatalf("history changed freshness: %+v %v", after, err)
			}
			// Restart service; an expired lease or a later collector correction must
			// not make an identical committed import run again.
			restarted, err := NewService(service.registry, store, ServiceOptions{Now: func() time.Time { return now.Add(24 * time.Hour) }})
			if err != nil {
				t.Fatal(err)
			}
			replay, err := restarted.ImportUsage(ctx, database.AccountID, "operator", request, true)
			if err != nil || !reflect.DeepEqual(applied, replay) {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			originalPolicy := service.registry.usage
			service.registry.usage.Window = 24 * time.Hour
			service.registry.usage.Enabled = false
			if replay, err := restarted.ImportUsage(ctx, database.AccountID, "operator", request, true); err != nil || !reflect.DeepEqual(applied, replay) {
				t.Fatalf("replay after policy disable/change: %+v %v", replay, err)
			}
			service.registry.usage = originalPolicy
			request.Reason = "different evidence"
			if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("conflicting replay: %v", err)
			}
			if pg, ok := store.(*PostgresStore); ok {
				var count int
				if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM managed_postgres_usage_imports WHERE account_id=$1", database.AccountID).Scan(&count); err != nil || count != 1 {
					t.Fatalf("audit count=%d %v", count, err)
				}
				if _, err := pg.pool.Exec(ctx, "UPDATE managed_postgres_usage_imports SET reason='changed' WHERE account_id=$1", database.AccountID); err == nil {
					t.Fatal("audit mutable")
				}
			}
		})
	}
}

func TestUsageImportRejectsInvalidEvidenceWithoutWrites(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, original, now := usageImportFixture(t, kind)
			for _, tc := range []struct {
				name   string
				mutate func(*UsageImportRequest)
				want   error
			}{
				{"missing meter", func(r *UsageImportRequest) { r.Windows[1].Readings = nil }, ErrInvalid},
				{"duplicate meter", func(r *UsageImportRequest) {
					r.Windows[1].Readings = append(r.Windows[1].Readings, r.Windows[1].Readings[0])
				}, ErrInvalid},
				{"negative quantity", func(r *UsageImportRequest) { r.Windows[1].Readings[0].Quantity = -1 }, ErrInvalid},
				{"gap", func(r *UsageImportRequest) {
					r.Windows[1].From = r.Windows[1].From.Add(time.Hour)
					r.Windows[1].To = r.Windows[1].To.Add(time.Hour)
				}, ErrInvalid},
				{"daily overlap", func(r *UsageImportRequest) {
					r.Windows = r.Windows[:1]
					r.Windows[0].To = r.Windows[0].From.Add(24 * time.Hour)
				}, ErrInvalid},
				{"future observation", func(r *UsageImportRequest) { r.Windows[1].ObservedAt = now.Add(time.Second) }, ErrInvalid},
				{"open evidence", func(r *UsageImportRequest) { r.Windows[1].ObservedAt = r.Windows[1].From }, ErrInvalid},
				{"unrepresentable precision", func(r *UsageImportRequest) { r.Windows[1].ObservedAt = r.Windows[1].ObservedAt.Add(time.Nanosecond) }, ErrInvalid},
				{"bad evidence hash", func(r *UsageImportRequest) { r.EvidenceSHA256 = "bad" }, ErrInvalid},
				{"unknown database", func(r *UsageImportRequest) { r.DatabaseID = uuid.NewString() }, ErrNotFound},
			} {
				t.Run(tc.name, func(t *testing.T) {
					request := original
					request.Windows = append([]UsageImportWindow(nil), original.Windows...)
					for i := range request.Windows {
						request.Windows[i].Readings = append([]MeterReading(nil), original.Windows[i].Readings...)
					}
					tc.mutate(&request)
					if _, err := service.ImportUsage(context.Background(), database.AccountID, "operator", request, false); !errors.Is(err, tc.want) {
						t.Fatalf("error=%v want=%v", err, tc.want)
					}
					progress, err := store.UsageProgress(context.Background(), database.AccountID, database.ID, time.Hour)
					if err != nil || !progress.CollectedUntil.IsZero() {
						t.Fatal("invalid evidence advanced coverage")
					}
				})
			}
			if _, err := service.ImportUsage(context.Background(), uuid.NewString(), "operator", original, false); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign account: %v", err)
			}
			if _, err := service.ImportUsage(context.Background(), database.AccountID, "operator", original, true); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unpreviewed apply: %v", err)
			}
		})
	}
}

func TestUsageImportCorrectionAndCollectorPreviewRace(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := usageImportFixture(t, kind)
			ctx := context.Background()
			preview, err := service.ImportUsage(ctx, database.AccountID, "operator", request, false)
			if err != nil {
				t.Fatal(err)
			}
			request.ExpectedRevision = preview.Revision
			first := request.Windows[0]
			record := UsageRecord{AccountID: database.AccountID, DatabaseID: database.ID, BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint, WindowFrom: first.From, WindowTo: first.To, ObservedAt: now.Add(-5 * time.Hour), Meter: MeterComputeUnitSeconds, Quantity: 120, CostMillicents: 120}
			if err := store.RecordUsage(ctx, []UsageRecord{record}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale preview: %v", err)
			}
			request.ExpectedRevision = ""
			preview, err = service.ImportUsage(ctx, database.AccountID, "operator", request, false)
			if err != nil {
				t.Fatal(err)
			}
			if preview.PreviousCostMillicents != 120 || preview.CostDeltaMillicents != preview.ImportedCostMillicents-120 {
				t.Fatalf("correction preview: %+v", preview)
			}
			request.ExpectedRevision = preview.Revision
			var group sync.WaitGroup
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				group.Add(1)
				go func() {
					defer group.Done()
					_, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true)
					results <- err
				}()
			}
			group.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatalf("concurrent replay: %v", err)
				}
			}
			request.ImportID = uuid.NewString()
			request.ExpectedRevision = ""
			request.Windows[0].ObservedAt = record.ObservedAt
			if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("regressing evidence: %v", err)
			}
		})
	}
}

func TestUsageImportLifecycleAndSharedAccountingFences(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, root, request, now := usageImportFixture(t, kind)
			ctx := context.Background()
			unknown := diagnosticUnknownDatabase(t, store, root, "unknown-import", root.CreatedAt)
			unknownRequest := request
			unknownRequest.DatabaseID = unknown.ID
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", unknownRequest, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("unknown identity: %v", err)
			}
			unknown = retirementDelete(t, store, unknown, now.Add(-time.Hour), true)
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", unknownRequest, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("legacy tombstone: %v", err)
			}
			input := postgresTestDatabase(root.AccountID, "shared-import", root.CreatedAt.Add(time.Hour))
			input.BackendID, input.BackendFingerprint = root.BackendID, root.BackendFingerprint
			input.RestoreSourceDatabaseID, input.RestoreSourceResourceID = root.ID, root.ProviderResourceID
			input.RestorePointInTime = input.CreatedAt
			child := retirementReadyDatabase(t, store, input)
			if err := store.RecordSharedUsage(ctx, root.AccountID, child.ID, root.ID, time.Hour); err != nil {
				t.Fatal(err)
			}
			childRequest := request
			childRequest.DatabaseID = child.ID
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", childRequest, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("shared child: %v", err)
			}
			retirementDelete(t, store, child, now.Add(-time.Hour), true)
			if _, err := store.ClaimDelete(ctx, root.AccountID, root.ID, "busy-import", now, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", request, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("active lease: %v", err)
			}
			if _, err := store.FinishDelete(ctx, root.ID, "busy-import", now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", request, false); err != nil {
				t.Fatalf("confirmed terminal history: %v", err)
			}
			future := request
			future.Windows = append([]UsageImportWindow(nil), request.Windows...)
			future.Windows[0].From = now.Truncate(time.Hour).Add(time.Hour)
			future.Windows[0].To = future.Windows[0].From.Add(time.Hour)
			future.Windows[0].ObservedAt = now.Add(3 * time.Hour)
			future.Windows = future.Windows[:1]
			service.now = func() time.Time { return now.Add(4 * time.Hour) }
			if _, err := service.ImportUsage(ctx, root.AccountID, "operator", future, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("past shutdown: %v", err)
			}
		})
	}
}

func TestUsageImportAuditFailureRollsBackLedger(t *testing.T) {
	store, service, database, request, now := usageImportFixture(t, "postgres")
	pg := store.(*PostgresStore)
	ctx := context.Background()
	preview, err := service.ImportUsage(ctx, database.AccountID, "operator", request, false)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = preview.Revision
	_, err = pg.pool.Exec(ctx, `CREATE FUNCTION reject_usage_import_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$;
CREATE TRIGGER reject_usage_import_test BEFORE INSERT ON managed_postgres_usage_imports FOR EACH ROW EXECUTE FUNCTION reject_usage_import_test();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true); err == nil {
		t.Fatal("audit failure accepted")
	}
	snapshot, err := store.UsageSnapshot(ctx, database.AccountID, now)
	if err != nil || snapshot.ComputeUnitSeconds != 0 || !snapshot.Databases[0].CollectedUntil.IsZero() {
		t.Fatalf("partial commit: %+v %v", snapshot, err)
	}
	if _, err := pg.pool.Exec(ctx, "DROP TRIGGER reject_usage_import_test ON managed_postgres_usage_imports; DROP FUNCTION reject_usage_import_test();"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	request.ImportID = uuid.NewString()
	request.ExpectedRevision = ""
	if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled import: %v", err)
	}
}

func TestUsageImportEvidenceAllowsFinalAccountErasure(t *testing.T) {
	store, service, database, request, now := usageImportFixture(t, "postgres")
	ctx := context.Background()
	preview, err := service.ImportUsage(ctx, database.AccountID, "operator", request, false)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = preview.Revision
	if _, err := service.ImportUsage(ctx, database.AccountID, "operator", request, true); err != nil {
		t.Fatal(err)
	}
	retirementDelete(t, store, database, now, true)
	pg := store.(*PostgresStore)
	accounts := state.NewPgStore(pg.pool)
	if err := accounts.MarkAccountDeletionPending(ctx, database.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := accounts.DeleteAccount(ctx, database.AccountID); err != nil {
		t.Fatalf("receipt prevented account erasure: %v", err)
	}
	var remaining int
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM managed_postgres_usage_imports WHERE account_id=$1", database.AccountID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("retained after erasure: %d %v", remaining, err)
	}
}
