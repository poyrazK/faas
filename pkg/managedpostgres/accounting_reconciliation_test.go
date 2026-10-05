// adr: 591 — legacy repair retains money and requires evidence-backed recovery.
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
)

func reconciliationFixture(t *testing.T, kind string) (retirementUsageStore, *Service, Database, AccountingReconciliationRequest, time.Time) {
	t.Helper()
	store, service, database, _, now := usageImportFixture(t, kind)
	providerID := database.ProviderResourceID
	database = reconciliationLegacyDatabase(t, store, database, now.Add(-7*time.Hour))
	request := AccountingReconciliationRequest{ReconciliationID: uuid.NewString(), DatabaseID: database.ID, BackendID: database.BackendID,
		BackendFingerprint: database.BackendFingerprint, ProviderResourceID: providerID, ShutdownAt: now.Add(-5 * time.Hour), ObservedAt: now.Add(-time.Hour),
		EvidenceReference: "retained/shutdown-export", EvidenceSHA256: strings.Repeat("a", 64), Reason: "Repair lost legacy identity and confirmed shutdown"}
	return store, service, database, request, now
}

func reconciliationLegacyDatabase(t *testing.T, store retirementUsageStore, database Database, at time.Time) Database {
	t.Helper()
	database = retirementDelete(t, store, database, at, true)
	switch s := store.(type) {
	case *MemoryStore:
		s.mu.Lock()
		database.ProviderResourceID = ""
		s.databases[database.ID] = cloneDatabase(database)
		s.mu.Unlock()
	case *PostgresStore:
		if _, err := s.pool.Exec(context.Background(), "UPDATE managed_postgres_databases SET provider_resource_id=NULL WHERE id=$1", database.ID); err != nil {
			t.Fatal(err)
		}
	}
	database, err := store.Get(context.Background(), database.AccountID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func seedReconciliationLedger(t *testing.T, store retirementUsageStore, service *Service, database Database, observed time.Time) {
	t.Helper()
	backend, err := service.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	from := database.CreatedAt.UTC().Truncate(time.Hour)
	for i := 0; i < 2; i++ {
		var records []UsageRecord
		for _, meter := range backend.Capabilities.UsageMeters {
			cost, err := service.registry.UsagePolicy().Cost(MeterReading{Meter: meter, Quantity: 60})
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, UsageRecord{AccountID: database.AccountID, DatabaseID: database.ID, BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
				WindowFrom: from, WindowTo: from.Add(time.Hour), ObservedAt: observed, Meter: meter, Quantity: 60, CostMillicents: cost})
		}
		if err := store.RecordUsage(context.Background(), records); err != nil {
			t.Fatal(err)
		}
		from = from.Add(time.Hour)
	}
}

func TestAccountingReconciliationPreservesLedgerAndRequiresRecovery(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := reconciliationFixture(t, kind)
			ctx := context.Background()
			seedReconciliationLedger(t, store, service, database, now.Add(-2*time.Hour))
			before, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, false)
			if err != nil || preview.Applied || !preview.RequiresUsageRecovery || !validSHA256(preview.Revision) {
				t.Fatalf("preview: %+v %v", preview, err)
			}
			afterPreview, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil || !reflect.DeepEqual(before, afterPreview) {
				t.Fatalf("preview changed accounting: %+v %v", afterPreview, err)
			}
			request.ExpectedRevision = preview.Revision
			applied, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true)
			if err != nil || !applied.Applied || applied.Revision != preview.Revision {
				t.Fatalf("apply: %+v %v", applied, err)
			}
			current, err := store.Get(ctx, database.AccountID, database.ID)
			if err != nil || current.State != StateDeleted || !current.AccountingRequired || current.ProviderResourceID != request.ProviderResourceID ||
				current.DeletedAt == nil || !current.DeletedAt.Equal(request.ShutdownAt) || applied.PreviousDeletedAt == nil || !applied.PreviousDeletedAt.Equal(*database.DeletedAt) {
				t.Fatalf("catalog: %+v %v", current, err)
			}
			after, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil || after.ComputeUnitSeconds != before.ComputeUnitSeconds || after.CostMillicents != before.CostMillicents ||
				!after.Databases[0].CollectedUntil.IsZero() || after.Databases[0].Unresolved || !after.Stale(service.registry.UsagePolicy(), now) {
				t.Fatalf("repair settled or lost money: %+v %v", after, err)
			}
			if err := service.registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("unrecovered repair admitted: %v", err)
			}
			importRequest := UsageImportRequest{ImportID: uuid.NewString(), DatabaseID: database.ID, EvidenceReference: "retained/usage-export", EvidenceSHA256: strings.Repeat("b", 64), Reason: "Recover reconciled history"}
			backend, _ := service.registry.Resolve(database.BackendID, database.BackendFingerprint)
			for from := database.CreatedAt.UTC().Truncate(time.Hour); from.Before(usageEnd(request.ShutdownAt, time.Hour)); from = from.Add(time.Hour) {
				window := UsageImportWindow{From: from, To: from.Add(time.Hour), ObservedAt: request.ObservedAt}
				for _, meter := range backend.Capabilities.UsageMeters {
					window.Readings = append(window.Readings, MeterReading{Meter: meter, Quantity: 60})
				}
				importRequest.Windows = append(importRequest.Windows, window)
			}
			imported, err := service.ImportUsage(ctx, database.AccountID, "operator", importRequest, false)
			if err != nil {
				t.Fatal(err)
			}
			importRequest.ExpectedRevision = imported.Revision
			if _, err := service.ImportUsage(ctx, database.AccountID, "operator", importRequest, true); err != nil {
				t.Fatal(err)
			}
			if err := service.registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); err != nil {
				t.Fatalf("complete repair not admitted: %v", err)
			}
			policy := service.registry.usage
			service.registry.usage.Enabled = false
			service.registry.usage.Window = 24 * time.Hour
			replay, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true)
			if err != nil || !reflect.DeepEqual(applied, replay) {
				t.Fatalf("durable replay: %+v %v", replay, err)
			}
			*replay.PreviousDeletedAt = replay.PreviousDeletedAt.Add(time.Hour)
			replay, err = service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true)
			if err != nil || !reflect.DeepEqual(applied, replay) {
				t.Fatalf("caller changed retained response: %+v %v", replay, err)
			}
			service.registry.usage = policy
			if _, err := service.ReconcileAccounting(ctx, database.AccountID, "another-operator", request, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("actor collision: %v", err)
			}
			request.Reason = "changed reason"
			if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("request collision: %v", err)
			}
		})
	}
}

func TestAccountingReconciliationRejectsInvalidEvidence(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := reconciliationFixture(t, kind)
			cases := []struct {
				name   string
				mutate func(*AccountingReconciliationRequest)
				want   error
			}{
				{"bad ID", func(r *AccountingReconciliationRequest) { r.ReconciliationID = "bad" }, ErrInvalid},
				{"empty identity", func(r *AccountingReconciliationRequest) { r.ProviderResourceID = "" }, ErrInvalid},
				{"wrong backend", func(r *AccountingReconciliationRequest) { r.BackendID = "foreign" }, ErrConflict},
				{"wrong fingerprint", func(r *AccountingReconciliationRequest) { r.BackendFingerprint = strings.Repeat("b", 64) }, ErrConflict},
				{"bad digest", func(r *AccountingReconciliationRequest) { r.EvidenceSHA256 = "bad" }, ErrInvalid},
				{"control text", func(r *AccountingReconciliationRequest) { r.Reason = "bad\ntext" }, ErrInvalid},
				{"before creation", func(r *AccountingReconciliationRequest) { r.ShutdownAt = database.CreatedAt.Add(-time.Second) }, ErrInvalid},
				{"before shutdown observation", func(r *AccountingReconciliationRequest) { r.ObservedAt = r.ShutdownAt.Add(-time.Second) }, ErrInvalid},
				{"future observation", func(r *AccountingReconciliationRequest) { r.ObservedAt = now.Add(time.Second) }, ErrInvalid},
				{"nanosecond shutdown", func(r *AccountingReconciliationRequest) { r.ShutdownAt = r.ShutdownAt.Add(time.Nanosecond) }, ErrInvalid},
				{"nanosecond observation", func(r *AccountingReconciliationRequest) { r.ObservedAt = r.ObservedAt.Add(time.Nanosecond) }, ErrInvalid},
				{"revision in preview", func(r *AccountingReconciliationRequest) { r.ExpectedRevision = strings.Repeat("a", 64) }, ErrInvalid},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					r := request
					tc.mutate(&r)
					if _, err := service.ReconcileAccounting(context.Background(), database.AccountID, "operator", r, false); !errors.Is(err, tc.want) {
						t.Fatalf("error=%v want=%v", err, tc.want)
					}
				})
			}
			if _, err := service.ReconcileAccounting(context.Background(), uuid.NewString(), "operator", request, false); !errors.Is(err, ErrNotFound) {
				t.Fatalf("wrong owner: %v", err)
			}
			if _, err := service.ReconcileAccounting(context.Background(), database.AccountID, "operator", request, true); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unpreviewed apply: %v", err)
			}
			current, _ := store.Get(context.Background(), database.AccountID, database.ID)
			if current.ProviderResourceID != "" || !current.DeletedAt.Equal(*database.DeletedAt) {
				t.Fatal("invalid requests changed catalog")
			}
		})
	}
}

func TestAccountingReconciliationCollectorAndLedgerFences(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := reconciliationFixture(t, kind)
			ctx := context.Background()
			preview, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, false)
			if err != nil {
				t.Fatal(err)
			}
			seedReconciliationLedger(t, store, service, database, now.Add(-2*time.Hour))
			request.ExpectedRevision = preview.Revision
			if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale preview: %v", err)
			}
			request.ExpectedRevision = ""
			stale := request
			stale.ObservedAt = now.Add(-3 * time.Hour)
			if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", stale, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("older proof: %v", err)
			}
			early := request
			early.ShutdownAt = database.CreatedAt.Add(30 * time.Minute)
			if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", early, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("ledger beyond shutdown: %v", err)
			}
			input := postgresTestDatabase(database.AccountID, "identity-in-use", database.CreatedAt)
			input.BackendID, input.BackendFingerprint = database.BackendID, database.BackendFingerprint
			other := retirementReadyDatabase(t, store, input)
			duplicate := request
			duplicate.ProviderResourceID = other.ProviderResourceID
			if _, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", duplicate, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("duplicate identity: %v", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := service.ReconcileAccounting(canceled, database.AccountID, "operator", request, false); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
}

func TestAccountingReconciliationConcurrentReplayAndIdentityClaims(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			store, service, database, request, now := reconciliationFixture(t, kind)
			ctx := context.Background()
			preview, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, false)
			if err != nil {
				t.Fatal(err)
			}
			request.ExpectedRevision = preview.Revision
			var group sync.WaitGroup
			results := make(chan error, 8)
			for i := 0; i < 8; i++ {
				group.Add(1)
				go func() {
					defer group.Done()
					_, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", request, true)
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
			input := postgresTestDatabase(database.AccountID, "competing-one", database.CreatedAt)
			input.BackendID, input.BackendFingerprint = database.BackendID, database.BackendFingerprint
			first := reconciliationLegacyDatabase(t, store, retirementReadyDatabase(t, store, input), now.Add(-7*time.Hour))
			input.ID, input.Name = uuid.NewString(), "competing-two"
			second := reconciliationLegacyDatabase(t, store, retirementReadyDatabase(t, store, input), now.Add(-7*time.Hour))
			a, b := request, request
			a.ReconciliationID, a.DatabaseID, a.ProviderResourceID, a.ExpectedRevision = uuid.NewString(), first.ID, "same-retained-provider-identity", ""
			b.ReconciliationID, b.DatabaseID, b.ProviderResourceID, b.ExpectedRevision = uuid.NewString(), second.ID, a.ProviderResourceID, ""
			for _, r := range []*AccountingReconciliationRequest{&a, &b} {
				p, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", *r, false)
				if err != nil {
					t.Fatal(err)
				}
				r.ExpectedRevision = p.Revision
			}
			claims := make(chan error, 2)
			for _, r := range []AccountingReconciliationRequest{a, b} {
				group.Add(1)
				go func(r AccountingReconciliationRequest) {
					defer group.Done()
					_, err := service.ReconcileAccounting(ctx, database.AccountID, "operator", r, true)
					claims <- err
				}(r)
			}
			group.Wait()
			close(claims)
			succeeded, conflicted := 0, 0
			for err := range claims {
				if err == nil {
					succeeded++
				} else if errors.Is(err, ErrConflict) {
					conflicted++
				} else {
					t.Fatal(err)
				}
			}
			if succeeded != 1 || conflicted != 1 {
				t.Fatalf("identity claims succeeded=%d conflicted=%d", succeeded, conflicted)
			}
		})
	}
}
